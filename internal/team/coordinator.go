package team

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/junhoyeo/contrabass/internal/agent"
	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/junhoyeo/contrabass/internal/workspace"
)

const (
	governanceRetryLimit = 10
	governanceRetryDelay = 500 * time.Millisecond
)

// Coordinator manages a team of workers executing a staged pipeline.
type Coordinator struct {
	teamName   string
	cfg        *config.WorkflowConfig
	store      *Store
	paths      *Paths
	tasks      *TaskRegistry
	heartbeats *HeartbeatMonitor
	dispatch   *DispatchQueue
	mailbox    *Mailbox
	ownership  *OwnershipRegistry
	phases     *PhaseMachine
	governance *GovernancePolicy
	recovery   *Recovery
	workspace  *workspace.Manager
	runner     agent.AgentRunner
	logger     *slog.Logger

	mu      sync.Mutex
	workers map[string]*workerHandle
	cancel  context.CancelFunc

	Events chan types.TeamEvent
}

// workerHandle tracks a running worker goroutine.
type workerHandle struct {
	state   types.WorkerState
	process *agent.AgentProcess
	cancel  context.CancelFunc
}

// TeamStatus holds a snapshot of the team's current state.
type TeamStatus struct {
	TeamName     string              `json:"team_name"`
	BoardIssueID string              `json:"board_issue_id,omitempty"`
	Phase        types.TeamPhase     `json:"phase"`
	Workers      []types.WorkerState `json:"workers"`
	Tasks        []types.TeamTask    `json:"tasks"`
	FixLoopCount int                 `json:"fix_loop_count"`
}

// NewCoordinator creates a Coordinator for the given team.
func NewCoordinator(
	teamName string,
	cfg *config.WorkflowConfig,
	runner agent.AgentRunner,
	ws *workspace.Manager,
	logger *slog.Logger,
) *Coordinator {
	if logger == nil {
		logger = slog.Default()
	}

	paths := NewPaths(cfg.TeamStateDir())
	store := NewStore(paths)
	tasks := NewTaskRegistry(store, paths, cfg.TeamClaimLeaseSeconds())
	staleThreshold := time.Duration(cfg.TeamClaimLeaseSeconds()) * time.Second
	heartbeats := NewHeartbeatMonitor(store, paths, staleThreshold)
	dispatch := NewDispatchQueue(store, paths, staleThreshold)
	mailbox := NewMailbox(store, paths)
	ownership := NewOwnershipRegistry(store, paths)
	phases := NewPhaseMachine(store, tasks, cfg.TeamMaxFixLoops())
	governance := NewGovernancePolicy(
		MaxConcurrentTasksRule{MaxTasksPerWorker: 3},
		PhaseGateRule{},
		WorkerCapacityRule{MaxWorkers: cfg.TeamMaxWorkers()},
	)
	recovery := NewRecovery(store, paths, heartbeats, tasks, dispatch, logger)

	return &Coordinator{
		teamName:   teamName,
		cfg:        cfg,
		store:      store,
		paths:      paths,
		tasks:      tasks,
		heartbeats: heartbeats,
		dispatch:   dispatch,
		mailbox:    mailbox,
		ownership:  ownership,
		phases:     phases,
		governance: governance,
		recovery:   recovery,
		workspace:  ws,
		runner:     runner,
		logger:     logger,
		workers:    make(map[string]*workerHandle),
		Events:     make(chan types.TeamEvent, 100),
	}
}

// Store returns the underlying Store for direct access.
func (c *Coordinator) Store() *Store { return c.store }

// Tasks returns the task registry.
func (c *Coordinator) Tasks() *TaskRegistry { return c.tasks }

// Phases returns the phase machine.
func (c *Coordinator) Phases() *PhaseMachine { return c.phases }

// Mailbox returns the mailbox.
func (c *Coordinator) Mailbox() *Mailbox { return c.mailbox }

// Ownership returns the ownership registry.
func (c *Coordinator) Ownership() *OwnershipRegistry { return c.ownership }

// Initialize creates the team manifest and directory structure.
func (c *Coordinator) Initialize(teamCfg types.TeamConfig) error {
	if _, err := c.store.CreateManifest(c.teamName, teamCfg); err != nil {
		return fmt.Errorf("create team manifest: %w", err)
	}

	c.emitEvent("team_created", map[string]interface{}{
		"team_name":      c.teamName,
		"max_workers":    teamCfg.MaxWorkers,
		"board_issue_id": teamCfg.BoardIssueID,
	})
	return nil
}

