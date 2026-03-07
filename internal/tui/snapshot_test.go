package tui

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/junhoyeo/contrabass/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "update golden files")

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
var spinnerRegex = regexp.MustCompile(`[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏⣾⣽⣻⢿⡿⣟⣯⣷]`)

func stripANSI(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

func normalizeSpinner(s string) string {
	return spinnerRegex.ReplaceAllString(s, "●")
}

func goldenPath(name string) string {
	return filepath.Join("..", "..", "testdata", "snapshots", name+".txt")
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := goldenPath(name)
	if *update {
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(got), 0o644)
		return
	}
	expected, err := os.ReadFile(path)
	require.NoError(t, err, "golden file %s not found — run with -update", path)
	assert.Equal(t, string(expected), got)
}

func newSnapshotModel() Model {
	// Pin logo to a blank placeholder so snapshots don't depend on the PNG asset path.
	placeholder := strings.Repeat(" ", logoBoxCols)
	lines := make([]string, logoBoxRows)
	for i := range lines {
		lines[i] = placeholder
	}
	overrideLogoArt = strings.Join(lines, "\n")

	m := NewModel()
	m.width = 100
	m.height = 40
	m.header = m.header.SetWidth(100)
	m.table = m.table.SetWidth(100)
	m.backoff = m.backoff.SetWidth(100)
	m.help.SetWidth(100)
	m.viewport.SetWidth(100)
	headerH := lipgloss.Height(m.header.View())
	helpH := lipgloss.Height(m.help.View(m.keys))
	m.viewport.SetHeight(40 - headerH - helpH)
	return m
}

// applyEvent drives model state through the real Update() path
func applyEvent(m Model, event orchestrator.OrchestratorEvent) Model {
	msg := OrchestratorEventMsg{Event: event}
	updated, _ := m.Update(msg)
	return updated.(Model)
}

// refreshModel updates derived fields (Age, RetryIn, etc.)
func refreshModel(m Model, now time.Time) Model {
	updated, _ := m.Update(tickMsg(now))
	return updated.(Model)
}

func TestSnapshotIdle(t *testing.T) {
	m := newSnapshotModel()
	// Refresh to sync tables and header
	m = refreshModel(m, time.Now())
	rendered := normalizeSpinner(stripANSI(m.View().Content))
	assertGolden(t, "idle", rendered)
}

func TestSnapshotSingleAgent(t *testing.T) {
	m := newSnapshotModel()
	now := time.Now()

	// Apply AgentStarted event
	m = applyEvent(m, orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventAgentStarted,
		IssueID:   "ISSUE-1",
		Timestamp: now,
		Data: orchestrator.AgentStarted{
			Attempt:   1,
			PID:       12345,
			SessionID: "sess-abc123",
		},
	})

	// Apply StatusUpdate event
	m = applyEvent(m, orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventStatusUpdate,
		IssueID:   "ISSUE-1",
		Timestamp: now.Add(2*time.Minute + 30*time.Second),
		Data: orchestrator.StatusUpdate{
			Stats: orchestrator.Stats{
				Running:        1,
				MaxAgents:      8,
				TotalTokensIn:  5000,
				TotalTokensOut: 2000,
				StartTime:      now,
			},
		},
	})

	// Refresh to update derived fields (Age, etc.)
	m = refreshModel(m, now.Add(2*time.Minute+30*time.Second))

	rendered := normalizeSpinner(stripANSI(m.View().Content))
	assertGolden(t, "single_agent", rendered)
}

