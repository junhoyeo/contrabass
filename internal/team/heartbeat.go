package team

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/junhoyeo/contrabass/internal/ipc"
)

type Heartbeat = ipc.Heartbeat

// HeartbeatMonitor manages file-based worker heartbeat read/write operations.
type HeartbeatMonitor struct {
	store          *Store
	paths          *Paths
	staleThreshold time.Duration
	logger         *slog.Logger
}

// NewHeartbeatMonitor creates a monitor backed by the given Store and Paths.
func NewHeartbeatMonitor(store *Store, paths *Paths, staleThreshold time.Duration) *HeartbeatMonitor {
	logger := slog.Default()
	return &HeartbeatMonitor{
		store:          store,
		paths:          paths,
		staleThreshold: staleThreshold,
		logger:         logger,
	}
}

// Write records a heartbeat for a worker using atomic JSON writes.
func (m *HeartbeatMonitor) Write(teamName string, hb Heartbeat) error {
	if hb.WorkerID == "" {
		return fmt.Errorf("write heartbeat: worker ID is required")
	}
	if hb.Timestamp.IsZero() {
		hb.Timestamp = time.Now()
	}

	m.store.mu.Lock()
	defer m.store.mu.Unlock()

	path := m.paths.HeartbeatPath(teamName, hb.WorkerID)
	if err := m.store.WriteJSON(path, &hb); err != nil {
		return fmt.Errorf("write heartbeat: %w", err)
	}
	return nil
}

// Read loads the heartbeat for a specific worker.
func (m *HeartbeatMonitor) Read(teamName, workerID string) (*Heartbeat, error) {
	m.store.mu.Lock()
	defer m.store.mu.Unlock()

	hb, err := m.readLocked(teamName, workerID)
	if err != nil {
		return nil, err
	}
	return hb, nil
}

// IsStale reports whether a worker heartbeat is older than the configured threshold.
func (m *HeartbeatMonitor) IsStale(teamName, workerID string) (bool, error) {
	m.store.mu.Lock()
	defer m.store.mu.Unlock()

	path := m.paths.HeartbeatPath(teamName, workerID)
	fi, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("read heartbeat metadata: %w", err)
	}
	return m.isStaleAt(fi.ModTime()), nil
}

// ListStale scans worker heartbeats and returns stale entries.
func (m *HeartbeatMonitor) ListStale(teamName string) ([]Heartbeat, error) {
	m.store.mu.Lock()
	defer m.store.mu.Unlock()

	all, err := m.listAllLocked(teamName)
	if err != nil {
		return nil, err
	}

	stale := make([]Heartbeat, 0, len(all))
	for _, hb := range all {
		path := m.paths.HeartbeatPath(teamName, hb.WorkerID)
		fi, statErr := os.Stat(path)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read heartbeat metadata %s: %w", path, statErr)
		}

		if m.isStaleAt(fi.ModTime()) {
			stale = append(stale, hb)
		}
	}
	return stale, nil
}

// ListAll scans worker heartbeat files and returns all readable entries.
func (m *HeartbeatMonitor) ListAll(teamName string) ([]Heartbeat, error) {
	m.store.mu.Lock()
	defer m.store.mu.Unlock()

	return m.listAllLocked(teamName)
}

func (m *HeartbeatMonitor) readLocked(teamName, workerID string) (*Heartbeat, error) {
	path := m.paths.HeartbeatPath(teamName, workerID)
	var hb Heartbeat
	if err := m.store.ReadJSON(path, &hb); err != nil {
		return nil, fmt.Errorf("read heartbeat: %w", err)
	}
	return &hb, nil
}

func (m *HeartbeatMonitor) listAllLocked(teamName string) ([]Heartbeat, error) {
	workersDir := m.paths.WorkersDir(teamName)
	entries, err := os.ReadDir(workersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Heartbeat{}, nil
		}
		return nil, fmt.Errorf("read workers dir: %w", err)
	}

	heartbeats := make([]Heartbeat, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		hb, readErr := m.readLocked(teamName, entry.Name())
		if readErr != nil {
			hbPath := m.paths.HeartbeatPath(teamName, entry.Name())
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			if isJSONUnmarshalError(readErr) {
				m.logger.Warn("skipping malformed heartbeat entry", "team", teamName, "path", hbPath, "error", readErr)
				continue
			}
			return nil, fmt.Errorf("read heartbeat %s: %w", hbPath, readErr)
		}
		heartbeats = append(heartbeats, *hb)
	}

	return heartbeats, nil
}

func (m *HeartbeatMonitor) isStaleAt(mtime time.Time) bool {
	if m.staleThreshold <= 0 {
		return false
	}
	return time.Since(mtime) > m.staleThreshold
}
