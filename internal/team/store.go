package team

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

// Store provides atomic filesystem operations for team state.
type Store struct {
	paths *Paths
	mu    sync.Mutex
}

// NewStore creates a new Store backed by the given Paths resolver.
func NewStore(paths *Paths) *Store {
	return &Store{paths: paths}
}

// EnsureDirs creates the directory structure for a team.
func (s *Store) EnsureDirs(teamName string) error {
	dirs := []string{
		s.paths.TeamDir(teamName),
		s.paths.TasksDir(teamName),
		s.paths.WorkersDir(teamName),
		s.paths.MailboxDir(teamName),
		s.paths.EventsDir(teamName),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}
	return nil
}

// WriteJSON atomically writes a value as JSON to the given path.
// It writes to a temp file first, then renames for crash safety.
func (s *Store) WriteJSON(path string, v interface{}) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create dir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write temp file %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) // best-effort cleanup
		return fmt.Errorf("rename %s to %s: %w", tmp, path, err)
	}
	return nil
}

// ReadJSON reads and unmarshals JSON from the given path.
func (s *Store) ReadJSON(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("unmarshal %s: %w", path, err)
	}
	return nil
}

// CreateManifest initializes a new team with its manifest and directory structure.
func (s *Store) CreateManifest(teamName string, cfg types.TeamConfig) (*types.TeamManifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.EnsureDirs(teamName); err != nil {
		return nil, fmt.Errorf("ensure dirs: %w", err)
	}

	now := time.Now()
	manifest := &types.TeamManifest{
		Name:      teamName,
		Workers:   []types.WorkerState{},
		Config:    cfg,
		CreatedAt: now,
		UpdatedAt: now,
	}

	path := s.paths.ManifestPath(teamName)
	if err := s.WriteJSON(path, manifest); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}

	// Initialize phase state at team-plan
	phaseState := &types.TeamPhaseState{
		Phase:        types.PhasePlan,
		Artifacts:    map[string]string{},
		Transitions:  []types.PhaseTransition{},
		FixLoopCount: 0,
	}
	phasePath := s.paths.PhaseStatePath(teamName)
	if err := s.WriteJSON(phasePath, phaseState); err != nil {
		return nil, fmt.Errorf("write phase state: %w", err)
	}

	return manifest, nil
}

// LoadManifest reads a team manifest from disk.
func (s *Store) LoadManifest(teamName string) (*types.TeamManifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.paths.ManifestPath(teamName)
	var manifest types.TeamManifest
	if err := s.ReadJSON(path, &manifest); err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}
	return &manifest, nil
}

// UpdateManifest reads, modifies, and writes back a team manifest atomically.
func (s *Store) UpdateManifest(teamName string, fn func(*types.TeamManifest) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.paths.ManifestPath(teamName)
	var manifest types.TeamManifest
	if err := s.ReadJSON(path, &manifest); err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}

	if err := fn(&manifest); err != nil {
		return fmt.Errorf("update manifest: %w", err)
	}

	manifest.UpdatedAt = time.Now()
	if err := s.WriteJSON(path, &manifest); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// DeleteTeam removes all state for a team.
func (s *Store) DeleteTeam(teamName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.paths.TeamDir(teamName)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove team dir %s: %w", dir, err)
	}
	return nil
}

// TeamExists checks whether a team's manifest file exists.
func (s *Store) TeamExists(teamName string) bool {
	path := s.paths.ManifestPath(teamName)
	_, err := os.Stat(path)
	return err == nil
}

// LoadPhaseState reads the current phase state from disk.
func (s *Store) LoadPhaseState(teamName string) (*types.TeamPhaseState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.paths.PhaseStatePath(teamName)
	var state types.TeamPhaseState
	if err := s.ReadJSON(path, &state); err != nil {
		return nil, fmt.Errorf("load phase state: %w", err)
	}
	return &state, nil
}

// SavePhaseState writes the phase state to disk.
func (s *Store) SavePhaseState(teamName string, state *types.TeamPhaseState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.paths.PhaseStatePath(teamName)
	if err := s.WriteJSON(path, state); err != nil {
		return fmt.Errorf("save phase state: %w", err)
	}
	return nil
}
