package team

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const defaultStaleLockThreshold = 5 * time.Minute

type EventLogRotator struct {
	paths  *Paths
	logger *slog.Logger
	mu     sync.Mutex
}

func NewEventLogRotator(paths *Paths, logger *slog.Logger) *EventLogRotator {
	if logger == nil {
		logger = slog.Default()
	}

	return &EventLogRotator{
		paths:  paths,
		logger: logger,
	}
}

func (r *EventLogRotator) Rotate(teamName string) error {
	if r == nil || r.paths == nil {
		return errors.New("event log rotator is not initialized")
	}
	if teamName == "" {
		return errors.New("team name is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	eventsDir := r.paths.EventsDir(teamName)
	if err := os.MkdirAll(eventsDir, 0o755); err != nil {
		return fmt.Errorf("create events dir: %w", err)
	}

	activePath := filepath.Join(eventsDir, eventsFileName)
	if _, err := os.Stat(activePath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat active event log: %w", err)
		}
	} else {
		archivePath := filepath.Join(eventsDir, archiveLogName(time.Now().UTC()))
		if err := os.Rename(activePath, archivePath); err != nil {
			return fmt.Errorf("archive event log: %w", err)
		}
		r.logger.Info("rotated event log", "team", teamName, "archive", archivePath)
	}

	file, err := os.OpenFile(activePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create new active event log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close new active event log: %w", err)
	}

	return nil
}

func (r *EventLogRotator) CleanupOldLogs(teamName string, maxAge time.Duration) error {
	if r == nil || r.paths == nil {
		return errors.New("event log rotator is not initialized")
	}
	if teamName == "" {
		return errors.New("team name is required")
	}
	if maxAge <= 0 {
		return errors.New("maxAge must be greater than zero")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	eventsDir := r.paths.EventsDir(teamName)
	entries, err := os.ReadDir(eventsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read events dir: %w", err)
	}

	cutoff := time.Now().Add(-maxAge)
	var errs []error

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !isArchiveLogName(name) {
			continue
		}

		info, infoErr := entry.Info()
		if infoErr != nil {
			errs = append(errs, fmt.Errorf("stat archived log %s: %w", name, infoErr))
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}

		path := filepath.Join(eventsDir, name)
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove archived log %s: %w", path, removeErr))
			continue
		}

		r.logger.Info("cleaned old archived event log", "team", teamName, "path", path)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

type StaleLockRecovery struct {
	paths          *Paths
	logger         *slog.Logger
	staleThreshold time.Duration
	mu             sync.Mutex
}

func NewStaleLockRecovery(paths *Paths, logger *slog.Logger, staleThreshold ...time.Duration) *StaleLockRecovery {
	if logger == nil {
		logger = slog.Default()
	}

	threshold := defaultStaleLockThreshold
	if len(staleThreshold) > 0 && staleThreshold[0] > 0 {
		threshold = staleThreshold[0]
	}

	return &StaleLockRecovery{
		paths:          paths,
		logger:         logger,
		staleThreshold: threshold,
	}
}

func (r *StaleLockRecovery) RecoverStaleLocks(teamName string) error {
	if r == nil || r.paths == nil {
		return errors.New("stale lock recovery is not initialized")
	}
	if teamName == "" {
		return errors.New("team name is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	teamDir := r.paths.TeamDir(teamName)
	if _, err := os.Stat(teamDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat team dir: %w", err)
	}

	cutoff := time.Now().Add(-r.staleThreshold)
	var errs []error

	err := filepath.WalkDir(teamDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			errs = append(errs, fmt.Errorf("walk team dir: %w", walkErr))
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".tmp") {
			return nil
		}

		info, infoErr := d.Info()
		if infoErr != nil {
			errs = append(errs, fmt.Errorf("stat temp file %s: %w", path, infoErr))
			return nil
		}
		if info.ModTime().After(cutoff) {
			return nil
		}

		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove stale temp file %s: %w", path, removeErr))
			return nil
		}

		r.logger.Info("recovered stale lock file", "team", teamName, "path", path)
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk team dir: %w", err)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func archiveLogName(now time.Time) string {
	return fmt.Sprintf("events-%s_%d.jsonl", now.Format("20060102-150405"), now.UnixNano()%1000000)
}

func isArchiveLogName(name string) bool {
	return strings.HasPrefix(name, "events-") && strings.HasSuffix(name, ".jsonl")
}
