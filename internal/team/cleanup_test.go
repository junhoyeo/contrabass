package team

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeTeamSession struct {
	killErr   error
	killCalls int
}

func (s *fakeTeamSession) Kill(context.Context) error {
	s.killCalls++
	return s.killErr
}

type fakeSessionProvider struct {
	sessions map[string]*fakeTeamSession
}

func (p *fakeSessionProvider) SessionForTeam(teamName string) TeamSession {
	if p == nil {
		return nil
	}
	return p.sessions[teamName]
}

type fakeHeartbeatInspector struct {
	list      []Heartbeat
	listErr   error
	stale     map[string]bool
	staleErrs map[string]error
}

func (f *fakeHeartbeatInspector) ListAll(string) ([]Heartbeat, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.list, nil
}

func (f *fakeHeartbeatInspector) IsStale(_ string, workerID string) (bool, error) {
	if err, ok := f.staleErrs[workerID]; ok {
		return false, err
	}
	return f.stale[workerID], nil
}

func TestCleaner_CleanupTeam(t *testing.T) {
	tests := []struct {
		name          string
		tmuxErr       error
		wantErr       bool
		wantKillCalls int
	}{
		{
			name:          "cleans tmux and team state",
			wantKillCalls: 1,
		},
		{
			name:          "continues on tmux kill failure",
			tmuxErr:       errors.New("kill failed"),
			wantErr:       true,
			wantKillCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			teamName := "team-a"
			paths := NewPaths(t.TempDir())
			store := NewStore(paths)
			require.NoError(t, store.EnsureDirs(teamName))

			heartbeatPathStale := paths.HeartbeatPath(teamName, "worker-stale")
			heartbeatPathFresh := paths.HeartbeatPath(teamName, "worker-fresh")
			require.NoError(t, os.MkdirAll(filepath.Dir(heartbeatPathStale), 0o755))
			require.NoError(t, os.MkdirAll(filepath.Dir(heartbeatPathFresh), 0o755))
			require.NoError(t, store.WriteJSON(heartbeatPathStale, &Heartbeat{WorkerID: "worker-stale", Timestamp: time.Now().Add(-time.Minute)}))
			require.NoError(t, store.WriteJSON(heartbeatPathFresh, &Heartbeat{WorkerID: "worker-fresh", Timestamp: time.Now()}))

			require.NoError(t, os.MkdirAll(paths.DispatchDir(teamName), 0o755))
			require.NoError(t, store.WriteJSON(paths.DispatchPath(teamName, "task-pending"), &DispatchEntry{TaskID: "task-pending", WorkerID: "worker-stale", Status: DispatchStatusPending}))
			require.NoError(t, store.WriteJSON(paths.DispatchPath(teamName, "task-completed"), &DispatchEntry{TaskID: "task-completed", WorkerID: "worker-fresh", Status: DispatchStatusCompleted}))

			session := &fakeTeamSession{killErr: tt.tmuxErr}
			provider := &fakeSessionProvider{sessions: map[string]*fakeTeamSession{teamName: session}}
			heartbeats := &fakeHeartbeatInspector{
				list: []Heartbeat{{WorkerID: "worker-stale"}, {WorkerID: "worker-fresh"}},
				stale: map[string]bool{
					"worker-stale": true,
					"worker-fresh": false,
				},
			}

			cleaner := NewCleaner(paths, store, provider, heartbeats, slog.New(slog.NewTextHandler(io.Discard, nil)))
			err := cleaner.CleanupTeam(context.Background(), teamName)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.wantKillCalls, session.killCalls)
			_, statErr := os.Stat(paths.TeamDir(teamName))
			assert.ErrorIs(t, statErr, os.ErrNotExist)
		})
	}
}