// Run starts the team coordination loop.
func (c *Coordinator) Run(ctx context.Context, tasks []types.TeamTask) error {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	defer cancel()
	defer close(c.Events)

	if err := c.recovery.Recover(ctx, c.teamName); err != nil {
		c.logger.Warn("startup recovery failed", "team", c.teamName, "error", err)
	}

	for i := range tasks {
		if err := c.tasks.CreateTask(c.teamName, &tasks[i]); err != nil {
			return fmt.Errorf("create task %s: %w", tasks[i].ID, err)
		}
	}

	c.emitEvent("pipeline_started", map[string]interface{}{
		"task_count": len(tasks),
	})

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("team coordination cancelled", "team", c.teamName)
			if err := c.phases.Cancel(c.teamName, "context cancelled"); err != nil {
				c.logger.Warn("failed to cancel phase machine", "team", c.teamName, "error", err)
			}
			c.stopAllWorkers()
			return ctx.Err()
		default:
		}

		phase, err := c.phases.CurrentPhase(c.teamName)
		if err != nil {
			return fmt.Errorf("get current phase: %w", err)
		}

		if phase.IsTerminal() {
			c.stopAllWorkers()
			c.emitEvent("pipeline_completed", map[string]interface{}{"phase": string(phase)})
			return nil
		}

		if err := c.runPhase(ctx, phase); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			c.logger.Error("phase execution failed", "team", c.teamName, "phase", phase, "error", err)
			if transitionErr := c.phases.Transition(c.teamName, types.PhaseFailed, err.Error()); transitionErr != nil {
				c.logger.Warn("failed to transition to failed phase", "team", c.teamName, "error", transitionErr)
			}
			c.stopAllWorkers()
			return err
		}
	}
}

func (c *Coordinator) runPhase(ctx context.Context, phase types.TeamPhase) error {
	c.logger.Info("entering phase", "team", c.teamName, "phase", phase)
	c.emitEvent("phase_started", map[string]interface{}{"phase": string(phase)})

	switch phase {
	case types.PhasePlan:
		return c.runWorkerPhase(ctx, types.PhasePRD, "planning complete")
	case types.PhasePRD:
		return c.runWorkerPhase(ctx, types.PhaseExec, "prd complete")
	case types.PhaseExec:
		return c.runExecPhase(ctx)
	case types.PhaseVerify:
		return c.runVerifyPhase(ctx)
	case types.PhaseFix:
		return c.runFixPhase(ctx)
	default:
		return fmt.Errorf("unexpected phase: %s", phase)
	}
}

func (c *Coordinator) runWorkerPhase(ctx context.Context, next types.TeamPhase, reason string) error {
	task, token, err := c.tasks.ClaimNextTask(c.teamName, "coordinator")
	if err != nil {
		if errors.Is(err, ErrTaskNotClaimable) {
			return c.phases.Transition(c.teamName, next, reason+": no tasks")
		}
		return fmt.Errorf("claim next task: %w", err)
	}

	c.emitEvent("task_claimed", map[string]interface{}{
		"worker_id": "coordinator",
		"task_id":   task.ID,
		"task":      task.Subject,
	})

	if err := c.executeTask(ctx, task, token, "coordinator"); err != nil {
		if failErr := c.tasks.FailTask(c.teamName, task.ID, token, err.Error()); failErr != nil {
			c.logger.Warn("failed to mark task as failed", "team", c.teamName, "task", task.ID, "error", failErr)
		}
		c.emitEvent("task_failed", map[string]interface{}{
			"worker_id": "coordinator",
			"task_id":   task.ID,
			"error":     err.Error(),
		})
		return err
	}

	if err := c.tasks.CompleteTask(c.teamName, task.ID, token, "completed by coordinator"); err != nil {
		return err
	}

	c.emitEvent("task_completed", map[string]interface{}{
		"worker_id": "coordinator",
		"task_id":   task.ID,
	})

	return c.phases.Transition(c.teamName, next, reason)
}

