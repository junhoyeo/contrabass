package orchestrator

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"golang.org/x/sync/errgroup"

	"github.com/junhoyeo/symphony-charm/internal/agent"
	"github.com/junhoyeo/symphony-charm/internal/config"
	"github.com/junhoyeo/symphony-charm/internal/logging"
	"github.com/junhoyeo/symphony-charm/internal/tracker"
	"github.com/junhoyeo/symphony-charm/internal/types"
)

const defaultEventBufferSize = 256

type WorkspaceManager interface {
	Create(ctx context.Context, issue types.Issue) (string, error)
	Cleanup(ctx context.Context, issueID string) error
	CleanupAll(ctx context.Context) error
}

type ConfigProvider interface {
	GetConfig() *config.WorkflowConfig
}

type runEntry struct {
	issue       types.Issue
	attempt     types.RunAttempt
	process     *agent.AgentProcess
	cancel      context.CancelFunc
	workspace   string
	lastEventAt time.Time
}

type Stats struct {
	Running        int
	MaxAgents      int
	TotalTokensIn  int64
	TotalTokensOut int64
	StartTime      time.Time
	PollCount      int
}

type Orchestrator struct {
	tracker   tracker.Tracker
	workspace WorkspaceManager
	agent     agent.AgentRunner
	config    ConfigProvider
	logger    *log.Logger

	mu           sync.Mutex
	shutdownOnce sync.Once
	running      map[string]*runEntry
	backoff      []types.BackoffEntry
	events       chan OrchestratorEvent
	stats        Stats

	issueCache map[string]types.Issue
}

type runSignal struct {
	issueID string
	event   *types.AgentEvent
	done    bool
	err     error
}

func NewOrchestrator(
	tracker tracker.Tracker,
	workspace WorkspaceManager,
	agentRunner agent.AgentRunner,
	configProvider ConfigProvider,
	logger *log.Logger,
) *Orchestrator {
	if logger == nil {
		logger = logging.NewLogger(logging.LogOptions{Prefix: "orchestrator"})
	}

	cfg := &config.WorkflowConfig{}
	if configProvider != nil && configProvider.GetConfig() != nil {
		cfg = configProvider.GetConfig()
	}

	return &Orchestrator{
		tracker:    tracker,
		workspace:  workspace,
		agent:      agentRunner,
		config:     configProvider,
		logger:     logger,
		running:    make(map[string]*runEntry),
		backoff:    []types.BackoffEntry{},
		events:     make(chan OrchestratorEvent, defaultEventBufferSize),
		issueCache: make(map[string]types.Issue),
		stats: Stats{
			MaxAgents: cfg.MaxConcurrency(),
			StartTime: time.Now(),
		},
	}
}

func (o *Orchestrator) Events() <-chan OrchestratorEvent {
	return o.events
}

func (o *Orchestrator) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is nil")
	}

	pollInterval := time.Duration(o.currentConfig().PollIntervalMs()) * time.Millisecond
	if pollInterval <= 0 {
		pollInterval = time.Second
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	defer close(o.events)

	runSignals := make(chan runSignal, defaultEventBufferSize)
	supervisor, supervisorCtx := errgroup.WithContext(ctx)

	o.runCycle(supervisorCtx, supervisor, runSignals)

	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = o.gracefulShutdown(shutdownCtx)
			cancel()
			_ = supervisor.Wait()
			return nil
		case signal := <-runSignals:
			o.handleRunSignal(supervisorCtx, signal)
		case <-ticker.C:
			o.runCycle(supervisorCtx, supervisor, runSignals)
		}
	}
}

