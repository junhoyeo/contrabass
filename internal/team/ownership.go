package team

import (
	"fmt"
	"os"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

// OwnershipRegistry tracks advisory file ownership per worker.
type OwnershipRegistry struct {
	store *Store
	paths *Paths
}

// NewOwnershipRegistry creates an OwnershipRegistry backed by the given Store and Paths.
func NewOwnershipRegistry(store *Store, paths *Paths) *OwnershipRegistry {
	return &OwnershipRegistry{store: store, paths: paths}
}

type ownershipData struct {
	Entries map[string]types.FileOwnership `json:"entries"`
}

func (r *OwnershipRegistry) loadOwnership(teamName string) (*ownershipData, error) {
	path := r.paths.OwnershipPath(teamName)
	var data ownershipData
	if err := r.store.ReadJSON(path, &data); err != nil {
		if os.IsNotExist(err) {
			return &ownershipData{Entries: map[string]types.FileOwnership{}}, nil
		}
		return nil, err
	}
	if data.Entries == nil {
		data.Entries = map[string]types.FileOwnership{}
	}
	return &data, nil
}

func (r *OwnershipRegistry) saveOwnership(teamName string, data *ownershipData) error {
	path := r.paths.OwnershipPath(teamName)
	return r.store.WriteJSON(path, data)
}

// Claim registers ownership of file paths for a worker's task.
// Returns a list of conflicts (paths already owned by a different worker).
func (r *OwnershipRegistry) Claim(teamName, workerID, taskID string, paths []string) ([]types.FileOwnership, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	data, err := r.loadOwnership(teamName)
	if err != nil {
		return nil, fmt.Errorf("load ownership: %w", err)
	}

	var conflicts []types.FileOwnership
	now := time.Now()

	for _, p := range paths {
		if existing, ok := data.Entries[p]; ok {
			if existing.WorkerID != workerID {
				conflicts = append(conflicts, existing)
				continue
			}
		}
		data.Entries[p] = types.FileOwnership{
			Path:      p,
			WorkerID:  workerID,
			TaskID:    taskID,
			ClaimedAt: now,
		}
	}

	if err := r.saveOwnership(teamName, data); err != nil {
		return nil, fmt.Errorf("save ownership: %w", err)
	}

	return conflicts, nil
}

// ReleaseTask removes ownership entries for a specific task.
func (r *OwnershipRegistry) ReleaseTask(teamName, taskID string) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	data, err := r.loadOwnership(teamName)
	if err != nil {
		return fmt.Errorf("load ownership: %w", err)
	}

	for path, entry := range data.Entries {
		if entry.TaskID == taskID {
			delete(data.Entries, path)
		}
	}

	return r.saveOwnership(teamName, data)
}

func (r *OwnershipRegistry) ListAll(teamName string) ([]types.FileOwnership, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	data, err := r.loadOwnership(teamName)
	if err != nil {
		return nil, fmt.Errorf("load ownership: %w", err)
	}

	entries := make([]types.FileOwnership, 0, len(data.Entries))
	for _, entry := range data.Entries {
		entries = append(entries, entry)
	}
	return entries, nil
}
