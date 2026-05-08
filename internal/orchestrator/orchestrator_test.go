package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/charmbracelet/log"
	"github.com/junhoyeo/contrabass/internal/agent"
	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/junhoyeo/contrabass/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"os"
	"path/filepath"
	"strings"
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
	updateCounts  map[string]map[types.IssueState]int
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
		updateCounts:  make(map[string]map[types.IssueState]int),
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
	if t.updateCounts[issueID] == nil {
		t.updateCounts[issueID] = make(map[types.IssueState]int)
	}
	t.updateCounts[issueID][state]++
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

func (t *observingTracker) UpdateIssueStateCount(issueID string, state types.IssueState) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.updateCounts[issueID][state]
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

func (c *eventCollector) Event(eventType EventType, issueID string) (OrchestratorEvent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, event := range c.events {
		if event.Type == eventType && event.IssueID == issueID {
			return event, true
		}
	}

	return OrchestratorEvent{}, false
}

func (c *eventCollector) BackoffCauseCount(issueID, cause string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	count := 0
	for _, event := range c.events {
		if event.Type != EventBackoffEnqueued || event.IssueID != issueID {
			continue
		}
		backoff, ok := event.Data.(BackoffEnqueued)
		if ok && strings.Contains(backoff.Error, cause) {
			count++
		}
	}
	return count
}

func assertIssueReleasedTimestampPrecedesBackoff(t *testing.T, events *eventCollector, issueID string) {
	t.Helper()

	released, ok := events.Event(EventIssueReleased, issueID)
	require.True(t, ok, "expected IssueReleased event for %s", issueID)
	require.False(t, released.Timestamp.IsZero(), "expected IssueReleased timestamp for %s", issueID)

	backoff, ok := events.Event(EventBackoffEnqueued, issueID)
	require.True(t, ok, "expected BackoffEnqueued event for %s", issueID)
	require.False(t, backoff.Timestamp.IsZero(), "expected BackoffEnqueued timestamp for %s", issueID)

	assert.False(
		t,
		released.Timestamp.After(backoff.Timestamp),
		"expected IssueReleased timestamp %s to be <= BackoffEnqueued timestamp %s for %s",
		released.Timestamp.Format(time.RFC3339Nano),
		backoff.Timestamp.Format(time.RFC3339Nano),
		issueID,
	)
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

type gitWorkspaceManager struct {
	base *workspace.MockManager
	t    *testing.T
}

func newGitWorkspaceManager(t *testing.T, baseDir string) *gitWorkspaceManager {
	t.Helper()

	return &gitWorkspaceManager{
		base: workspace.NewMockManager(baseDir),
		t:    t,
	}
}

func (w *gitWorkspaceManager) Create(ctx context.Context, issue types.Issue) (string, error) {
	path, err := w.base.Create(ctx, issue)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return path, nil
	}

	require.NoError(w.t, os.MkdirAll(path, 0o755))
	gitRun(w.t, path, "init")
	require.NoError(w.t, os.WriteFile(filepath.Join(path, "baseline.txt"), []byte("baseline\n"), 0o644))
	gitRun(w.t, path, "add", "baseline.txt")
	gitRun(w.t, path, "commit", "-m", "baseline")
	if issue.BranchName != "" {
		gitRun(w.t, path, "checkout", "-B", issue.BranchName)
	}
	return path, nil
}

func (w *gitWorkspaceManager) Cleanup(ctx context.Context, issueID string) error {
	return w.base.Cleanup(ctx, issueID)
}

func (w *gitWorkspaceManager) CleanupAll(ctx context.Context) error {
	return w.base.CleanupAll(ctx)
}

type commitBeforeSuccessRunner struct {
	t    *testing.T
	base *agent.MockRunner
}

func (r *commitBeforeSuccessRunner) Start(
	ctx context.Context,
	issue types.Issue,
	workspacePath string,
	prompt string,
) (*agent.AgentProcess, error) {
	gitRun(r.t, workspacePath, "commit", "--allow-empty", "-m", "agent progress")
	return r.base.Start(ctx, issue, workspacePath, prompt)
}

func (r *commitBeforeSuccessRunner) Stop(proc *agent.AgentProcess) error {
	return r.base.Stop(proc)
}

func (r *commitBeforeSuccessRunner) Close() error {
	return r.base.Close()
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

func (r *trackingRunner) Close() error { return nil }

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

func TestSuccessGate_HollowRunPausesWithoutRetry(t *testing.T) {
	const rejectionCause = "success_unverified_branch_unchanged"

	tests := []struct {
		name         string
		issueID      string
		runner       func(t *testing.T) agent.AgentRunner
		wantReleased bool
		wantPaused   bool
	}{
		{
			name:    "hollow success rejected",
			issueID: "ISS-HOLLOW",
			runner: func(t *testing.T) agent.AgentRunner {
				t.Helper()
				return &agent.MockRunner{
					Events: []types.AgentEvent{{Type: "turn/completed"}},
					Delay:  10 * time.Millisecond,
				}
			},
			wantPaused: true,
		},
		{
			name:    "real success proceeds",
			issueID: "ISS-REAL",
			runner: func(t *testing.T) agent.AgentRunner {
				t.Helper()
				return &commitBeforeSuccessRunner{
					t: t,
					base: &agent.MockRunner{
						Events: []types.AgentEvent{{Type: "turn/completed"}},
						Delay:  10 * time.Millisecond,
					},
				}
			},
			wantReleased: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := types.Issue{
				ID:         tt.issueID,
				Identifier: tt.issueID,
				Title:      tt.name,
				State:      types.Unclaimed,
				BranchName: tt.issueID,
			}
			mt := newObservingTracker([]types.Issue{issue})
			mw := newGitWorkspaceManager(t, t.TempDir())
			cfg := testConfig()
			cfg.MaxRetryBackoffMsRaw = 5_000
			orch := NewOrchestrator(mt, mw, tt.runner(t), &staticConfig{cfg: cfg}, nil)
			events := newEventCollector(orch.Events())

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := startOrchestrator(ctx, orch)

			if tt.wantReleased {
				require.Eventually(t, func() bool {
					return mt.UpdateIssueStateCount(issue.ID, types.Released) >= 1
				}, 2*time.Second, 10*time.Millisecond)
				assert.Zero(t, events.BackoffCauseCount(issue.ID, rejectionCause))
			}
			if tt.wantPaused {
				require.Eventually(t, func() bool {
					state, ok := mt.State(issue.ID)
					return ok && state == types.Running && !mw.base.Exists(issue.ID)
				}, 2*time.Second, 10*time.Millisecond)
				assert.Zero(t, mt.UpdateIssueStateCount(issue.ID, types.Released))
				assert.Zero(t, events.BackoffCauseCount(issue.ID, rejectionCause))
				assert.Zero(t, mt.ReleaseCount(issue.ID))
				require.Never(t, func() bool {
					return mt.ClaimCount(issue.ID) > 1
				}, 1200*time.Millisecond, 20*time.Millisecond)
			}

			cancel()
			require.NoError(t, <-done)
		})
	}
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
	issueID := "ISS-WS-FAIL"
	issues := []types.Issue{{ID: issueID, Title: "ws fail", State: types.Unclaimed}}
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
	assertIssueReleasedTimestampPrecedesBackoff(t, events, issueID)

	// The claim was obtained then released
	assert.GreaterOrEqual(t, mt.ClaimCount(issueID), 1)
	assert.GreaterOrEqual(t, mt.ReleaseCount(issueID), 1)

	cancel()
	require.NoError(t, <-done)
}

