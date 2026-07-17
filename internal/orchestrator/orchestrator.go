package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"golang.org/x/sync/errgroup"

	"github.com/junhoyeo/contrabass/internal/agent"
	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/logging"
	"github.com/junhoyeo/contrabass/internal/timeline"
	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/types"
)

// ErrAgentNotRunning is returned by StopAgent when no managed run exists for
// the given issue ID — typically because the agent already finished or the
// caller's snapshot is stale.
var ErrAgentNotRunning = errors.New("agent not running")

type WorkspaceManager interface {
	Create(ctx context.Context, issue types.Issue) (string, error)
	Cleanup(ctx context.Context, issueID string) error
	CleanupAll(ctx context.Context) error
}

type ConfigProvider interface {
	GetConfig() *config.WorkflowConfig
}

type runEntry struct {
	issue            types.Issue
	attempt          types.RunAttempt
	process          *agent.AgentProcess
	cancel           context.CancelFunc
	workspace        string
	lastEventAt      time.Time
	lastActivityAt   time.Time
	lastActivityKind string
	lastHeartbeatAt  time.Time
	stageState       agentStageState
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
	timeline  *timeline.Store

	suppressLinearLegacyComments bool

	mu           sync.Mutex
	shutdownOnce sync.Once
	running      map[string]*runEntry
	backoff      []types.BackoffEntry
	paused       map[string]string
	events       chan OrchestratorEvent
	eventsClosed atomic.Bool
	stats        Stats

	issueCache          map[string]types.Issue
	issueCacheOrder     []string
	issueCacheSize      int
	runSignalBufferSize int

	// recoveredSet tracks issue IDs for which orphan_claim_recovered has
	// already been logged since the last process start. It is populated by
	// recoverOrphanedClaims and is never cleared, so each issue is logged at
	// most once per orchestrator lifetime.
	recoveredSet map[string]struct{}

	// warnedCycles tracks BlockedBy cycle signatures already warned about,
	// so each unique cycle is surfaced at most once per orchestrator
	// lifetime instead of once per poll tick.
	warnedCycles map[string]struct{}

	buildInfo BuildInfo
}

func (o *Orchestrator) SetWorkflowTimeline(store *timeline.Store, suppressLinearLegacyComments bool) {
	o.timeline = store
	o.suppressLinearLegacyComments = suppressLinearLegacyComments
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
	policy := runtimePolicyFromConfig(cfg)

	return &Orchestrator{
		tracker:             tracker,
		workspace:           workspace,
		agent:               agentRunner,
		config:              configProvider,
		logger:              logger,
		running:             make(map[string]*runEntry),
		backoff:             []types.BackoffEntry{},
		paused:              make(map[string]string),
		events:              make(chan OrchestratorEvent, policy.eventBufferSize),
		issueCache:          make(map[string]types.Issue),
		issueCacheSize:      policy.issueCacheSize,
		runSignalBufferSize: policy.runSignalBufferSize,
		recoveredSet:        make(map[string]struct{}),
		warnedCycles:        make(map[string]struct{}),
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

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	defer func() {
		o.mu.Lock()
		if !o.eventsClosed.Load() {
			o.eventsClosed.Store(true)
			close(o.events)
		}
		o.mu.Unlock()
	}()

	runSignalBufferSize := o.runSignalBufferSize
	if runSignalBufferSize <= 0 {
		runSignalBufferSize = runtimePolicyFromConfig(nil).runSignalBufferSize
	}
	runSignals := make(chan runSignal, runSignalBufferSize)
	supervisor, supervisorCtx := errgroup.WithContext(ctx)

	o.runCycle(supervisorCtx, supervisor, runSignals)

	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), o.runtimePolicy().runShutdownTimeout)
			if err := o.gracefulShutdown(shutdownCtx); err != nil {
				o.logger.Warn("orchestrator", "event", "graceful_shutdown_failed", "err", err)
			}
			cancel()
			if err := supervisor.Wait(); err != nil {
				o.logger.Warn("orchestrator", "event", "supervisor_wait_failed", "err", err)
			}
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
		o.putIssueCacheLocked(id, issue)
	}
	o.mu.Unlock()

	openIDs := buildOpenIDSet(issues)

	o.clearRequeuedPaused(issues)
	o.warnBlockedByCycles(issues, openIDs)
	o.dispatchReadyBackoff(ctx, supervisorCtxOr(ctx), cfg, issuesByID, openIDs, supervisor, runSignals)
	recoveredAttempts := o.recoverOrphanedClaims(issues)
	o.dispatchUnclaimedIssues(ctx, supervisorCtxOr(ctx), cfg, issues, openIDs, recoveredAttempts, supervisor, runSignals)
	o.releaseBlockedRunning(ctx, issuesByID, openIDs)
	o.emitStatusUpdate()
}

