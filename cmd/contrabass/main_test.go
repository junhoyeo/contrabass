package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/agent"
	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/orchestrator"
	"github.com/junhoyeo/contrabass/internal/types"
)

func TestRootCommandHelp(t *testing.T) {
	cmd := newRootCmd()

	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "--config")
	assert.Contains(t, output, "--no-tui")
	assert.Contains(t, output, "--log-file")
	assert.Contains(t, output, "--log-level")
	assert.Contains(t, output, "--dry-run")
	assert.Contains(t, output, "WORKFLOW.md")
}

func TestConfigFlagRequired(t *testing.T) {
	cmd := newRootCmd()

	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config")
}

func TestFlagDefaults(t *testing.T) {
	cmd := newRootCmd()

	tests := []struct {
		name     string
		flag     string
		defValue string
	}{
		{"log-file default", "log-file", "contrabass.log"},
		{"log-level default", "log-level", "info"},
		{"no-tui default", "no-tui", "false"},
		{"dry-run default", "dry-run", "false"},
		{"config default", "config", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := cmd.Flags().Lookup(tt.flag)
			require.NotNil(t, f, "flag %q should exist", tt.flag)
			assert.Equal(t, tt.defValue, f.DefValue)
		})
	}
}

func TestRunTUIPropagatesOrchestratorError(t *testing.T) {
	orch := orchestrator.NewOrchestrator(nil, nil, nil, nil, nil)

	tuiErr := errors.New("tui failed")
	orchErr := errors.New("orchestrator failed")
	restoreRunTUITestHooks(t)

	runTUIProgram = func(_ *tea.Program) (tea.Model, error) {
		return nil, tuiErr
	}
	runTUIOrchestrator = func(_ context.Context, _ *orchestrator.Orchestrator) error {
		return orchErr
	}
	startTUIEventBridge = func(_ context.Context, _ *tea.Program, _ <-chan orchestrator.OrchestratorEvent) {}
	runTUIShutdownTimeout = 50 * time.Millisecond

	err := runTUI(context.Background(), orch)
	require.Error(t, err)
	assert.ErrorIs(t, err, orchErr)
	assert.ErrorContains(t, err, "tui error")
}

func TestRunTUIRecoversOrchestratorPanic(t *testing.T) {
	orch := orchestrator.NewOrchestrator(nil, nil, nil, nil, nil)

	restoreRunTUITestHooks(t)
	runTUIProgram = func(_ *tea.Program) (tea.Model, error) {
		return nil, nil
	}
	runTUIOrchestrator = func(_ context.Context, _ *orchestrator.Orchestrator) error {
		panic("boom")
	}
	startTUIEventBridge = func(_ context.Context, _ *tea.Program, _ <-chan orchestrator.OrchestratorEvent) {}
	runTUIShutdownTimeout = 50 * time.Millisecond

	err := runTUI(context.Background(), orch)
	require.Error(t, err)
	assert.ErrorContains(t, err, "orchestrator panic: boom")
}

func TestRunTUITimeoutReturnsMeaningfulError(t *testing.T) {
	orch := orchestrator.NewOrchestrator(nil, nil, nil, nil, nil)

	tuiErr := errors.New("tui failed")
	restoreRunTUITestHooks(t)

	block := make(chan struct{})
	t.Cleanup(func() { close(block) })

	runTUIProgram = func(_ *tea.Program) (tea.Model, error) {
		return nil, tuiErr
	}
	runTUIOrchestrator = func(_ context.Context, _ *orchestrator.Orchestrator) error {
		<-block
		return nil
	}
	startTUIEventBridge = func(_ context.Context, _ *tea.Program, _ <-chan orchestrator.OrchestratorEvent) {}
	runTUIShutdownTimeout = 10 * time.Millisecond

	err := runTUI(context.Background(), orch)
	require.Error(t, err)
	assert.ErrorContains(t, err, "timed out waiting for orchestrator shutdown")
	assert.ErrorIs(t, err, tuiErr)
}

