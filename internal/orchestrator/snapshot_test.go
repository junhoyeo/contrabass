package orchestrator

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/types"
)

func TestSnapshot_ReturnsCorrectStats(t *testing.T) {
	o := &Orchestrator{
		running:    make(map[string]*runEntry),
		backoff:    []types.BackoffEntry{},
		issueCache: make(map[string]types.Issue),
		stats: Stats{
			Running:        5,
			MaxAgents:      10,
			TotalTokensIn:  1000,
			TotalTokensOut: 2000,
			StartTime:      time.Now().Add(-1 * time.Hour),
			PollCount:      42,
		},
	}

	snapshot := o.Snapshot()

	assert.Equal(t, 5, snapshot.Stats.Running)
	assert.Equal(t, 10, snapshot.Stats.MaxAgents)
	assert.Equal(t, int64(1000), snapshot.Stats.TotalTokensIn)
	assert.Equal(t, int64(2000), snapshot.Stats.TotalTokensOut)
	assert.Equal(t, 42, snapshot.Stats.PollCount)
	assert.NotZero(t, snapshot.GeneratedAt)
}

func TestSnapshot_IncludesRunningEntries(t *testing.T) {
	now := time.Now()
	issue := types.Issue{
		ID:    "issue-1",
		Title: "Test Issue",
	}
	attempt := types.RunAttempt{
		IssueID:   "issue-1",
		Attempt:   1,
		PID:       12345,
		SessionID: "session-abc",
		StartTime: now,
		Phase:     types.StreamingTurn,
		TokensIn:  100,
		TokensOut: 200,
	}
	entry := &runEntry{
		issue:     issue,
		attempt:   attempt,
		workspace: "/tmp/workspace",
	}

	o := &Orchestrator{
		running:    map[string]*runEntry{"issue-1": entry},
		backoff:    []types.BackoffEntry{},
		issueCache: make(map[string]types.Issue),
		stats:      Stats{},
	}

	snapshot := o.Snapshot()

	require.Len(t, snapshot.Running, 1)
	assert.Equal(t, "issue-1", snapshot.Running[0].IssueID)
	assert.Equal(t, 1, snapshot.Running[0].Attempt)
	assert.Equal(t, 12345, snapshot.Running[0].PID)
	assert.Equal(t, "session-abc", snapshot.Running[0].SessionID)
	assert.Equal(t, "/tmp/workspace", snapshot.Running[0].Workspace)
	assert.Equal(t, now, snapshot.Running[0].StartedAt)
	assert.Equal(t, types.StreamingTurn, snapshot.Running[0].Phase)
	assert.Equal(t, int64(100), snapshot.Running[0].TokensIn)
	assert.Equal(t, int64(200), snapshot.Running[0].TokensOut)
}

func TestSnapshot_IncludesBackoffEntries(t *testing.T) {
	retryTime := time.Now().Add(5 * time.Minute)
	backoffEntry := types.BackoffEntry{
		IssueID: "issue-2",
		Attempt: 2,
		RetryAt: retryTime,
		Error:   "timeout",
	}

	o := &Orchestrator{
		running:    make(map[string]*runEntry),
		backoff:    []types.BackoffEntry{backoffEntry},
		issueCache: make(map[string]types.Issue),
		stats:      Stats{},
	}

	snapshot := o.Snapshot()

	require.Len(t, snapshot.Backoff, 1)
	assert.Equal(t, "issue-2", snapshot.Backoff[0].IssueID)
	assert.Equal(t, 2, snapshot.Backoff[0].Attempt)
	assert.Equal(t, retryTime, snapshot.Backoff[0].RetryAt)
	assert.Equal(t, "timeout", snapshot.Backoff[0].Error)
}

func TestSnapshot_IsIsolatedFromState(t *testing.T) {
	issue := types.Issue{
		ID:    "issue-1",
		Title: "Original Title",
	}

	o := &Orchestrator{
		running:    make(map[string]*runEntry),
		backoff:    []types.BackoffEntry{},
		issueCache: map[string]types.Issue{"issue-1": issue},
		stats:      Stats{Running: 1},
	}

	snapshot := o.Snapshot()

	assert.Equal(t, "Original Title", snapshot.Issues["issue-1"].Title)
	assert.Equal(t, 1, snapshot.Stats.Running)

	o.mu.Lock()
	o.issueCache["issue-1"] = types.Issue{
		ID:    "issue-1",
		Title: "Modified Title",
	}
	o.stats.Running = 5
	o.mu.Unlock()

	assert.Equal(t, "Original Title", snapshot.Issues["issue-1"].Title)
	assert.Equal(t, 1, snapshot.Stats.Running)
}