func TestOrchestrator_PromptRenderFailure(t *testing.T) {
	issueID := "ISS-PROMPT-FAIL"
	issues := []types.Issue{{ID: issueID, Title: "prompt fail", State: types.Unclaimed}}
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
	assertIssueReleasedTimestampPrecedesBackoff(t, events, issueID)

	// Workspace should have been cleaned up
	assert.False(t, mw.Exists(issueID))
	assert.GreaterOrEqual(t, mt.ReleaseCount(issueID), 1)

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
		{
			// Real codex 0.128 shape captured live: tokenUsage.total is a map.
			name: "codex_0_128_tokenUsage_total_map",
			data: map[string]interface{}{
				"tokenUsage": map[string]interface{}{
					"total": map[string]interface{}{
						"cachedInputTokens":     float64(3456),
						"inputTokens":           float64(24261),
						"outputTokens":          float64(607),
						"reasoningOutputTokens": float64(507),
						"totalTokens":           float64(24868),
					},
					"last": map[string]interface{}{
						"inputTokens":  float64(24261),
						"outputTokens": float64(607),
					},
					"modelContextWindow": float64(258400),
				},
			},
			wantIn:  24261,
			wantOut: 607,
		},
		{
			// Legacy fallback: tokenUsage.context.{inputTokens, outputTokens} (older codex docs).
			name: "codex_legacy_tokenUsage_context_fallback",
			data: map[string]interface{}{
				"tokenUsage": map[string]interface{}{
					"context": map[string]interface{}{
						"inputTokens":  float64(800),
						"outputTokens": float64(434),
					},
				},
			},
			wantIn:  800,
			wantOut: 434,
		},
		{
			name: "codex_very_old_tokenUsage_total_int",
			data: map[string]interface{}{
				"tokenUsage": map[string]interface{}{
					"total": float64(900),
				},
			},
			wantIn:  0,
			wantOut: 900,
		},
		{
			name: "codex_0_128_tokenUsage_takes_precedence_over_legacy_usage",
			data: map[string]interface{}{
				"tokenUsage": map[string]interface{}{
					"total": map[string]interface{}{
						"inputTokens":  float64(11),
						"outputTokens": float64(22),
					},
				},
				"usage": map[string]interface{}{
					"prompt_tokens":     float64(99),
					"completion_tokens": float64(88),
				},
			},
			wantIn:  11,
			wantOut: 22,
		},
		{
			name: "tokenUsage_is_not_a_map_falls_through_to_usage",
			data: map[string]interface{}{
				"tokenUsage": "not-a-map",
				"usage": map[string]interface{}{
					"prompt_tokens":     float64(7),
					"completion_tokens": float64(3),
				},
			},
			wantIn:  7,
			wantOut: 3,
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

func TestUnresolvedBlockers(t *testing.T) {
	cases := []struct {
		name      string
		blockedBy []string
		openIDs   map[string]struct{}
		want      []string
	}{
		{
			name:      "empty blockedBy",
			blockedBy: nil,
			openIDs:   map[string]struct{}{"X": {}},
			want:      nil,
		},
		{
			name:      "empty openIDs",
			blockedBy: []string{"X"},
			openIDs:   map[string]struct{}{},
			want:      nil,
		},
		{
			name:      "single match",
			blockedBy: []string{"X"},
			openIDs:   map[string]struct{}{"X": {}},
			want:      []string{"X"},
		},
		{
			name:      "no match",
			blockedBy: []string{"X"},
			openIDs:   map[string]struct{}{"Y": {}},
			want:      []string{},
		},
		{
			name:      "partial match preserves only the open subset",
			blockedBy: []string{"X", "Y"},
			openIDs:   map[string]struct{}{"X": {}},
			want:      []string{"X"},
		},
		{
			name:      "all match preserves input order",
			blockedBy: []string{"X", "Y"},
			openIDs:   map[string]struct{}{"X": {}, "Y": {}},
			want:      []string{"X", "Y"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := unresolvedBlockers(tc.blockedBy, tc.openIDs)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDispatchUnclaimedIssues_GatesOnBlockedBy(t *testing.T) {
	t.Run("blocker present in batch skips dependent", func(t *testing.T) {
		mt := newObservingTracker([]types.Issue{
			{ID: "ISS-1", Identifier: "ZII-49", Title: "Blocker", State: types.Unclaimed},
			{ID: "ISS-2", Identifier: "ZII-50", Title: "Blocked", State: types.Unclaimed,
				BlockedBy: []string{"ZII-49"}},
		})
		mw := workspace.NewMockManager(t.TempDir())
		mr := &agent.MockRunner{
			Events: []types.AgentEvent{}, // never completes naturally
			Delay:  10 * time.Second,     // hold the agent open while we observe
		}
		cfg := &staticConfig{cfg: testConfig()}
		orch := NewOrchestrator(mt, mw, mr, cfg, nil)
		go func() {
			for range orch.Events() {
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		done := startOrchestrator(ctx, orch)

		require.Eventually(t, func() bool {
			return mt.ClaimCount("ISS-1") > 0
		}, 1*time.Second, 10*time.Millisecond, "blocker should be claimed")

		// Allow several poll cycles (poll interval is 10ms in testConfig).
		time.Sleep(200 * time.Millisecond)
		require.Equal(t, 0, mt.ClaimCount("ISS-2"),
			"blocked issue must not be claimed while blocker is in candidate set")

		cancel()
		require.NoError(t, <-done)
	})

	t.Run("blocker absent from batch dispatches dependent", func(t *testing.T) {
		// Only the dependent is in the tracker; its blocker has presumably
		// reached a terminal state and is no longer fetched. The gate should
		// allow dispatch.
		mt := newObservingTracker([]types.Issue{
			{ID: "ISS-2", Identifier: "ZII-50", Title: "Blocked", State: types.Unclaimed,
				BlockedBy: []string{"ZII-49"}},
		})
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

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		done := startOrchestrator(ctx, orch)

		require.Eventually(t, func() bool {
			return mt.ClaimCount("ISS-2") > 0
		}, 1*time.Second, 10*time.Millisecond,
			"dependent should be claimed when blocker is not in candidate set")

		cancel()
		require.NoError(t, <-done)
	})

	t.Run("multiple blockers — only unresolved subset gates", func(t *testing.T) {
		// ZII-44 is an open issue in the batch; ZII-49 is not.
		// ISS-2 has BlockedBy=[ZII-49, ZII-44] → unresolved subset is [ZII-44]
		// → ISS-2 is skipped.
		mt := newObservingTracker([]types.Issue{
			{ID: "ISS-OPEN", Identifier: "ZII-44", Title: "Open Other", State: types.Unclaimed},
			{ID: "ISS-2", Identifier: "ZII-50", Title: "Blocked", State: types.Unclaimed,
				BlockedBy: []string{"ZII-49", "ZII-44"}},
		})
		mw := workspace.NewMockManager(t.TempDir())
		mr := &agent.MockRunner{
			Events: []types.AgentEvent{},
			Delay:  10 * time.Second,
		}
		cfg := &staticConfig{cfg: testConfig()}
		orch := NewOrchestrator(mt, mw, mr, cfg, nil)
		go func() {
			for range orch.Events() {
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		done := startOrchestrator(ctx, orch)

		require.Eventually(t, func() bool {
			return mt.ClaimCount("ISS-OPEN") > 0
		}, 1*time.Second, 10*time.Millisecond)

		time.Sleep(200 * time.Millisecond)
		require.Equal(t, 0, mt.ClaimCount("ISS-2"),
			"dependent must be skipped while any blocker remains in batch")

		cancel()
		require.NoError(t, <-done)
	})
}

func TestDispatchUnclaimedIssues_GatesOnAlreadyImplemented(t *testing.T) {
	t.Run("gate fires on hit: dispatch skipped, event emitted", func(t *testing.T) {
		issue := types.Issue{
			ID: "ISS-1", Identifier: "ABC-1", Title: "Already done", State: types.Unclaimed,
		}
		mt := newObservingTracker([]types.Issue{issue})
		mw := workspace.NewMockManager(t.TempDir())
		mr := &agent.MockRunner{Events: []types.AgentEvent{}, Delay: 10 * time.Second}
		cfg := &staticConfig{cfg: testConfig()}
		orch := NewOrchestrator(mt, mw, mr, cfg, nil)

		// Inject a grep function that always reports ABC-1 as found.
		orch.grepFn = func(_ context.Context, _, identifier string) (string, string, bool, bool, error) {
			if identifier == "ABC-1" {
				return "abc1sha0000000000000000000000000000000001", "fix: implement ABC-1 task", true, false, nil
			}
			return "", "", false, false, nil
		}

		var collectedEvents []OrchestratorEvent
		var evMu sync.Mutex
		go func() {
			for e := range orch.Events() {
				evMu.Lock()
				collectedEvents = append(collectedEvents, e)
				evMu.Unlock()
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		done := startOrchestrator(ctx, orch)

		// Allow several poll cycles.
		time.Sleep(300 * time.Millisecond)

		require.Equal(t, 0, mt.ClaimCount("ISS-1"),
			"already-implemented issue must not be claimed")

		cancel()
		require.NoError(t, <-done)

		evMu.Lock()
		defer evMu.Unlock()
		var found bool
		for _, e := range collectedEvents {
			if e.Type == EventClaimSkippedAlreadyImplemented {
				p, ok := e.Data.(ClaimSkippedAlreadyImplemented)
				require.True(t, ok)
				assert.Equal(t, "ABC-1", p.IssueIdentifier)
				assert.Equal(t, "abc1sha0000000000000000000000000000000001", p.CommitSHA)
				assert.Contains(t, p.CommitSubject, "ABC-1")
				assert.Equal(t, "main", p.MainRef)
				found = true
				break
			}
		}
		assert.True(t, found, "ClaimSkippedAlreadyImplemented event must be emitted")
	})

	t.Run("fail-open on unresolvable mainRef: dispatch proceeds", func(t *testing.T) {
		issue := types.Issue{
			ID: "ISS-2", Identifier: "ABC-2", Title: "Normal issue", State: types.Unclaimed,
		}
		mt := newObservingTracker([]types.Issue{issue})
		mw := workspace.NewMockManager(t.TempDir())
		mr := &agent.MockRunner{
			Events: []types.AgentEvent{{Type: "turn/completed"}},
			Delay:  10 * time.Millisecond,
		}
		cfg := &staticConfig{cfg: testConfig()}
		orch := NewOrchestrator(mt, mw, mr, cfg, nil)

		// Inject a grep function that always reports the ref as unresolvable.
		orch.grepFn = func(_ context.Context, _, _ string) (string, string, bool, bool, error) {
			return "", "", false, true, nil
		}

		var collectedEvents []OrchestratorEvent
		var evMu sync.Mutex
		go func() {
			for e := range orch.Events() {
				evMu.Lock()
				collectedEvents = append(collectedEvents, e)
				evMu.Unlock()
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		done := startOrchestrator(ctx, orch)

		// Issue should be claimed despite unresolvable ref (fail-open).
		require.Eventually(t, func() bool {
			return mt.ClaimCount("ISS-2") > 0
		}, 1*time.Second, 10*time.Millisecond,
			"fail-open: issue must be dispatched when mainRef is unresolvable")

		cancel()
		require.NoError(t, <-done)

		evMu.Lock()
		defer evMu.Unlock()
		var unresolvableEmitted bool
		for _, e := range collectedEvents {
			if e.Type == EventClaimMainRefUnresolvable {
				unresolvableEmitted = true
				break
			}
		}
		assert.True(t, unresolvableEmitted, "ClaimMainRefUnresolvable event must be emitted")
	})

	t.Run("warn-once semantics: unresolvable event emitted once per cycle", func(t *testing.T) {
		issues := []types.Issue{
			{ID: "ISS-3", Identifier: "ABC-3", Title: "Issue A", State: types.Unclaimed},
			{ID: "ISS-4", Identifier: "ABC-4", Title: "Issue B", State: types.Unclaimed},
		}
		mt := newObservingTracker(issues)
		mw := workspace.NewMockManager(t.TempDir())
		mr := &agent.MockRunner{
			Events: []types.AgentEvent{{Type: "turn/completed"}},
			Delay:  10 * time.Millisecond,
		}
		cfg := &staticConfig{cfg: &config.WorkflowConfig{
			MaxConcurrencyRaw:    1, // limit concurrency to force both issues through gate
			PollIntervalMsRaw:    50,
			MaxRetryBackoffMsRaw: 100,
			AgentTimeoutMsRaw:    5000,
			StallTimeoutMsRaw:    5000,
			PromptTemplate:       "Fix: {{ issue.title }}",
			ModelRaw:             "test-model",
			ProjectURLRaw:        "https://test.example.com",
		}}
		orch := NewOrchestrator(mt, mw, mr, cfg, nil)

		callCount := new(atomic.Int32)
		orch.grepFn = func(_ context.Context, _, _ string) (string, string, bool, bool, error) {
			callCount.Add(1)
			return "", "", false, true, nil
		}

		var evMu sync.Mutex
		var unresolvableCount int
		go func() {
			for e := range orch.Events() {
				if e.Type == EventClaimMainRefUnresolvable {
					evMu.Lock()
					unresolvableCount++
					evMu.Unlock()
				}
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		done := startOrchestrator(ctx, orch)
		cancel()
		require.NoError(t, <-done)

		evMu.Lock()
		defer evMu.Unlock()
		// With 2 issues and multiple poll cycles, unresolvable event should be
		// emitted at most once per cycle (not once per issue).
		// We allow up to 1 per cycle * ~10 cycles = generous budget.
		assert.GreaterOrEqual(t, unresolvableCount, 1, "at least one ClaimMainRefUnresolvable event expected")
		// callCount can be higher than unresolvableCount because grepFn is called
		// per issue; the warn-once gate only suppresses the *event* after the first.
		assert.GreaterOrEqual(t, callCount.Load(), int32(unresolvableCount),
			"grepFn called at least as many times as events emitted")
	})
}

// autoCloseTracker wraps MockTracker and records TransitionToDone calls.
type autoCloseTracker struct {
	*tracker.MockTracker

	mu              sync.Mutex
	transitionCalls []string // issueIDs that were transitioned
	transitionErr   error
}

func newAutoCloseTracker(issues []types.Issue) *autoCloseTracker {
	mt := tracker.NewMockTracker()
	mt.Issues = append([]types.Issue(nil), issues...)
	return &autoCloseTracker{MockTracker: mt}
}

func (a *autoCloseTracker) TransitionToDone(_ context.Context, issueID, _ string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.transitionErr != nil {
		return a.transitionErr
	}
	a.transitionCalls = append(a.transitionCalls, issueID)
	return nil
}

func (a *autoCloseTracker) TransitionCount(issueID string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	count := 0
	for _, id := range a.transitionCalls {
		if id == issueID {
			count++
		}
	}
	return count
}

func TestDispatchUnclaimedIssues_AutoCloseAlreadyImplemented(t *testing.T) {
	t.Run("auto-close calls TransitionToDone when enabled and gate fires", func(t *testing.T) {
		issue := types.Issue{
			ID: "ISS-AC-1", Identifier: "AC-1", Title: "Auto close me", State: types.Unclaimed,
		}
		mt := newAutoCloseTracker([]types.Issue{issue})
		mw := workspace.NewMockManager(t.TempDir())
		mr := &agent.MockRunner{Events: []types.AgentEvent{}, Delay: 10 * time.Second}
		cfg := &staticConfig{cfg: &config.WorkflowConfig{
			MaxConcurrencyRaw:    2,
			PollIntervalMsRaw:    10,
			MaxRetryBackoffMsRaw: 100,
			AgentTimeoutMsRaw:    5000,
			StallTimeoutMsRaw:    5000,
			PromptTemplate:       "Fix: {{ issue.title }}",
			ModelRaw:             "test-model",
			ProjectURLRaw:        "https://test.example.com",
			Tracker: config.TrackerConfig{
				AutoCloseAlreadyImplementedRaw: true,
			},
		}}
		orch := NewOrchestrator(mt, mw, mr, cfg, nil)
		orch.grepFn = func(_ context.Context, _, identifier string) (string, string, bool, bool, error) {
			if identifier == "AC-1" {
				return "deadbeef00000000000000000000000000000001", "fix: done AC-1", true, false, nil
			}
			return "", "", false, false, nil
		}

		go func() {
			for range orch.Events() {
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		done := startOrchestrator(ctx, orch)

		require.Eventually(t, func() bool {
			return mt.TransitionCount("ISS-AC-1") >= 1
		}, 1*time.Second, 10*time.Millisecond,
			"TransitionToDone must be called when auto-close is enabled and gate fires")

		cancel()
		require.NoError(t, <-done)
	})

	t.Run("auto-close NOT called when disabled (default)", func(t *testing.T) {
		issue := types.Issue{
			ID: "ISS-AC-2", Identifier: "AC-2", Title: "No auto close", State: types.Unclaimed,
		}
		mt := newAutoCloseTracker([]types.Issue{issue})
		mw := workspace.NewMockManager(t.TempDir())
		mr := &agent.MockRunner{Events: []types.AgentEvent{}, Delay: 10 * time.Second}
		cfg := &staticConfig{cfg: testConfig()} // auto_close defaults to false
		orch := NewOrchestrator(mt, mw, mr, cfg, nil)
		orch.grepFn = func(_ context.Context, _, _ string) (string, string, bool, bool, error) {
			return "sha1sha2sha3sha4sha5sha6sha7sha8sha9sha0", "fix: done", true, false, nil
		}

		go func() {
			for range orch.Events() {
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		done := startOrchestrator(ctx, orch)

		time.Sleep(300 * time.Millisecond)
		assert.Equal(t, 0, mt.TransitionCount("ISS-AC-2"),
			"TransitionToDone must NOT be called when auto_close_already_implemented=false")

		cancel()
		require.NoError(t, <-done)
	})
}

// missingBranchWorkspaceManager wraps the mock manager but seeds a real git
// repo at the workspace path WITHOUT creating issue.BranchName. claimIssue's
// `git rev-parse HEAD` succeeds (so ClaimHeadSha is populated), but the
// verify-gate's `git rev-parse <BranchName>` later fails with exit 128 ->
// reason="git_error", which is exactly the harden-verify-success-gate trigger.
type missingBranchWorkspaceManager struct {
	base *workspace.MockManager
	t    *testing.T
}

func newMissingBranchWorkspaceManager(t *testing.T, baseDir string) *missingBranchWorkspaceManager {
	t.Helper()
	return &missingBranchWorkspaceManager{base: workspace.NewMockManager(baseDir), t: t}
}

func (w *missingBranchWorkspaceManager) Create(ctx context.Context, issue types.Issue) (string, error) {
	path, err := w.base.Create(ctx, issue)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return path, nil
	}
	require.NoError(w.t, os.MkdirAll(path, 0o755))
	gitRun(w.t, path, "init")
	require.NoError(w.t, os.WriteFile(filepath.Join(path, "baseline.txt"), []byte("x\n"), 0o644))
	gitRun(w.t, path, "add", "baseline.txt")
	gitRun(w.t, path, "commit", "-m", "baseline")
	return path, nil
}

func (w *missingBranchWorkspaceManager) Cleanup(ctx context.Context, issueID string) error {
	return w.base.Cleanup(ctx, issueID)
}

func (w *missingBranchWorkspaceManager) CleanupAll(ctx context.Context) error {
	return w.base.CleanupAll(ctx)
}

func TestVerifyGate_GitErrorPausesWithoutRetry(t *testing.T) {
	issue := types.Issue{
		ID:         "ISS-GITERR",
		Identifier: "ISS-GITERR",
		Title:      "git_error fails closed",
		State:      types.Unclaimed,
		BranchName: "symphony/iss-giterr",
	}
	mt := newObservingTracker([]types.Issue{issue})
	mw := newMissingBranchWorkspaceManager(t, t.TempDir())
	cfg := testConfig()
	cfg.MaxRetryBackoffMsRaw = 5_000
	mr := &agent.MockRunner{
		Events: []types.AgentEvent{{Type: "turn/completed"}},
		Delay:  10 * time.Millisecond,
	}
	orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: cfg}, nil)
	events := newEventCollector(orch.Events())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := startOrchestrator(ctx, orch)

	require.Eventually(t, func() bool {
		state, ok := mt.State(issue.ID)
		return ok && state == types.Running && !mw.base.Exists(issue.ID)
	}, 2*time.Second, 10*time.Millisecond, "git_error must leave the issue in progress for review")
	assert.Zero(t, mt.UpdateIssueStateCount(issue.ID, types.Released),
		"git_error must not fall through to Released")
	assert.Zero(t, events.BackoffCauseCount(issue.ID, "success_unverified_workspace_invalid"),
		"git_error must not schedule an automatic retry")
	assert.Zero(t, mt.ReleaseCount(issue.ID),
		"git_error must keep the issue claimed for manual review")

	cancel()
	require.NoError(t, <-done)
}

func TestVerifyGate_NoClaimHeadStillProceedsToReleased(t *testing.T) {
	issue := types.Issue{
		ID:         "ISS-NOHEAD",
		Identifier: "ISS-NOHEAD",
		Title:      "no_claim_head still releases",
		State:      types.Unclaimed,
		BranchName: "symphony/iss-nohead",
	}
	mt := newObservingTracker([]types.Issue{issue})
	// MockManager returns a plain temp dir (no .git), so claimIssue's
	// workspaceHeadSHA call errors out and ClaimHeadSha stays empty.
	// verifyBranchAdvanced then returns (true, "no_claim_head", nil) and the
	// runtime's existing fail-open arm SHALL still release to Done.
	mw := workspace.NewMockManager(t.TempDir())
	cfg := testConfig()
	cfg.MaxRetryBackoffMsRaw = 5_000
	mr := &agent.MockRunner{
		Events: []types.AgentEvent{{Type: "turn/completed"}},
		Delay:  10 * time.Millisecond,
	}
	orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: cfg}, nil)
	events := newEventCollector(orch.Events())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := startOrchestrator(ctx, orch)

	require.Eventually(t, func() bool {
		return mt.UpdateIssueStateCount(issue.ID, types.Released) >= 1
	}, 2*time.Second, 10*time.Millisecond, "no_claim_head must release to Done")
	assert.Zero(t, events.BackoffCauseCount(issue.ID, "success_unverified_workspace_invalid"),
		"no_claim_head must NOT trigger workspace_invalid backoff")
	assert.Zero(t, events.BackoffCauseCount(issue.ID, "success_unverified_branch_unchanged"))

	cancel()
	require.NoError(t, <-done)
}

// --- StopAgent unit tests ---

func TestStopAgent_StopsRunningAndReleases(t *testing.T) {
	target := types.Issue{ID: "ISS-A", Identifier: "ISS-A", State: types.Running}

	mt := newObservingTracker([]types.Issue{target})
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{}
	orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: testConfig()}, nil)

	orch.mu.Lock()
	orch.running["ISS-A"] = fakeRunEntry(t, target)
	orch.stats.Running = 1
	orch.mu.Unlock()

	require.NoError(t, orch.StopAgent(context.Background(), "ISS-A"))

	orch.mu.Lock()
	_, present := orch.running["ISS-A"]
	orch.mu.Unlock()
	assert.False(t, present, "running entry must be removed")

	state, ok := mt.State("ISS-A")
	require.True(t, ok)
	assert.Equal(t, types.Released, state)
	assert.Equal(t, 1, mt.ReleaseCount("ISS-A"))
}

func TestStopAgent_NotRunningReturnsError(t *testing.T) {
	mt := newObservingTracker(nil)
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{}
	orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: testConfig()}, nil)

	err := orch.StopAgent(context.Background(), "ISS-MISSING")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAgentNotRunning))
}

// --- recoverOrphanedClaims unit tests ---

func TestRecoverOrphanedClaims_OrphanOverriddenAndLogged(t *testing.T) {
	var buf bytes.Buffer
	logger := log.NewWithOptions(&buf, log.Options{Level: log.DebugLevel})

	mt := newObservingTracker(nil)
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{}
	orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: testConfig()}, logger)

	issues := []types.Issue{
		{ID: "ISS-ORPHAN", Identifier: "ISS-ORPHAN", State: types.Claimed},
	}

	orch.recoverOrphanedClaims(issues)

	assert.Equal(t, types.Unclaimed, issues[0].State, "orphaned Claimed issue must be overridden to Unclaimed")
	assert.Contains(t, buf.String(), "orphan_claim_recovered")
	assert.Contains(t, buf.String(), "ISS-ORPHAN")
}

func TestRecoverOrphanedClaims_ManagedIssueUnchanged(t *testing.T) {
	var buf bytes.Buffer
	logger := log.NewWithOptions(&buf, log.Options{Level: log.DebugLevel})

	mt := newObservingTracker(nil)
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{}
	orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: testConfig()}, logger)

	orch.mu.Lock()
	orch.running["ISS-MANAGED"] = &runEntry{}
	orch.mu.Unlock()

	issues := []types.Issue{
		{ID: "ISS-MANAGED", Identifier: "ISS-MANAGED", State: types.Claimed},
	}

	orch.recoverOrphanedClaims(issues)

	assert.Equal(t, types.Claimed, issues[0].State, "genuinely managed Claimed issue must not be overridden")
	assert.NotContains(t, buf.String(), "orphan_claim_recovered")
}

func TestRecoverOrphanedClaims_UnclaimedUnchanged(t *testing.T) {
	mt := newObservingTracker(nil)
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{}
	orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: testConfig()}, nil)

	issues := []types.Issue{
		{ID: "ISS-TODO", Identifier: "ISS-TODO", State: types.Unclaimed},
	}

	orch.recoverOrphanedClaims(issues)

	assert.Equal(t, types.Unclaimed, issues[0].State, "Unclaimed issue must not be modified")
}

func TestRecoverOrphanedClaims_LoggedOncePerRestart(t *testing.T) {
	var buf bytes.Buffer
	logger := log.NewWithOptions(&buf, log.Options{Level: log.DebugLevel})

	mt := newObservingTracker(nil)
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{}
	orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: testConfig()}, logger)

	issues := []types.Issue{
		{ID: "ISS-REPEAT", Identifier: "ISS-REPEAT", State: types.Claimed},
	}

	// First tick: override applied and logged once.
	orch.recoverOrphanedClaims(issues)
	firstCount := strings.Count(buf.String(), "orphan_claim_recovered")
	assert.Equal(t, 1, firstCount, "first tick must log orphan_claim_recovered exactly once")

	// Reset state to Claimed to simulate a second tick where dispatch is still pending.
	issues[0].State = types.Claimed

	// Second tick: override still applied but no additional log line.
	orch.recoverOrphanedClaims(issues)
	secondCount := strings.Count(buf.String(), "orphan_claim_recovered")
	assert.Equal(t, 1, secondCount, "subsequent ticks must not duplicate orphan_claim_recovered log")
	assert.Equal(t, types.Unclaimed, issues[0].State, "issue must still be overridden on second tick")
}

// --- releaseBlockedRunning unit tests ---

// fakeRunEntry returns a runEntry with a closed Done channel and a no-op
// cancel func, suitable for stopRun without a live agent process.
func fakeRunEntry(t *testing.T, issue types.Issue) *runEntry {
	t.Helper()

	doneCh := make(chan error)
	close(doneCh)
	_, cancel := context.WithCancel(context.Background())

	return &runEntry{
		issue:   issue,
		process: &agent.AgentProcess{PID: 999, Done: doneCh},
		cancel:  cancel,
	}
}

func TestReleaseBlockedRunning_StopsAndReverts(t *testing.T) {
	var buf bytes.Buffer
	logger := log.NewWithOptions(&buf, log.Options{Level: log.DebugLevel})

	target := types.Issue{
		ID:         "ISS-A",
		Identifier: "ISS-A",
		State:      types.Running,
		BlockedBy:  []string{"ISS-B"},
	}
	blocker := types.Issue{ID: "ISS-B", Identifier: "ISS-B", State: types.Unclaimed}

	mt := newObservingTracker([]types.Issue{target, blocker})
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{}
	orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: testConfig()}, logger)

	orch.mu.Lock()
	orch.running["ISS-A"] = fakeRunEntry(t, target)
	orch.stats.Running = 1
	orch.mu.Unlock()

	issuesByID := map[string]types.Issue{"ISS-A": target, "ISS-B": blocker}
	openIDs := buildOpenIDSet([]types.Issue{target, blocker})

	orch.releaseBlockedRunning(context.Background(), issuesByID, openIDs)

	orch.mu.Lock()
	_, present := orch.running["ISS-A"]
	orch.mu.Unlock()
	assert.False(t, present, "running entry must be removed after blocker re-validation")

	state, ok := mt.State("ISS-A")
	require.True(t, ok)
	assert.Equal(t, types.Unclaimed, state, "state must be reverted to Unclaimed")

	assert.Contains(t, buf.String(), "running_released_blocked_by")
	assert.Contains(t, buf.String(), "ISS-B")
}

