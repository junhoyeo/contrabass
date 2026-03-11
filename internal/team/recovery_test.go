package team

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/types"
)

func setupRecovery(t *testing.T, staleThreshold time.Duration) (*Store, *Paths, *HeartbeatMonitor, *TaskRegistry, *DispatchQueue, *Recovery) {
	t.Helper()

	paths := NewPaths(t.TempDir())
	store := NewStore(paths)
	heartbeats := NewHeartbeatMonitor(store, paths, staleThreshold)
	tasks := NewTaskRegistry(store, paths, 3600)
	dispatch := NewDispatchQueue(store, paths, 5*time.Minute)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	recovery := NewRecovery(store, paths, heartbeats, tasks, dispatch, logger)

	return store, paths, heartbeats, tasks, dispatch, recovery
}

func TestRecovery_DiagnoseAndRecover(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, store *Store, paths *Paths, heartbeats *HeartbeatMonitor, tasks *TaskRegistry, dispatch *DispatchQueue)
		assertion func(t *testing.T, report *DiagnosisReport, store *Store, paths *Paths, heartbeats *HeartbeatMonitor, tasks *TaskRegistry, dispatch *DispatchQueue)
	}{
		{
			name: "clean state no recovery needed",
			setup: func(t *testing.T, store *Store, _ *Paths, heartbeats *HeartbeatMonitor, tasks *TaskRegistry, dispatch *DispatchQueue) {
				teamName := "team-a"
				require.NoError(t, store.EnsureDirs(teamName))
				require.NoError(t, store.SavePhaseState(teamName, &types.TeamPhaseState{Phase: types.PhaseExec}))

				require.NoError(t, tasks.CreateTask(teamName, &types.TeamTask{ID: "task-1", Subject: "s", Description: "d"}))
				require.NoError(t, heartbeats.Write(teamName, Heartbeat{WorkerID: "worker-1", Status: "working", Timestamp: time.Now()}))
				require.NoError(t, dispatch.Dispatch(teamName, DispatchEntry{TaskID: "task-1", WorkerID: "worker-1", Prompt: "run"}))
				require.NoError(t, dispatch.Ack(teamName, "task-1", "worker-1"))
			},
			assertion: func(t *testing.T, report *DiagnosisReport, store *Store, paths *Paths, _ *HeartbeatMonitor, tasks *TaskRegistry, _ *DispatchQueue) {
				teamName := "team-a"
				require.NotNil(t, report.PhaseState)
				assert.Equal(t, types.PhaseExec, report.PhaseState.Phase)
				assert.Empty(t, report.StaleWorkers)
				assert.Empty(t, report.OrphanedTasks)
				assert.Empty(t, report.PendingDispatch)

				task, err := tasks.GetTask(teamName, "task-1")
				require.NoError(t, err)
				assert.Equal(t, types.TaskPending, task.Status)

				_, err = os.Stat(paths.HeartbeatPath(teamName, "worker-1"))
				require.NoError(t, err)

				phaseState, err := store.LoadPhaseState(teamName)
				require.NoError(t, err)
				assert.Equal(t, types.PhaseExec, phaseState.Phase)
			},
		},
		{
			name: "stale workers with orphaned tasks",
			setup: func(t *testing.T, store *Store, paths *Paths, heartbeats *HeartbeatMonitor, tasks *TaskRegistry, _ *DispatchQueue) {
				teamName := "team-a"
				require.NoError(t, store.EnsureDirs(teamName))
				require.NoError(t, store.SavePhaseState(teamName, &types.TeamPhaseState{Phase: types.PhaseVerify}))

				require.NoError(t, tasks.CreateTask(teamName, &types.TeamTask{ID: "task-orphan", Subject: "s", Description: "d"}))
				_, err := tasks.ClaimTask(teamName, "task-orphan", "worker-dead", 1)
				require.NoError(t, err)

				require.NoError(t, heartbeats.Write(teamName, Heartbeat{WorkerID: "worker-dead", Status: "working", Timestamp: time.Now().Add(-2 * time.Minute)}))
				require.NoError(t, heartbeats.Write(teamName, Heartbeat{WorkerID: "worker-alive", Status: "working", Timestamp: time.Now()}))

				staleMTime := time.Now().Add(-2 * time.Minute)
				freshMTime := time.Now()
				require.NoError(t, os.Chtimes(paths.HeartbeatPath(teamName, "worker-dead"), staleMTime, staleMTime))
				require.NoError(t, os.Chtimes(paths.HeartbeatPath(teamName, "worker-alive"), freshMTime, freshMTime))
			},
			assertion: func(t *testing.T, report *DiagnosisReport, store *Store, paths *Paths, _ *HeartbeatMonitor, tasks *TaskRegistry, _ *DispatchQueue) {
				teamName := "team-a"
				require.NotNil(t, report.PhaseState)
				assert.Equal(t, types.PhaseVerify, report.PhaseState.Phase)
				require.Len(t, report.StaleWorkers, 1)
				assert.Equal(t, "worker-dead", report.StaleWorkers[0].WorkerID)
				require.Len(t, report.OrphanedTasks, 1)
				assert.Equal(t, "task-orphan", report.OrphanedTasks[0].ID)

				task, err := tasks.GetTask(teamName, "task-orphan")
				require.NoError(t, err)
				assert.Equal(t, types.TaskPending, task.Status)
				assert.Nil(t, task.Claim)

				_, err = os.Stat(paths.HeartbeatPath(teamName, "worker-dead"))
				assert.True(t, os.IsNotExist(err))
				_, err = os.Stat(paths.HeartbeatPath(teamName, "worker-alive"))
				require.NoError(t, err)

				phaseState, err := store.LoadPhaseState(teamName)
				require.NoError(t, err)
				assert.Equal(t, types.PhaseVerify, phaseState.Phase)
			},
		},
		{
			name: "tasks stuck in claimed state",
			setup: func(t *testing.T, store *Store, _ *Paths, _ *HeartbeatMonitor, tasks *TaskRegistry, _ *DispatchQueue) {
				teamName := "team-a"
				require.NoError(t, store.EnsureDirs(teamName))
				require.NoError(t, store.SavePhaseState(teamName, &types.TeamPhaseState{Phase: types.PhaseExec}))

				require.NoError(t, tasks.CreateTask(teamName, &types.TeamTask{ID: "task-stuck", Subject: "s", Description: "d"}))
				_, err := tasks.ClaimTask(teamName, "task-stuck", "worker-missing", 1)
				require.NoError(t, err)
			},
			assertion: func(t *testing.T, report *DiagnosisReport, store *Store, _ *Paths, _ *HeartbeatMonitor, tasks *TaskRegistry, _ *DispatchQueue) {
				teamName := "team-a"
				assert.Empty(t, report.StaleWorkers)
				require.Len(t, report.OrphanedTasks, 1)
				assert.Equal(t, "task-stuck", report.OrphanedTasks[0].ID)

				task, err := tasks.GetTask(teamName, "task-stuck")
				require.NoError(t, err)
				assert.Equal(t, types.TaskPending, task.Status)
				assert.Nil(t, task.Claim)

				phaseState, err := store.LoadPhaseState(teamName)
				require.NoError(t, err)
				assert.Equal(t, types.PhaseExec, phaseState.Phase)
			},
		},
		{
			name: "missing state files handled gracefully",
			setup: func(t *testing.T, _ *Store, _ *Paths, _ *HeartbeatMonitor, _ *TaskRegistry, _ *DispatchQueue) {
			},
			assertion: func(t *testing.T, report *DiagnosisReport, _ *Store, _ *Paths, _ *HeartbeatMonitor, _ *TaskRegistry, _ *DispatchQueue) {
				assert.Nil(t, report.PhaseState)
				assert.Empty(t, report.StaleWorkers)
				assert.Empty(t, report.OrphanedTasks)
				assert.Empty(t, report.PendingDispatch)
			},
		},
		{
			name: "malformed phase and heartbeat files are skipped",
			setup: func(t *testing.T, store *Store, paths *Paths, _ *HeartbeatMonitor, tasks *TaskRegistry, _ *DispatchQueue) {
				teamName := "team-a"
				require.NoError(t, store.EnsureDirs(teamName))

				require.NoError(t, os.WriteFile(paths.PhaseStatePath(teamName), []byte("{\"phase\":"), 0o644))
				require.NoError(t, os.MkdirAll(filepath.Dir(paths.HeartbeatPath(teamName, "worker-bad")), 0o755))
				require.NoError(t, os.WriteFile(paths.HeartbeatPath(teamName, "worker-bad"), []byte("{\"worker_id\":"), 0o644))
				require.NoError(t, tasks.CreateTask(teamName, &types.TeamTask{ID: "task-good", Subject: "s", Description: "d"}))
				require.NoError(t, os.WriteFile(paths.TaskPath(teamName, "task-bad"), []byte("{\"id\":"), 0o644))
			},
			assertion: func(t *testing.T, report *DiagnosisReport, _ *Store, _ *Paths, _ *HeartbeatMonitor, tasks *TaskRegistry, _ *DispatchQueue) {
				assert.Nil(t, report.PhaseState)
				assert.Empty(t, report.StaleWorkers)
				assert.Empty(t, report.OrphanedTasks)

				task, err := tasks.GetTask("team-a", "task-good")
				require.NoError(t, err)
				assert.Equal(t, types.TaskPending, task.Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, paths, heartbeats, tasks, dispatch, recovery := setupRecovery(t, 30*time.Second)
			if tt.name == "stale workers with orphaned tasks" {
				store, paths, heartbeats, tasks, dispatch, recovery = setupRecovery(t, 5*time.Second)
			}

			tt.setup(t, store, paths, heartbeats, tasks, dispatch)

			report, err := recovery.Diagnose("team-a")
			require.NoError(t, err)

			require.NoError(t, recovery.Recover(context.Background(), "team-a"))
			require.NoError(t, recovery.Recover(context.Background(), "team-a"))

			tt.assertion(t, report, store, paths, heartbeats, tasks, dispatch)
		})
	}
}