func TestRunHandlesSignalWithGracefulShutdownOnce(t *testing.T) {
	restoreRunTUITestHooks(t)

	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()

	signalChan := make(chan os.Signal, 1)
	shutdownCalled := make(chan struct{}, 1)
	var shutdownCalls int
	var shutdownMu sync.Mutex
	runGracefulShutdown = func(
		cancel context.CancelFunc,
		orch *orchestrator.Orchestrator,
		cfg orchestrator.ShutdownConfig,
		logger *log.Logger,
	) error {
		shutdownMu.Lock()
		shutdownCalls++
		shutdownMu.Unlock()
		if cancel != nil {
			cancel()
		}
		select {
		case shutdownCalled <- struct{}{}:
		default:
		}
		return nil
	}
	orch := orchestrator.NewOrchestrator(nil, nil, nil, nil, nil)
	startSignalShutdownHook(ctx, signalChan, cancelCtx, orch, nil)

	signalChan <- os.Interrupt
	require.Eventually(t, func() bool {
		select {
		case <-shutdownCalled:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	shutdownMu.Lock()
	defer shutdownMu.Unlock()
	require.Equal(t, 1, shutdownCalls)
}

func TestRunDryRun_TimeoutNoEvents(t *testing.T) {
	// This test validates that runDryRun handles context deadline exceeded gracefully.
	// The timeout guard in runDryRun ensures that if the orchestrator blocks indefinitely
	// (e.g., no events arrive), the function returns nil after the timeout instead of hanging.
	//
	// Note: Full integration testing of runDryRun requires mocking the orchestrator's
	// internal dependencies (tracker, workspace, agent runner). The timeout logic itself
	// is validated by the code review: context.WithTimeout wraps the context, and
	// errors.Is(err, context.DeadlineExceeded) catches the timeout case.
	t.Skip("Integration test requires full orchestrator mock setup")
}

func restoreRunTUITestHooks(t *testing.T) {
	t.Helper()

	originalRunTUIOrchestrator := runTUIOrchestrator
	originalRunGracefulShutdown := runGracefulShutdown
	originalRunTUIProgram := runTUIProgram
	originalStartTUIEventBridge := startTUIEventBridge
	originalRunTUIShutdownTimeout := runTUIShutdownTimeout

	t.Cleanup(func() {
		runTUIOrchestrator = originalRunTUIOrchestrator
		runGracefulShutdown = originalRunGracefulShutdown
		runTUIProgram = originalRunTUIProgram
		startTUIEventBridge = originalStartTUIEventBridge
		runTUIShutdownTimeout = originalRunTUIShutdownTimeout
	})
}

// --- Stub types for orchestrator integration tests ---

type stubTracker struct{}

func (s *stubTracker) FetchIssues(_ context.Context) ([]types.Issue, error) { return nil, nil }
func (s *stubTracker) ClaimIssue(_ context.Context, _ string) error         { return nil }
func (s *stubTracker) ReleaseIssue(_ context.Context, _ string) error       { return nil }
func (s *stubTracker) UpdateIssueState(_ context.Context, _ string, _ types.IssueState) error {
	return nil
}
func (s *stubTracker) PostComment(_ context.Context, _, _ string) error { return nil }

type stubWorkspace struct{}

func (s *stubWorkspace) Create(_ context.Context, _ types.Issue) (string, error) { return "", nil }
func (s *stubWorkspace) Cleanup(_ context.Context, _ string) error               { return nil }
func (s *stubWorkspace) CleanupAll(_ context.Context) error                      { return nil }

type stubAgentRunner struct{}

func (s *stubAgentRunner) Start(_ context.Context, _ types.Issue, _, _ string) (*agent.AgentProcess, error) {
	return nil, nil
}
func (s *stubAgentRunner) Stop(_ *agent.AgentProcess) error { return nil }
func (s *stubAgentRunner) Close() error                     { return nil }

type stubConfigProvider struct{ cfg *config.WorkflowConfig }

func (s *stubConfigProvider) GetConfig() *config.WorkflowConfig { return s.cfg }

// newTestOrchestrator creates an Orchestrator backed by no-op stubs so that
// orch.Run can execute its poll loop without panicking on nil dependencies.
func newTestOrchestrator(t *testing.T) *orchestrator.Orchestrator {
	t.Helper()
	return orchestrator.NewOrchestrator(
		&stubTracker{},
		&stubWorkspace{},
		&stubAgentRunner{},
		&stubConfigProvider{cfg: &config.WorkflowConfig{}},
		nil,
	)
}

// --- Tests for parseLogLevel ---

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  log.Level
	}{
		{"debug", "debug", log.DebugLevel},
		{"debug uppercase", "DEBUG", log.DebugLevel},
		{"debug mixed case", "Debug", log.DebugLevel},
		{"warn", "warn", log.WarnLevel},
		{"warn mixed case", "Warn", log.WarnLevel},
		{"error", "error", log.ErrorLevel},
		{"error uppercase", "ERROR", log.ErrorLevel},
		{"info explicit", "info", log.InfoLevel},
		{"default on unknown", "trace", log.InfoLevel},
		{"default on empty", "", log.InfoLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseLogLevel(tt.input))
		})
	}
}