func TestSnapshotMultipleAgents(t *testing.T) {
	m := newSnapshotModel()
	now := time.Now()

	// Apply AgentStarted for ISSUE-1
	m = applyEvent(m, orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventAgentStarted,
		IssueID:   "ISSUE-1",
		Timestamp: now,
		Data: orchestrator.AgentStarted{
			Attempt:   1,
			PID:       10001,
			SessionID: "sess-aaa111",
		},
	})

	// Apply AgentStarted for ISSUE-2
	m = applyEvent(m, orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventAgentStarted,
		IssueID:   "ISSUE-2",
		Timestamp: now.Add(10 * time.Second),
		Data: orchestrator.AgentStarted{
			Attempt:   2,
			PID:       10002,
			SessionID: "sess-bbb222",
		},
	})

	// Apply AgentStarted for ISSUE-3
	m = applyEvent(m, orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventAgentStarted,
		IssueID:   "ISSUE-3",
		Timestamp: now.Add(10 * time.Second),
		Data: orchestrator.AgentStarted{
			Attempt:   1,
			PID:       10003,
			SessionID: "sess-ccc333",
		},
	})

	// Apply StatusUpdate with all stats
	m = applyEvent(m, orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventStatusUpdate,
		IssueID:   "ISSUE-1",
		Timestamp: now.Add(1*time.Minute + 45*time.Second),
		Data: orchestrator.StatusUpdate{
			Stats: orchestrator.Stats{
				Running:        3,
				MaxAgents:      8,
				TotalTokensIn:  23100,
				TotalTokensOut: 9550,
				StartTime:      now,
			},
		},
	})

	// Refresh to update derived fields
	m = refreshModel(m, now.Add(5*time.Minute))

	rendered := normalizeSpinner(stripANSI(m.View().Content))
	assertGolden(t, "multiple_agents", rendered)
}

func TestSnapshotBackoffQueue(t *testing.T) {
	m := newSnapshotModel()
	now := time.Now()

	// Apply BackoffEnqueued for ISSUE-5
	m = applyEvent(m, orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventBackoffEnqueued,
		IssueID:   "ISSUE-5",
		Timestamp: now,
		Data: orchestrator.BackoffEnqueued{
			Attempt: 2,
			RetryAt: now.Add(15 * time.Second),
			Error:   "rate limit exceeded",
		},
	})

	// Apply BackoffEnqueued for ISSUE-7
	m = applyEvent(m, orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventBackoffEnqueued,
		IssueID:   "ISSUE-7",
		Timestamp: now,
		Data: orchestrator.BackoffEnqueued{
			Attempt: 4,
			RetryAt: now.Add(45 * time.Second),
			Error:   "server overload (-32001)",
		},
	})

	// Refresh to update derived fields
	m = refreshModel(m, now)

	rendered := normalizeSpinner(stripANSI(m.View().Content))
	assertGolden(t, "backoff_queue", rendered)
}

func TestSnapshotMixed(t *testing.T) {
	m := newSnapshotModel()
	now := time.Now()

	// Apply AgentStarted for ISSUE-1
	m = applyEvent(m, orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventAgentStarted,
		IssueID:   "ISSUE-1",
		Timestamp: now,
		Data: orchestrator.AgentStarted{
			Attempt:   1,
			PID:       20001,
			SessionID: "sess-mix111",
		},
	})

	// Apply AgentStarted for ISSUE-2
	m = applyEvent(m, orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventAgentStarted,
		IssueID:   "ISSUE-2",
		Timestamp: now.Add(5 * time.Second),
		Data: orchestrator.AgentStarted{
			Attempt:   3,
			PID:       20002,
			SessionID: "sess-mix222",
		},
	})

	// Apply BackoffEnqueued for ISSUE-3
	m = applyEvent(m, orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventBackoffEnqueued,
		IssueID:   "ISSUE-3",
		Timestamp: now.Add(5 * time.Second),
		Data: orchestrator.BackoffEnqueued{
			Attempt: 2,
			RetryAt: now.Add(35 * time.Second),
			Error:   "context deadline exceeded",
		},
	})

	// Apply StatusUpdate
	m = applyEvent(m, orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventStatusUpdate,
		IssueID:   "ISSUE-1",
		Timestamp: now.Add(3*time.Minute + 15*time.Second),
		Data: orchestrator.StatusUpdate{
			Stats: orchestrator.Stats{
				Running:        2,
				MaxAgents:      8,
				TotalTokensIn:  12000,
				TotalTokensOut: 4500,
				StartTime:      now,
			},
		},
	})

	// Refresh to update derived fields
	m = refreshModel(m, now.Add(3*time.Minute+15*time.Second))

	rendered := normalizeSpinner(stripANSI(m.View().Content))
	assertGolden(t, "mixed", rendered)
}
