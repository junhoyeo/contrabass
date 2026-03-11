package team

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupDispatchQueue(t *testing.T, ackTimeout time.Duration) (*Store, *Paths, *DispatchQueue) {
	t.Helper()
	paths := NewPaths(t.TempDir())
	store := NewStore(paths)
	queue := NewDispatchQueue(store, paths, ackTimeout)
	return store, paths, queue
}

func TestDispatchQueue_DispatchCreatesFile(t *testing.T) {
	tests := []struct {
		name  string
		entry DispatchEntry
	}{
		{
			name: "creates dispatch file and persists entry",
			entry: DispatchEntry{
				TaskID:       "task-1",
				WorkerID:     "worker-1",
				Prompt:       "run task",
				DispatchedAt: time.Now().Add(-1 * time.Minute),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, paths, queue := setupDispatchQueue(t, 10*time.Second)
			teamName := "team-a"
			require.NoError(t, store.EnsureDirs(teamName))

			require.NoError(t, queue.Dispatch(teamName, tt.entry))

			path := paths.DispatchPath(teamName, tt.entry.TaskID)
			_, err := os.Stat(path)
			require.NoError(t, err)

			var got DispatchEntry
			require.NoError(t, store.ReadJSON(path, &got))
			assert.Equal(t, tt.entry.TaskID, got.TaskID)
			assert.Equal(t, tt.entry.WorkerID, got.WorkerID)
			assert.Equal(t, tt.entry.Prompt, got.Prompt)
			assert.Equal(t, DispatchStatusPending, got.Status)
			assert.Nil(t, got.AckedAt)
			assert.False(t, got.DispatchedAt.IsZero())
		})
	}
}

func TestDispatchQueue_Ack(t *testing.T) {
	tests := []struct {
		name      string
		workerID  string
		ackWorker string
		wantErr   bool
		setup     func(*DispatchQueue, string, string)
	}{
		{
			name:      "ack updates status and timestamp",
			workerID:  "worker-1",
			ackWorker: "worker-1",
			wantErr:   false,
			setup: func(q *DispatchQueue, teamName, taskID string) {
				require.NoError(t, q.Dispatch(teamName, DispatchEntry{
					TaskID:   taskID,
					WorkerID: "worker-1",
					Prompt:   "run task",
				}))
			},
		},
		{
			name:      "ack with wrong worker ID returns error",
			workerID:  "worker-1",
			ackWorker: "worker-2",
			wantErr:   true,
			setup: func(q *DispatchQueue, teamName, taskID string) {
				require.NoError(t, q.Dispatch(teamName, DispatchEntry{
					TaskID:   taskID,
					WorkerID: "worker-1",
					Prompt:   "run task",
				}))
			},
		},
		{
			name:      "ack fails on already completed entry",
			workerID:  "worker-1",
			ackWorker: "worker-1",
			wantErr:   true,
			setup: func(q *DispatchQueue, teamName, taskID string) {
				require.NoError(t, q.Dispatch(teamName, DispatchEntry{
					TaskID:   taskID,
					WorkerID: "worker-1",
					Prompt:   "run task",
				}))
				require.NoError(t, q.Ack(teamName, taskID, "worker-1"))
				require.NoError(t, q.Complete(teamName, taskID))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, paths, queue := setupDispatchQueue(t, 10*time.Second)
			teamName := "team-a"
			taskID := "task-1"
			require.NoError(t, store.EnsureDirs(teamName))

			tt.setup(queue, teamName, taskID)

			err := queue.Ack(teamName, taskID, tt.ackWorker)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			var got DispatchEntry
			require.NoError(t, store.ReadJSON(paths.DispatchPath(teamName, taskID), &got))
			assert.Equal(t, DispatchStatusAcked, got.Status)
			assert.NotNil(t, got.AckedAt)
		})
	}
}

func TestDispatchQueue_Complete(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
		setup   func(*DispatchQueue, string, string)
	}{
		{
			name:    "complete marks entry completed after ack",
			wantErr: false,
			setup: func(q *DispatchQueue, teamName, taskID string) {
				require.NoError(t, q.Dispatch(teamName, DispatchEntry{
					TaskID:   taskID,
					WorkerID: "worker-1",
					Prompt:   "run task",
				}))
				require.NoError(t, q.Ack(teamName, taskID, "worker-1"))
			},
		},
		{
			name:    "complete fails on pending entry",
			wantErr: true,
			setup: func(q *DispatchQueue, teamName, taskID string) {
				require.NoError(t, q.Dispatch(teamName, DispatchEntry{
					TaskID:   taskID,
					WorkerID: "worker-1",
					Prompt:   "run task",
				}))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, paths, queue := setupDispatchQueue(t, 10*time.Second)
			teamName := "team-a"
			taskID := "task-1"
			require.NoError(t, store.EnsureDirs(teamName))

			tt.setup(queue, teamName, taskID)

			err := queue.Complete(teamName, taskID)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			var got DispatchEntry
			require.NoError(t, store.ReadJSON(paths.DispatchPath(teamName, taskID), &got))
			assert.Equal(t, DispatchStatusCompleted, got.Status)
		})
	}
}