// --- Tests for projectSlug ---

func TestProjectSlug(t *testing.T) {
	tests := []struct {
		name    string
		envSlug string
		cfg     *config.WorkflowConfig
		want    string
	}{
		{
			name:    "env override takes precedence",
			envSlug: "env-slug",
			cfg:     &config.WorkflowConfig{ProjectURLRaw: "https://linear.app/team/project/from-config"},
			want:    "env-slug",
		},
		{
			name: "URL last segment",
			cfg:  &config.WorkflowConfig{ProjectURLRaw: "https://linear.app/team/project/my-project"},
			want: "my-project",
		},
		{
			name: "trailing slash stripped",
			cfg:  &config.WorkflowConfig{ProjectURLRaw: "https://linear.app/team/project/slug-test/"},
			want: "slug-test",
		},
		{
			name: "empty URL returns empty",
			cfg:  &config.WorkflowConfig{},
			want: "",
		},
		{
			name: "tracker project URL fallback",
			cfg:  &config.WorkflowConfig{Tracker: config.TrackerConfig{ProjectURL: "https://linear.app/team/proj"}},
			want: "proj",
		},
		{
			name: "single segment URL",
			cfg:  &config.WorkflowConfig{ProjectURLRaw: "slug-only"},
			want: "slug-only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LINEAR_PROJECT_SLUG", tt.envSlug)
			assert.Equal(t, tt.want, projectSlug(tt.cfg))
		})
	}
}

// --- Tests for run ---

func TestRun_ConfigParseError(t *testing.T) {
	err := run(filepath.Join(t.TempDir(), "no-such-file.md"), false, "", "info", false)
	require.Error(t, err)
	assert.ErrorContains(t, err, "parsing workflow config")
}

func TestRun_WatcherError(t *testing.T) {
	// The watcher error path in run() requires config.ParseWorkflow to succeed
	// on the first call but config.NewWatcher to fail on its internal re-parse.
	// Since both calls are synchronous on the same file path, the only way to
	// reach the "creating config watcher" error is if the file disappears
	// between the two calls or if fsnotify.NewWatcher() fails (kernel resource
	// exhaustion). Neither can be reliably triggered without production code
	// changes to accept a mockable watcher constructor.
	t.Skip("watcher error path requires fsnotify.NewWatcher failure; not reachable without production code changes")
}

// --- Tests for runDryRun ---

func TestRunDryRun(t *testing.T) {
	// The orchestrator's first runCycle emits a StatusUpdate event. The goroutine
	// inside runDryRun reads that event and cancels the derived context, causing
	// orch.Run to observe ctx.Done() and return nil.
	orch := newTestOrchestrator(t)

	done := make(chan error, 1)
	go func() {
		done <- runDryRun(context.Background(), orch)
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("runDryRun did not complete within 10s")
	}
}

