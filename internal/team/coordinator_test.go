package team

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdatePhaseStateSerializesCallbacks(t *testing.T) {
	store, _ := setupTestStore(t)

	_, err := store.CreateManifest("test-team", types.TeamConfig{MaxWorkers: 1, MaxFixLoops: 2})
	require.NoError(t, err)

	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondEntered := make(chan struct{})
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)

	go func() {
		firstDone <- store.UpdatePhaseState("test-team", func(state *types.TeamPhaseState) error {
			close(firstEntered)
			<-releaseFirst
			state.Phase = types.PhasePRD
			return nil
		})
	}()

	<-firstEntered

	go func() {
		secondDone <- store.UpdatePhaseState("test-team", func(state *types.TeamPhaseState) error {
			close(secondEntered)
			if state.Artifacts == nil {
				state.Artifacts = map[string]string{}
			}
			state.Artifacts["plan"] = "/tmp/plan.md"
			return nil
		})
	}()

	select {
	case <-secondEntered:
		t.Fatal("second phase-state update entered callback before first released the lock")
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseFirst)

	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)

	state, err := store.LoadPhaseState("test-team")
	require.NoError(t, err)
	assert.Equal(t, types.PhasePRD, state.Phase)
	assert.Equal(t, "/tmp/plan.md", state.Artifacts["plan"])
}

func TestRunFixPhaseResetsFailedTasks(t *testing.T) {
	cfg := &config.WorkflowConfig{
		Agent: config.AgentConfig{Type: "codex"},
		Team: config.TeamSectionConfig{
			MaxWorkers:        1,
			MaxFixLoops:       2,
			ClaimLeaseSeconds: 300,
			StateDir:          t.TempDir(),
		},
	}

	coordinator := NewCoordinator(
		"test-team",
		cfg,
		nil,
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	err := coordinator.Initialize(types.TeamConfig{
		MaxWorkers:        cfg.TeamMaxWorkers(),
		MaxFixLoops:       cfg.TeamMaxFixLoops(),
		ClaimLeaseSeconds: cfg.TeamClaimLeaseSeconds(),
		StateDir:          cfg.TeamStateDir(),
		AgentType:         cfg.AgentType(),
	})
	require.NoError(t, err)

	task := &types.TeamTask{
		ID:          "task-1",
		Subject:     "Fix task",
		Description: "Reset this failed task",
	}
	require.NoError(t, coordinator.tasks.CreateTask("test-team", task))

	token, err := coordinator.tasks.ClaimTask("test-team", "task-1", "worker-1", 1)
	require.NoError(t, err)
	require.NoError(t, coordinator.tasks.FailTask("test-team", "task-1", token, "needs retry"))

	require.NoError(t, coordinator.phases.Transition("test-team", types.PhasePRD, "plan complete"))
	require.NoError(t, coordinator.phases.Transition("test-team", types.PhaseExec, "prd complete"))
	require.NoError(t, coordinator.phases.Transition("test-team", types.PhaseVerify, "exec complete"))
	require.NoError(t, coordinator.phases.Transition("test-team", types.PhaseFix, "verification failed"))

	require.NoError(t, coordinator.runFixPhase(context.Background()))

	resetTask, err := coordinator.tasks.GetTask("test-team", "task-1")
	require.NoError(t, err)
	assert.Equal(t, types.TaskPending, resetTask.Status)
	assert.Nil(t, resetTask.Claim)

	phase, err := coordinator.phases.CurrentPhase("test-team")
	require.NoError(t, err)
	assert.Equal(t, types.PhaseExec, phase)
}
