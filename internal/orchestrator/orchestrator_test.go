package orchestrator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/junhoyeo/symphony-charm/internal/agent"
	"github.com/junhoyeo/symphony-charm/internal/config"
	"github.com/junhoyeo/symphony-charm/internal/tracker"
	"github.com/junhoyeo/symphony-charm/internal/types"
	"github.com/junhoyeo/symphony-charm/internal/workspace"
	"github.com/stretchr/testify/require"
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

type trackingRunner struct {
	base *agent.MockRunner

	mu        sync.Mutex
	active    int
	maxActive int
	starts    int
	stops     int
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