func (c *Coordinator) runExecPhase(ctx context.Context) error {
	numWorkers := c.cfg.TeamMaxWorkers()
	g, gCtx := errgroup.WithContext(ctx)

	for i := range numWorkers {
		workerID := fmt.Sprintf("worker-%d", i)
		g.Go(func() error {
			return c.workerLoop(gCtx, workerID)
		})
	}

	// Periodic stale worker detection during exec phase (non-blocking).
	monitorCtx, monitorCancel := context.WithCancel(gCtx)
	go func() {
		defer monitorCancel()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-monitorCtx.Done():
				return
			case <-ticker.C:
				stale := c.checkForStaleWorkers()
				for _, workerID := range stale {
					c.logger.Warn("stale worker detected during exec phase",
						"team", c.teamName, "worker", workerID)
				}
			}
		}
	}()

	err := g.Wait()
	monitorCancel() // Stop the monitor goroutine.

	if err != nil {
		// If the parent context was cancelled (e.g., signal), propagate immediately.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A worker returned a real error. Check if all tasks reached terminal
		// state despite the error — if not, surface it.
		allDone, doneErr := c.phases.AllTasksTerminal(c.teamName)
		if doneErr != nil {
			return doneErr
		}
		if !allDone {
			return err
		}
	}

	if err := c.checkTransitionGovernance(ctx, types.PhaseExec); err != nil {
		return err
	}

	return c.phases.Transition(c.teamName, types.PhaseVerify, "exec phase complete")
}

func (c *Coordinator) runVerifyPhase(ctx context.Context) error {
	task, token, err := c.tasks.ClaimNextTask(c.teamName, "verifier")
	if err != nil {
		allCompleted, allErr := c.phases.AllTasksCompleted(c.teamName)
		if allErr != nil {
			return allErr
		}
		if allCompleted {
			if err := c.checkTransitionGovernance(ctx, types.PhaseVerify); err != nil {
				return err
			}
			return c.phases.Transition(c.teamName, types.PhaseComplete, "verification passed")
		}
		if err := c.checkTransitionGovernance(ctx, types.PhaseVerify); err != nil {
			return err
		}
		return c.phases.Transition(c.teamName, types.PhaseFix, "verification found failures")
	}

	if err := c.executeTask(ctx, task, token, "verifier"); err != nil {
		if failErr := c.tasks.FailTask(c.teamName, task.ID, token, err.Error()); failErr != nil {
			c.logger.Warn("failed to mark verify task as failed", "team", c.teamName, "task", task.ID, "error", failErr)
		}
		return c.phases.Transition(c.teamName, types.PhaseFix, "verification failed")
	}

	if err := c.tasks.CompleteTask(c.teamName, task.ID, token, "verification completed"); err != nil {
		return err
	}

	allCompleted, err := c.phases.AllTasksCompleted(c.teamName)
	if err != nil {
		return err
	}
	if allCompleted {
		if err := c.checkTransitionGovernance(ctx, types.PhaseVerify); err != nil {
			return err
		}
		return c.phases.Transition(c.teamName, types.PhaseComplete, "verification passed")
	}

	if err := c.checkTransitionGovernance(ctx, types.PhaseVerify); err != nil {
		return err
	}
	return c.phases.Transition(c.teamName, types.PhaseFix, "verification requires fixes")
}

func (c *Coordinator) runFixPhase(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	tasks, err := c.tasks.ListTasks(c.teamName)
	if err != nil {
		return err
	}

	hasPending := false
	for _, t := range tasks {
		if err := ctx.Err(); err != nil {
			return err
		}

		if t.Status == types.TaskFailed {
			reset, err := c.tasks.ResetFailedTask(c.teamName, t.ID)
			if err != nil {
				return fmt.Errorf("reset task %s: %w", t.ID, err)
			}
			hasPending = hasPending || reset
		}
	}

	if !hasPending {
		if err := c.checkTransitionGovernance(ctx, types.PhaseFix); err != nil {
			return err
		}
		return c.phases.Transition(c.teamName, types.PhaseComplete, "no failed tasks to fix")
	}

	return c.phases.Transition(c.teamName, types.PhaseExec, "fix phase: retrying failed tasks")
}