func TestDispatchQueue_GetPending(t *testing.T) {
	tests := []struct {
		name      string
		wantTasks []string
	}{
		{name: "returns only pending entries", wantTasks: []string{"task-pending"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, _, queue := setupDispatchQueue(t, 10*time.Second)
			teamName := "team-a"
			require.NoError(t, store.EnsureDirs(teamName))

			require.NoError(t, queue.Dispatch(teamName, DispatchEntry{TaskID: "task-pending", WorkerID: "worker-1", Prompt: "pending"}))
			require.NoError(t, queue.Dispatch(teamName, DispatchEntry{TaskID: "task-acked", WorkerID: "worker-2", Prompt: "acked"}))
			require.NoError(t, queue.Dispatch(teamName, DispatchEntry{TaskID: "task-completed", WorkerID: "worker-3", Prompt: "completed"}))
			require.NoError(t, queue.Ack(teamName, "task-acked", "worker-2"))
			require.NoError(t, queue.Ack(teamName, "task-completed", "worker-3"))
			require.NoError(t, queue.Complete(teamName, "task-completed"))

			pending, err := queue.GetPending(teamName)
			require.NoError(t, err)

			taskIDs := make([]string, 0, len(pending))
			for _, entry := range pending {
				taskIDs = append(taskIDs, entry.TaskID)
			}
			assert.ElementsMatch(t, tt.wantTasks, taskIDs)
		})
	}
}

func TestDispatchQueue_GetTimedOut(t *testing.T) {
	tests := []struct {
		name      string
		timeout   time.Duration
		wantTasks []string
	}{
		{name: "returns entries past timeout", timeout: 2 * time.Second, wantTasks: []string{"task-timeout"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, _, queue := setupDispatchQueue(t, tt.timeout)
			teamName := "team-a"
			require.NoError(t, store.EnsureDirs(teamName))

			require.NoError(t, queue.Dispatch(teamName, DispatchEntry{
				TaskID:       "task-timeout",
				WorkerID:     "worker-1",
				Prompt:       "timed out",
				DispatchedAt: time.Now().Add(-10 * time.Second),
			}))
			require.NoError(t, queue.Dispatch(teamName, DispatchEntry{
				TaskID:       "task-fresh",
				WorkerID:     "worker-2",
				Prompt:       "fresh",
				DispatchedAt: time.Now(),
			}))

			timedOut, err := queue.GetTimedOut(teamName)
			require.NoError(t, err)

			taskIDs := make([]string, 0, len(timedOut))
			for _, entry := range timedOut {
				taskIDs = append(taskIDs, entry.TaskID)
				assert.Equal(t, DispatchStatusTimeout, entry.Status)
			}
			assert.ElementsMatch(t, tt.wantTasks, taskIDs)
		})
	}
}

func TestDispatchQueue_Requeue(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "requeue resets timed-out entry"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, paths, queue := setupDispatchQueue(t, 2*time.Second)
			teamName := "team-a"
			taskID := "task-timeout"
			require.NoError(t, store.EnsureDirs(teamName))

			require.NoError(t, queue.Dispatch(teamName, DispatchEntry{
				TaskID:       taskID,
				WorkerID:     "worker-1",
				Prompt:       "timed out",
				DispatchedAt: time.Now().Add(-10 * time.Second),
			}))
			_, err := queue.GetTimedOut(teamName)
			require.NoError(t, err)

			require.NoError(t, queue.Requeue(teamName, taskID))

			var got DispatchEntry
			require.NoError(t, store.ReadJSON(paths.DispatchPath(teamName, taskID), &got))
			assert.Equal(t, DispatchStatusPending, got.Status)
			assert.Equal(t, "", got.WorkerID)
			assert.Nil(t, got.AckedAt)
		})
	}
}

func TestDispatchQueue_DispatchValidation(t *testing.T) {
	tests := []struct {
		name     string
		teamName string
		entry    DispatchEntry
	}{
		{name: "empty team name", teamName: "", entry: DispatchEntry{TaskID: "task-1", WorkerID: "worker-1", Prompt: "run"}},
		{name: "empty task ID", teamName: "team-a", entry: DispatchEntry{TaskID: "", WorkerID: "worker-1", Prompt: "run"}},
		{name: "empty worker ID", teamName: "team-a", entry: DispatchEntry{TaskID: "task-1", WorkerID: "", Prompt: "run"}},
		{name: "empty prompt", teamName: "team-a", entry: DispatchEntry{TaskID: "task-1", WorkerID: "worker-1", Prompt: ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, _, queue := setupDispatchQueue(t, 10*time.Second)
			require.NoError(t, store.EnsureDirs("team-a"))
			err := queue.Dispatch(tt.teamName, tt.entry)
			require.Error(t, err)
		})
	}
}
