package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/junhoyeo/contrabass/internal/types"
)

// AgentRow holds display data for one row in the agent table.
type AgentRow struct {
	IssueID   string
	Stage     string
	PID       int
	Age       string
	Turn      int
	TokensIn  int64
	TokensOut int64
	SessionID string
	LastEvent string
	Phase     types.RunPhase
}

// Table renders a static agent status table using lipgloss.
type Table struct {
	width       int
	rows        []AgentRow
	spinnerView string
}

func NewTable() Table { return Table{} }
func (t Table) Update(rows []AgentRow, spinnerView string) Table {
	t.rows = rows
	t.spinnerView = spinnerView
	return t
}
func (t Table) SetWidth(w int) Table { t.width = w; return t }

func (t Table) View() string {
	if len(t.rows) == 0 {
		return lipgloss.NewStyle().Faint(true).Render("  No agents running")
	}
	hdr := fmt.Sprintf("  %-12s %-18s %-7s %-7s %-14s %-16s %s",
		"ID", "STAGE", "PID", "AGE", "TOKENS", "SESSION", "EVENT")
	headerStyle := lipgloss.NewStyle().Bold(true).Faint(true)
	sepWidth := 90
	if t.width > 4 {
		sepWidth = t.width - 4
	}
	sep := "  " + strings.Repeat("─", sepWidth)

	var b strings.Builder
	b.WriteString(headerStyle.Render(hdr))
	b.WriteByte('\n')
	b.WriteString(lipgloss.NewStyle().Faint(true).Render(sep))
	for _, r := range t.rows {
		tok := fmt.Sprintf("%s/%s", formatTokensShort(r.TokensIn), formatTokensShort(r.TokensOut))
		sess := truncateSessionID(r.SessionID, 14)
		line := fmt.Sprintf("  %s %-12s %-18s %-7d %-7s %-14s %-16s %s",
			statusIndicator(r.Phase, t.spinnerView), r.IssueID, r.Stage, r.PID, r.Age, tok, sess, r.LastEvent)
		b.WriteByte('\n')
		b.WriteString(line)
	}
	return b.String()
}

func statusIndicator(phase types.RunPhase, spinnerView string) string {
	switch phase {
	case types.StreamingTurn, types.Finishing:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(spinnerView)
	case types.InitializingSession, types.LaunchingAgentProcess:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Render(spinnerView)
	case types.PreparingWorkspace, types.BuildingPrompt:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("33")).Render(spinnerView)
	case types.Failed, types.TimedOut, types.Stalled:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render("●")
	case types.CanceledByReconciliation:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render("●")
	case types.Succeeded:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("●")
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("7")).Render("●")
	}
}

func truncateSessionID(id string, maxLen int) string {
	if len(id) <= maxLen {
		return id
	}
	return id[:maxLen-3] + "..."
}

func formatTokensShort(n int64) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}