func (c *Coordinator) workerLoop(ctx context.Context, workerID string) error {
	c.logger.Info("worker started", "worker", workerID, "team", c.teamName)
	governanceRetries := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		activeCount, activeWorkerCount := c.workerGovernanceSnapshot(workerID)
		if err := c.governance.Check(ctx, Decision{
			Type:              DecisionClaim,
			WorkerID:          workerID,
			TeamName:          c.teamName,
			WorkerActiveTasks: activeCount,
			ActiveWorkerCount: activeWorkerCount,
		}); err != nil {
			governanceRetries++
			if governanceRetries >= governanceRetryLimit {
				c.logger.Warn(
					"worker exiting after governance retries exhausted",
					"team", c.teamName,
					"worker", workerID,
					"retries", governanceRetries,
					"error", err,
				)
				return nil
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(governanceRetryDelay):
			}
			continue
		}

		governanceRetries = 0

		task, token, err := c.claimPendingTask(workerID)
		if err != nil {
			if errors.Is(err, ErrTaskNotClaimable) {
				return nil
			}
			return fmt.Errorf("claim next task: %w", err)
		}

		c.emitEvent("task_claimed", map[string]interface{}{
			"worker_id": workerID,
			"task_id":   task.ID,
			"task":      task.Subject,
		})

		c.notifyTaskAssignment(workerID, task)

		err = c.executeTask(ctx, task, token, workerID)
		if releaseErr := c.ownership.ReleaseTask(c.teamName, task.ID); releaseErr != nil {
			c.logger.Warn("failed to release task ownership", "worker", workerID, "task", task.ID, "error", releaseErr)
		}

		if err != nil {
			if failErr := c.tasks.FailTask(c.teamName, task.ID, token, err.Error()); failErr != nil {
				c.logger.Warn("failed to mark worker task as failed", "worker", workerID, "task", task.ID, "error", failErr)
			}
			c.emitEvent("task_failed", map[string]interface{}{
				"worker_id": workerID,
				"task_id":   task.ID,
				"error":     err.Error(),
			})
			continue
		}

		if err := c.tasks.CompleteTask(c.teamName, task.ID, token, "completed by "+workerID); err != nil {
			return err
		}

		c.emitEvent("task_completed", map[string]interface{}{
			"worker_id": workerID,
			"task_id":   task.ID,
		})
	}
}

func (c *Coordinator) claimPendingTask(workerID string) (*types.TeamTask, string, error) {
	tasks, err := c.tasks.ListTasks(c.teamName)
	if err != nil {
		return nil, "", err
	}

	workers := make([]AllocationWorkerInput, 0, c.cfg.TeamMaxWorkers())
	for i := range c.cfg.TeamMaxWorkers() {
		workers = append(workers, AllocationWorkerInput{
			Name: fmt.Sprintf("worker-%d", i),
			Role: c.cfg.AgentType(),
		})
	}

	if len(workers) <= 1 {
		return c.tasks.ClaimNextTask(c.teamName, workerID)
	}

	pendingTasks := make([]types.TeamTask, 0, len(tasks))
	currentAssignments := make([]AllocationDecision, 0, len(tasks))
	for i := range tasks {
		task := tasks[i]
		if task.Status == types.TaskInProgress && task.Claim != nil {
			currentAssignments = append(currentAssignments, AllocationDecision{Owner: task.Claim.WorkerID})
			continue
		}
		if task.Status != types.TaskPending {
			continue
		}

		blocked, blockedErr := c.tasks.isBlocked(c.teamName, &task)
		if blockedErr != nil {
			return nil, "", blockedErr
		}
		if blocked {
			continue
		}

		pendingTasks = append(pendingTasks, task)
	}

	if len(pendingTasks) <= 1 {
		return c.tasks.ClaimNextTask(c.teamName, workerID)
	}

	sort.Slice(pendingTasks, func(i, j int) bool {
		return pendingTasks[i].ID < pendingTasks[j].ID
	})

	for _, task := range pendingTasks {
		decision := ChooseTaskOwner(AllocationTaskInput{
			ID:          task.ID,
			Subject:     task.Subject,
			Description: task.Description,
			BlockedBy:   task.BlockedBy,
			DependsOn:   task.DependsOn,
			Status:      string(task.Status),
		}, workers, currentAssignments)
		if decision.Owner != workerID {
			continue
		}

		token, claimErr := c.tasks.ClaimTask(c.teamName, task.ID, workerID, task.Version)
		if claimErr != nil {
			if errors.Is(claimErr, ErrVersionConflict) || errors.Is(claimErr, ErrTaskNotClaimable) || errors.Is(claimErr, ErrTaskBlocked) {
				continue
			}
			return nil, "", claimErr
		}

		claimedTask, getErr := c.tasks.GetTask(c.teamName, task.ID)
		if getErr != nil {
			return nil, "", getErr
		}
		return claimedTask, token, nil
	}

	return c.tasks.ClaimNextTask(c.teamName, workerID)
}

