package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/charmbracelet/log"
	"github.com/junhoyeo/symphony-charm/internal/agent"
	"github.com/junhoyeo/symphony-charm/internal/config"
	"github.com/junhoyeo/symphony-charm/internal/tracker"
	"github.com/junhoyeo/symphony-charm/internal/types"
	"github.com/junhoyeo/symphony-charm/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type staticConfig struct{ cfg *config.WorkflowConfig }

func (s *staticConfig) GetConfig() *config.WorkflowConfig { return s.cfg }

func testConfig() *config.WorkflowConfig {
	return &config.WorkflowConfig{
		MaxConcurrencyRaw:    2,
		PollIntervalMsRaw:    10,
		MaxRetryBackoffMsRaw: 100,
		AgentTimeoutMsRaw:    5000,
		StallTimeoutMsRaw:    5000,
		PromptTemplate:       "Fix: {{ issue.title }}",
		ModelRaw:             "test-model",
		ProjectURLRaw:        "https://test.example.com",
	}
}

type observingTracker struct {
	base *tracker.MockTracker

	mu            sync.Mutex
	states        map[string]types.IssueState
	claims        map[string]int
	releases      map[string]int
	currentClaims map[string]bool
}

var _ tracker.Tracker = (*observingTracker)(nil)

func newObservingTracker(issues []types.Issue) *observingTracker {
	mt := tracker.NewMockTracker()
	mt.Issues = append([]types.Issue(nil), issues...)

	states := make(map[string]types.IssueState, len(issues))
	for _, issue := range issues {
		states[issue.ID] = issue.State
	}

	return &observingTracker{
		base:          mt,
		states:        states,
		claims:        make(map[string]int),
		releases:      make(map[string]int),
		currentClaims: make(map[string]bool),
	}
}

func (t *observingTracker) FetchIssues(ctx context.Context) ([]types.Issue, error) {
	issues, err := t.base.FetchIssues(ctx)
	if err != nil {
		return nil, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for i := range issues {
		if state, ok := t.states[issues[i].ID]; ok {
			issues[i].State = state
		}
	}

	return issues, nil
}

func (t *observingTracker) ClaimIssue(ctx context.Context, issueID string) error {
	if err := t.base.ClaimIssue(ctx, issueID); err != nil {
		return err
	}

	t.mu.Lock()
	t.claims[issueID]++
	t.currentClaims[issueID] = true
	t.mu.Unlock()

	return nil
}

func (t *observingTracker) ReleaseIssue(ctx context.Context, issueID string) error {
	if err := t.base.ReleaseIssue(ctx, issueID); err != nil {
		return err
	}

	t.mu.Lock()
	t.releases[issueID]++
	delete(t.currentClaims, issueID)
	t.mu.Unlock()

	return nil
}

func (t *observingTracker) UpdateIssueState(ctx context.Context, issueID string, state types.IssueState) error {
	if err := t.base.UpdateIssueState(ctx, issueID, state); err != nil {
		return err
	}

	t.mu.Lock()
	t.states[issueID] = state
	t.mu.Unlock()

	return nil
}

func (t *observingTracker) PostComment(ctx context.Context, issueID string, body string) error {
	return t.base.PostComment(ctx, issueID, body)
}

func (t *observingTracker) ClaimCount(issueID string) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.claims[issueID]
}

func (t *observingTracker) ReleaseCount(issueID string) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.releases[issueID]
}

func (t *observingTracker) State(issueID string) (types.IssueState, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	state, ok := t.states[issueID]
	return state, ok
}

func (t *observingTracker) TotalClaimedIssues() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	return len(t.claims)
}

type eventCollector struct {
	mu     sync.Mutex
	events []OrchestratorEvent
}

func newEventCollector(events <-chan OrchestratorEvent) *eventCollector {
	c := &eventCollector{
		events: make([]OrchestratorEvent, 0),
	}

	go func() {
		for event := range events {
			c.mu.Lock()
			c.events = append(c.events, event)
			c.mu.Unlock()
		}
	}()

	return c
}

func (c *eventCollector) Has(eventType EventType) bool {
	return c.Count(eventType) > 0
}

func (c *eventCollector) Count(eventType EventType) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	count := 0
	for _, event := range c.events {
		if event.Type == eventType {
			count++
		}
	}

	return count
}

func (c *eventCollector) HasStartedIssue(issueID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, event := range c.events {
		if event.Type == EventAgentStarted && event.IssueID == issueID {
			return true
		}
	}

	return false
}

func (c *eventCollector) FinishedPhase(issueID string) (types.RunPhase, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, event := range c.events {
		if event.Type != EventAgentFinished || event.IssueID != issueID {
			continue
		}
		finished, ok := event.Data.(AgentFinished)
		if !ok {
			continue
		}
		return finished.Phase, true
	}

	return types.RunPhase(0), false
}

