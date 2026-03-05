package tui

import (
	"fmt"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	ltable "charm.land/lipgloss/v2/table"
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
	rows := make([][]string, 0, len(t.rows))
	for i, r := range t.rows {
		tok := fmt.Sprintf("%s/%s", formatTokensShort(r.TokensIn), formatTokensShort(r.TokensOut))
		sess := truncateSessionID(r.SessionID, 14)
		_ = i
		rows = append(rows, []string{
			statusGlyph(r.Phase, t.spinnerView),
			displayIssueID(r.IssueID),
			compactStage(r.Stage),
			fmt.Sprintf("%d", r.PID),
			r.Age,
			tok,
			sess,
			compactEvent(r.LastEvent),
		})
	}

	tbl := ltable.New().
		Headers("", "ID", "STAGE", "PID", "AGE", "TOKENS", "SESSION", "EVENT").
		Rows(rows...).
		Wrap(false).
		Border(lipgloss.NormalBorder()).
		BorderTop(false).
		BorderBottom(false).
		BorderLeft(false).
		BorderRight(false).
		BorderColumn(false).
		BorderRow(false).
		BorderHeader(true).
		BorderStyle(lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("240"))).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == ltable.HeaderRow {
				return lipgloss.NewStyle().Bold(true).Faint(true).Padding(0, 1)
			}
			phase := t.rows[row].Phase
			bg := "234"
			if row%2 == 1 {
				bg = "235"
			}
			style := lipgloss.NewStyle().Padding(0, 1).Background(lipgloss.Color(bg))
			if isActivePhase(phase) {
				style = style.Bold(true).Foreground(lipgloss.Color("255"))
			} else {
				style = style.Foreground(lipgloss.Color("250"))
			}

			switch col {
			case 0:
				return style.Foreground(lipgloss.Color(phaseColor(phase)))
			case 3, 4, 5:
				return style.Align(lipgloss.Right)
			default:
				return style
			}
		})

	if t.width > 2 {
		tbl.Width(t.width - 2)
	}

	return lipgloss.NewStyle().PaddingLeft(2).Render(tbl.String())
}

func isActivePhase(phase types.RunPhase) bool {
	switch phase {
	case types.InitializingSession,
		types.LaunchingAgentProcess,
		types.PreparingWorkspace,
		types.BuildingPrompt,
		types.StreamingTurn,
		types.Finishing:
		return true
	default:
		return false
	}
}

func phaseColor(phase types.RunPhase) string {
	switch phase {
	case types.StreamingTurn, types.Finishing, types.Succeeded:
		return "42"
	case types.InitializingSession, types.LaunchingAgentProcess:
		return "5"
	case types.PreparingWorkspace, types.BuildingPrompt:
		return "33"
	case types.Failed, types.TimedOut, types.Stalled:
		return "1"
	case types.CanceledByReconciliation:
		return "3"
	default:
		return "7"
	}
}

func statusGlyph(phase types.RunPhase, spinnerView string) string {
	if isActivePhase(phase) {
		return spinnerView
	}
	return "●"
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

var issueKeyPattern = regexp.MustCompile(`^[A-Z]+-[0-9]+$`)
var uuidPrefixPattern = regexp.MustCompile(`^[0-9a-f]{8}-`)

func displayIssueID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "-"
	}
	if issueKeyPattern.MatchString(id) {
		return id
	}
	if uuidPrefixPattern.MatchString(id) && len(id) >= 8 {
		return id[:8] + "..."
	}
	if len(id) > 12 {
		return id[:9] + "..."
	}
	return id
}

func compactStage(stage string) string {
	switch stage {
	case types.InitializingSession.String():
		return "Init"
	case types.LaunchingAgentProcess.String():
		return "Launch"
	case types.PreparingWorkspace.String():
		return "Prep"
	case types.BuildingPrompt.String():
		return "Prompt"
	case types.StreamingTurn.String():
		return "Turn"
	case types.Finishing.String():
		return "Finish"
	case types.Succeeded.String():
		return "Done"
	case types.Failed.String():
		return "Failed"
	case types.TimedOut.String():
		return "Timeout"
	case types.Stalled.String():
		return "Stalled"
	case types.CanceledByReconciliation.String():
		return "Cancel"
	default:
		if len(stage) > 8 {
			return stage[:8]
		}
		return stage
	}
}

func compactEvent(event string) string {
	if event == "" {
		return "-"
	}
	switch event {
	case "AgentStarted":
		return "session init"
	case "AgentFinished":
		return "run finished"
	case "turn/started":
		return "turn started"
	case "turn/completed":
		return "turn done"
	case "item/started":
		return "item start"
	case "item/completed":
		return "item done"
	case "codex/event/task_started":
		return "task started"
	case "codex/event/token_count", "thread/tokenUsage/updated":
		return "tokens updated"
	}
	if strings.HasPrefix(event, "codex/event/") {
		trimmed := strings.TrimPrefix(event, "codex/event/")
		trimmed = strings.ReplaceAll(trimmed, "_", " ")
		if len(trimmed) > 26 {
			return trimmed[:23] + "..."
		}
		return trimmed
	}
	if len(event) > 26 {
		return event[:23] + "..."
	}
	return event
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