func (c *Coordinator) notifyTaskAssignment(workerID string, task *types.TeamTask) {
	body := fmt.Sprintf("Task %s assigned to you: %s", task.ID, task.Subject)
	if err := c.mailbox.Send(c.teamName, "coordinator", workerID, body); err != nil {
		c.logger.Warn("failed to send task assignment notification",
			"worker", workerID, "task", task.ID, "error", err)
	}
}

func (c *Coordinator) workerGovernanceSnapshot(workerID string) (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	activeCount := 0
	if worker, ok := c.workers[workerID]; ok && worker.state.CurrentTask != "" {
		activeCount = 1
	}

	return activeCount, len(c.workers)
}

func (c *Coordinator) checkTransitionGovernance(ctx context.Context, phase types.TeamPhase) error {
	allTerminal, err := c.phases.AllTasksTerminal(c.teamName)
	if err != nil {
		return err
	}

	_, activeWorkerCount := c.workerGovernanceSnapshot("")

	return c.governance.Check(ctx, Decision{
		Type:                      DecisionTransition,
		Phase:                     phase,
		TeamName:                  c.teamName,
		ActiveWorkerCount:         activeWorkerCount,
		CurrentPhaseTasksTerminal: allTerminal,
	})
}

func (c *Coordinator) executeTask(ctx context.Context, task *types.TeamTask, token string, workerID string) error {
	issue := types.Issue{
		ID:          fmt.Sprintf("team-%s-%s", c.teamName, workerID),
		Identifier:  task.ID,
		Title:       task.Subject,
		Description: task.Description,
	}

	workDir, err := c.workspace.Create(ctx, issue)
	if err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}

	c.mu.Lock()
	wCtx, wCancel := context.WithCancel(ctx)
	c.workers[workerID] = &workerHandle{
		state: types.WorkerState{
			ID:            workerID,
			AgentType:     c.cfg.AgentType(),
			Status:        "working",
			CurrentTask:   task.ID,
			WorkDir:       workDir,
			StartedAt:     time.Now(),
			LastHeartbeat: time.Now(),
		},
		cancel: wCancel,
	}
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		if workerID == "coordinator" || workerID == "verifier" {
			delete(c.workers, workerID)
		} else if w, ok := c.workers[workerID]; ok {
			w.state.Status = "idle"
			w.state.CurrentTask = ""
			w.state.LastHeartbeat = time.Now()
		}
		c.mu.Unlock()
		wCancel()
	}()

	prompt := task.Description
	if injection, err := c.mailbox.DrainPending(c.teamName, workerID); err == nil && injection != "" {
		prompt += "\n" + injection
	}

	if len(task.FileOwnership) > 0 {
		conflicts, err := c.ownership.Claim(c.teamName, workerID, task.ID, task.FileOwnership)
		if err != nil {
			c.logger.Warn("file ownership claim failed", "worker", workerID, "error", err)
		}
		if len(conflicts) > 0 {
			c.logger.Warn("file ownership conflicts detected", "worker", workerID, "conflicts", len(conflicts))
		}
	}

	proc, err := c.runner.Start(wCtx, issue, workDir, prompt)
	if err != nil {
		return fmt.Errorf("start agent: %w", err)
	}

	c.mu.Lock()
	if w, ok := c.workers[workerID]; ok {
		w.process = proc
		w.state.PID = proc.PID
	}
	c.mu.Unlock()

	leaseCtx, leaseCancel := context.WithCancel(wCtx)
	defer leaseCancel()
	go c.renewLeaseLoop(leaseCtx, task.ID, token)

	// Write heartbeats so the health monitor can detect stale workers.
	go c.heartbeatLoop(leaseCtx, workerID, proc.PID)

	select {
	case err := <-proc.Done:
		if err != nil {
			return fmt.Errorf("agent process failed: %w", err)
		}
		return nil
	case <-wCtx.Done():
		_ = c.runner.Stop(proc)
		return wCtx.Err()
	}
}