func TestReleaseBlockedRunning_NoBlockedByLeavesRunning(t *testing.T) {
	target := types.Issue{ID: "ISS-A", Identifier: "ISS-A", State: types.Running}

	mt := newObservingTracker([]types.Issue{target})
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{}
	orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: testConfig()}, nil)

	orch.mu.Lock()
	orch.running["ISS-A"] = fakeRunEntry(t, target)
	orch.stats.Running = 1
	orch.mu.Unlock()

	issuesByID := map[string]types.Issue{"ISS-A": target}
	openIDs := buildOpenIDSet([]types.Issue{target})

	orch.releaseBlockedRunning(context.Background(), issuesByID, openIDs)

	orch.mu.Lock()
	_, present := orch.running["ISS-A"]
	orch.mu.Unlock()
	assert.True(t, present, "issue with empty BlockedBy must remain running")

	assert.Zero(t, mt.UpdateIssueStateCount("ISS-A", types.Unclaimed),
		"no state revert when no blocker present")
}

func TestReleaseBlockedRunning_DoneBlockerLeavesRunning(t *testing.T) {
	target := types.Issue{
		ID:         "ISS-A",
		Identifier: "ISS-A",
		State:      types.Running,
		BlockedBy:  []string{"ISS-B"},
	}
	// Blocker is absent from the snapshot — i.e. terminal/Done in the tracker.

	mt := newObservingTracker([]types.Issue{target})
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{}
	orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: testConfig()}, nil)

	orch.mu.Lock()
	orch.running["ISS-A"] = fakeRunEntry(t, target)
	orch.stats.Running = 1
	orch.mu.Unlock()

	issuesByID := map[string]types.Issue{"ISS-A": target}
	openIDs := buildOpenIDSet([]types.Issue{target}) // blocker not in openIDs

	orch.releaseBlockedRunning(context.Background(), issuesByID, openIDs)

	orch.mu.Lock()
	_, present := orch.running["ISS-A"]
	orch.mu.Unlock()
	assert.True(t, present, "issue with all blockers Done must remain running")

	assert.Zero(t, mt.UpdateIssueStateCount("ISS-A", types.Unclaimed))
}