type trackingRunner struct {
	base *agent.MockRunner

	mu        sync.Mutex
	active    int
	maxActive int
	starts    int
	stops     int
}

type countingWorkspace struct {
	base *workspace.MockManager

	mu              sync.Mutex
	cleanupCalls    int
	cleanupAllCalls int
}

func newCountingWorkspace(baseDir string) *countingWorkspace {
	return &countingWorkspace{base: workspace.NewMockManager(baseDir)}
}

func (w *countingWorkspace) Create(ctx context.Context, issue types.Issue) (string, error) {
	return w.base.Create(ctx, issue)
}

func (w *countingWorkspace) Cleanup(ctx context.Context, issueID string) error {
	w.mu.Lock()
	w.cleanupCalls++
	w.mu.Unlock()
	return w.base.Cleanup(ctx, issueID)
}

func (w *countingWorkspace) CleanupAll(ctx context.Context) error {
	w.mu.Lock()
	w.cleanupAllCalls++
	w.mu.Unlock()
	return w.base.CleanupAll(ctx)
}

func (w *countingWorkspace) CleanupCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cleanupCalls
}

func (w *countingWorkspace) CleanupAllCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cleanupAllCalls
}

var _ agent.AgentRunner = (*trackingRunner)(nil)

func newTrackingRunner(base *agent.MockRunner) *trackingRunner {
	return &trackingRunner{base: base}
}

func (r *trackingRunner) Start(
	ctx context.Context,
	issue types.Issue,
	workspacePath string,
	prompt string,
) (*agent.AgentProcess, error) {
	proc, err := r.base.Start(ctx, issue, workspacePath, prompt)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.active++
	r.starts++
	if r.active > r.maxActive {
		r.maxActive = r.active
	}
	r.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		err, ok := <-proc.Done
		if ok {
			done <- err
		} else {
			done <- nil
		}
		close(done)

		r.mu.Lock()
		if r.active > 0 {
			r.active--
		}
		r.mu.Unlock()
	}()

	return &agent.AgentProcess{
		PID:       proc.PID,
		SessionID: proc.SessionID,
		Events:    proc.Events,
		Done:      done,
	}, nil
}

func (r *trackingRunner) Stop(proc *agent.AgentProcess) error {
	r.mu.Lock()
	r.stops++
	r.mu.Unlock()

	return r.base.Stop(proc)
}

func (r *trackingRunner) MaxActive() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.maxActive
}

func (r *trackingRunner) StartCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.starts
}

func (r *trackingRunner) StopCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.stops
}

func startOrchestrator(ctx context.Context, orch *Orchestrator) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- orch.Run(ctx)
	}()

	return done
}

func backoffSnapshot(orch *Orchestrator) []types.BackoffEntry {
	orch.mu.Lock()
	defer orch.mu.Unlock()

	result := make([]types.BackoffEntry, len(orch.backoff))
	copy(result, orch.backoff)
	return result
}

func TestPollAndDispatch(t *testing.T) {
	mt := newObservingTracker([]types.Issue{
		{ID: "ISS-1", Title: "First", State: types.Unclaimed},
		{ID: "ISS-2", Title: "Second", State: types.Unclaimed},
	})
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{
		Events: []types.AgentEvent{{Type: "turn/completed"}},
		Delay:  10 * time.Millisecond,
	}
	cfg := &staticConfig{cfg: testConfig()}
	orch := NewOrchestrator(mt, mw, mr, cfg, nil)
	events := newEventCollector(orch.Events())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := startOrchestrator(ctx, orch)

	require.Eventually(t, func() bool {
		return mt.ClaimCount("ISS-1") > 0 &&
			mt.ClaimCount("ISS-2") > 0 &&
			events.HasStartedIssue("ISS-1") &&
			events.HasStartedIssue("ISS-2")
	}, 2*time.Second, 10*time.Millisecond)

	cancel()
	require.NoError(t, <-done)
}