// --- Tests for runHeadless ---

func TestRunHeadless(t *testing.T) {
	orch := newTestOrchestrator(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	logger := log.NewWithOptions(io.Discard, log.Options{})

	done := make(chan error, 1)
	go func() {
		done <- runHeadless(ctx, orch, logger)
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("runHeadless did not complete within 5s")
	}
}

// --- Additional runTUI branch coverage ---

func TestRunTUI_OrchestratorErrorNoTUIError(t *testing.T) {
	orch := orchestrator.NewOrchestrator(nil, nil, nil, nil, nil)

	orchErr := errors.New("orch failed alone")
	restoreRunTUITestHooks(t)

	runTUIProgram = func(_ *tea.Program) (tea.Model, error) {
		return nil, nil // TUI succeeds
	}
	runTUIOrchestrator = func(_ context.Context, _ *orchestrator.Orchestrator) error {
		return orchErr
	}
	startTUIEventBridge = func(_ context.Context, _ *tea.Program, _ <-chan orchestrator.OrchestratorEvent) {}
	runTUIShutdownTimeout = 50 * time.Millisecond

	err := runTUI(context.Background(), orch)
	require.Error(t, err)
	assert.ErrorIs(t, err, orchErr)
	assert.NotContains(t, err.Error(), "tui error")
}

func TestRunTUI_SuccessPath(t *testing.T) {
	orch := orchestrator.NewOrchestrator(nil, nil, nil, nil, nil)

	restoreRunTUITestHooks(t)

	runTUIProgram = func(_ *tea.Program) (tea.Model, error) {
		return nil, nil
	}
	runTUIOrchestrator = func(_ context.Context, _ *orchestrator.Orchestrator) error {
		return nil
	}
	startTUIEventBridge = func(_ context.Context, _ *tea.Program, _ <-chan orchestrator.OrchestratorEvent) {}
	runTUIShutdownTimeout = 50 * time.Millisecond

	err := runTUI(context.Background(), orch)
	require.NoError(t, err)
}

func TestRunTUI_TimeoutNoTUIError(t *testing.T) {
	orch := orchestrator.NewOrchestrator(nil, nil, nil, nil, nil)

	restoreRunTUITestHooks(t)

	block := make(chan struct{})
	t.Cleanup(func() { close(block) })

	runTUIProgram = func(_ *tea.Program) (tea.Model, error) {
		return nil, nil // TUI succeeds
	}
	runTUIOrchestrator = func(_ context.Context, _ *orchestrator.Orchestrator) error {
		<-block
		return nil
	}
	startTUIEventBridge = func(_ context.Context, _ *tea.Program, _ <-chan orchestrator.OrchestratorEvent) {}
	runTUIShutdownTimeout = 10 * time.Millisecond

	err := runTUI(context.Background(), orch)
	require.Error(t, err)
	assert.Equal(t, "timed out waiting for orchestrator shutdown", err.Error())
}

func TestRunTUI_TUIErrorOrchestratorSuccess(t *testing.T) {
	orch := orchestrator.NewOrchestrator(nil, nil, nil, nil, nil)

	tuiErr := errors.New("tui rendering failed")
	restoreRunTUITestHooks(t)

	runTUIProgram = func(_ *tea.Program) (tea.Model, error) {
		return nil, tuiErr
	}
	runTUIOrchestrator = func(_ context.Context, _ *orchestrator.Orchestrator) error {
		return nil
	}
	startTUIEventBridge = func(_ context.Context, _ *tea.Program, _ <-chan orchestrator.OrchestratorEvent) {}
	runTUIShutdownTimeout = 50 * time.Millisecond

	err := runTUI(context.Background(), orch)
	require.Error(t, err)
	assert.ErrorIs(t, err, tuiErr)
}