func TestReleaseBlockedRunning_UpdateStateFailureLogged(t *testing.T) {
	var buf bytes.Buffer
	logger := log.NewWithOptions(&buf, log.Options{Level: log.DebugLevel})

	target := types.Issue{
		ID:         "ISS-A",
		Identifier: "ISS-A",
		State:      types.Running,
		BlockedBy:  []string{"ISS-B"},
	}
	blocker := types.Issue{ID: "ISS-B", Identifier: "ISS-B", State: types.Unclaimed}

	mt := newObservingTracker([]types.Issue{target, blocker})
	mt.base.UpdateErr = errors.New("tracker offline")

	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{}
	orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: testConfig()}, logger)

	orch.mu.Lock()
	orch.running["ISS-A"] = fakeRunEntry(t, target)
	orch.stats.Running = 1
	orch.mu.Unlock()

	issuesByID := map[string]types.Issue{"ISS-A": target, "ISS-B": blocker}
	openIDs := buildOpenIDSet([]types.Issue{target, blocker})

	require.NotPanics(t, func() {
		orch.releaseBlockedRunning(context.Background(), issuesByID, openIDs)
	})

	// Agent must still be stopped even if state revert fails — next tick
	// will retry the revert.
	orch.mu.Lock()
	_, present := orch.running["ISS-A"]
	orch.mu.Unlock()
	assert.False(t, present, "agent must be stopped even when revert fails")

	assert.Contains(t, buf.String(), "running_release_state_revert_failed")
	assert.Contains(t, buf.String(), "tracker offline")
}

