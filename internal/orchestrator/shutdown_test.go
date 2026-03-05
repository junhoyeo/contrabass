package orchestrator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/junhoyeo/symphony-charm/internal/agent"
	"github.com/junhoyeo/symphony-charm/internal/tracker"
	"github.com/junhoyeo/symphony-charm/internal/types"
	"github.com/junhoyeo/symphony-charm/internal/workspace"
	"github.com/stretchr/testify/require"
)

type stopCountingRunner struct {
	mu        sync.Mutex
	stopCalls int
}

func (r *stopCountingRunner) Start(context.Context, types.Issue, string, string) (*agent.AgentProcess, error) {
	return nil, errors.New("not implemented")
}

func (r *stopCountingRunner) Stop(*agent.AgentProcess) error {
	r.mu.Lock()
	r.stopCalls++
	r.mu.Unlock()

	return nil
}

func (r *stopCountingRunner) StopCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.stopCalls
}

type observingWorkspace struct {
	base *workspace.MockManager

	mu              sync.Mutex
	cleanupAllCalls int
}

func newObservingWorkspace(baseDir string) *observingWorkspace {
	return &observingWorkspace{base: workspace.NewMockManager(baseDir)}
}

func (w *observingWorkspace) Create(ctx context.Context, issue types.Issue) (string, error) {
	return w.base.Create(ctx, issue)
}

func (w *observingWorkspace) Cleanup(ctx context.Context, issueID string) error {
	return w.base.Cleanup(ctx, issueID)
}

func (w *observingWorkspace) CleanupAll(ctx context.Context) error {
	w.mu.Lock()
	w.cleanupAllCalls++
	w.mu.Unlock()

	return w.base.CleanupAll(ctx)
}

func (w *observingWorkspace) CleanupAllCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.cleanupAllCalls
}

func seedRunning(orch *Orchestrator, issueID string) {
	orch.mu.Lock()
	orch.running[issueID] = &runEntry{
		issue:   types.Issue{ID: issueID},
		attempt: types.RunAttempt{IssueID: issueID},
		process: &agent.AgentProcess{PID: 1, SessionID: "seed"},
		cancel:  func() {},
	}
	orch.stats.Running = len(orch.running)
	orch.mu.Unlock()
}

func TestShutdownDrain(t *testing.T) {
	mt := tracker.NewMockTracker()
	ws := newObservingWorkspace(t.TempDir())
	runner := &stopCountingRunner{}
	orch := NewOrchestrator(mt, ws, runner, nil, nil)
	seedRunning(orch, "ISS-1")

	go func() {
		time.Sleep(30 * time.Millisecond)
		orch.mu.Lock()
		delete(orch.running, "ISS-1")
		orch.stats.Running = len(orch.running)
		orch.mu.Unlock()
	}()

	err := GracefulShutdown(func() {}, orch, ShutdownConfig{
		DrainTimeout:   500 * time.Millisecond,
		CleanupTimeout: 500 * time.Millisecond,
	}, nil)

	require.NoError(t, err)
	require.Equal(t, 0, orch.RunningCount())
	require.Equal(t, 0, runner.StopCount())
	require.Equal(t, 1, ws.CleanupAllCount())
}

func TestShutdownTimeout(t *testing.T) {
	mt := tracker.NewMockTracker()
	ws := newObservingWorkspace(t.TempDir())
	runner := &stopCountingRunner{}
	orch := NewOrchestrator(mt, ws, runner, nil, nil)
	seedRunning(orch, "ISS-1")

	err := GracefulShutdown(func() {}, orch, ShutdownConfig{
		DrainTimeout:   100 * time.Millisecond,
		CleanupTimeout: 500 * time.Millisecond,
	}, nil)

	require.NoError(t, err)
	require.Equal(t, 0, orch.RunningCount())
	require.Equal(t, 1, runner.StopCount())
	require.Equal(t, 1, ws.CleanupAllCount())
}

func TestShutdownCleanup(t *testing.T) {
	mt := tracker.NewMockTracker()
	ws := newObservingWorkspace(t.TempDir())
	runner := &stopCountingRunner{}
	orch := NewOrchestrator(mt, ws, runner, nil, nil)

	err := GracefulShutdown(func() {}, orch, ShutdownConfig{
		DrainTimeout:   100 * time.Millisecond,
		CleanupTimeout: 100 * time.Millisecond,
	}, nil)

	require.NoError(t, err)
	require.Equal(t, 1, ws.CleanupAllCount())
}

func TestShutdownNoAgents(t *testing.T) {
	mt := tracker.NewMockTracker()
	ws := newObservingWorkspace(t.TempDir())
	runner := &stopCountingRunner{}
	orch := NewOrchestrator(mt, ws, runner, nil, nil)

	err := GracefulShutdown(func() {}, orch, ShutdownConfig{
		DrainTimeout:   100 * time.Millisecond,
		CleanupTimeout: 100 * time.Millisecond,
	}, nil)

	require.NoError(t, err)
	require.Equal(t, 0, orch.RunningCount())
	require.Equal(t, 0, runner.StopCount())
	require.Equal(t, 1, ws.CleanupAllCount())
}

func TestDefaultShutdownConfig(t *testing.T) {
	cfg := DefaultShutdownConfig()

	require.Equal(t, 30*time.Second, cfg.DrainTimeout)
	require.Equal(t, 10*time.Second, cfg.CleanupTimeout)
}
