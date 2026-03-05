package tui

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/junhoyeo/symphony-charm/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "update golden files")

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
var spinnerRegex = regexp.MustCompile(`[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]`)

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

func syncModel(m Model) Model {
	m.table = m.table.Update(agentRowsSorted(m.agents), m.spinner.View())
	m.backoff = m.backoff.Update(backoffRowsSorted(m.backoffs))
	m.header = m.header.Update(m.stats)
	content := m.table.View()
	if bv := m.backoff.View(); bv != "" {
		content += "\n" + bv
	}
	m.viewport.SetContent(content)
	return m
}

func TestSnapshotIdle(t *testing.T) {
	m := syncModel(newSnapshotModel())
	rendered := normalizeSpinner(stripANSI(m.View().Content))
	assertGolden(t, "idle", rendered)
}

func TestSnapshotSingleAgent(t *testing.T) {
	m := newSnapshotModel()
	m.agents["ISSUE-1"] = AgentRow{
		IssueID:   "ISSUE-1",
		Stage:     "StreamingTurn",
		PID:       12345,
		Age:       "2m 30s",
		Turn:      1,
		TokensIn:  5000,
		TokensOut: 2000,
		SessionID: "sess-abc123",
		LastEvent: "AgentStarted",
		Phase:     types.StreamingTurn,
	}
	m = syncModel(m)
	rendered := normalizeSpinner(stripANSI(m.View().Content))
	assertGolden(t, "single_agent", rendered)
}

func TestSnapshotMultipleAgents(t *testing.T) {
	m := newSnapshotModel()
	m.agents["ISSUE-1"] = AgentRow{
		IssueID:   "ISSUE-1",
		Stage:     "InitializingSession",
		PID:       10001,
		Age:       "10s",
		Turn:      1,
		TokensIn:  100,
		TokensOut: 50,
		SessionID: "sess-aaa111",
		LastEvent: "AgentStarted",
		Phase:     types.InitializingSession,
	}
	m.agents["ISSUE-2"] = AgentRow{
		IssueID:   "ISSUE-2",
		Stage:     "StreamingTurn",
		PID:       10002,
		Age:       "1m 45s",
		Turn:      2,
		TokensIn:  8000,
		TokensOut: 3500,
		SessionID: "sess-bbb222",
		LastEvent: "StatusUpdate",
		Phase:     types.StreamingTurn,
	}
	m.agents["ISSUE-3"] = AgentRow{
		IssueID:   "ISSUE-3",
		Stage:     "Finishing",
		PID:       10003,
		Age:       "5m 0s",
		Turn:      1,
		TokensIn:  15000,
		TokensOut: 6000,
		SessionID: "sess-ccc333",
		LastEvent: "AgentFinished",
		Phase:     types.Finishing,
	}
	m.stats = HeaderData{
		RunningAgents: 3,
		MaxAgents:     8,
		TokensIn:      23100,
		TokensOut:     9550,
		TokensTotal:   32650,
		RefreshIn:     1,
	}
	m = syncModel(m)
	rendered := normalizeSpinner(stripANSI(m.View().Content))
	assertGolden(t, "multiple_agents", rendered)
}

func TestSnapshotBackoffQueue(t *testing.T) {
	m := newSnapshotModel()
	m.backoffs["ISSUE-5"] = BackoffRow{
		IssueID: "ISSUE-5",
		Attempt: 2,
		RetryIn: "15s",
		Error:   "rate limit exceeded",
	}
	m.backoffs["ISSUE-7"] = BackoffRow{
		IssueID: "ISSUE-7",
		Attempt: 4,
		RetryIn: "45s",
		Error:   "server overload (-32001)",
	}
	m = syncModel(m)
	rendered := normalizeSpinner(stripANSI(m.View().Content))
	assertGolden(t, "backoff_queue", rendered)
}

func TestSnapshotMixed(t *testing.T) {
	m := newSnapshotModel()
	m.agents["ISSUE-1"] = AgentRow{
		IssueID:   "ISSUE-1",
		Stage:     "StreamingTurn",
		PID:       20001,
		Age:       "3m 15s",
		Turn:      1,
		TokensIn:  12000,
		TokensOut: 4500,
		SessionID: "sess-mix111",
		LastEvent: "StatusUpdate",
		Phase:     types.StreamingTurn,
	}
	m.agents["ISSUE-2"] = AgentRow{
		IssueID:   "ISSUE-2",
		Stage:     "InitializingSession",
		PID:       20002,
		Age:       "5s",
		Turn:      3,
		TokensIn:  0,
		TokensOut: 0,
		SessionID: "sess-mix222",
		LastEvent: "AgentStarted",
		Phase:     types.InitializingSession,
	}
	m.backoffs["ISSUE-3"] = BackoffRow{
		IssueID: "ISSUE-3",
		Attempt: 2,
		RetryIn: "30s",
		Error:   "context deadline exceeded",
	}
	m.stats = HeaderData{
		RunningAgents: 2,
		MaxAgents:     8,
		TokensIn:      12000,
		TokensOut:     4500,
		TokensTotal:   16500,
		RefreshIn:     1,
	}
	m = syncModel(m)
	rendered := normalizeSpinner(stripANSI(m.View().Content))
	assertGolden(t, "mixed", rendered)
}