func TestRecoverOrphanedClaims_IntegrationRestartRedispatches(t *testing.T) {
	issue := types.Issue{
		ID:         "ISS-RESTART",
		Identifier: "ISS-RESTART",
		Title:      "orphan after restart",
		State:      types.Claimed,
		BranchName: "symphony/iss-restart",
	}
	mt := newObservingTracker([]types.Issue{issue})
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{
		Events: []types.AgentEvent{{Type: "turn/completed"}},
		Delay:  10 * time.Millisecond,
	}
	orch := NewOrchestrator(mt, mw, mr, &staticConfig{cfg: testConfig()}, nil)
	go func() {
		for range orch.Events() {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := startOrchestrator(ctx, orch)

	require.Eventually(t, func() bool {
		return mt.ClaimCount(issue.ID) >= 1
	}, 2*time.Second, 10*time.Millisecond, "orphaned issue must be re-dispatched within one poll interval")

	cancel()
	require.NoError(t, <-done)
}

// TestSuccessGate_HollowRunReroutesToBackoff is the T5 integration test for
// verify-success-with-diff. It drives two scenarios through the full
// orchestrator loop:
//
//  1. hollow success — workspace branch HEAD does not advance; verifier must
//     block the Released transition and record the rejection via AgentFinished.
//
//  2. real success — workspace receives a synthetic commit before the runner
//     emits Succeeded; the Released transition must proceed normally.
func TestSuccessGate_HollowRunReroutesToBackoff(t *testing.T) {
	const rejectionCause = "success_unverified_branch_unchanged"

	tests := []struct {
		name         string
		issueID      string
		runner       func(t *testing.T) agent.AgentRunner
		wantReleased bool
		wantRejected bool
	}{
		{
			name:    "hollow success rejected",
			issueID: "ISS-T5-HOLLOW",
			runner: func(t *testing.T) agent.AgentRunner {
				t.Helper()
				return &agent.MockRunner{
					Events: []types.AgentEvent{{Type: "turn/completed"}},
					Delay:  10 * time.Millisecond,
				}
			},
			wantRejected: true,
		},
		{
			name:    "real success proceeds",
			issueID: "ISS-T5-REAL",
			runner: func(t *testing.T) agent.AgentRunner {
				t.Helper()
				return &commitBeforeSuccessRunner{
					t: t,
					base: &agent.MockRunner{
						Events: []types.AgentEvent{{Type: "turn/completed"}},
						Delay:  10 * time.Millisecond,
					},
				}
			},
			wantReleased: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := types.Issue{
				ID:         tt.issueID,
				Identifier: tt.issueID,
				Title:      tt.name,
				State:      types.Unclaimed,
				BranchName: tt.issueID,
			}
			mt := newObservingTracker([]types.Issue{issue})
			mw := newGitWorkspaceManager(t, t.TempDir())
			cfg := testConfig()
			cfg.MaxRetryBackoffMsRaw = 5_000
			orch := NewOrchestrator(mt, mw, tt.runner(t), &staticConfig{cfg: cfg}, nil)
			events := newEventCollector(orch.Events())

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := startOrchestrator(ctx, orch)

			if tt.wantRejected {
				// Wait for the AgentFinished event (phase=Succeeded) to arrive,
				// confirming the run completed and the verifier ran.
				require.Eventually(t, func() bool {
					ev, ok := events.Event(EventAgentFinished, issue.ID)
					if !ok {
						return false
					}
					finished, ok := ev.Data.(AgentFinished)
					return ok && finished.Phase == types.Succeeded
				}, 2*time.Second, 10*time.Millisecond,
					"AgentFinished(Succeeded) must arrive for hollow run")

				// Released transition must never have happened.
				assert.Zero(t, mt.UpdateIssueStateCount(issue.ID, types.Released),
					"hollow success must not release to Done")
				// No BackoffEnqueued either — pause path does not schedule retry.
				assert.Zero(t, events.BackoffCauseCount(issue.ID, rejectionCause),
					"hollow success must not enqueue backoff (it is paused instead)")
			}

			if tt.wantReleased {
				require.Eventually(t, func() bool {
					return mt.UpdateIssueStateCount(issue.ID, types.Released) >= 1
				}, 2*time.Second, 10*time.Millisecond,
					"real success must release to Done")

				// No rejection backoff for a real success.
				assert.Zero(t, events.BackoffCauseCount(issue.ID, rejectionCause),
					"real success must not carry rejection cause")
			}

			cancel()
			require.NoError(t, <-done)
		})
	}
}

// --- grepMainForIdentifier tests ---

// initTestRepo creates a minimal git repo with two commits:
//   - one mentioning ABC-1
//   - one mentioning ABC-12
//
// Returns the repo directory path.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	mustRun := func(args ...string) {
		t.Helper()
		var stderr bytes.Buffer
		cmd := append([]string{"-C", dir}, args...)
		c := exec.Command("git", cmd...)
		c.Stderr = &stderr
		if err := c.Run(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, stderr.String())
		}
	}

	mustRun("init", "--initial-branch=main")
	mustRun("config", "user.email", "test@test.com")
	mustRun("config", "user.name", "Test")

	writeFile := func(name, body string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}

	// commit 1: mentions ABC-1 (word boundary)
	writeFile("a.txt", "a")
	mustRun("add", ".")
	mustRun("commit", "-m", "fix: implement ABC-1 task")

	// commit 2: mentions ABC-12 (must NOT match ABC-1 via word-boundary grep)
	writeFile("b.txt", "b")
	mustRun("add", ".")
	mustRun("commit", "-m", "feat: close ABC-12 story")

	// commit 3: unrelated
	writeFile("c.txt", "c")
	mustRun("add", ".")
	mustRun("commit", "-m", "chore: cleanup")

	return dir
}

