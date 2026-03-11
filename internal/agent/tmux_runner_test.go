package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/tmux"
	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTmuxRunner_CompileTimeCheck(t *testing.T) {
	var _ AgentRunner = (*TmuxRunner)(nil)
}

func TestTmuxRunner_StartCreatesProcess(t *testing.T) {
	workspace := t.TempDir()
	runner := setupTmuxRunner(t, workspace, tmuxMockSpecs{
		"tmux new-window -t contrabass-team -n worker-1 -P -F #{pane_id}": {output: []byte("%1\n")},
		"tmux send-keys -t %1 cd " + shellQuoteForKey(workspace) + " C-m": {},
		"tmux send-keys -t %1 codex app-server C-m":                       {},
		"tmux list-panes -t %1 -F #{pane_dead}":                           {output: []byte("0\n")},
		"tmux kill-pane -t %1":                                            {},
	})

	proc, err := runner.Start(context.Background(), types.Issue{ID: "CB-1", Title: "tmux"}, workspace, "run this")
	require.NoError(t, err)
	require.NotNil(t, proc)
	assert.Equal(t, 1, proc.PID)
	assert.Equal(t, "%1", proc.SessionID)
	assert.NotEmpty(t, proc.serverURL)

	event := waitForEvent(t, proc.Events)
	assert.Equal(t, "turn/started", event.Type)

	require.NoError(t, runner.Stop(proc))
	assertDoneNil(t, proc.Done)
}

func TestTmuxRunner_StopKillsPaneAndSignalsDone(t *testing.T) {
	workspace := t.TempDir()
	killErr := errors.New("kill failed")
	runner := setupTmuxRunner(t, workspace, tmuxMockSpecs{
		"tmux new-window -t contrabass-team -n worker-1 -P -F #{pane_id}": {output: []byte("%1\n")},
		"tmux send-keys -t %1 cd " + shellQuoteForKey(workspace) + " C-m": {},
		"tmux send-keys -t %1 codex app-server C-m":                       {},
		"tmux list-panes -t %1 -F #{pane_dead}":                           {output: []byte("0\n")},
		"tmux kill-pane -t %1":                                            {err: killErr},
	})

	proc, err := runner.Start(context.Background(), types.Issue{ID: "CB-2"}, workspace, "run this")
	require.NoError(t, err)

	err = runner.Stop(proc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kill failed")
	// Done channel now propagates the KillPane error (not nil)
	select {
	case err := <-proc.Done:
		require.Error(t, err, "expected kill error on Done channel")
		assert.Contains(t, err.Error(), "kill failed")
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Done")
	}
}

func TestTmuxRunner_CloseStopsAllProcesses(t *testing.T) {
	workspace := t.TempDir()
	runner := setupTmuxRunner(t, workspace, tmuxMockSpecs{
		"tmux new-window -t contrabass-team -n worker-1 -P -F #{pane_id}": {output: []byte("%1\n")},
		"tmux send-keys -t %1 cd " + shellQuoteForKey(workspace) + " C-m": {},
		"tmux send-keys -t %1 codex app-server C-m":                       {},
		"tmux list-panes -t %1 -F #{pane_dead}":                           {output: []byte("0\n")},
		"tmux kill-pane -t %1":                                            {},
		"tmux new-window -t contrabass-team -n worker-2 -P -F #{pane_id}": {output: []byte("%2\n")},
		"tmux send-keys -t %2 cd " + shellQuoteForKey(workspace) + " C-m": {},
		"tmux send-keys -t %2 codex app-server C-m":                       {},
		"tmux list-panes -t %2 -F #{pane_dead}":                           {output: []byte("0\n")},
		"tmux kill-pane -t %2":                                            {},
	})

	proc1, err := runner.Start(context.Background(), types.Issue{ID: "CB-3"}, workspace, "run one")
	require.NoError(t, err)
	proc2, err := runner.Start(context.Background(), types.Issue{ID: "CB-4"}, workspace, "run two")
	require.NoError(t, err)

	require.NoError(t, runner.Close())
	assertDoneNil(t, proc1.Done)
	assertDoneNil(t, proc2.Done)
}

func TestTmuxRunner_MonitorDetectsDeadPane(t *testing.T) {
	workspace := t.TempDir()
	runner := setupTmuxRunner(t, workspace, tmuxMockSpecs{
		"tmux new-window -t contrabass-team -n worker-1 -P -F #{pane_id}": {output: []byte("%1\n")},
		"tmux send-keys -t %1 cd " + shellQuoteForKey(workspace) + " C-m": {},
		"tmux send-keys -t %1 codex app-server C-m":                       {},
		"tmux list-panes -t %1 -F #{pane_dead}":                           {output: []byte("1\n")},
	})

	proc, err := runner.Start(context.Background(), types.Issue{ID: "CB-5"}, workspace, "run this")
	require.NoError(t, err)

	events := collectOpenCodeEvents(t, proc.Events, proc.Done, 2, 2*time.Second)
	require.Len(t, events, 2)
	assert.Equal(t, "turn/started", events[0].Type)
	assert.Equal(t, "task/completed", events[1].Type)
	assertDoneNil(t, proc.Done)
}

func TestTmuxRunner_StartUnknownAgentType(t *testing.T) {
	workspace := t.TempDir()
	mockRunner := newConfiguredTmuxMockRunner(tmuxMockSpecs{})
	runner := NewTmuxRunner(TmuxRunnerConfig{
		TeamName:     "team",
		AgentType:    "unknown",
		Session:      tmux.NewSession("team", mockRunner),
		Registry:     tmux.NewCLIRegistry(),
		PollInterval: 10 * time.Millisecond,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	_, err := runner.Start(context.Background(), types.Issue{ID: "CB-6"}, workspace, "run this")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown agent type")
}

type tmuxMockSpec struct {
	output []byte
	err    error
}

type tmuxMockSpecs map[string]tmuxMockSpec

type mockTmuxRunner struct {
	mu      sync.Mutex
	results tmuxMockSpecs
	calls   []string
}

func setupTmuxRunner(t *testing.T, workspace string, specs tmuxMockSpecs) *TmuxRunner {
	t.Helper()

	mockRunner := newConfiguredTmuxMockRunner(specs)
	return NewTmuxRunner(TmuxRunnerConfig{
		TeamName:     "team",
		AgentType:    "codex",
		Session:      tmux.NewSession("team", mockRunner),
		Registry:     tmux.NewCLIRegistry(),
		PollInterval: 10 * time.Millisecond,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func newConfiguredTmuxMockRunner(specs tmuxMockSpecs) *mockTmuxRunner {
	return &mockTmuxRunner{results: specs}
}

func (m *mockTmuxRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := name + " " + strings.Join(args, " ")
	m.calls = append(m.calls, key)

	result, ok := m.results[key]
	if !ok {
		return nil, nil
	}

	return result.output, result.err
}

func shellQuoteForKey(value string) string {
	if value == "" {
		return "''"
	}

	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