// buildOpenIDSet returns the set of issue identifiers visible in the snapshot
// (i.e. not in a tracker-terminal state). Used by dispatch and revalidation
// passes to test BlockedBy membership consistently within a tick.
func buildOpenIDSet(issues []types.Issue) map[string]struct{} {
	openIDs := make(map[string]struct{}, len(issues))
	for _, iss := range issues {
		if iss.Identifier != "" {
			openIDs[iss.Identifier] = struct{}{}
		}
	}
	return openIDs
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
	openIDs map[string]struct{},
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

		// Retries obey the same BlockedBy gate as fresh dispatch; the entry
		// is requeued with its attempt preserved so retry accounting is not
		// reset by a blocker that appeared while the issue sat in backoff.
		if unresolved := unresolvedBlockers(issue.BlockedBy, openIDs); len(unresolved) > 0 {
			logging.LogIssueEvent(o.logger, issue.ID,
				"retry_skipped_blocked_by",
				"blockers", strings.Join(unresolved, ","))
			o.requeueBackoff(backoffEntry)
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
	openIDs map[string]struct{},
	recoveredAttempts map[string]int,
	supervisor *errgroup.Group,
	runSignals chan<- runSignal,
) {
	for _, issue := range issues {
		if issue.State != types.Unclaimed {
			continue
		}
		if unresolved := unresolvedBlockers(issue.BlockedBy, openIDs); len(unresolved) > 0 {
			logging.LogIssueEvent(o.logger, issue.ID,
				"dispatch_skipped_blocked_by",
				"blockers", strings.Join(unresolved, ","))
			continue
		}
		if !o.canDispatch(cfg.MaxConcurrency()) {
			return
		}
		if o.isManagedIssue(issue.ID) {
			continue
		}

		attemptNumber := 1
		if recoveredAttempt, recovered := recoveredAttempts[issue.ID]; recovered {
			attemptNumber = recoveredAttempt
		}
		o.dispatchIssue(ctx, watchCtx, cfg, issue, attemptNumber, supervisor, runSignals)
	}
}

// recoverOrphanedClaims overrides the state of any issue that is Claimed in
// the tracker snapshot but absent from the live managed-issues set. This
// handles the restart/crash case where Linear still shows "In Progress" but
// no agent is running: the issue is treated as Unclaimed so it re-enters the
// dispatch queue on this tick.
//
// The orphan_claim_recovered log event is emitted at most once per issue per
// orchestrator lifetime (tracked via recoveredSet) to prevent log spam when
// dispatch is deferred across multiple ticks (e.g. max concurrency reached).
func (o *Orchestrator) recoverOrphanedClaims(issues []types.Issue) map[string]int {
	recoveredAttempts := make(map[string]int)
	o.mu.Lock()
	managed := make(map[string]struct{}, len(o.running)+len(o.paused))
	for id := range o.running {
		managed[id] = struct{}{}
	}
	for id := range o.paused {
		managed[id] = struct{}{}
	}
	o.mu.Unlock()

	for i, issue := range issues {
		if issue.State != types.Claimed {
			continue
		}
		if _, ok := managed[issue.ID]; ok {
			continue
		}
		issues[i].State = types.Unclaimed
		// A durable tracker claim proves that a prior run already started. The
		// replacement agent cannot resume its dead process, but it must be
		// represented as a recovery attempt rather than a new attempt 1.
		recoveredAttempts[issue.ID] = 2

		o.mu.Lock()
		_, alreadyLogged := o.recoveredSet[issue.ID]
		if !alreadyLogged {
			o.recoveredSet[issue.ID] = struct{}{}
		}
		o.mu.Unlock()

		if !alreadyLogged {
			logging.LogIssueEvent(o.logger, issue.ID, "orphan_claim_recovered",
				"identifier", issue.Identifier)
		}
	}

	return recoveredAttempts
}

// releaseBlockedRunning re-evaluates BlockedBy for every currently-managed
// issue against the latest snapshot. If a previously-absent blocker now
// appears in the open candidate set, the running agent is gracefully stopped
// and the issue's tracker state is reverted to Unclaimed so it re-enters the
// dispatch queue once its blockers resolve.
func (o *Orchestrator) releaseBlockedRunning(
	ctx context.Context,
	issuesByID map[string]types.Issue,
	openIDs map[string]struct{},
) {
	o.mu.Lock()
	managed := make([]string, 0, len(o.running))
	for id := range o.running {
		managed = append(managed, id)
	}
	o.mu.Unlock()

	for _, id := range managed {
		issue, ok := issuesByID[id]
		if !ok {
			continue
		}
		unresolved := unresolvedBlockers(issue.BlockedBy, openIDs)
		if len(unresolved) == 0 {
			continue
		}

		logging.LogIssueEvent(o.logger, id,
			"running_released_blocked_by",
			"blockers", strings.Join(unresolved, ","))

		if !o.stopRun(ctx, id) {
			// Termination unconfirmed: keep the claim so the issue cannot be
			// re-dispatched onto a workspace the old process may still write.
			// The blocker is still unresolved next tick, so stop is retried.
			continue
		}

		if err := o.tracker.UpdateIssueState(ctx, id, types.Unclaimed); err != nil {
			logging.LogIssueEvent(o.logger, id,
				"running_release_state_revert_failed",
				"err", err)
		}
		if err := o.tracker.ReleaseIssue(ctx, id); err != nil {
			logging.LogIssueEvent(o.logger, id,
				"running_release_claim_release_failed",
				"err", err)
		}
	}
}

// StopAgent gracefully terminates the agent run for issueID, removes the
// entry from the running map, and releases the tracker claim so the issue
// returns to a queued state. Returns ErrAgentNotRunning if no managed run
// exists for the given ID.
func (o *Orchestrator) StopAgent(ctx context.Context, issueID string) error {
	o.mu.Lock()
	_, ok := o.running[issueID]
	o.mu.Unlock()
	if !ok {
		return ErrAgentNotRunning
	}

	logging.LogIssueEvent(o.logger, issueID, "stop_agent_requested")

	if !o.stopRun(ctx, issueID) {
		// Keep the claim: releasing it while the process may still be alive
		// would let the next tick start a second agent in the same workspace.
		return fmt.Errorf("agent for %s did not exit within the stop grace window; claim retained", issueID)
	}

	if err := o.tracker.UpdateIssueState(ctx, issueID, types.Released); err != nil {
		logging.LogIssueEvent(o.logger, issueID, "stop_agent_state_release_failed", "err", err)
	}
	if err := o.tracker.ReleaseIssue(ctx, issueID); err != nil {
		logging.LogIssueEvent(o.logger, issueID, "stop_agent_release_failed", "err", err)
	}

	o.emitStatusUpdate()
	return nil
}

// unresolvedBlockers returns the subset of blockers that still appear in the
// open candidate set (i.e. issues currently visible to the orchestrator that
// have not reached a tracker-terminal state). An empty result means the issue
// is free to dispatch.
func unresolvedBlockers(blockedBy []string, openIDs map[string]struct{}) []string {
	if len(blockedBy) == 0 || len(openIDs) == 0 {
		return nil
	}
	unresolved := make([]string, 0, len(blockedBy))
	for _, b := range blockedBy {
		if _, blocked := openIDs[b]; blocked {
			unresolved = append(unresolved, b)
		}
	}
	return unresolved
}

// detectBlockedByCycles returns the identifier cycles (including
// self-references) in the BlockedBy graph of the fetched snapshot. Only edges
// into the open candidate set matter — those are the ones the dispatch gate
// tests — and each cycle's members are sorted for a stable signature.
func detectBlockedByCycles(issues []types.Issue, openIDs map[string]struct{}) [][]string {
	adjacency := make(map[string][]string, len(issues))
	for _, issue := range issues {
		if issue.Identifier == "" {
			continue
		}
		for _, blocker := range issue.BlockedBy {
			if _, open := openIDs[blocker]; open {
				adjacency[issue.Identifier] = append(adjacency[issue.Identifier], blocker)
			}
		}
	}

	nodes := make([]string, 0, len(adjacency))
	for node := range adjacency {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)

	const (
		unvisited = 0
		inStack   = 1
		finished  = 2
	)
	state := make(map[string]int, len(adjacency))
	stack := make([]string, 0, len(adjacency))
	cycles := make([][]string, 0)

	var visit func(node string)
	visit = func(node string) {
		state[node] = inStack
		stack = append(stack, node)
		for _, next := range adjacency[node] {
			switch state[next] {
			case unvisited:
				visit(next)
			case inStack:
				// Back edge: the cycle is the stack suffix starting at next.
				start := len(stack) - 1
				for start > 0 && stack[start] != next {
					start--
				}
				cycle := append([]string(nil), stack[start:]...)
				sort.Strings(cycle)
				cycles = append(cycles, cycle)
			}
		}
		stack = stack[:len(stack)-1]
		state[node] = finished
	}

	for _, node := range nodes {
		if state[node] == unvisited {
			visit(node)
		}
	}
	return cycles
}

// warnBlockedByCycles surfaces circular (or self-referencing) BlockedBy
// dependencies, whose members the dispatch gate would otherwise skip silently
// forever. Cycle members are deliberately NOT force-dispatched: running work
// whose declared prerequisites are unsatisfiable risks executing it in the
// wrong order, so the safer semantic is to keep skipping them and alert a
// human to fix the dependency data. Each unique cycle is warned once per
// orchestrator lifetime (tracked via warnedCycles).
func (o *Orchestrator) warnBlockedByCycles(issues []types.Issue, openIDs map[string]struct{}) {
	for _, cycle := range detectBlockedByCycles(issues, openIDs) {
		signature := strings.Join(cycle, ",")

		o.mu.Lock()
		_, seen := o.warnedCycles[signature]
		if !seen {
			o.warnedCycles[signature] = struct{}{}
		}
		o.mu.Unlock()
		if seen {
			continue
		}

		o.logger.Warn("blocked_by_cycle_detected",
			"members", signature,
			"action", "members stay undispatched until the cycle is broken in the tracker")
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

	o.recordTimelineRun(ctx, issue, attemptNumber, timeline.NodeStatusStarted, time.Now(), time.Time{})
	if err := o.claimIssue(ctx, issue); err != nil {
		logging.LogIssueEvent(o.logger, issue.ID, "claim_failed", "err", err)
		o.recordTimelineNode(ctx, issue,
			preAgentTimelineAttempt(issue, attemptNumber, types.PreparingWorkspace, err),
			"claim-failed", timeline.NodeStatusFailed, "Issue claim failed", "Contrabass could not claim the issue before starting an agent.", err.Error(), true)
		o.enqueueContinuation(issue.ID, attemptNumber, err.Error())
		return
	}

	runAttempt := types.RunAttempt{
		IssueID:         issue.ID,
		IssueIdentifier: issue.Identifier,
		Attempt:         attemptNumber,
		Phase:           types.PreparingWorkspace,
		StartTime:       time.Now(),
	}

	workspacePath, err := o.workspace.Create(ctx, issue)
	if err != nil {
		logging.LogIssueEvent(o.logger, issue.ID, "workspace_create_failed", "err", err)
		o.recordTimelineNode(ctx, issue, runAttempt,
			"workspace-failed", timeline.NodeStatusFailed, "Workspace creation failed", "Contrabass could not create the issue workspace.", err.Error(), true)
		o.releaseClaimAndQueueContinuation(ctx, issue.ID, runAttempt.Attempt, err)
		return
	}
	runAttempt.WorkspacePath = workspacePath
	sha, err := workspaceHeadSHAWithTimeout(ctx, workspacePath, o.runtimePolicy().gitCommandTimeout)
	if err != nil {
		o.logger.Warn("claim_head_sha_unavailable",
			"issue_id", issue.ID, "err", err)
		sha = ""
	}
	runAttempt.ClaimHeadSha = sha

	if phaseErr := TransitionRunPhase(runAttempt.Phase, types.BuildingPrompt); phaseErr == nil {
		runAttempt.Phase = types.BuildingPrompt
	} else {
		logging.LogIssueEvent(o.logger, issue.ID, "phase_transition_failed", "from", runAttempt.Phase.String(), "to", types.BuildingPrompt.String(), "err", phaseErr)
	}

	prompt, err := config.RenderPrompt(cfg.PromptTemplate, issue)
	if err != nil {
		if cleanupErr := o.workspace.Cleanup(ctx, issue.ID); cleanupErr != nil {
			logging.LogIssueEvent(o.logger, issue.ID, "workspace_cleanup_failed", "stage", "prompt_render", "err", cleanupErr)
		}
		logging.LogIssueEvent(o.logger, issue.ID, "prompt_build_failed", "err", err)
		o.recordTimelineNode(ctx, issue, runAttempt,
			"prompt-failed", timeline.NodeStatusFailed, "Prompt render failed", "Contrabass could not render the prompt before agent start.", err.Error(), true)
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
		if cleanupErr := o.workspace.Cleanup(ctx, issue.ID); cleanupErr != nil {
			logging.LogIssueEvent(o.logger, issue.ID, "workspace_cleanup_failed", "stage", "agent_start", "err", cleanupErr)
		}
		logging.LogAgentEvent(o.logger, issue.ID, "start_failed", "err", err)
		o.recordTimelineNode(ctx, issue, runAttempt,
			"agent-start-failed", timeline.NodeStatusFailed, "Agent start failed", "Contrabass could not start the agent process.", err.Error(), true)
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
	o.putIssueCacheLocked(issue.ID, issue)
	o.stats.Running = len(o.running)
	eventTimestamp := time.Now()
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
		Type:      EventAgentStarted,
		IssueID:   issue.ID,
		Timestamp: eventTimestamp,
		Data: AgentStarted{
			IssueIdentifier: issue.Identifier,
			IssueURL:        issue.URL,
			Attempt:         runAttempt.Attempt,
			PID:             process.PID,
			SessionID:       process.SessionID,
			Workspace:       workspacePath,
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
		if releaseErr := o.tracker.ReleaseIssue(ctx, issue.ID); releaseErr != nil {
			logging.LogIssueEvent(o.logger, issue.ID, "claim_rollback_release_failed", "err", releaseErr)
		}
		return err
	}

	logging.LogIssueEvent(o.logger, issue.ID, "claimed")
	return nil
}

// workspaceHeadSHA returns the 40-char HEAD SHA of the workspace's current
// branch. Caller logs/handles the error and falls back to an empty SHA, which
// the verifier will treat as "unknown".
func workspaceHeadSHA(ctx context.Context, workspace string) (string, error) {
	return workspaceHeadSHAWithTimeout(ctx, workspace, runtimePolicyFromConfig(nil).gitCommandTimeout)
}

func workspaceHeadSHAWithTimeout(ctx context.Context, workspace string, timeout time.Duration) (string, error) {
	return workspaceRevParse(ctx, workspace, "HEAD", timeout)
}

// verifyBranchAdvanced compares the workspace branch's current HEAD against
// the claim-time SHA. Git lookup failures fail open so a transient verifier
// issue does not discard an otherwise successful run.
func verifyBranchAdvanced(ctx context.Context, workspace, branch, claimHead string) (bool, string, error) {
	return verifyBranchAdvancedWithTimeout(ctx, workspace, branch, claimHead, runtimePolicyFromConfig(nil).gitCommandTimeout)
}

func verifyBranchAdvancedWithTimeout(ctx context.Context, workspace, branch, claimHead string, timeout time.Duration) (bool, string, error) {
	if strings.TrimSpace(claimHead) == "" {
		return true, "no_claim_head", nil
	}

	rev := strings.TrimSpace(branch)
	if rev == "" {
		rev = "HEAD"
	}

	currentHead, err := workspaceRevParse(ctx, workspace, rev, timeout)
	if err != nil {
		return true, "git_error", err
	}
	if currentHead == strings.TrimSpace(claimHead) {
		return false, "branch_unchanged", nil
	}

	return true, "", nil
}

func workspaceRevParse(ctx context.Context, workspace, rev string, timeout time.Duration) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	output, err := exec.CommandContext(cmdCtx, "git", "-C", workspace, "rev-parse", rev).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
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

// putIssueCacheLocked inserts or updates an entry in the issue cache.
// If the cache exceeds the configured limit, the oldest entries are evicted.
// Caller must hold o.mu.
func (o *Orchestrator) putIssueCacheLocked(id string, issue types.Issue) {
	if _, exists := o.issueCache[id]; exists {
		o.issueCache[id] = issue
		return
	}
	maxSize := o.issueCacheSize
	if maxSize <= 0 {
		maxSize = runtimePolicyFromConfig(nil).issueCacheSize
	}
	for len(o.issueCache) >= maxSize && len(o.issueCacheOrder) > 0 {
		oldest := o.issueCacheOrder[0]
		o.issueCacheOrder = o.issueCacheOrder[1:]
		delete(o.issueCache, oldest)
	}
	o.issueCache[id] = issue
	o.issueCacheOrder = append(o.issueCacheOrder, id)
}
