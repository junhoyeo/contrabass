package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
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

const defaultEventBufferSize = 256
const maxIssueCacheSize = 1000

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

	issueCache      map[string]types.Issue
	issueCacheOrder []string

	// recoveredSet tracks issue IDs for which orphan_claim_recovered has
	// already been logged since the last process start. It is populated by
	// recoverOrphanedClaims and is never cleared, so each issue is logged at
	// most once per orchestrator lifetime.
	recoveredSet map[string]struct{}

	buildInfo BuildInfo

	// grepFn is used to find commits on mainRef matching an issue identifier.
	// Defaults to a no-op (always miss). Call EnableMainRefGate() to activate the
	// real git-based implementation in production, or inject a stub in tests.
	grepFn func(ctx context.Context, mainRef, identifier string) (sha, subject string, found, unresolvable bool, err error)
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

	return &Orchestrator{
		tracker:      tracker,
		workspace:    workspace,
		agent:        agentRunner,
		config:       configProvider,
		logger:       logger,
		running:      make(map[string]*runEntry),
		backoff:      []types.BackoffEntry{},
		paused:       make(map[string]string),
		events:       make(chan OrchestratorEvent, defaultEventBufferSize),
		issueCache:   make(map[string]types.Issue),
		recoveredSet: make(map[string]struct{}),
		stats: Stats{
			MaxAgents: cfg.MaxConcurrency(),
			StartTime: time.Now(),
		},
		// Default to a no-op so the gate is opt-in. Call EnableMainRefGate() in
		// production startup to activate real git-based already-implemented checks.
		grepFn: func(_ context.Context, _, _ string) (string, string, bool, bool, error) {
			return "", "", false, false, nil
		},
	}
}