func TestConcurrencyBounded(t *testing.T) {
	mt := newObservingTracker([]types.Issue{
		{ID: "ISS-1", Title: "First", State: types.Unclaimed},
		{ID: "ISS-2", Title: "Second", State: types.Unclaimed},
		{ID: "ISS-3", Title: "Third", State: types.Unclaimed},
	})
	mw := workspace.NewMockManager(t.TempDir())
	baseRunner := &agent.MockRunner{
		Events: []types.AgentEvent{{Type: "turn/completed"}},
		Delay:  10 * time.Millisecond,
	}
	runner := newTrackingRunner(baseRunner)

	workflowCfg := testConfig()
	workflowCfg.MaxConcurrencyRaw = 1
	orch := NewOrchestrator(mt, mw, runner, &staticConfig{cfg: workflowCfg}, nil)
	go func() {
		for range orch.Events() {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := startOrchestrator(ctx, orch)

	require.Eventually(t, func() bool {
		return runner.StartCount() >= 3
	}, 2*time.Second, 10*time.Millisecond)

	time.Sleep(100 * time.Millisecond)
	cancel()
	require.NoError(t, <-done)

	require.Equal(t, 1, runner.MaxActive())
	require.Equal(t, 1, mt.ClaimCount("ISS-1"))
	require.Equal(t, 1, mt.ClaimCount("ISS-2"))
	require.Equal(t, 1, mt.ClaimCount("ISS-3"))
}

func TestSuccessfulAgentReleases(t *testing.T) {
	mt := newObservingTracker([]types.Issue{{ID: "ISS-1", Title: "Test", State: types.Unclaimed}})
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{
		Events: []types.AgentEvent{{Type: "turn/completed"}},
		Delay:  10 * time.Millisecond,
	}
	cfg := &staticConfig{cfg: testConfig()}
	orch := NewOrchestrator(mt, mw, mr, cfg, nil)
	go func() {
		for range orch.Events() {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := startOrchestrator(ctx, orch)

	require.Eventually(t, func() bool {
		state, ok := mt.State("ISS-1")
		if !ok {
			return false
		}

		return mt.ReleaseCount("ISS-1") > 0 &&
			state == types.Released &&
			!mw.Exists("ISS-1")
	}, 2*time.Second, 10*time.Millisecond)

	cancel()
	require.NoError(t, <-done)
}

func TestNoEventSuccessResolvesToSucceeded(t *testing.T) {
	mt := newObservingTracker([]types.Issue{{ID: "ISS-1", Title: "Test", State: types.Unclaimed}})
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{}
	orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: testConfig()}, nil)
	events := newEventCollector(orch.Events())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := startOrchestrator(ctx, orch)

	require.Eventually(t, func() bool {
		phase, ok := events.FinishedPhase("ISS-1")
		if !ok {
			return false
		}
		if phase != types.Succeeded {
			return false
		}
		return !events.Has(EventBackoffEnqueued)
	}, 2*time.Second, 10*time.Millisecond)

	cancel()
	require.NoError(t, <-done)
}

func TestFailedAgentBackoff(t *testing.T) {
	mt := newObservingTracker([]types.Issue{{ID: "ISS-1", Title: "Test", State: types.Unclaimed}})
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{
		Events:  []types.AgentEvent{{Type: "turn/completed"}},
		DoneErr: errors.New("agent failed"),
		Delay:   10 * time.Millisecond,
	}

	workflowCfg := testConfig()
	workflowCfg.MaxRetryBackoffMsRaw = 5_000
	orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: workflowCfg}, nil)
	go func() {
		for range orch.Events() {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := startOrchestrator(ctx, orch)

	require.Eventually(t, func() bool {
		state, ok := mt.State("ISS-1")
		if !ok || state != types.RetryQueued {
			return false
		}

		entries := backoffSnapshot(orch)
		if len(entries) != 1 {
			return false
		}

		return mt.ReleaseCount("ISS-1") > 0 &&
			!mw.Exists("ISS-1") &&
			entries[0].IssueID == "ISS-1" &&
			entries[0].Attempt == 2
	}, 2*time.Second, 10*time.Millisecond)

	cancel()
	require.NoError(t, <-done)
}

func TestOrchestrator_FollowUpTurnContinuation(t *testing.T) {
	t.Run("core_test.exs", func(t *testing.T) {
		issue := types.Issue{
			ID:         "ISS-FOLLOW-1",
			Identifier: "CORE-422",
			Title:      "Follow up",
			State:      types.Unclaimed,
		}
		mt := newObservingTracker([]types.Issue{issue})
		mw := workspace.NewMockManager(t.TempDir())
		mr := &agent.MockRunner{
			HandshakeEvents: []types.AgentEvent{{Type: "turn/started"}},
			Events:          []types.AgentEvent{{Type: "turn/failed", Data: map[string]interface{}{"message": "follow-up still active"}}},
			Delay:           10 * time.Millisecond,
		}

		workflowCfg := testConfig()
		workflowCfg.MaxRetryBackoffMsRaw = 5_000
		orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: workflowCfg}, nil)
		events := newEventCollector(orch.Events())

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		done := startOrchestrator(ctx, orch)

		require.Eventually(t, func() bool {
			entries := backoffSnapshot(orch)
			if len(entries) != 1 {
				return false
			}
			state, ok := mt.State(issue.ID)
			if !ok || state != types.RetryQueued {
				return false
			}
			return entries[0].IssueID == issue.ID && entries[0].Attempt == 2
		}, 2*time.Second, 10*time.Millisecond)

		events.mu.Lock()
		deferred := append([]OrchestratorEvent(nil), events.events...)
		events.mu.Unlock()

		var backoffPayload BackoffEnqueued
		foundBackoff := false
		for _, event := range deferred {
			if event.Type != EventBackoffEnqueued || event.IssueID != issue.ID {
				continue
			}
			payload, ok := event.Data.(BackoffEnqueued)
			require.True(t, ok)
			backoffPayload = payload
			foundBackoff = true
			break
		}
		require.True(t, foundBackoff)
		assert.Equal(t, 2, backoffPayload.Attempt)
		assert.Equal(t, "follow-up still active", backoffPayload.Error)

		cancel()
		require.NoError(t, <-done)
	})
}

