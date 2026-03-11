package team

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DispatchStatusPending   = "pending"
	DispatchStatusAcked     = "acked"
	DispatchStatusTimeout   = "timeout"
	DispatchStatusCompleted = "completed"
)

// DispatchEntry represents a dispatch attempt for a task.
type DispatchEntry struct {
	TaskID       string     `json:"task_id"`
	WorkerID     string     `json:"worker_id"`
	Prompt       string     `json:"prompt"`
	DispatchedAt time.Time  `json:"dispatched_at"`
	AckedAt      *time.Time `json:"acked_at,omitempty"`
	Status       string     `json:"status"`
}

// DispatchQueue manages dispatch entries and acknowledgment tracking.
type DispatchQueue struct {
	store      *Store
	paths      *Paths
	ackTimeout time.Duration
}

// NewDispatchQueue creates a queue backed by Store and Paths.
func NewDispatchQueue(store *Store, paths *Paths, ackTimeout time.Duration) *DispatchQueue {
	return &DispatchQueue{
		store:      store,
		paths:      paths,
		ackTimeout: ackTimeout,
	}
}

// Dispatch writes a task dispatch entry to disk.
func (q *DispatchQueue) Dispatch(teamName string, entry DispatchEntry) error {
	if teamName == "" {
		return fmt.Errorf("dispatch: team name is required")
	}
	if entry.TaskID == "" {
		return fmt.Errorf("dispatch: task ID is required")
	}
	if entry.WorkerID == "" {
		return fmt.Errorf("dispatch: worker ID is required")
	}
	if entry.Prompt == "" {
		return fmt.Errorf("dispatch: prompt is required")
	}

	if entry.DispatchedAt.IsZero() {
		entry.DispatchedAt = time.Now()
	}
	entry.AckedAt = nil
	entry.Status = DispatchStatusPending

	q.store.mu.Lock()
	defer q.store.mu.Unlock()

	path := q.paths.DispatchPath(teamName, entry.TaskID)
	if err := q.store.WriteJSON(path, &entry); err != nil {
		return fmt.Errorf("dispatch: %w", err)
	}
	return nil
}

// Ack marks a dispatched task as acknowledged by the assigned worker.
func (q *DispatchQueue) Ack(teamName, taskID, workerID string) error {
	if teamName == "" {
		return fmt.Errorf("ack dispatch: team name is required")
	}
	if taskID == "" {
		return fmt.Errorf("ack dispatch: task ID is required")
	}
	if workerID == "" {
		return fmt.Errorf("ack dispatch: worker ID is required")
	}

	q.store.mu.Lock()
	defer q.store.mu.Unlock()

	entry, err := q.readLocked(teamName, taskID)
	if err != nil {
		return err
	}
	if entry.WorkerID != workerID {
		return fmt.Errorf("ack dispatch: worker mismatch for task %s", taskID)
	}

	now := time.Now()
	entry.AckedAt = &now
	entry.Status = DispatchStatusAcked

	if err := q.store.WriteJSON(q.paths.DispatchPath(teamName, taskID), entry); err != nil {
		return fmt.Errorf("ack dispatch: %w", err)
	}
	return nil
}

// Complete marks a dispatch entry as completed.
func (q *DispatchQueue) Complete(teamName, taskID string) error {
	if teamName == "" {
		return fmt.Errorf("complete dispatch: team name is required")
	}
	if taskID == "" {
		return fmt.Errorf("complete dispatch: task ID is required")
	}

	q.store.mu.Lock()
	defer q.store.mu.Unlock()

	entry, err := q.readLocked(teamName, taskID)
	if err != nil {
		return err
	}
	entry.Status = DispatchStatusCompleted

	if err := q.store.WriteJSON(q.paths.DispatchPath(teamName, taskID), entry); err != nil {
		return fmt.Errorf("complete dispatch: %w", err)
	}
	return nil
}