func (o *Orchestrator) runCycle(ctx context.Context, supervisor *errgroup.Group, runSignals chan<- runSignal) {
	cfg := o.currentConfig()

	o.mu.Lock()
	o.stats.MaxAgents = cfg.MaxConcurrency()
	o.stats.PollCount++
	o.mu.Unlock()

	o.reconcileRunning(ctx, cfg)
	o.detectStalledRuns(ctx, cfg)

	issues, err := o.tracker.FetchIssues(ctx)
	if err != nil {
		logging.LogOrchestratorEvent(o.logger, "fetch_issues_failed", "err", err)
		o.emitStatusUpdate()
		return
	}

	issuesByID := make(map[string]types.Issue, len(issues))
	for _, issue := range issues {
		issuesByID[issue.ID] = issue
	}

	o.mu.Lock()
	for id, issue := range issuesByID {
		o.issueCache[id] = issue
	}
	o.mu.Unlock()

	o.dispatchReadyBackoff(ctx, supervisorCtxOr(ctx), cfg, issuesByID, supervisor, runSignals)
	o.dispatchUnclaimedIssues(ctx, supervisorCtxOr(ctx), cfg, issues, supervisor, runSignals)
	o.emitStatusUpdate()
}

func supervisorCtxOr(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

func (o *Orchestrator) dispatchReadyBackoff(
	ctx context.Context,
	watchCtx context.Context,
	cfg *config.WorkflowConfig,
	issuesByID map[string]types.Issue,
	supervisor *errgroup.Group,
	runSignals chan<- runSignal,
) {
	if !o.canDispatch(cfg.MaxConcurrency()) {
		return
	}

	now := time.Now()
	ready := make([]types.BackoffEntry, 0)

	o.mu.Lock()
	remaining := make([]types.BackoffEntry, 0, len(o.backoff))
	for _, entry := range o.backoff {
		if entry.RetryAt.After(now) {
			remaining = append(remaining, entry)
			continue
		}
		ready = append(ready, entry)
	}
	o.backoff = remaining
	o.mu.Unlock()

	for _, backoffEntry := range ready {
		if !o.canDispatch(cfg.MaxConcurrency()) {
			o.requeueBackoff(backoffEntry)
			continue
		}

		issue, ok := issuesByID[backoffEntry.IssueID]
		if !ok {
			o.mu.Lock()
			issue, ok = o.issueCache[backoffEntry.IssueID]
			o.mu.Unlock()
		}
		if !ok {
			o.enqueueContinuation(backoffEntry.IssueID, backoffEntry.Attempt, "issue details unavailable")
			continue
		}

		issue.State = types.RetryQueued
		o.dispatchIssue(ctx, watchCtx, cfg, issue, backoffEntry.Attempt, supervisor, runSignals)
	}
}

func (o *Orchestrator) dispatchUnclaimedIssues(
	ctx context.Context,
	watchCtx context.Context,
	cfg *config.WorkflowConfig,
	issues []types.Issue,
	supervisor *errgroup.Group,
	runSignals chan<- runSignal,
) {
	for _, issue := range issues {
		if issue.State != types.Unclaimed {
			continue
		}
		if !o.canDispatch(cfg.MaxConcurrency()) {
			return
		}
		if o.isManagedIssue(issue.ID) {
			continue
		}

		o.dispatchIssue(ctx, watchCtx, cfg, issue, 1, supervisor, runSignals)
	}
}

func (o *Orchestrator) dispatchIssue(
	ctx context.Context,
	watchCtx context.Context,
	cfg *config.WorkflowConfig,
	issue types.Issue,
	attemptNumber int,
	supervisor *errgroup.Group,
	runSignals chan<- runSignal,
) {
	if issue.ID == "" {
		return
	}

	if err := o.claimIssue(ctx, issue); err != nil {
		logging.LogIssueEvent(o.logger, issue.ID, "claim_failed", "err", err)
		o.enqueueContinuation(issue.ID, attemptNumber, err.Error())
		return
	}

	runAttempt := types.RunAttempt{
		IssueID:   issue.ID,
		Attempt:   attemptNumber,
		Phase:     types.PreparingWorkspace,
		StartTime: time.Now(),
	}

	workspacePath, err := o.workspace.Create(ctx, issue)
	if err != nil {
		logging.LogIssueEvent(o.logger, issue.ID, "workspace_create_failed", "err", err)
		o.releaseClaimAndQueueContinuation(ctx, issue.ID, runAttempt.Attempt, err)
		return
	}

	if phaseErr := TransitionRunPhase(runAttempt.Phase, types.BuildingPrompt); phaseErr == nil {
		runAttempt.Phase = types.BuildingPrompt
	}

	prompt, err := config.RenderPrompt(cfg.PromptTemplate, issue)
	if err != nil {
		_ = o.workspace.Cleanup(ctx, issue.ID)
		logging.LogIssueEvent(o.logger, issue.ID, "prompt_build_failed", "err", err)
		o.releaseClaimAndQueueContinuation(ctx, issue.ID, runAttempt.Attempt, err)
		return
	}

	if issueTransitionErr := TransitionIssueState(types.Claimed, types.Running); issueTransitionErr == nil {
		if updateErr := o.tracker.UpdateIssueState(ctx, issue.ID, types.Running); updateErr != nil {
			logging.LogIssueEvent(o.logger, issue.ID, "update_running_failed", "err", updateErr)
		}
	}

	if phaseErr := TransitionRunPhase(runAttempt.Phase, types.LaunchingAgentProcess); phaseErr == nil {
		runAttempt.Phase = types.LaunchingAgentProcess
	}

	runCtx, cancel := context.WithCancel(ctx)
	process, err := o.agent.Start(runCtx, issue, workspacePath, prompt)
	if err != nil {
		cancel()
		_ = o.workspace.Cleanup(ctx, issue.ID)
		logging.LogAgentEvent(o.logger, issue.ID, "start_failed", "err", err)
		o.enqueueBackoffFromRunning(ctx, issue, runAttempt, err)
		return
	}

	if phaseErr := TransitionRunPhase(runAttempt.Phase, types.InitializingSession); phaseErr == nil {
		runAttempt.Phase = types.InitializingSession
	}

	runAttempt.PID = process.PID
	runAttempt.SessionID = process.SessionID
	runAttempt.LastEvent = "agent_started"

	entry := &runEntry{
		issue:       issue,
		attempt:     runAttempt,
		process:     process,
		cancel:      cancel,
		workspace:   workspacePath,
		lastEventAt: time.Now(),
	}

	o.mu.Lock()
	o.running[issue.ID] = entry
	o.issueCache[issue.ID] = issue
	o.stats.Running = len(o.running)
	o.mu.Unlock()

	logging.LogAgentEvent(
		o.logger,
		issue.ID,
		"started",
		"attempt", runAttempt.Attempt,
		"pid", process.PID,
		"session_id", process.SessionID,
	)

	o.emitEvent(OrchestratorEvent{
		Type:    EventAgentStarted,
		IssueID: issue.ID,
		Data: AgentStarted{
			Attempt:   runAttempt.Attempt,
			PID:       process.PID,
			SessionID: process.SessionID,
			Workspace: workspacePath,
		},
	})

	supervisor.Go(func() error {
		o.watchProcess(watchCtx, issue.ID, process, runSignals)
		return nil
	})
}

func (o *Orchestrator) claimIssue(ctx context.Context, issue types.Issue) error {
	if issue.ID == "" {
		return errors.New("issue id is required")
	}

	if transitionErr := TransitionIssueState(issue.State, types.Claimed); transitionErr != nil {
		return transitionErr
	}

	if err := o.tracker.ClaimIssue(ctx, issue.ID); err != nil {
		return err
	}

	if err := o.tracker.UpdateIssueState(ctx, issue.ID, types.Claimed); err != nil {
		_ = o.tracker.ReleaseIssue(ctx, issue.ID)
		return err
	}

	logging.LogIssueEvent(o.logger, issue.ID, "claimed")
	return nil
}

func (o *Orchestrator) watchProcess(
	ctx context.Context,
	issueID string,
	process *agent.AgentProcess,
	runSignals chan<- runSignal,
) {
	events := process.Events
	done := process.Done

	for events != nil || done != nil {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if event.Timestamp.IsZero() {
				event.Timestamp = time.Now()
			}
			eventCopy := event
			if !o.sendRunSignal(ctx, runSignals, runSignal{issueID: issueID, event: &eventCopy}) {
				return
			}
		case err, ok := <-done:
			if !ok {
				err = nil
			}
			o.sendRunSignal(ctx, runSignals, runSignal{issueID: issueID, done: true, err: err})
			return
		}
	}
}

func (o *Orchestrator) sendRunSignal(ctx context.Context, runSignals chan<- runSignal, signal runSignal) bool {
	select {
	case <-ctx.Done():
		return false
	case runSignals <- signal:
		return true
	}
}