func (c *Coordinator) renewLeaseLoop(ctx context.Context, taskID, token string) {
	leaseSeconds := c.cfg.TeamClaimLeaseSeconds()
	interval := time.Duration(leaseSeconds/3) * time.Second
	if interval < 1*time.Second {
		interval = 1 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			select {
			case <-ctx.Done():
				return
			default:
			}

			if err := c.tasks.RenewLease(c.teamName, taskID, token); err != nil {
				c.logger.Warn("lease renewal failed", "task", taskID, "error", err)
				return
			}
		}
	}
}

// heartbeatLoop periodically writes heartbeats for a worker so stale-worker
// detection works correctly during long-running tasks.
func (c *Coordinator) heartbeatLoop(ctx context.Context, workerID string, pid int) {
	// Use lease seconds / 3 as interval, similar to renewLeaseLoop.
	leaseSeconds := c.cfg.TeamClaimLeaseSeconds()
	interval := time.Duration(leaseSeconds/3) * time.Second
	if interval < 1*time.Second {
		interval = 1 * time.Second
	}

	writeHB := func() {
		hb := Heartbeat{
			WorkerID:  workerID,
			PID:       pid,
			Status:    "working",
			Timestamp: time.Now(),
		}
		if err := c.heartbeats.Write(c.teamName, hb); err != nil {
			c.logger.Warn("heartbeat write failed", "worker", workerID, "error", err)
		}
	}

	// Write an initial heartbeat immediately.
	writeHB()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			writeHB()
		}
	}
}

// checkForStaleWorkers uses the heartbeat monitor to detect workers that have
// stopped reporting. Called during the exec phase to identify problems early.
func (c *Coordinator) checkForStaleWorkers() []string {
	c.mu.Lock()
	workerIDs := make([]string, 0, len(c.workers))
	for id := range c.workers {
		workerIDs = append(workerIDs, id)
	}
	c.mu.Unlock()

	var stale []string
	for _, id := range workerIDs {
		isStale, err := c.heartbeats.IsStale(c.teamName, id)
		if err != nil {
			c.logger.Warn("failed to check heartbeat staleness", "worker", id, "error", err)
			continue
		}
		if isStale {
			stale = append(stale, id)
		}
	}

	if len(stale) > 0 {
		c.emitEvent("workers_stale", map[string]interface{}{
			"stale_workers": stale,
			"count":         len(stale),
		})
	}

	return stale
}

func (c *Coordinator) stopAllWorkers() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for id, w := range c.workers {
		if w.cancel != nil {
			w.cancel()
		}
		if w.process != nil {
			_ = c.runner.Stop(w.process)
		}
		w.state.Status = "stopped"
		c.logger.Info("stopped worker", "worker", id)
	}
}

// Cancel stops the team coordination.
func (c *Coordinator) Cancel() {
	if c.cancel != nil {
		c.cancel()
	}
}

// Status returns the current team status including phase and worker states.
func (c *Coordinator) Status() (*TeamStatus, error) {
	phase, err := c.phases.CurrentPhase(c.teamName)
	if err != nil {
		return nil, err
	}

	tasks, err := c.tasks.ListTasks(c.teamName)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	workerStates := make([]types.WorkerState, 0, len(c.workers))
	for _, worker := range c.workers {
		workerStates = append(workerStates, worker.state)
	}
	c.mu.Unlock()

	fixCount, _ := c.phases.FixLoopCount(c.teamName)

	return &TeamStatus{
		TeamName:     c.teamName,
		BoardIssueID: c.storeBoardIssueID(),
		Phase:        phase,
		Workers:      workerStates,
		Tasks:        tasks,
		FixLoopCount: fixCount,
	}, nil
}

func (c *Coordinator) storeBoardIssueID() string {
	manifest, err := c.store.LoadManifest(c.teamName)
	if err != nil || manifest == nil {
		return ""
	}
	return manifest.Config.BoardIssueID
}

// emitEvent sends a team event to the Events channel (non-blocking).
func (c *Coordinator) emitEvent(eventType string, data map[string]interface{}) {
	event := types.TeamEvent{
		Type:      eventType,
		TeamName:  c.teamName,
		Data:      data,
		Timestamp: time.Now(),
	}

	select {
	case c.Events <- event:
	default:
	}
}
