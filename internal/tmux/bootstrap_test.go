package tmux

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkerBootstrapBootstrap(t *testing.T) {
	runner := &MockRunner{
		results: map[string]mockResult{
			"tmux new-window -t contrabass-team -n worker-1 -P -F #{pane_id}": {output: []byte("%3\n")},
		},
	}

	b := NewWorkerBootstrap(NewSession("team", runner), BootstrapConfig{
		WorkerID:   "worker-1",
		TeamName:   "team",
		WorkDir:    "/tmp/work dir",
		CLICommand: "contrabass",
		CLIArgs:    []string{"team", "2:executor"},
		Env: map[string]string{
			"ZED":       "value",
			"API_TOKEN": "a b",
		},
	})
	b.startupDelay = time.Millisecond

	paneID, err := b.Bootstrap(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "%3", paneID)

	got := make([][]string, 0, len(runner.calls))
	for _, call := range runner.calls {
		require.Equal(t, "tmux", call.name)
		got = append(got, call.args)
	}

	expected := [][]string{
		{"new-window", "-t", "contrabass-team", "-n", "worker-1", "-P", "-F", "#{pane_id}"},
		{"send-keys", "-t", "%3", "export API_TOKEN='a b'", "C-m"},
		{"send-keys", "-t", "%3", "export ZED='value'", "C-m"},
		{"send-keys", "-t", "%3", "cd '/tmp/work dir'", "C-m"},
		{"send-keys", "-t", "%3", "'contrabass' 'team' '2:executor'", "C-m"},
	}
	assert.Equal(t, expected, got)
}

func TestWorkerBootstrapBootstrapValidationErrors(t *testing.T) {
	testCases := []struct {
		name    string
		config  BootstrapConfig
		errLike string
	}{
		{
			name:    "empty worker id",
			config:  BootstrapConfig{WorkerID: "", TeamName: "team", WorkDir: "/tmp/work", CLICommand: "contrabass"},
			errLike: "worker id is empty",
		},
		{
			name:    "empty team name",
			config:  BootstrapConfig{WorkerID: "worker-1", TeamName: "", WorkDir: "/tmp/work", CLICommand: "contrabass"},
			errLike: "team name is empty",
		},
		{
			name:    "empty work dir",
			config:  BootstrapConfig{WorkerID: "worker-1", TeamName: "team", WorkDir: "", CLICommand: "contrabass"},
			errLike: "work dir is empty",
		},
		{
			name:    "empty cli command",
			config:  BootstrapConfig{WorkerID: "worker-1", TeamName: "team", WorkDir: "/tmp/work", CLICommand: ""},
			errLike: "cli command is empty",
		},
		{
			name: "invalid env key",
			config: BootstrapConfig{
				WorkerID:   "worker-1",
				TeamName:   "team",
				WorkDir:    "/tmp/work",
				CLICommand: "contrabass",
				Env:        map[string]string{"BAD-KEY": "x"},
			},
			errLike: "invalid environment variable key",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &MockRunner{results: map[string]mockResult{}}
			b := NewWorkerBootstrap(NewSession("team", runner), testCase.config)
			b.startupDelay = time.Millisecond

			_, err := b.Bootstrap(context.Background())
			require.Error(t, err)
			assert.Contains(t, err.Error(), testCase.errLike)
			assert.Empty(t, runner.calls)
		})
	}
}

func TestWorkerBootstrapBootstrapCleansUpOnFailure(t *testing.T) {
	runner := &MockRunner{
		results: map[string]mockResult{
			"tmux new-window -t contrabass-team -n worker-1 -P -F #{pane_id}": {output: []byte("%7")},
			"tmux send-keys -t %7 cd '/tmp/work' C-m":                         {err: errors.New("send failed")},
		},
	}

	b := NewWorkerBootstrap(NewSession("team", runner), BootstrapConfig{
		WorkerID:   "worker-1",
		TeamName:   "team",
		WorkDir:    "/tmp/work",
		CLICommand: "contrabass",
	})

	_, err := b.Bootstrap(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "change worker")

	last := runner.calls[len(runner.calls)-1]
	assert.Equal(t, "tmux", last.name)
	assert.Equal(t, []string{"kill-pane", "-t", "%7"}, last.args)
}

func TestWorkerBootstrapInjectPrompt(t *testing.T) {
	runner := &MockRunner{results: map[string]mockResult{}}
	b := NewWorkerBootstrap(NewSession("team", runner), BootstrapConfig{})

	err := b.InjectPrompt(context.Background(), "%2", "line1\nline2")
	require.NoError(t, err)

	require.Len(t, runner.calls, 1)
	assert.Equal(t, []string{"send-keys", "-t", "%2", "line1\\nline2", "C-m"}, runner.calls[0].args)
}

func TestWorkerBootstrapIsWorkerAlive(t *testing.T) {
	testCases := []struct {
		name       string
		output     []byte
		runErr     error
		expected   bool
		expectErr  bool
		errContain string
	}{
		{name: "alive pane", output: []byte("0\n"), expected: true},
		{name: "dead pane", output: []byte("1\n"), expected: false},
		{name: "runner error", runErr: errors.New("tmux failed"), expectErr: true, errContain: "check pane"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &MockRunner{results: map[string]mockResult{}}
			runner.results["tmux list-panes -t %9 -F #{pane_dead}"] = mockResult{output: testCase.output, err: testCase.runErr}

			b := NewWorkerBootstrap(NewSession("team", runner), BootstrapConfig{})
			alive, err := b.IsWorkerAlive(context.Background(), "%9")

			if testCase.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), testCase.errContain)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.expected, alive)
		})
	}
}
