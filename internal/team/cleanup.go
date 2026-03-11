package team

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type TeamSession interface {
	Kill(ctx context.Context) error
}

type TeamSessionProvider interface {
	SessionForTeam(teamName string) TeamSession
}

type HeartbeatInspector interface {
	ListAll(teamName string) ([]Heartbeat, error)
	IsStale(teamName, workerID string) (bool, error)
}

type Cleaner struct {
	paths      *Paths
	store      *Store
	sessions   TeamSessionProvider
	heartbeats HeartbeatInspector
	logger     *slog.Logger

	mu sync.Mutex
}

func NewCleaner(paths *Paths, store *Store, sessions TeamSessionProvider, heartbeats HeartbeatInspector, logger *slog.Logger) *Cleaner {
	if logger == nil {
		logger = slog.Default()
	}

	return &Cleaner{
		paths:      paths,
		store:      store,
		sessions:   sessions,
		heartbeats: heartbeats,
		logger:     logger,
	}
}

func (c *Cleaner) CleanupTeam(ctx context.Context, teamName string) error {
	if teamName == "" {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	var errs []error

	if c.sessions != nil {
		session := c.sessions.SessionForTeam(teamName)
		if session != nil {
			if err := session.Kill(ctx); err != nil {
				c.logger.Warn("failed to kill tmux session", "team", teamName, "error", err)
				errs = append(errs, fmt.Errorf("kill tmux session: %w", err))
			}
		}
	}

	if err := c.cleanupStaleHeartbeatsLocked(teamName); err != nil {
		c.logger.Warn("failed to cleanup stale heartbeats", "team", teamName, "error", err)
		errs = append(errs, err)
	}

	if err := c.cleanupPendingDispatchLocked(teamName, ""); err != nil {
		c.logger.Warn("failed to cleanup pending dispatch entries", "team", teamName, "error", err)
		errs = append(errs, err)
	}

	if c.store != nil {
		if err := c.store.DeleteTeam(teamName); err != nil {
			c.logger.Warn("failed to delete team state", "team", teamName, "error", err)
			errs = append(errs, fmt.Errorf("delete team state: %w", err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (c *Cleaner) CleanupWorker(_ context.Context, teamName, workerID string) error {
	if teamName == "" || workerID == "" {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	var errs []error

	paths := []string{
		c.paths.WorkerPath(teamName, workerID),
		c.paths.HeartbeatPath(teamName, workerID),
	}

	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			c.logger.Warn("failed to remove worker file", "team", teamName, "worker", workerID, "path", path, "error", err)
			errs = append(errs, fmt.Errorf("remove %s: %w", path, err))
		}
	}

	dirs := []string{
		filepath.Dir(c.paths.HeartbeatPath(teamName, workerID)),
		c.paths.WorkerMailboxDir(teamName, workerID),
	}

	for _, dir := range dirs {
		if err := os.RemoveAll(dir); err != nil {
			c.logger.Warn("failed to remove worker directory", "team", teamName, "worker", workerID, "path", dir, "error", err)
			errs = append(errs, fmt.Errorf("remove %s: %w", dir, err))
		}
	}

	if err := c.cleanupPendingDispatchLocked(teamName, workerID); err != nil {
		c.logger.Warn("failed to cleanup worker dispatch entries", "team", teamName, "worker", workerID, "error", err)
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (c *Cleaner) CleanupStaleHeartbeats(_ context.Context, teamName string) error {
	if teamName == "" {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	return c.cleanupStaleHeartbeatsLocked(teamName)
}

func (c *Cleaner) cleanupStaleHeartbeatsLocked(teamName string) error {
	if c.heartbeats == nil {
		return nil
	}

	heartbeats, err := c.heartbeats.ListAll(teamName)
	if err != nil {
		return fmt.Errorf("list heartbeats: %w", err)
	}

	var errs []error
	for _, hb := range heartbeats {
		stale, staleErr := c.heartbeats.IsStale(teamName, hb.WorkerID)
		if staleErr != nil {
			c.logger.Warn("failed to evaluate heartbeat staleness", "team", teamName, "worker", hb.WorkerID, "error", staleErr)
			errs = append(errs, fmt.Errorf("check stale heartbeat %s: %w", hb.WorkerID, staleErr))
			continue
		}
		if !stale {
			continue
		}

		path := c.paths.HeartbeatPath(teamName, hb.WorkerID)
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			c.logger.Warn("failed to remove stale heartbeat", "team", teamName, "worker", hb.WorkerID, "path", path, "error", removeErr)
			errs = append(errs, fmt.Errorf("remove stale heartbeat %s: %w", hb.WorkerID, removeErr))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (c *Cleaner) cleanupPendingDispatchLocked(teamName, workerID string) error {
	if c.store == nil {
		return nil
	}

	c.store.mu.Lock()
	defer c.store.mu.Unlock()

	dispatchDir := c.paths.DispatchDir(teamName)
	entries, err := os.ReadDir(dispatchDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read dispatch dir: %w", err)
	}

	var errs []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := filepath.Join(dispatchDir, entry.Name())
		var dispatch DispatchEntry
		if readErr := c.store.ReadJSON(path, &dispatch); readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			if isJSONUnmarshalError(readErr) {
				c.logger.Warn("skipping malformed dispatch entry", "team", teamName, "path", path, "error", readErr)
				continue
			}
			errs = append(errs, fmt.Errorf("read dispatch entry %s: %w", path, readErr))
			continue
		}

		if workerID != "" && dispatch.WorkerID != workerID {
			continue
		}
		if dispatch.Status != DispatchStatusPending || dispatch.AckedAt != nil {
			continue
		}

		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove dispatch entry %s: %w", path, removeErr))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}
