package tmux

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockCall struct {
	name string
	args []string
}

type mockResult struct {
	output []byte
	err    error
}

type MockRunner struct {
	calls   []mockCall
	results map[string]mockResult
}

func (m *MockRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	m.calls = append(m.calls, mockCall{name: name, args: append([]string(nil), args...)})
	key := name + " " + strings.Join(args, " ")
	if result, ok := m.results[key]; ok {
		return result.output, result.err
	}

	return nil, nil
}

func TestNewSession(t *testing.T) {
	s := NewSession("workers", nil)
	require.NotNil(t, s)
	assert.Equal(t, "contrabass-workers", s.Name)
	assert.NotNil(t, s.runner)
}

func TestSessionCreate(t *testing.T) {
	testCases := []struct {
		name      string
		results   map[string]mockResult
		assertErr func(t *testing.T, err error)
		callCount int
		lastCall  []string
	}{
		{
			name: "creates session when absent",
			results: map[string]mockResult{
				"tmux has-session -t =contrabass-team": {err: errors.New("exit status 1")},
			},
			assertErr: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
			callCount: 2,
			lastCall:  []string{"new-session", "-d", "-s", "contrabass-team"},
		},
		{
			name: "returns already exists error",
			results: map[string]mockResult{
				"tmux has-session -t =contrabass-team": {},
			},
			assertErr: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "already exists")
			},
			callCount: 1,
			lastCall:  []string{"has-session", "-t", "=contrabass-team"},
		},
		{
			name: "returns clear error when tmux is missing",
			results: map[string]mockResult{
				"tmux has-session -t =contrabass-team": {err: errors.New("exit status 1")},
				"tmux new-session -d -s contrabass-team": {
					err: &exec.Error{Name: "tmux", Err: exec.ErrNotFound},
				},
			},
			assertErr: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "tmux executable not found")
			},
			callCount: 2,
			lastCall:  []string{"new-session", "-d", "-s", "contrabass-team"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &MockRunner{results: testCase.results}
			session := NewSession("team", runner)

			err := session.Create(context.Background())
			testCase.assertErr(t, err)

			require.Len(t, runner.calls, testCase.callCount)
			assert.Equal(t, "tmux", runner.calls[len(runner.calls)-1].name)
			assert.Equal(t, testCase.lastCall, runner.calls[len(runner.calls)-1].args)
		})
	}
}

func TestSessionListPanes(t *testing.T) {
	runner := &MockRunner{
		results: map[string]mockResult{
			"tmux list-panes -t =contrabass-team -F " + paneListFormat: {
				output: []byte("%0:0:1:0:4242:bash\n%1:1:0:1:5252:worker"),
			},
		},
	}

	session := NewSession("team", runner)
	panes, err := session.ListPanes(context.Background())
	require.NoError(t, err)
	require.Len(t, panes, 2)

	assert.Equal(t, PaneInfo{ID: "%0", Index: 0, Active: true, Dead: false, PID: 4242, Title: "bash"}, panes[0])
	assert.Equal(t, PaneInfo{ID: "%1", Index: 1, Active: false, Dead: true, PID: 5252, Title: "worker"}, panes[1])
}

func TestSessionSplitPane(t *testing.T) {
	runner := &MockRunner{
		results: map[string]mockResult{
			"tmux split-window -t %0 -h -P -F #{pane_id}": {output: []byte("%2\n")},
		},
	}

	session := NewSession("team", runner)
	paneID, err := session.SplitPane(context.Background(), "%0", true)
	require.NoError(t, err)
	assert.Equal(t, "%2", paneID)

	require.Len(t, runner.calls, 1)
	assert.Equal(t, []string{"split-window", "-t", "%0", "-h", "-P", "-F", "#{pane_id}"}, runner.calls[0].args)
}

func TestSessionNewWindow(t *testing.T) {
	runner := &MockRunner{
		results: map[string]mockResult{
			"tmux new-window -t =contrabass-team -n worker-1 -P -F #{pane_id}": {output: []byte("%3")},
		},
	}

	session := NewSession("team", runner)
	paneID, err := session.NewWindow(context.Background(), "worker-1")
	require.NoError(t, err)
	assert.Equal(t, "%3", paneID)
}

// Session-level targets must use tmux's "=" exact-match prefix; a bare name
// prefix-matches other sessions (contrabass-team vs contrabass-team-2), so
// Kill/IsAlive could hit another team.
func TestSessionTargetsUseExactMatch(t *testing.T) {
	runner := &MockRunner{
		results: map[string]mockResult{
			"tmux has-session -t =contrabass-team": {},
		},
	}
	session := NewSession("team", runner)

	assert.True(t, session.IsAlive(context.Background()))
	require.NoError(t, session.Kill(context.Background()))

	require.Len(t, runner.calls, 2)
	assert.Equal(t, []string{"has-session", "-t", "=contrabass-team"}, runner.calls[0].args)
	assert.Equal(t, []string{"kill-session", "-t", "=contrabass-team"}, runner.calls[1].args)
}

func TestSessionSendKeys(t *testing.T) {
	runner := &MockRunner{results: map[string]mockResult{}}
	session := NewSession("team", runner)

	err := session.SendKeys(context.Background(), "%1", "echo hello", "C-m")
	require.NoError(t, err)

	require.Len(t, runner.calls, 1)
	assert.Equal(t, []string{"send-keys", "-t", "%1", "echo hello", "C-m"}, runner.calls[0].args)
}

func TestSessionCapturePaneOutput(t *testing.T) {
	runner := &MockRunner{
		results: map[string]mockResult{
			"tmux capture-pane -t %1 -p -S -50": {output: []byte("line1\nline2\n")},
		},
	}

	session := NewSession("team", runner)
	output, err := session.CapturePaneOutput(context.Background(), "%1", 50)
	require.NoError(t, err)
	assert.Equal(t, "line1\nline2\n", output)
}

func TestSessionKillPane(t *testing.T) {
	runner := &MockRunner{results: map[string]mockResult{}}
	session := NewSession("team", runner)

	err := session.KillPane(context.Background(), "%7")
	require.NoError(t, err)
	require.Len(t, runner.calls, 1)
	assert.Equal(t, []string{"kill-pane", "-t", "%7"}, runner.calls[0].args)
}

func TestSessionIsPaneDead(t *testing.T) {
	testCases := []struct {
		name     string
		output   []byte
		expected bool
	}{
		{name: "dead pane", output: []byte("1\n"), expected: true},
		{name: "alive pane", output: []byte("0\n"), expected: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &MockRunner{
				results: map[string]mockResult{
					"tmux list-panes -t %9 -F #{pane_dead}": {output: testCase.output},
				},
			}
			session := NewSession("team", runner)

			dead, err := session.IsPaneDead(context.Background(), "%9")
			require.NoError(t, err)
			assert.Equal(t, testCase.expected, dead)
		})
	}
}

func TestIsTmuxAvailable(t *testing.T) {
	t.Run("available", func(t *testing.T) {
		runner := &MockRunner{results: map[string]mockResult{}}
		assert.True(t, IsTmuxAvailable(context.Background(), runner))
	})

	t.Run("not available", func(t *testing.T) {
		runner := &MockRunner{
			results: map[string]mockResult{
				"tmux -V": {err: &exec.Error{Name: "tmux", Err: exec.ErrNotFound}},
			},
		}
		assert.False(t, IsTmuxAvailable(context.Background(), runner))
	})
}
