package team

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupHeartbeatMonitor(t *testing.T, threshold time.Duration) (*Store, *Paths, *HeartbeatMonitor) {
	t.Helper()
	paths := NewPaths(t.TempDir())
	store := NewStore(paths)
	monitor := NewHeartbeatMonitor(store, paths, threshold)
	return store, paths, monitor
}

func TestHeartbeatMonitor_WriteRead(t *testing.T) {
	tests := []struct {
		name string
		hb   Heartbeat
	}{
		{
			name: "writes and reads heartbeat",
			hb: Heartbeat{
				WorkerID:    "worker-1",
				PID:         1234,
				CurrentTask: "task-1",
				Status:      "running",
				Timestamp:   time.Now().Add(-2 * time.Second),
			},
		},
		{
			name: "sets timestamp when zero",
			hb: Heartbeat{
				WorkerID:    "worker-2",
				PID:         2345,
				CurrentTask: "task-2",
				Status:      "idle",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, paths, monitor := setupHeartbeatMonitor(t, 5*time.Minute)
			teamName := "team-a"
			require.NoError(t, store.EnsureDirs(teamName))

			err := monitor.Write(teamName, tt.hb)
			require.NoError(t, err)

			hbPath := paths.HeartbeatPath(teamName, tt.hb.WorkerID)
			_, err = os.Stat(hbPath)
			require.NoError(t, err)

			got, err := monitor.Read(teamName, tt.hb.WorkerID)
			require.NoError(t, err)
			assert.Equal(t, tt.hb.WorkerID, got.WorkerID)
			assert.Equal(t, tt.hb.PID, got.PID)
			assert.Equal(t, tt.hb.CurrentTask, got.CurrentTask)
			assert.Equal(t, tt.hb.Status, got.Status)
			assert.False(t, got.Timestamp.IsZero())
		})
	}
}

func TestHeartbeatMonitor_IsStale(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name      string
		threshold time.Duration
		mtime     time.Time
		wantStale bool
	}{
		{
			name:      "stale when older than threshold",
			threshold: 5 * time.Second,
			mtime:     now.Add(-10 * time.Second),
			wantStale: true,
		},
		{
			name:      "fresh when within threshold",
			threshold: 30 * time.Second,
			mtime:     now.Add(-5 * time.Second),
			wantStale: false,
		},
		{
			name:      "never stale when threshold disabled",
			threshold: 0,
			mtime:     now.Add(-24 * time.Hour),
			wantStale: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, paths, monitor := setupHeartbeatMonitor(t, tt.threshold)
			teamName := "team-a"
			require.NoError(t, store.EnsureDirs(teamName))

			writeTimestamp := now
			if tt.wantStale {
				writeTimestamp = now.Add(1 * time.Hour)
			}

			err := monitor.Write(teamName, Heartbeat{
				WorkerID:  "worker-1",
				PID:       1234,
				Status:    "running",
				Timestamp: writeTimestamp,
			})
			require.NoError(t, err)

			hbPath := paths.HeartbeatPath(teamName, "worker-1")
			require.NoError(t, os.Chtimes(hbPath, tt.mtime, tt.mtime))

			got, err := monitor.IsStale(teamName, "worker-1")
			require.NoError(t, err)
			assert.Equal(t, tt.wantStale, got)
		})
	}
}

func TestHeartbeatMonitor_IsStale_UsesFileModTimeOverPayloadTimestamp(t *testing.T) {
	store, paths, monitor := setupHeartbeatMonitor(t, 5*time.Second)
	teamName := "team-a"
	require.NoError(t, store.EnsureDirs(teamName))

	require.NoError(t, monitor.Write(teamName, Heartbeat{
		WorkerID:  "worker-1",
		PID:       1234,
		Status:    "running",
		Timestamp: time.Now().Add(1 * time.Hour),
	}))

	oldTime := time.Now().Add(-30 * time.Second)
	hbPath := paths.HeartbeatPath(teamName, "worker-1")
	require.NoError(t, os.Chtimes(hbPath, oldTime, oldTime))

	stale, err := monitor.IsStale(teamName, "worker-1")
	require.NoError(t, err)
	assert.True(t, stale)
}

