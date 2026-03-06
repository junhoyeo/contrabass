package team

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/junhoyeo/contrabass/internal/agent"
	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/junhoyeo/contrabass/internal/workspace"
)

// Coordinator manages a team of workers executing a staged pipeline.
type Coordinator struct {
	teamName  string
	cfg       *config.WorkflowConfig
	store     *Store
	paths     *Paths
	tasks     *TaskRegistry
	mailbox   *Mailbox
	ownership *OwnershipRegistry
	phases    *PhaseMachine
	workspace *workspace.Manager
	runner    agent.AgentRunner
	logger    *slog.Logger

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
	mailbox := NewMailbox(store, paths)
	ownership := NewOwnershipRegistry(store, paths)
	phases := NewPhaseMachine(store, tasks, cfg.TeamMaxFixLoops())

	return &Coordinator{
		teamName:  teamName,
		cfg:       cfg,
		store:     store,
		paths:     paths,
		tasks:     tasks,
		mailbox:   mailbox,
		ownership: ownership,
		phases:    phases,
		workspace: ws,
		runner:    runner,
		logger:    logger,
		workers:   make(map[string]*workerHandle),
		Events:    make(chan types.TeamEvent, 100),
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
		"team_name":   c.teamName,
		"max_workers": teamCfg.MaxWorkers,
	})
	return nil
}

// Run starts the team coordination loop.
func (c *Coordinator) Run(ctx context.Context, tasks []types.TeamTask) error {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	defer cancel()

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
			_ = c.phases.Cancel(c.teamName, "context cancelled")
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
			_ = c.phases.Transition(c.teamName, types.PhaseFailed, err.Error())
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
		return c.phases.Transition(c.teamName, next, reason+": no tasks")
	}

	if err := c.executeTask(ctx, task, token, "coordinator"); err != nil {
		_ = c.tasks.FailTask(c.teamName, task.ID, token, err.Error())
		return err
	}

	if err := c.tasks.CompleteTask(c.teamName, task.ID, token, "completed by coordinator"); err != nil {
		return err
	}

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

	if err := g.Wait(); err != nil && gCtx.Err() == nil {
		allDone, doneErr := c.phases.AllTasksTerminal(c.teamName)
		if doneErr != nil {
			return doneErr
		}
		if !allDone {
			return err
		}
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
			return c.phases.Transition(c.teamName, types.PhaseComplete, "verification passed")
		}
		return c.phases.Transition(c.teamName, types.PhaseFix, "verification found failures")
	}

	if err := c.executeTask(ctx, task, token, "verifier"); err != nil {
		_ = c.tasks.FailTask(c.teamName, task.ID, token, err.Error())
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
		return c.phases.Transition(c.teamName, types.PhaseComplete, "verification passed")
	}

	return c.phases.Transition(c.teamName, types.PhaseFix, "verification requires fixes")
}

func (c *Coordinator) runFixPhase(ctx context.Context) error {
	_ = ctx
	tasks, err := c.tasks.ListTasks(c.teamName)
	if err != nil {
		return err
	}

	hasPending := false
	for _, t := range tasks {
		if t.Status == types.TaskFailed {
			t.Status = types.TaskPending
			t.Claim = nil
			t.Version++
			t.UpdatedAt = time.Now()
			if err := c.store.WriteJSON(c.paths.TaskPath(c.teamName, t.ID), &t); err != nil {
				return fmt.Errorf("reset task %s: %w", t.ID, err)
			}
			hasPending = true
		}
	}

	if !hasPending {
		return c.phases.Transition(c.teamName, types.PhaseComplete, "no failed tasks to fix")
	}

	return c.phases.Transition(c.teamName, types.PhaseExec, "fix phase: retrying failed tasks")
}

func (c *Coordinator) workerLoop(ctx context.Context, workerID string) error {
	c.logger.Info("worker started", "worker", workerID, "team", c.teamName)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		task, token, err := c.tasks.ClaimNextTask(c.teamName, workerID)
		if err != nil {
			return nil
		}

		c.emitEvent("task_claimed", map[string]interface{}{
			"worker_id": workerID,
			"task_id":   task.ID,
			"task":      task.Subject,
		})

		err = c.executeTask(ctx, task, token, workerID)
		if releaseErr := c.ownership.ReleaseTask(c.teamName, task.ID); releaseErr != nil {
			c.logger.Warn("failed to release task ownership", "worker", workerID, "task", task.ID, "error", releaseErr)
		}

		if err != nil {
			_ = c.tasks.FailTask(c.teamName, task.ID, token, err.Error())
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

func (c *Coordinator) executeTask(ctx context.Context, task *types.TeamTask, token, workerID string) error {
	_ = token
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
		if w, ok := c.workers[workerID]; ok {
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
	interval := time.Duration(c.cfg.TeamClaimLeaseSeconds()/3) * time.Second
	if interval < 10*time.Second {
		interval = 10 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.tasks.RenewLease(c.teamName, taskID, token); err != nil {
				c.logger.Warn("lease renewal failed", "task", taskID, "error", err)
				return
			}
		}
	}
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
		Phase:        phase,
		Workers:      workerStates,
		Tasks:        tasks,
		FixLoopCount: fixCount,
	}, nil
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