func TestContextCancellation(t *testing.T) {
	mt := newObservingTracker([]types.Issue{{ID: "ISS-1", Title: "Test", State: types.Unclaimed}})
	mw := workspace.NewMockManager(t.TempDir())
	baseRunner := &agent.MockRunner{
		Events: []types.AgentEvent{{Type: "turn/completed"}},
		Delay:  2 * time.Second,
	}
	runner := newTrackingRunner(baseRunner)
	orch := NewOrchestrator(mt, mw, runner, &staticConfig{cfg: testConfig()}, nil)
	events := newEventCollector(orch.Events())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := startOrchestrator(ctx, orch)

	require.Eventually(t, func() bool {
		return events.Has(EventAgentStarted)
	}, 2*time.Second, 10*time.Millisecond)

	time.Sleep(100 * time.Millisecond)
	cancel()
	require.NoError(t, <-done)

	require.GreaterOrEqual(t, runner.StopCount(), 1)
	require.Empty(t, mw.List())
}

func TestOrchestrator_GracefulShutdownOnce(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "concurrent_triggers_execute_shutdown_once"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issueID := "ISS-SHUT-1"
			mt := newObservingTracker([]types.Issue{{ID: issueID, Title: "Shutdown", State: types.Running}})
			ws := newCountingWorkspace(t.TempDir())
			runner := &stopCountingRunner{}
			orch := NewOrchestrator(mt, ws, runner, &staticConfig{cfg: testConfig()}, nil)

			var cancelCalls atomic.Int32
			orch.mu.Lock()
			orch.running[issueID] = &runEntry{
				issue:   types.Issue{ID: issueID, State: types.Running},
				attempt: types.RunAttempt{IssueID: issueID, Attempt: 1},
				process: &agent.AgentProcess{PID: 101, SessionID: "shutdown-once"},
				cancel: func() {
					cancelCalls.Add(1)
				},
			}
			orch.stats.Running = len(orch.running)
			orch.mu.Unlock()

			ctx := context.Background()
			var wg sync.WaitGroup
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_ = orch.gracefulShutdown(ctx)
				}()
			}
			wg.Wait()

			require.Equal(t, 1, int(cancelCalls.Load()))
			require.Equal(t, 1, runner.StopCount())
			require.Equal(t, 1, ws.CleanupCount())
			require.Equal(t, 1, ws.CleanupAllCount())
			require.Equal(t, 1, mt.ReleaseCount(issueID))
			require.Equal(t, 0, orch.RunningCount())
		})
	}
}

func TestEmptyPoll(t *testing.T) {
	mt := newObservingTracker(nil)
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{}
	orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: testConfig()}, nil)
	events := newEventCollector(orch.Events())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := startOrchestrator(ctx, orch)

	require.Eventually(t, func() bool {
		return events.Has(EventStatusUpdate)
	}, 2*time.Second, 10*time.Millisecond)

	cancel()
	require.NoError(t, <-done)
	require.Equal(t, 0, mt.TotalClaimedIssues())
}

func TestEventsEmitted(t *testing.T) {
	mt := newObservingTracker([]types.Issue{{ID: "ISS-1", Title: "Test", State: types.Unclaimed}})
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{
		Events: []types.AgentEvent{{Type: "turn/completed"}},
		Delay:  10 * time.Millisecond,
	}
	orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: testConfig()}, nil)
	events := newEventCollector(orch.Events())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := startOrchestrator(ctx, orch)

	require.Eventually(t, func() bool {
		return events.Has(EventStatusUpdate) &&
			events.Has(EventAgentStarted) &&
			events.Has(EventAgentFinished)
	}, 2*time.Second, 10*time.Millisecond)

	cancel()
	require.NoError(t, <-done)
}

