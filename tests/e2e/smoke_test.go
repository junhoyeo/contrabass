package e2e

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/symphony-charm/internal/agent"
	"github.com/junhoyeo/symphony-charm/internal/config"
	"github.com/junhoyeo/symphony-charm/internal/orchestrator"
	"github.com/junhoyeo/symphony-charm/internal/tracker"
	"github.com/junhoyeo/symphony-charm/internal/types"
	"github.com/junhoyeo/symphony-charm/internal/workspace"
)

type staticConfig struct {
	cfg *config.WorkflowConfig
}

func (s *staticConfig) GetConfig() *config.WorkflowConfig { return s.cfg }

type eventCollector struct {
	mu     sync.Mutex
	events []orchestrator.OrchestratorEvent
}

func newEventCollector(events <-chan orchestrator.OrchestratorEvent, onEvent func(orchestrator.OrchestratorEvent)) *eventCollector {
	c := &eventCollector{events: make([]orchestrator.OrchestratorEvent, 0)}

	go func() {
		for event := range events {
			if onEvent != nil {
				onEvent(event)
			}

			c.mu.Lock()
			c.events = append(c.events, event)
			c.mu.Unlock()
		}
	}()

	return c
}

func (c *eventCollector) Count(eventType orchestrator.EventType) int {
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

func (c *eventCollector) Total() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.events)
}

type countingRunner struct {
	base *agent.MockRunner

	mu     sync.Mutex
	starts int
}

func newCountingRunner(base *agent.MockRunner) *countingRunner {
	return &countingRunner{base: base}
}

func (r *countingRunner) Start(
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
	r.starts++
	r.mu.Unlock()

	return proc, nil
}

func (r *countingRunner) Stop(proc *agent.AgentProcess) error {
	return r.base.Stop(proc)
}

func (r *countingRunner) StartCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.starts
}

func testConfig() *config.WorkflowConfig {
	return &config.WorkflowConfig{
		MaxConcurrencyRaw:    2,
		PollIntervalMsRaw:    100,
		MaxRetryBackoffMsRaw: 200,
		AgentTimeoutMsRaw:    5_000,
		StallTimeoutMsRaw:    5_000,
		PromptTemplate:       "Fix issue: {{ issue.title }}",
		ModelRaw:             "test-model",
		ProjectURLRaw:        "https://example.test/project",
	}
}

func runOrchestrator(ctx context.Context, orch *orchestrator.Orchestrator) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- orch.Run(ctx)
	}()

	return done
}

func TestFullFlow(t *testing.T) {
	mt := tracker.NewMockTracker()
	mt.Issues = []types.Issue{
		{ID: "ISS-1", Title: "First issue", State: types.Unclaimed},
		{ID: "ISS-2", Title: "Second issue", State: types.Unclaimed},
	}
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{
		Events: []types.AgentEvent{
			{Type: "session/started"},
			{Type: "turn/completed", Data: map[string]interface{}{"usage": map[string]interface{}{"input_tokens": 42, "output_tokens": 7}}},
		},
		Delay: 10 * time.Millisecond,
	}
	orch := orchestrator.NewOrchestrator(mt, mw, mr, &staticConfig{cfg: testConfig()}, nil)
	collector := newEventCollector(orch.Events(), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := runOrchestrator(ctx, orch)

	require.Eventually(t, func() bool {
		return collector.Count(orchestrator.EventAgentStarted) >= 2 &&
			collector.Count(orchestrator.EventAgentFinished) >= 2
	}, 4*time.Second, 20*time.Millisecond)

	cancel()
	require.NoError(t, <-done)
	assert.Greater(t, collector.Total(), 0)
	assert.GreaterOrEqual(t, collector.Count(orchestrator.EventStatusUpdate), 1)
}

func TestDryRunFlow(t *testing.T) {
	mt := tracker.NewMockTracker()
	mt.Issues = []types.Issue{
		{ID: "ISS-1", Title: "Dry run issue", State: types.Unclaimed},
		{ID: "ISS-2", Title: "Dry run issue 2", State: types.Unclaimed},
	}
	mw := workspace.NewMockManager(t.TempDir())
	mr := &agent.MockRunner{
		Events: []types.AgentEvent{
			{Type: "session/started"},
			{Type: "turn/completed"},
		},
		Delay: 20 * time.Millisecond,
	}
	orch := orchestrator.NewOrchestrator(mt, mw, mr, &staticConfig{cfg: testConfig()}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var cancelOnce sync.Once
	collector := newEventCollector(orch.Events(), func(orchestrator.OrchestratorEvent) {
		cancelOnce.Do(cancel)
	})

	done := runOrchestrator(ctx, orch)
	require.NoError(t, <-done)

	assert.Greater(t, collector.Total(), 0)
}

func TestNoIssuesFlow(t *testing.T) {
	mt := tracker.NewMockTracker()
	mw := workspace.NewMockManager(t.TempDir())
	runner := newCountingRunner(&agent.MockRunner{})
	orch := orchestrator.NewOrchestrator(mt, mw, runner, &staticConfig{cfg: testConfig()}, nil)
	collector := newEventCollector(orch.Events(), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := runOrchestrator(ctx, orch)

	time.Sleep(250 * time.Millisecond)
	cancel()

	require.NoError(t, <-done)
	assert.Equal(t, 0, runner.StartCount())
	assert.Equal(t, 0, collector.Count(orchestrator.EventAgentStarted))
	assert.GreaterOrEqual(t, collector.Count(orchestrator.EventStatusUpdate), 2)
}
