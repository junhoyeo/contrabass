package orchestrator

import (
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

// StateSnapshot represents a thread-safe point-in-time copy of orchestrator state.
type StateSnapshot struct {
	Stats       Stats                  `json:"stats"`
	Running     []RunningEntry         `json:"running"`
	Backoff     []types.BackoffEntry   `json:"backoff"`
	Issues      map[string]types.Issue `json:"issues"`
	GeneratedAt time.Time              `json:"generated_at"`
}

// RunningEntry represents a running issue execution in the snapshot.
type RunningEntry struct {
	IssueID   string         `json:"issue_id"`
	Attempt   int            `json:"attempt"`
	PID       int            `json:"pid"`
	SessionID string         `json:"session_id"`
	Workspace string         `json:"workspace"`
	StartedAt time.Time      `json:"started_at"`
	Phase     types.RunPhase `json:"phase"`
	TokensIn  int64          `json:"tokens_in"`
	TokensOut int64          `json:"tokens_out"`
}

// Snapshot returns a thread-safe point-in-time copy of orchestrator state.
// The returned snapshot is isolated from the orchestrator's internal state,
// so mutations to the orchestrator after Snapshot() returns do not affect
// the returned snapshot.
func (o *Orchestrator) Snapshot() StateSnapshot {
	o.mu.Lock()

	// Copy stats (value type)
	statsCopy := o.stats

	// Build running entries from running map
	runningEntries := make([]RunningEntry, 0, len(o.running))
	for _, entry := range o.running {
		runningEntries = append(runningEntries, RunningEntry{
			IssueID:   entry.issue.ID,
			Attempt:   entry.attempt.Attempt,
			PID:       entry.attempt.PID,
			SessionID: entry.attempt.SessionID,
			Workspace: entry.workspace,
			StartedAt: entry.attempt.StartTime,
			Phase:     entry.attempt.Phase,
			TokensIn:  entry.attempt.TokensIn,
			TokensOut: entry.attempt.TokensOut,
		})
	}

	// Copy backoff slice
	backoffCopy := make([]types.BackoffEntry, len(o.backoff))
	copy(backoffCopy, o.backoff)

	// Deep-copy issue cache map
	issuesCopy := make(map[string]types.Issue, len(o.issueCache))
	for id, issue := range o.issueCache {
		issuesCopy[id] = issue
	}

	o.mu.Unlock()

	return StateSnapshot{
		Stats:       statsCopy,
		Running:     runningEntries,
		Backoff:     backoffCopy,
		Issues:      issuesCopy,
		GeneratedAt: time.Now(),
	}
}