func TestOrchestrator_StopRunCleansOrphanedEntry(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "running_entry_is_removed_and_capacity_recovers"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			issue := types.Issue{ID: "ISS-STOP-1", Title: "Stop Test", State: types.Unclaimed}
			mt := newObservingTracker([]types.Issue{issue})
			mw := workspace.NewMockManager(t.TempDir())
			mr := &agent.MockRunner{
				Events: []types.AgentEvent{{Type: "turn/output"}},
				Delay:  2 * time.Second,
			}
			cfg := &staticConfig{cfg: testConfig()}
			orch := NewOrchestrator(mt, mw, mr, cfg, nil)

			runSignals := make(chan runSignal, 16)
			supervisor := &errgroup.Group{}

			orch.dispatchIssue(ctx, ctx, cfg.cfg, issue, 1, supervisor, runSignals)

			require.Eventually(t, func() bool {
				return orch.RunningCount() == 1
			}, time.Second, 10*time.Millisecond)
			assert.False(t, orch.canDispatch(1))

			orch.stopRun(ctx, issue.ID)

			require.Eventually(t, func() bool {
				return orch.RunningCount() == 0
			}, time.Second, 10*time.Millisecond)
			assert.True(t, orch.canDispatch(1))
			require.NoError(t, supervisor.Wait())
		})
	}
}

func TestOrchestrator_ReconcileForceRemovesBrokenDone(t *testing.T) {
	tests := []struct {
		name   string
		entry  *runEntry
		issue  string
		config *config.WorkflowConfig
	}{
		{
			name:  "nil_done_channel_is_deleted_without_stop",
			issue: "ISS-BROKEN-DONE",
			entry: &runEntry{
				issue:   types.Issue{ID: "ISS-BROKEN-DONE", State: types.Running},
				attempt: types.RunAttempt{IssueID: "ISS-BROKEN-DONE", Phase: types.InitializingSession, StartTime: time.Now()},
				process: &agent.AgentProcess{PID: 42, SessionID: "broken", Done: nil},
				cancel:  func() {},
			},
			config: testConfig(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := newObservingTracker(nil)
			mw := workspace.NewMockManager(t.TempDir())
			runner := &agent.MockRunner{}
			orch := NewOrchestrator(mt, mw, runner, &staticConfig{cfg: tt.config}, nil)

			orch.mu.Lock()
			orch.running[tt.issue] = tt.entry
			orch.stats.Running = len(orch.running)
			orch.mu.Unlock()

			orch.reconcileRunning(context.Background(), tt.config)

			assert.Equal(t, 0, orch.RunningCount())
			orch.mu.Lock()
			_, exists := orch.running[tt.issue]
			orch.mu.Unlock()
			assert.False(t, exists)
		})
	}
}

func TestOrchestrator_IssueCacheEvictsOldest(t *testing.T) {
	tests := []struct {
		name       string
		prefill    int
		insertID   string
		expectSize int
		expectGone string
	}{
		{
			name:       "evicts_oldest_when_exceeding_max",
			prefill:    maxIssueCacheSize,
			insertID:   "ISS-OVERFLOW",
			expectSize: maxIssueCacheSize,
			expectGone: "ISS-0",
		},
		{
			name:       "no_eviction_below_limit",
			prefill:    5,
			insertID:   "ISS-NEW",
			expectSize: 6,
			expectGone: "",
		},
		{
			name:       "update_existing_key_does_not_grow",
			prefill:    maxIssueCacheSize,
			insertID:   "ISS-0",
			expectSize: maxIssueCacheSize,
			expectGone: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := newObservingTracker(nil)
			mw := workspace.NewMockManager(t.TempDir())
			mr := &agent.MockRunner{}
			orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: testConfig()}, nil)

			// Prefill the cache with tt.prefill entries
			orch.mu.Lock()
			for i := 0; i < tt.prefill; i++ {
				id := fmt.Sprintf("ISS-%d", i)
				orch.putIssueCacheLocked(id, types.Issue{ID: id, Title: fmt.Sprintf("Issue %d", i)})
			}
			orch.mu.Unlock()

			// Insert the new entry
			orch.mu.Lock()
			orch.putIssueCacheLocked(tt.insertID, types.Issue{ID: tt.insertID, Title: "Overflow"})
			cacheSize := len(orch.issueCache)
			orderLen := len(orch.issueCacheOrder)
			_, newExists := orch.issueCache[tt.insertID]
			var goneExists bool
			if tt.expectGone != "" {
				_, goneExists = orch.issueCache[tt.expectGone]
			}
			orch.mu.Unlock()

			assert.Equal(t, tt.expectSize, cacheSize, "cache size")
			assert.Equal(t, tt.expectSize, orderLen, "order slice size")
			assert.True(t, newExists, "new entry should be in cache")
			if tt.expectGone != "" {
				assert.False(t, goneExists, "oldest entry should be evicted")
			}
		})
	}
}

