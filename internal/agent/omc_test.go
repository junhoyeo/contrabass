package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOMCRunner_CompileTimeCheck(t *testing.T) {
	var _ AgentRunner = (*OMCRunner)(nil)
}

func TestOMCRunner_Defaults(t *testing.T) {
	cfg := &config.WorkflowConfig{}
	runner := NewOMCRunner(cfg, 0)
	require.NotNil(t, runner)
	assert.Equal(t, defaultOMCBinaryPath, runner.binaryPath)
	assert.Equal(t, defaultOMCTeamSpec, runner.teamSpec)
	assert.Equal(t, time.Duration(defaultOMCPollIntervalMs)*time.Millisecond, runner.pollInterval)
	assert.Equal(t, time.Duration(defaultOMCStartupTimeoutMs)*time.Millisecond, runner.startupTimeout)
}

func TestOMCRunner_UsesTeamRuntime(t *testing.T) {
	workspace := t.TempDir()
	logPath := filepath.Join(workspace, "omc-commands.log")
	server := newFakeTeamCLIServer(t, logPath)
	defer server.Close()

	cfg := &config.WorkflowConfig{
		OMC: config.OMCConfig{
			BinaryPath: server.binaryPath,
			TeamSpec:   "1:claude",
		},
	}
	runner := NewOMCRunner(cfg, time.Second)

	proc, err := runner.Start(context.Background(), types.Issue{ID: "CB-104", Title: "Add OMC runner"}, workspace, "Do the OMC task")
	require.NoError(t, err)

	events := collectOpenCodeEvents(t, proc.Events, proc.Done, 4, 5*time.Second)
	require.Len(t, events, 4)
	assert.Equal(t, "turn/started", events[0].Type)
	assert.Equal(t, "session.status", events[1].Type)
	assert.Equal(t, "item/completed", events[2].Type)
	assert.Equal(t, "task/completed", events[3].Type)
	assertDoneNil(t, proc.Done)
}