// GetPending returns all unacknowledged pending dispatch entries.
func (q *DispatchQueue) GetPending(teamName string) ([]DispatchEntry, error) {
	if teamName == "" {
		return nil, fmt.Errorf("get pending dispatch: team name is required")
	}

	q.store.mu.Lock()
	defer q.store.mu.Unlock()

	all, err := q.listAllLocked(teamName)
	if err != nil {
		return nil, err
	}

	pending := make([]DispatchEntry, 0, len(all))
	for _, entry := range all {
		if entry.Status == DispatchStatusPending && entry.AckedAt == nil {
			pending = append(pending, entry)
		}
	}
	return pending, nil
}

// GetTimedOut returns dispatch entries that exceeded acknowledgment timeout.
func (q *DispatchQueue) GetTimedOut(teamName string) ([]DispatchEntry, error) {
	if teamName == "" {
		return nil, fmt.Errorf("get timed out dispatch: team name is required")
	}

	q.store.mu.Lock()
	defer q.store.mu.Unlock()

	all, err := q.listAllLocked(teamName)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	timedOut := make([]DispatchEntry, 0)
	for i := range all {
		entry := &all[i]
		if entry.Status != DispatchStatusPending || entry.AckedAt != nil {
			continue
		}
		if q.ackTimeout <= 0 || now.Sub(entry.DispatchedAt) <= q.ackTimeout {
			continue
		}

		entry.Status = DispatchStatusTimeout
		if err := q.store.WriteJSON(q.paths.DispatchPath(teamName, entry.TaskID), entry); err != nil {
			return nil, fmt.Errorf("get timed out dispatch: write task %s: %w", entry.TaskID, err)
		}
		timedOut = append(timedOut, *entry)
	}

	return timedOut, nil
}

// Requeue resets a timed-out dispatch entry back to pending.
func (q *DispatchQueue) Requeue(teamName, taskID string) error {
	if teamName == "" {
		return fmt.Errorf("requeue dispatch: team name is required")
	}
	if taskID == "" {
		return fmt.Errorf("requeue dispatch: task ID is required")
	}

	q.store.mu.Lock()
	defer q.store.mu.Unlock()

	entry, err := q.readLocked(teamName, taskID)
	if err != nil {
		return err
	}
	if entry.Status != DispatchStatusTimeout {
		return fmt.Errorf("requeue dispatch: task %s is not timed out", taskID)
	}

	entry.Status = DispatchStatusPending
	entry.WorkerID = ""
	entry.AckedAt = nil
	entry.DispatchedAt = time.Now()

	if err := q.store.WriteJSON(q.paths.DispatchPath(teamName, taskID), entry); err != nil {
		return fmt.Errorf("requeue dispatch: %w", err)
	}
	return nil
}

// ListAll returns all dispatch entries for the team.
func (q *DispatchQueue) ListAll(teamName string) ([]DispatchEntry, error) {
	if teamName == "" {
		return nil, fmt.Errorf("list dispatch: team name is required")
	}

	q.store.mu.Lock()
	defer q.store.mu.Unlock()

	return q.listAllLocked(teamName)
}

func (q *DispatchQueue) readLocked(teamName, taskID string) (*DispatchEntry, error) {
	path := q.paths.DispatchPath(teamName, taskID)
	var entry DispatchEntry
	if err := q.store.ReadJSON(path, &entry); err != nil {
		return nil, fmt.Errorf("read dispatch: %w", err)
	}
	return &entry, nil
}

func (q *DispatchQueue) listAllLocked(teamName string) ([]DispatchEntry, error) {
	dir := q.paths.DispatchDir(teamName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []DispatchEntry{}, nil
		}
		return nil, fmt.Errorf("read dispatch dir: %w", err)
	}

	list := make([]DispatchEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		var dispatchEntry DispatchEntry
		if err := q.store.ReadJSON(path, &dispatchEntry); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read dispatch entry %s: %w", path, err)
		}
		list = append(list, dispatchEntry)
	}

	return list, nil
}