func TestHeartbeatMonitor_ListStale(t *testing.T) {
	tests := []struct {
		name          string
		threshold     time.Duration
		heartbeats    []Heartbeat
		workersNoFile []string
		wantWorkerIDs []string
	}{
		{
			name:      "returns only stale worker heartbeats",
			threshold: 5 * time.Second,
			heartbeats: []Heartbeat{
				{WorkerID: "worker-stale", PID: 10, Status: "running", Timestamp: time.Now().Add(1 * time.Hour)},
				{WorkerID: "worker-fresh", PID: 11, Status: "running", Timestamp: time.Now().Add(-1 * time.Hour)},
			},
			workersNoFile: []string{"worker-no-heartbeat"},
			wantWorkerIDs: []string{"worker-stale"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, paths, monitor := setupHeartbeatMonitor(t, tt.threshold)
			teamName := "team-a"
			require.NoError(t, store.EnsureDirs(teamName))

			for _, hb := range tt.heartbeats {
				require.NoError(t, monitor.Write(teamName, hb))
			}

			staleMtime := time.Now().Add(-20 * time.Second)
			freshMtime := time.Now().Add(-1 * time.Second)
			stalePath := paths.HeartbeatPath(teamName, "worker-stale")
			freshPath := paths.HeartbeatPath(teamName, "worker-fresh")
			require.NoError(t, os.Chtimes(stalePath, staleMtime, staleMtime))
			require.NoError(t, os.Chtimes(freshPath, freshMtime, freshMtime))

			for _, workerID := range tt.workersNoFile {
				dir := filepath.Dir(paths.HeartbeatPath(teamName, workerID))
				require.NoError(t, os.MkdirAll(dir, 0o755))
			}

			stale, err := monitor.ListStale(teamName)
			require.NoError(t, err)

			workerIDs := make([]string, 0, len(stale))
			for _, hb := range stale {
				workerIDs = append(workerIDs, hb.WorkerID)
			}

			assert.ElementsMatch(t, tt.wantWorkerIDs, workerIDs)
		})
	}
}

func TestHeartbeatMonitor_ListAll(t *testing.T) {
	tests := []struct {
		name       string
		heartbeats []Heartbeat
		wantCount  int
	}{
		{
			name: "lists all readable heartbeats",
			heartbeats: []Heartbeat{
				{WorkerID: "worker-1", PID: 1, Status: "running", Timestamp: time.Now()},
				{WorkerID: "worker-2", PID: 2, Status: "idle", Timestamp: time.Now()},
			},
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, paths, monitor := setupHeartbeatMonitor(t, 5*time.Minute)
			teamName := "team-a"
			require.NoError(t, store.EnsureDirs(teamName))

			for _, hb := range tt.heartbeats {
				require.NoError(t, monitor.Write(teamName, hb))
			}

			dir := filepath.Dir(paths.HeartbeatPath(teamName, "worker-no-heartbeat"))
			require.NoError(t, os.MkdirAll(dir, 0o755))

			all, err := monitor.ListAll(teamName)
			require.NoError(t, err)
			assert.Len(t, all, tt.wantCount)
		})
	}
}

func TestHeartbeatMonitor_ListAll_SkipsMalformedHeartbeat(t *testing.T) {
	store, paths, monitor := setupHeartbeatMonitor(t, 5*time.Minute)
	teamName := "team-a"
	require.NoError(t, store.EnsureDirs(teamName))

	require.NoError(t, monitor.Write(teamName, Heartbeat{WorkerID: "worker-valid", PID: 1, Status: "running", Timestamp: time.Now()}))

	malformedPath := paths.HeartbeatPath(teamName, "worker-malformed")
	require.NoError(t, os.MkdirAll(filepath.Dir(malformedPath), 0o755))
	require.NoError(t, os.WriteFile(malformedPath, []byte("{\"worker_id\":"), 0o644))

	all, err := monitor.ListAll(teamName)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "worker-valid", all[0].WorkerID)
}
