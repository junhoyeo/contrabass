package team

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type alwaysRejectPolicyRule struct{}

func (alwaysRejectPolicyRule) Name() string {
	return "always_reject"
}

func (alwaysRejectPolicyRule) Check(_ context.Context, _ Decision) error {
	return errors.New("rejected")
}

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

func TestCoordinatorRunStartupRecoveryCleansStaleHeartbeat(t *testing.T) {
	cfg := &config.WorkflowConfig{
		Agent: config.AgentConfig{Type: "codex"},
		Team: config.TeamSectionConfig{
			MaxWorkers:        1,
			MaxFixLoops:       2,
			ClaimLeaseSeconds: 1,
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

	require.NoError(t, coordinator.Initialize(types.TeamConfig{
		MaxWorkers:        cfg.TeamMaxWorkers(),
		MaxFixLoops:       cfg.TeamMaxFixLoops(),
		ClaimLeaseSeconds: cfg.TeamClaimLeaseSeconds(),
		StateDir:          cfg.TeamStateDir(),
		AgentType:         cfg.AgentType(),
	}))

	staleWorkerID := "stale-worker"
	require.NoError(t, coordinator.heartbeats.Write("test-team", Heartbeat{WorkerID: staleWorkerID, Timestamp: time.Now()}))

	heartbeatPath := coordinator.paths.HeartbeatPath("test-team", staleWorkerID)
	old := time.Now().Add(-5 * time.Second)
	require.NoError(t, os.Chtimes(heartbeatPath, old, old))

	runCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	require.NoError(t, coordinator.Run(runCtx, nil))

	_, err := os.Stat(heartbeatPath)
	assert.True(t, errors.Is(err, os.ErrNotExist), "expected stale heartbeat to be removed")
}

func TestWorkerLoopGovernanceBlocksClaimWhenMaxActiveExceeded(t *testing.T) {
	cfg := &config.WorkflowConfig{
		Agent: config.AgentConfig{Type: "codex"},
		Team: config.TeamSectionConfig{
			MaxWorkers:        1,
			MaxFixLoops:       2,
			ClaimLeaseSeconds: 60,
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

	require.NoError(t, coordinator.Initialize(types.TeamConfig{
		MaxWorkers:        cfg.TeamMaxWorkers(),
		MaxFixLoops:       cfg.TeamMaxFixLoops(),
		ClaimLeaseSeconds: cfg.TeamClaimLeaseSeconds(),
		StateDir:          cfg.TeamStateDir(),
		AgentType:         cfg.AgentType(),
	}))

	task := &types.TeamTask{ID: "task-1", Subject: "Blocked claim", Description: "should remain pending"}
	require.NoError(t, coordinator.tasks.CreateTask("test-team", task))

	coordinator.governance = NewGovernancePolicy(MaxConcurrentTasksRule{MaxTasksPerWorker: 1})
	coordinator.workers["worker-1"] = &workerHandle{state: types.WorkerState{ID: "worker-1", CurrentTask: "already-active"}}

	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()

	err := coordinator.workerLoop(ctx, "worker-1")
	assert.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	stored, getErr := coordinator.tasks.GetTask("test-team", "task-1")
	require.NoError(t, getErr)
	assert.Equal(t, types.TaskPending, stored.Status)
	assert.Nil(t, stored.Claim)
}

func TestWorkerLoopExitsAfterGovernanceRetryLimit(t *testing.T) {
	cfg := &config.WorkflowConfig{
		Agent: config.AgentConfig{Type: "codex"},
		Team: config.TeamSectionConfig{
			MaxWorkers:        1,
			MaxFixLoops:       2,
			ClaimLeaseSeconds: 60,
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

	require.NoError(t, coordinator.Initialize(types.TeamConfig{
		MaxWorkers:        cfg.TeamMaxWorkers(),
		MaxFixLoops:       cfg.TeamMaxFixLoops(),
		ClaimLeaseSeconds: cfg.TeamClaimLeaseSeconds(),
		StateDir:          cfg.TeamStateDir(),
		AgentType:         cfg.AgentType(),
	}))

	coordinator.governance = NewGovernancePolicy(alwaysRejectPolicyRule{})

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	start := time.Now()
	err := coordinator.workerLoop(ctx, "worker-1")
	elapsed := time.Since(start)

	assert.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, cfg.TeamGovernanceRetryDelay()*time.Duration(governanceRetryLimit-1))
	assert.Equal(t, 10, governanceRetryLimit)
}
