package main

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/agent"
	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogTeamEventsStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		logTeamEvents(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), make(chan types.TeamEvent))
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("logTeamEvents did not stop after context cancellation")
	}
}

func TestCreateRunnerForwardsLegacyModelToCodex(t *testing.T) {
	cfg := &config.WorkflowConfig{
		ModelRaw: "openai/gpt-5-codex",
		Agent:    config.AgentConfig{Type: "codex"},
	}

	runner, err := createRunner(cfg, "team-test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runner.Close()) })

	codexRunner, ok := runner.(*agent.CodexRunner)
	require.True(t, ok)
	options := reflect.ValueOf(codexRunner).Elem().FieldByName("options")
	assert.Equal(t, "openai/gpt-5-codex", options.FieldByName("Model").String())
}