func TestOrchestrator_EmitEventDropLogged(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "dropped_event_is_logged"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := log.NewWithOptions(&buf, log.Options{Level: log.InfoLevel})

			mt := newObservingTracker(nil)
			mw := workspace.NewMockManager(t.TempDir())
			mr := &agent.MockRunner{}
			orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: testConfig()}, logger)

			// Fill the events channel to capacity
			for i := 0; i < defaultEventBufferSize; i++ {
				orch.events <- OrchestratorEvent{Type: EventStatusUpdate}
			}

			// This event should be dropped and logged
			orch.emitEvent(OrchestratorEvent{
				Type:    EventAgentStarted,
				IssueID: "ISS-DROP",
			})

			logOutput := buf.String()
			assert.Contains(t, logOutput, "event_dropped")
			assert.Contains(t, logOutput, "ISS-DROP")
		})
	}
}

func TestOrchestrator_CompleteRunPostsComment(t *testing.T) {
	mt := newObservingTracker([]types.Issue{{ID: "ISS-1", Title: "Test", State: types.Unclaimed}})
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{
		Events: []types.AgentEvent{{Type: "turn/completed"}},
		Delay:  10 * time.Millisecond,
	}
	cfg := &staticConfig{cfg: testConfig()}
	orch := NewOrchestrator(mt, mw, mr, cfg, nil)
	events := newEventCollector(orch.Events())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := startOrchestrator(ctx, orch)

	require.Eventually(t, func() bool {
		phase, ok := events.FinishedPhase("ISS-1")
		return ok && phase == types.Succeeded
	}, 2*time.Second, 10*time.Millisecond)

	cancel()
	require.NoError(t, <-done)

	comments := mt.base.Comments["ISS-1"]
	require.NotEmpty(t, comments, "expected at least one comment posted")
	assert.Contains(t, comments[0], "Agent run completed")
	assert.Contains(t, comments[0], "phase=Succeeded")
}

func TestOrchestrator_ReconcileAssignsTimedOut(t *testing.T) {
	mt := newObservingTracker(nil)
	mw := workspace.NewMockManager(t.TempDir())
	runner := &agent.MockRunner{}
	cfg := testConfig()
	cfg.AgentTimeoutMsRaw = 1 // 1ms timeout
	orch := NewOrchestrator(mt, mw, runner, &staticConfig{cfg: cfg}, nil)
	go func() {
		for range orch.Events() {
		}
	}()

	// Seed a running entry with StartTime in the past
	done := make(chan error, 1)
	entry := &runEntry{
		issue: types.Issue{ID: "ISS-TIMEOUT-1", State: types.Running},
		attempt: types.RunAttempt{
			IssueID:   "ISS-TIMEOUT-1",
			Phase:     types.StreamingTurn,
			Attempt:   1,
			StartTime: time.Now().Add(-10 * time.Second),
		},
		process: &agent.AgentProcess{PID: 99, SessionID: "timeout-test", Done: done},
		cancel:  func() {},
	}

	orch.mu.Lock()
	orch.running["ISS-TIMEOUT-1"] = entry
	orch.stats.Running = 1
	orch.mu.Unlock()

	orch.reconcileRunning(context.Background(), cfg)
	// reconcileRunning calls stopRun which removes the entry from the map,
	// but the entry pointer was mutated in-place before removal.
	assert.Equal(t, types.TimedOut, entry.attempt.Phase)
	assert.Equal(t, "run timed out", entry.attempt.Error)
}

// --- Error Path Tests (T30) ---

type failingWorkspace struct {
	baseDir   string
	createErr error
}

func (w *failingWorkspace) Create(_ context.Context, _ types.Issue) (string, error) {
	return "", w.createErr
}
func (w *failingWorkspace) Cleanup(_ context.Context, _ string) error { return nil }
func (w *failingWorkspace) CleanupAll(_ context.Context) error        { return nil }

func TestOrchestrator_WorkspaceCreateFailure(t *testing.T) {
	issues := []types.Issue{{ID: "ISS-WS-FAIL", Title: "ws fail", State: types.Unclaimed}}
	mt := newObservingTracker(issues)
	ws := &failingWorkspace{createErr: errors.New("disk full")}
	mr := &agent.MockRunner{}
	cfg := &staticConfig{cfg: testConfig()}
	orch := NewOrchestrator(mt, ws, mr, cfg, nil)
	events := newEventCollector(orch.Events())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := startOrchestrator(ctx, orch)

	// Workspace creation failure should release the claim and enqueue backoff
	require.Eventually(t, func() bool {
		return events.Has(EventIssueReleased) && events.Has(EventBackoffEnqueued)
	}, 2*time.Second, 10*time.Millisecond)

	// The claim was obtained then released
	assert.GreaterOrEqual(t, mt.ClaimCount("ISS-WS-FAIL"), 1)
	assert.GreaterOrEqual(t, mt.ReleaseCount("ISS-WS-FAIL"), 1)

	cancel()
	require.NoError(t, <-done)
}