func TestCleaner_CleanupWorker(t *testing.T) {
	tests := []struct {
		name                string
		workerID            string
		wantWorkerHeartbeat bool
		wantWorkerState     bool
		wantWorkerMailbox   bool
		wantPendingDispatch bool
		wantOtherDispatch   bool
	}{
		{
			name:                "removes worker artifacts and pending dispatch",
			workerID:            "worker-1",
			wantWorkerHeartbeat: false,
			wantWorkerState:     false,
			wantWorkerMailbox:   false,
			wantPendingDispatch: false,
			wantOtherDispatch:   true,
		},
		{
			name:                "no-op for empty worker id",
			workerID:            "",
			wantWorkerHeartbeat: true,
			wantWorkerState:     true,
			wantWorkerMailbox:   true,
			wantPendingDispatch: true,
			wantOtherDispatch:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			teamName := "team-a"
			paths := NewPaths(t.TempDir())
			store := NewStore(paths)
			require.NoError(t, store.EnsureDirs(teamName))

			require.NoError(t, store.WriteJSON(paths.WorkerPath(teamName, "worker-1"), map[string]string{"id": "worker-1"}))

			heartbeatPath := paths.HeartbeatPath(teamName, "worker-1")
			require.NoError(t, os.MkdirAll(filepath.Dir(heartbeatPath), 0o755))
			require.NoError(t, store.WriteJSON(heartbeatPath, &Heartbeat{WorkerID: "worker-1", Timestamp: time.Now()}))

			mailboxPath := filepath.Join(paths.WorkerMailboxDir(teamName, "worker-1"), "msg-1.json")
			require.NoError(t, os.MkdirAll(filepath.Dir(mailboxPath), 0o755))
			require.NoError(t, os.WriteFile(mailboxPath, []byte("{}"), 0o644))

			require.NoError(t, os.MkdirAll(paths.DispatchDir(teamName), 0o755))
			require.NoError(t, store.WriteJSON(paths.DispatchPath(teamName, "task-pending-worker-1"), &DispatchEntry{TaskID: "task-pending-worker-1", WorkerID: "worker-1", Status: DispatchStatusPending}))
			require.NoError(t, store.WriteJSON(paths.DispatchPath(teamName, "task-pending-worker-2"), &DispatchEntry{TaskID: "task-pending-worker-2", WorkerID: "worker-2", Status: DispatchStatusPending}))

			cleaner := NewCleaner(paths, store, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			err := cleaner.CleanupWorker(context.Background(), teamName, tt.workerID)
			require.NoError(t, err)

			_, err = os.Stat(heartbeatPath)
			if tt.wantWorkerHeartbeat {
				require.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, os.ErrNotExist)
			}

			_, err = os.Stat(paths.WorkerPath(teamName, "worker-1"))
			if tt.wantWorkerState {
				require.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, os.ErrNotExist)
			}

			_, err = os.Stat(paths.WorkerMailboxDir(teamName, "worker-1"))
			if tt.wantWorkerMailbox {
				require.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, os.ErrNotExist)
			}

			_, err = os.Stat(paths.DispatchPath(teamName, "task-pending-worker-1"))
			if tt.wantPendingDispatch {
				require.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, os.ErrNotExist)
			}

			_, err = os.Stat(paths.DispatchPath(teamName, "task-pending-worker-2"))
			if tt.wantOtherDispatch {
				require.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}

func TestCleaner_CleanupStaleHeartbeats(t *testing.T) {
	tests := []struct {
		name            string
		listErr         error
		staleMap        map[string]bool
		wantErr         bool
		wantStaleExists bool
		wantFreshExists bool
	}{
		{
			name: "removes only stale heartbeats",
			staleMap: map[string]bool{
				"worker-stale": true,
				"worker-fresh": false,
			},
			wantStaleExists: false,
			wantFreshExists: true,
		},
		{
			name:            "returns list error",
			listErr:         errors.New("list failed"),
			wantErr:         true,
			wantStaleExists: true,
			wantFreshExists: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			teamName := "team-a"
			paths := NewPaths(t.TempDir())
			store := NewStore(paths)
			require.NoError(t, store.EnsureDirs(teamName))

			stalePath := paths.HeartbeatPath(teamName, "worker-stale")
			freshPath := paths.HeartbeatPath(teamName, "worker-fresh")
			require.NoError(t, os.MkdirAll(filepath.Dir(stalePath), 0o755))
			require.NoError(t, os.MkdirAll(filepath.Dir(freshPath), 0o755))
			require.NoError(t, store.WriteJSON(stalePath, &Heartbeat{WorkerID: "worker-stale", Timestamp: time.Now().Add(-10 * time.Minute)}))
			require.NoError(t, store.WriteJSON(freshPath, &Heartbeat{WorkerID: "worker-fresh", Timestamp: time.Now()}))

			heartbeats := &fakeHeartbeatInspector{
				listErr: tt.listErr,
				list:    []Heartbeat{{WorkerID: "worker-stale"}, {WorkerID: "worker-fresh"}},
				stale:   tt.staleMap,
			}

			cleaner := NewCleaner(paths, store, nil, heartbeats, slog.New(slog.NewTextHandler(io.Discard, nil)))
			err := cleaner.CleanupStaleHeartbeats(context.Background(), teamName)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			_, err = os.Stat(stalePath)
			if tt.wantStaleExists {
				require.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, os.ErrNotExist)
			}

			_, err = os.Stat(freshPath)
			if tt.wantFreshExists {
				require.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}

func TestCleaner_CleanupWorker_SkipsMalformedDispatchEntries(t *testing.T) {
	teamName := "team-a"
	paths := NewPaths(t.TempDir())
	store := NewStore(paths)
	require.NoError(t, store.EnsureDirs(teamName))

	require.NoError(t, os.MkdirAll(paths.DispatchDir(teamName), 0o755))
	require.NoError(t, store.WriteJSON(paths.DispatchPath(teamName, "task-pending-worker-1"), &DispatchEntry{TaskID: "task-pending-worker-1", WorkerID: "worker-1", Status: DispatchStatusPending}))

	malformedPath := paths.DispatchPath(teamName, "task-malformed")
	require.NoError(t, os.WriteFile(malformedPath, []byte("{\"task_id\":"), 0o644))

	cleaner := NewCleaner(paths, store, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, cleaner.CleanupWorker(context.Background(), teamName, "worker-1"))

	_, err := os.Stat(paths.DispatchPath(teamName, "task-pending-worker-1"))
	assert.ErrorIs(t, err, os.ErrNotExist)

	_, err = os.Stat(malformedPath)
	require.NoError(t, err)
}
