package team

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventLogRotator_Rotate(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, paths *Paths, teamName string)
		assertAfter func(t *testing.T, paths *Paths, teamName string)
	}{
		{
			name: "archives current event log and creates new active log",
			setup: func(t *testing.T, paths *Paths, teamName string) {
				t.Helper()
				eventsDir := paths.EventsDir(teamName)
				require.NoError(t, os.MkdirAll(eventsDir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(eventsDir, eventsFileName), []byte("{\"type\":\"x\"}\n"), 0o644))
			},
			assertAfter: func(t *testing.T, paths *Paths, teamName string) {
				t.Helper()
				eventsDir := paths.EventsDir(teamName)
				entries, err := os.ReadDir(eventsDir)
				require.NoError(t, err)
				archiveCount := 0
				for _, entry := range entries {
					if isArchiveLogName(entry.Name()) {
						archiveCount++
					}
				}
				assert.Equal(t, 1, archiveCount)

				activePath := filepath.Join(eventsDir, eventsFileName)
				data, err := os.ReadFile(activePath)
				require.NoError(t, err)
				assert.Empty(t, data)
			},
		},
		{
			name: "creates active log when events directory is missing",
			setup: func(t *testing.T, _ *Paths, _ string) {
				t.Helper()
			},
			assertAfter: func(t *testing.T, paths *Paths, teamName string) {
				t.Helper()
				activePath := filepath.Join(paths.EventsDir(teamName), eventsFileName)
				_, err := os.Stat(activePath)
				require.NoError(t, err)

				entries, err := os.ReadDir(paths.EventsDir(teamName))
				require.NoError(t, err)
				archiveCount := 0
				for _, entry := range entries {
					if isArchiveLogName(entry.Name()) {
						archiveCount++
					}
				}
				assert.Equal(t, 0, archiveCount)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			teamName := "team-a"
			paths := NewPaths(t.TempDir())
			rotator := NewEventLogRotator(paths, slog.New(slog.NewTextHandler(io.Discard, nil)))

			tt.setup(t, paths, teamName)
			err := rotator.Rotate(teamName)
			require.NoError(t, err)
			tt.assertAfter(t, paths, teamName)
		})
	}
}

func TestEventLogRotator_CleanupOldLogs(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, paths *Paths, teamName string)
		assertion func(t *testing.T, paths *Paths, teamName string)
	}{
		{
			name: "removes only archived logs older than maxAge",
			setup: func(t *testing.T, paths *Paths, teamName string) {
				t.Helper()
				eventsDir := paths.EventsDir(teamName)
				require.NoError(t, os.MkdirAll(eventsDir, 0o755))

				old := filepath.Join(eventsDir, "events-20200101-000000.jsonl")
				recent := filepath.Join(eventsDir, "events-20990101-000000.jsonl")
				active := filepath.Join(eventsDir, eventsFileName)

				require.NoError(t, os.WriteFile(old, []byte("old"), 0o644))
				require.NoError(t, os.WriteFile(recent, []byte("recent"), 0o644))
				require.NoError(t, os.WriteFile(active, []byte("active"), 0o644))

				oldTime := time.Now().Add(-2 * time.Hour)
				recentTime := time.Now().Add(-10 * time.Minute)
				require.NoError(t, os.Chtimes(old, oldTime, oldTime))
				require.NoError(t, os.Chtimes(recent, recentTime, recentTime))
			},
			assertion: func(t *testing.T, paths *Paths, teamName string) {
				t.Helper()
				eventsDir := paths.EventsDir(teamName)
				_, oldErr := os.Stat(filepath.Join(eventsDir, "events-20200101-000000.jsonl"))
				assert.True(t, os.IsNotExist(oldErr))

				_, recentErr := os.Stat(filepath.Join(eventsDir, "events-20990101-000000.jsonl"))
				require.NoError(t, recentErr)

				_, activeErr := os.Stat(filepath.Join(eventsDir, eventsFileName))
				require.NoError(t, activeErr)
			},
		},
		{
			name: "missing events directory is handled gracefully",
			setup: func(t *testing.T, _ *Paths, _ string) {
				t.Helper()
			},
			assertion: func(t *testing.T, _ *Paths, _ string) {
				t.Helper()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			teamName := "team-a"
			paths := NewPaths(t.TempDir())
			rotator := NewEventLogRotator(paths, slog.New(slog.NewTextHandler(io.Discard, nil)))

			tt.setup(t, paths, teamName)
			err := rotator.CleanupOldLogs(teamName, time.Hour)
			require.NoError(t, err)
			tt.assertion(t, paths, teamName)
		})
	}
}

func TestStaleLockRecovery_RecoverStaleLocks(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, paths *Paths, teamName string)
		assertion func(t *testing.T, paths *Paths, teamName string)
	}{
		{
			name: "removes only stale tmp files",
			setup: func(t *testing.T, paths *Paths, teamName string) {
				t.Helper()
				teamDir := paths.TeamDir(teamName)
				require.NoError(t, os.MkdirAll(filepath.Join(teamDir, "tasks"), 0o755))

				// Names must match the Store's real temp scheme
				// "<path>.tmp.<pid>.<unixnano>" (writeJSONBytesNoLock).
				staleTmp := filepath.Join(teamDir, "manifest.json.tmp.4242.1712000000000000000")
				recentTmp := filepath.Join(teamDir, "tasks", "task-1.json.tmp.4242.1712000000000000001")
				nonTmp := filepath.Join(teamDir, "phase-state.json")

				require.NoError(t, os.WriteFile(staleTmp, []byte("stale"), 0o644))
				require.NoError(t, os.WriteFile(recentTmp, []byte("recent"), 0o644))
				require.NoError(t, os.WriteFile(nonTmp, []byte("keep"), 0o644))

				staleTime := time.Now().Add(-10 * time.Minute)
				recentTime := time.Now().Add(-1 * time.Minute)
				require.NoError(t, os.Chtimes(staleTmp, staleTime, staleTime))
				require.NoError(t, os.Chtimes(recentTmp, recentTime, recentTime))
			},
			assertion: func(t *testing.T, paths *Paths, teamName string) {
				t.Helper()
				teamDir := paths.TeamDir(teamName)
				_, staleErr := os.Stat(filepath.Join(teamDir, "manifest.json.tmp.4242.1712000000000000000"))
				assert.True(t, os.IsNotExist(staleErr))

				_, recentErr := os.Stat(filepath.Join(teamDir, "tasks", "task-1.json.tmp.4242.1712000000000000001"))
				require.NoError(t, recentErr)

				_, nonTmpErr := os.Stat(filepath.Join(teamDir, "phase-state.json"))
				require.NoError(t, nonTmpErr)
			},
		},
		{
			name: "missing team directory is handled gracefully",
			setup: func(t *testing.T, _ *Paths, _ string) {
				t.Helper()
			},
			assertion: func(t *testing.T, _ *Paths, _ string) {
				t.Helper()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			teamName := "team-a"
			paths := NewPaths(t.TempDir())
			recovery := NewStaleLockRecovery(paths, slog.New(slog.NewTextHandler(io.Discard, nil)), 5*time.Minute)

			tt.setup(t, paths, teamName)
			err := recovery.RecoverStaleLocks(teamName)
			require.NoError(t, err)
			tt.assertion(t, paths, teamName)
		})
	}
}