func TestOrchestrator_PromptRenderFailure(t *testing.T) {
	issues := []types.Issue{{ID: "ISS-PROMPT-FAIL", Title: "prompt fail", State: types.Unclaimed}}
	mt := newObservingTracker(issues)
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{}
	cfg := &staticConfig{cfg: testConfig()}
	// Use an invalid liquid template that will cause RenderPrompt to fail
	cfg.cfg.PromptTemplate = "{{ invalid_var_that_does_not_exist }}"
	orch := NewOrchestrator(mt, mw, mr, cfg, nil)
	events := newEventCollector(orch.Events())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := startOrchestrator(ctx, orch)

	// Prompt render failure should release claim, cleanup workspace, and enqueue backoff
	require.Eventually(t, func() bool {
		return events.Has(EventIssueReleased) && events.Has(EventBackoffEnqueued)
	}, 2*time.Second, 10*time.Millisecond)

	// Workspace should have been cleaned up
	assert.False(t, mw.Exists("ISS-PROMPT-FAIL"))
	assert.GreaterOrEqual(t, mt.ReleaseCount("ISS-PROMPT-FAIL"), 1)

	cancel()
	require.NoError(t, <-done)
}

func TestOrchestrator_ClaimUpdateFailureRollback(t *testing.T) {
	issues := []types.Issue{{ID: "ISS-CLAIM-ROLL", Title: "claim rollback", State: types.Unclaimed}}
	mt := newObservingTracker(issues)

	// Inject UpdateErr on the base tracker so UpdateIssueState fails during claim
	mt.base.UpdateErr = errors.New("update state failed")

	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{}
	cfg := &staticConfig{cfg: testConfig()}
	orch := NewOrchestrator(mt, mw, mr, cfg, nil)
	events := newEventCollector(orch.Events())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := startOrchestrator(ctx, orch)

	// ClaimIssue calls ClaimIssue (succeeds) then UpdateIssueState (fails),
	// which triggers ReleaseIssue rollback inside claimIssue.
	// Then dispatchIssue gets the error and enqueues continuation.
	require.Eventually(t, func() bool {
		return events.Has(EventBackoffEnqueued)
	}, 2*time.Second, 10*time.Millisecond)

	// The claim was attempted
	assert.GreaterOrEqual(t, mt.ClaimCount("ISS-CLAIM-ROLL"), 1)
	// ReleaseIssue was called as part of rollback
	assert.GreaterOrEqual(t, mt.ReleaseCount("ISS-CLAIM-ROLL"), 1)

	cancel()
	require.NoError(t, <-done)
}

func TestResolveFinalPhase_ContextCanceled(t *testing.T) {
	tests := []struct {
		name      string
		phase     types.RunPhase
		message   string
		doneErr   error
		wantPhase types.RunPhase
		wantMsg   string
	}{
		{
			name:      "active_phase_canceled",
			phase:     types.StreamingTurn,
			message:   "",
			doneErr:   context.Canceled,
			wantPhase: types.CanceledByReconciliation,
			wantMsg:   "context canceled",
		},
		{
			name:      "already_failed_phase_canceled",
			phase:     types.Failed,
			message:   "earlier failure",
			doneErr:   context.Canceled,
			wantPhase: types.Failed,
			wantMsg:   "earlier failure",
		},
		{
			name:      "active_phase_with_generic_error",
			phase:     types.InitializingSession,
			message:   "",
			doneErr:   errors.New("process crashed"),
			wantPhase: types.Failed,
			wantMsg:   "process crashed",
		},
		{
			name:      "canceled_preserves_existing_message",
			phase:     types.StreamingTurn,
			message:   "prior error",
			doneErr:   context.Canceled,
			wantPhase: types.CanceledByReconciliation,
			wantMsg:   "prior error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase, msg := resolveFinalPhase(tt.phase, tt.message, tt.doneErr)
			assert.Equal(t, tt.wantPhase, phase)
			assert.Equal(t, tt.wantMsg, msg)
		})
	}
}