// EnableMainRefGate activates the git-based already-implemented gate. Call this
// after NewOrchestrator in production startup. Tests inject their own grepFn.
func (o *Orchestrator) EnableMainRefGate() {
	o.grepFn = grepMainForIdentifier
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
	defer func() {
		o.mu.Lock()
		if !o.eventsClosed.Load() {
			o.eventsClosed.Store(true)
			close(o.events)
		}
		o.mu.Unlock()
	}()

	runSignals := make(chan runSignal, defaultEventBufferSize)
	supervisor, supervisorCtx := errgroup.WithContext(ctx)

	o.runCycle(supervisorCtx, supervisor, runSignals)

	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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

	o.dispatchReadyBackoff(ctx, supervisorCtxOr(ctx), cfg, issuesByID, supervisor, runSignals)
	o.recoverOrphanedClaims(issues)
	o.dispatchUnclaimedIssues(ctx, supervisorCtxOr(ctx), cfg, issues, openIDs, supervisor, runSignals)
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
	openIDs map[string]struct{},
	supervisor *errgroup.Group,
	runSignals chan<- runSignal,
) {
	mainRef := cfg.TrackerMainRef()
	mainRefUnresolvableWarnedThisCycle := false

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

		// Gate: skip dispatch if the issue is already implemented on mainRef.
		if sha, subject, found, unresolvable, grepErr := o.grepFn(ctx, mainRef, issue.Identifier); grepErr != nil {
			logging.LogIssueEvent(o.logger, issue.ID, "grep_main_error", "err", grepErr)
		} else if unresolvable {
			if !mainRefUnresolvableWarnedThisCycle {
				mainRefUnresolvableWarnedThisCycle = true
				logging.LogOrchestratorEvent(o.logger, "claim_main_ref_unresolvable", "main_ref", mainRef)
				o.emitEvent(OrchestratorEvent{
					Type:    EventClaimMainRefUnresolvable,
					IssueID: issue.ID,
					Data:    ClaimMainRefUnresolvable{MainRef: mainRef},
				})
			}
			// Fail open: fall through to dispatch.
		} else if found {
			logging.LogIssueEvent(o.logger, issue.ID,
				"dispatch_skipped_already_implemented",
				"main_ref", mainRef,
				"commit_sha", sha,
				"commit_subject", subject)
			o.emitEvent(OrchestratorEvent{
				Type:    EventClaimSkippedAlreadyImplemented,
				IssueID: issue.ID,
				Data: ClaimSkippedAlreadyImplemented{
					IssueIdentifier: issue.Identifier,
					CommitSHA:       sha,
					CommitSubject:   subject,
					MainRef:         mainRef,
				},
			})

			if cfg.TrackerAutoCloseAlreadyImplemented() {
				o.autoCloseAlreadyImplemented(ctx, issue, sha, subject)
			}
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

// autoCloseAlreadyImplemented transitions issue to Done and posts a comment
// when auto_close_already_implemented is enabled. Errors are logged but do not
// block the caller; this is a best-effort operation.
func (o *Orchestrator) autoCloseAlreadyImplemented(ctx context.Context, issue types.Issue, sha, subject string) {
	lc, ok := o.tracker.(linearAutoCloser)
	if !ok {
		logging.LogIssueEvent(o.logger, issue.ID, "auto_close_skipped_unsupported_tracker")
		return
	}
	commentBody := fmt.Sprintf(
		"Contrabass: skipping claim — commit `%s` on `%s` already addresses this issue.\n\n> %s",
		sha, o.currentConfig().TrackerMainRef(), subject,
	)
	if err := lc.TransitionToDone(ctx, issue.ID, commentBody); err != nil {
		logging.LogIssueEvent(o.logger, issue.ID, "auto_close_failed", "err", err)
	} else {
		logging.LogIssueEvent(o.logger, issue.ID, "auto_closed_already_implemented", "commit_sha", sha)
	}
}

// linearAutoCloser is an optional tracker capability to transition an issue to
// Done and post a comment in a single operation.
type linearAutoCloser interface {
	TransitionToDone(ctx context.Context, issueID, commentBody string) error
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
func (o *Orchestrator) recoverOrphanedClaims(issues []types.Issue) {
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

		o.stopRun(ctx, id)

		if err := o.tracker.UpdateIssueState(ctx, id, types.Unclaimed); err != nil {
			logging.LogIssueEvent(o.logger, id,
				"running_release_state_revert_failed",
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

	o.stopRun(ctx, issueID)

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
	sha, err := workspaceHeadSHA(ctx, workspacePath)
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
	return workspaceRevParse(ctx, workspace, "HEAD", 2*time.Second)
}

// verifyBranchAdvanced compares the workspace branch's current HEAD against
// the claim-time SHA. Git lookup failures fail open so a transient verifier
// issue does not discard an otherwise successful run.
func verifyBranchAdvanced(ctx context.Context, workspace, branch, claimHead string) (bool, string, error) {
	if strings.TrimSpace(claimHead) == "" {
		return true, "no_claim_head", nil
	}

	rev := strings.TrimSpace(branch)
	if rev == "" {
		rev = "HEAD"
	}

	currentHead, err := workspaceRevParse(ctx, workspace, rev, 2*time.Second)
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

// grepMainForIdentifier searches the git log of mainRef for commits whose
// message contains identifier as a whole word. dir sets the working directory
// for the git command; an empty string uses the process CWD. It returns the
// SHA and subject of the first matching commit when found=true. When the ref
// cannot be resolved (unknown revision or ambiguous ref), it returns
// unresolvable=true so callers can fail open without treating it as an error.
// An empty identifier is a no-op that returns found=false without invoking git.
func grepMainForIdentifier(ctx context.Context, mainRef, identifier string) (sha, subject string, found, unresolvable bool, err error) {
	return grepMainForIdentifierIn(ctx, "", mainRef, identifier)
}

// grepMainForIdentifierIn is the testable variant of grepMainForIdentifier that
// accepts an explicit working directory so tests can point it at a temp repo
// without touching os.Chdir.
func grepMainForIdentifierIn(ctx context.Context, dir, mainRef, identifier string) (sha, subject string, found, unresolvable bool, err error) {
	if strings.TrimSpace(identifier) == "" {
		return "", "", false, false, nil
	}

	cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// --grep with -P (Perl regex) enables \b word-boundary matching so
	// ABC-1 does not match ABC-12. Note: -E (ERE) does not support \b on macOS.
	pattern := `\b` + identifier + `\b`
	cmd := exec.CommandContext(cmdCtx, "git", "log", mainRef,
		"--grep="+pattern, "-P",
		"-1", "--no-color", "--format=%H%n%s")
	if dir != "" {
		cmd.Dir = dir
	}

	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, runErr := cmd.Output()
	if runErr != nil {
		stderrStr := stderr.String()
		// git exits non-zero with "unknown revision or path" when the ref cannot
		// be resolved. Treat this as unresolvable so callers can fail open.
		if strings.Contains(stderrStr, "unknown revision") ||
			strings.Contains(stderrStr, "ambiguous argument") {
			return "", "", false, true, nil
		}
		return "", "", false, false, fmt.Errorf("git log: %w", runErr)
	}

	lines := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return "", "", false, false, nil
	}

	sha = strings.TrimSpace(lines[0])
	if len(lines) > 1 {
		subject = strings.TrimSpace(lines[1])
	}
	return sha, subject, true, false, nil
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
// If the cache exceeds maxIssueCacheSize, the oldest entry is evicted.
// Caller must hold o.mu.
func (o *Orchestrator) putIssueCacheLocked(id string, issue types.Issue) {
	if _, exists := o.issueCache[id]; exists {
		o.issueCache[id] = issue
		return
	}
	if len(o.issueCache) >= maxIssueCacheSize {
		oldest := o.issueCacheOrder[0]
		o.issueCacheOrder = o.issueCacheOrder[1:]
		delete(o.issueCache, oldest)
	}
	o.issueCache[id] = issue
	o.issueCacheOrder = append(o.issueCacheOrder, id)
}