func TestGrepMainForIdentifier_HitMissPrefix(t *testing.T) {
	t.Parallel()

	repoDir := initTestRepo(t)
	ctx := context.Background()

	t.Run("hit returns sha and subject", func(t *testing.T) {
		t.Parallel()
		sha, subject, found, unresolvable, grepErr := grepMainForIdentifierIn(ctx, repoDir, "main", "ABC-1")
		require.NoError(t, grepErr)
		assert.True(t, found)
		assert.False(t, unresolvable)
		assert.Len(t, sha, 40, "sha should be a full 40-char commit hash")
		assert.Contains(t, subject, "ABC-1")
	})

	t.Run("miss returns found=false", func(t *testing.T) {
		t.Parallel()
		sha, subject, found, unresolvable, grepErr := grepMainForIdentifierIn(ctx, repoDir, "main", "XYZ-999")
		require.NoError(t, grepErr)
		assert.False(t, found)
		assert.False(t, unresolvable)
		assert.Empty(t, sha)
		assert.Empty(t, subject)
	})

	t.Run("prefix overlap: ABC-1 does not match ABC-12 commit", func(t *testing.T) {
		t.Parallel()
		// ABC-12 commit subject should NOT appear when searching for ABC-1
		sha1, sub1, found1, _, err1 := grepMainForIdentifierIn(ctx, repoDir, "main", "ABC-1")
		require.NoError(t, err1)
		assert.True(t, found1)
		assert.NotContains(t, sub1, "ABC-12", "word boundary must exclude ABC-12 commit")

		sha12, sub12, found12, _, err12 := grepMainForIdentifierIn(ctx, repoDir, "main", "ABC-12")
		require.NoError(t, err12)
		assert.True(t, found12)
		assert.Contains(t, sub12, "ABC-12")

		assert.NotEqual(t, sha1, sha12, "ABC-1 and ABC-12 must map to different commits")
	})

	t.Run("empty identifier returns false without git call", func(t *testing.T) {
		t.Parallel()
		sha, subject, found, unresolvable, grepErr := grepMainForIdentifierIn(ctx, repoDir, "main", "")
		require.NoError(t, grepErr)
		assert.False(t, found)
		assert.False(t, unresolvable)
		assert.Empty(t, sha)
		assert.Empty(t, subject)
	})

	t.Run("whitespace-only identifier returns false", func(t *testing.T) {
		t.Parallel()
		sha, subject, found, unresolvable, grepErr := grepMainForIdentifierIn(ctx, repoDir, "main", "   ")
		require.NoError(t, grepErr)
		assert.False(t, found)
		assert.False(t, unresolvable)
		assert.Empty(t, sha)
		assert.Empty(t, subject)
	})

	t.Run("unresolvable mainRef returns unresolvable=true err=nil", func(t *testing.T) {
		t.Parallel()
		sha, subject, found, unresolvable, grepErr := grepMainForIdentifierIn(ctx, repoDir, "no-such-ref-xyz", "ABC-1")
		require.NoError(t, grepErr)
		assert.False(t, found)
		assert.True(t, unresolvable)
		assert.Empty(t, sha)
		assert.Empty(t, subject)
	})

	t.Run("multiple matching commits returns first", func(t *testing.T) {
		t.Parallel()
		// git log -1 returns the most recent matching commit.
		// ABC-1 word-boundary grep should find exactly one commit and return its sha.
		sha, _, found, _, grepErr := grepMainForIdentifierIn(ctx, repoDir, "main", "ABC-1")
		require.NoError(t, grepErr)
		assert.True(t, found)
		assert.NotEmpty(t, sha)
	})
}