func TestParseUsageTokens_TotalTokensFallback(t *testing.T) {
	tests := []struct {
		name    string
		data    map[string]interface{}
		wantIn  int64
		wantOut int64
	}{
		{
			name:    "nil_data",
			data:    nil,
			wantIn:  0,
			wantOut: 0,
		},
		{
			name:    "no_usage_key",
			data:    map[string]interface{}{"other": 42},
			wantIn:  0,
			wantOut: 0,
		},
		{
			name: "prompt_and_completion_tokens",
			data: map[string]interface{}{
				"usage": map[string]interface{}{
					"prompt_tokens":     float64(100),
					"completion_tokens": float64(50),
				},
			},
			wantIn:  100,
			wantOut: 50,
		},
		{
			name: "total_tokens_fallback_when_no_prompt_or_completion",
			data: map[string]interface{}{
				"usage": map[string]interface{}{
					"total_tokens": float64(200),
				},
			},
			wantIn:  0,
			wantOut: 200,
		},
		{
			name: "input_and_output_tokens",
			data: map[string]interface{}{
				"usage": map[string]interface{}{
					"input_tokens":  int64(80),
					"output_tokens": int64(40),
				},
			},
			wantIn:  80,
			wantOut: 40,
		},
		{
			name: "usage_is_not_a_map",
			data: map[string]interface{}{
				"usage": "not-a-map",
			},
			wantIn:  0,
			wantOut: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, out := parseUsageTokens(tt.data)
			assert.Equal(t, tt.wantIn, in)
			assert.Equal(t, tt.wantOut, out)
		})
	}
}

func TestOrchestrator_EventBufferFull(t *testing.T) {
	var buf bytes.Buffer
	logger := log.NewWithOptions(&buf, log.Options{Level: log.InfoLevel})

	mt := newObservingTracker(nil)
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{}
	orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: testConfig()}, logger)

	// Fill the events channel completely
	for i := 0; i < defaultEventBufferSize; i++ {
		orch.events <- OrchestratorEvent{
			Type: EventStatusUpdate,
			Data: StatusUpdate{Stats: Stats{Running: i}},
		}
	}

	// Emit multiple event types to test drop logging for each
	droppedEvents := []OrchestratorEvent{
		{Type: EventAgentStarted, IssueID: "ISS-BUF-1", Data: AgentStarted{Attempt: 1}},
		{Type: EventAgentFinished, IssueID: "ISS-BUF-2", Data: AgentFinished{Attempt: 1, Phase: types.Failed}},
		{Type: EventBackoffEnqueued, IssueID: "ISS-BUF-3", Data: BackoffEnqueued{Attempt: 2}},
	}

	for _, ev := range droppedEvents {
		orch.emitEvent(ev)
	}

	logOutput := buf.String()
	assert.Contains(t, logOutput, "event_dropped")
	assert.Contains(t, logOutput, "ISS-BUF-1")
	assert.Contains(t, logOutput, "ISS-BUF-2")
	assert.Contains(t, logOutput, "ISS-BUF-3")
}

func TestOrchestrator_NilContext(t *testing.T) {
	mt := newObservingTracker(nil)
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{}
	orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: testConfig()}, nil)
	go func() {
		for range orch.Events() {
		}
	}()

	//nolint:staticcheck // SA1012: intentionally passing nil context to test guard
	err := orch.Run(nil)
	require.Error(t, err)
	assert.Equal(t, "context is nil", err.Error())
}

func TestOrchestrator_BackoffIssueNotInCache(t *testing.T) {
	// Set up orchestrator with no issues in tracker (so issuesByID is empty)
	mt := newObservingTracker(nil)
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{}
	cfg := &staticConfig{cfg: testConfig()}
	orch := NewOrchestrator(mt, mw, mr, cfg, nil)
	events := newEventCollector(orch.Events())

	// Seed backoff for an issue that won't appear in fetched issues or cache
	orch.mu.Lock()
	orch.backoff = []types.BackoffEntry{{
		IssueID: "ISS-GHOST",
		Attempt: 2,
		RetryAt: time.Now().Add(-time.Second), // already ready
	}}
	orch.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := startOrchestrator(ctx, orch)

	// Should enqueue continuation because issue details are unavailable
	require.Eventually(t, func() bool {
		return events.Has(EventBackoffEnqueued)
	}, 2*time.Second, 10*time.Millisecond)

	// Should NOT have started any agent
	assert.False(t, events.Has(EventAgentStarted))

	cancel()
	require.NoError(t, <-done)
}

func TestEventTypeString_Unknown(t *testing.T) {
	tests := []struct {
		name string
		et   EventType
		want string
	}{
		{name: "StatusUpdate", et: EventStatusUpdate, want: "StatusUpdate"},
		{name: "AgentStarted", et: EventAgentStarted, want: "AgentStarted"},
		{name: "AgentFinished", et: EventAgentFinished, want: "AgentFinished"},
		{name: "BackoffEnqueued", et: EventBackoffEnqueued, want: "BackoffEnqueued"},
		{name: "IssueReleased", et: EventIssueReleased, want: "IssueReleased"},
		{name: "Unknown_99", et: EventType(99), want: "Unknown"},
		{name: "Unknown_neg1", et: EventType(-1), want: "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.et.String())
		})
	}
}
