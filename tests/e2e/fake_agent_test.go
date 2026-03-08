package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/agent"
	"github.com/junhoyeo/contrabass/internal/types"
)

func TestFakeAgentCodexProtocol(t *testing.T) {
	root := projectRoot(t)
	scriptPath := filepath.Join(root, "testdata", "mock-agent.sh")

	_, err := os.Stat(scriptPath)
	require.NoError(t, err)

	t.Setenv("MOCK_AGENT_DELAY", "0")

	runner := agent.NewCodexRunner("bash "+scriptPath, 5*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	issue := types.Issue{ID: "E2E-FAKE-1", Title: "fake agent protocol"}

	proc, err := runner.Start(ctx, issue, tmpDir, "test prompt")
	require.NoError(t, err)

	defer func() {
		stopErr := runner.Stop(proc)
		if stopErr != nil {
			require.True(t, strings.Contains(stopErr.Error(), "already stopped"), "unexpected stop error: %v", stopErr)
		}
	}()

	select {
	case doneErr := <-proc.Done:
		require.NoError(t, doneErr)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for fake agent process completion")
	}

	events := drainAgentEvents(proc.Events)
	assert.NotEmpty(t, events)
}

func projectRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Join(wd, "..", "..")
}

func drainAgentEvents(events <-chan types.AgentEvent) []types.AgentEvent {
	out := make([]types.AgentEvent, 0)
	for evt := range events {
		out = append(out, evt)
	}
	return out
}
