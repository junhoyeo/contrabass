package team

import "path/filepath"

// Paths provides filesystem path resolution for team state directories.
type Paths struct {
	baseDir string
}

// NewPaths creates a Paths resolver rooted at the given base directory.
// The base directory is typically config.TeamStateDir() (default: ".contrabass/state/team").
func NewPaths(baseDir string) *Paths {
	return &Paths{baseDir: baseDir}
}

// TeamDir returns the root directory for a specific team's state.
func (p *Paths) TeamDir(teamName string) string {
	return filepath.Join(p.baseDir, teamName)
}

// ManifestPath returns the path to the team manifest JSON file.
func (p *Paths) ManifestPath(teamName string) string {
	return filepath.Join(p.TeamDir(teamName), "manifest.json")
}

// TasksDir returns the directory containing individual task JSON files.
func (p *Paths) TasksDir(teamName string) string {
	return filepath.Join(p.TeamDir(teamName), "tasks")
}

// TaskPath returns the path to a specific task's JSON file.
func (p *Paths) TaskPath(teamName, taskID string) string {
	return filepath.Join(p.TasksDir(teamName), taskID+".json")
}

// WorkersDir returns the directory containing worker state files.
func (p *Paths) WorkersDir(teamName string) string {
	return filepath.Join(p.TeamDir(teamName), "workers")
}

// WorkerPath returns the path to a specific worker's state file.
func (p *Paths) WorkerPath(teamName, workerID string) string {
	return filepath.Join(p.WorkersDir(teamName), workerID+".json")
}

// MailboxDir returns the directory containing mailbox message files.
func (p *Paths) MailboxDir(teamName string) string {
	return filepath.Join(p.TeamDir(teamName), "mailbox")
}

// WorkerMailboxDir returns the mailbox directory for a specific worker.
func (p *Paths) WorkerMailboxDir(teamName, workerID string) string {
	return filepath.Join(p.MailboxDir(teamName), workerID)
}

// PhaseStatePath returns the path to the team phase state file.
func (p *Paths) PhaseStatePath(teamName string) string {
	return filepath.Join(p.TeamDir(teamName), "phase-state.json")
}

// OwnershipPath returns the path to the file ownership registry.
func (p *Paths) OwnershipPath(teamName string) string {
	return filepath.Join(p.TeamDir(teamName), "ownership.json")
}

// EventsDir returns the directory for team event logs.
func (p *Paths) EventsDir(teamName string) string {
	return filepath.Join(p.TeamDir(teamName), "events")
}
