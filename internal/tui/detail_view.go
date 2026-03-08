package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

type DetailView struct {
	width int
}

func NewDetailView() DetailView {
	return DetailView{}
}

func (d DetailView) SetWidth(w int) DetailView {
	d.width = w
	return d
}

func (d DetailView) RenderAgent(row AgentRow, events []EventLogEntry) string {
	w := d.width
	if w <= 0 {
		w = 80
	}

	labelStyle := lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("244"))
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("45"))
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	dimStyle := lipgloss.NewStyle().Faint(true)
	timeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	tok := fmt.Sprintf("%s/%s", formatTokensShort(row.TokensIn), formatTokensShort(row.TokensOut))

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("  AGENT  %s", displayIssueID(row.IssueID))))
	b.WriteByte('\n')
	b.WriteString(fmt.Sprintf("  %s %s    %s %s    %s %s",
		labelStyle.Render("Stage:"), valueStyle.Render(compactStage(row.Stage)),
		labelStyle.Render("PID:"), valueStyle.Render(fmt.Sprintf("%d", row.PID)),
		labelStyle.Render("Age:"), valueStyle.Render(row.Age),
	))
	b.WriteByte('\n')
	b.WriteString(fmt.Sprintf("  %s %s    %s %s    %s %s",
		labelStyle.Render("Tokens:"), valueStyle.Render(tok),
		labelStyle.Render("Turn:"), valueStyle.Render(fmt.Sprintf("%d", row.Turn)),
		labelStyle.Render("Session:"), valueStyle.Render(row.SessionID),
	))
	b.WriteByte('\n')

	separator := dimStyle.Render(strings.Repeat("─", w-4))
	b.WriteString("  " + separator)
	b.WriteByte('\n')
	b.WriteString(titleStyle.Render("  EVENT LOG"))
	b.WriteByte('\n')

	if len(events) == 0 {
		b.WriteString(dimStyle.Render("  No events recorded"))
	} else {
		for _, e := range events {
			ts := e.Timestamp.Format("15:04:05")
			line := fmt.Sprintf("  %s  %s",
				timeStyle.Render(ts),
				valueStyle.Render(compactEvent(e.Type)),
			)
			if e.Detail != "" {
				line += "  " + dimStyle.Render(e.Detail)
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}

	return b.String()
}

func (d DetailView) RenderTeam(row TeamRow, workers []TeamWorkerRow, events []EventLogEntry) string {
	w := d.width
	if w <= 0 {
		w = 80
	}

	labelStyle := lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("244"))
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("45"))
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	dimStyle := lipgloss.NewStyle().Faint(true)
	workerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("5"))

	tasksStr := fmt.Sprintf("%d/%d", row.CompletedTasks, row.Tasks)
	if row.FailedTasks > 0 {
		tasksStr += fmt.Sprintf(" (%d failed)", row.FailedTasks)
	}

	var b strings.Builder
	teamLabel := row.TeamName
	if row.BoardIssueID != "" {
		teamLabel = fmt.Sprintf("%s · %s", row.TeamName, row.BoardIssueID)
	}
	b.WriteString(titleStyle.Render(fmt.Sprintf("  TEAM  %s", teamLabel)))
	b.WriteByte('\n')
	b.WriteString(fmt.Sprintf("  %s %s    %s %s    %s %s",
		labelStyle.Render("Phase:"), valueStyle.Render(compactTeamPhase(row.Phase)),
		labelStyle.Render("Workers:"), valueStyle.Render(fmt.Sprintf("%d/%d", row.ActiveWorkers, row.Workers)),
		labelStyle.Render("Tasks:"), valueStyle.Render(tasksStr),
	))
	b.WriteByte('\n')
	b.WriteString(fmt.Sprintf("  %s %s    %s %s",
		labelStyle.Render("Fix Loops:"), valueStyle.Render(fmt.Sprintf("%d", row.FixLoops)),
		labelStyle.Render("Age:"), valueStyle.Render(row.Age),
	))
	b.WriteByte('\n')

	separator := dimStyle.Render(strings.Repeat("─", w-4))
	b.WriteString("  " + separator)
	b.WriteByte('\n')
	b.WriteString(titleStyle.Render("  WORKERS"))
	b.WriteByte('\n')

	if len(workers) == 0 {
		b.WriteString(dimStyle.Render("  No workers"))
		b.WriteByte('\n')
	} else {
		for _, wr := range workers {
			status := wr.Status
			task := wr.CurrentTask
			if task == "" {
				task = "-"
			}
			if len(task) > 40 {
				task = task[:37] + "..."
			}
			b.WriteString(fmt.Sprintf("  %s  %s %s  %s %s  %s %s",
				workerStyle.Render(wr.WorkerID),
				labelStyle.Render("status:"), valueStyle.Render(status),
				labelStyle.Render("task:"), valueStyle.Render(task),
				labelStyle.Render("pid:"), valueStyle.Render(fmt.Sprintf("%d", wr.PID)),
			))
			b.WriteByte('\n')
		}
	}

	b.WriteString("  " + separator)
	b.WriteByte('\n')
	b.WriteString(titleStyle.Render("  EVENT LOG"))
	b.WriteByte('\n')

	if len(events) == 0 {
		b.WriteString(dimStyle.Render("  No events recorded"))
	} else {
		timeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		for _, e := range events {
			ts := e.Timestamp.Format("15:04:05")
			line := fmt.Sprintf("  %s  %s",
				timeStyle.Render(ts),
				valueStyle.Render(compactTeamEvent(e.Type)),
			)
			if e.Detail != "" {
				line += "  " + dimStyle.Render(e.Detail)
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}

	return b.String()
}

func compactTeamEvent(event string) string {
	switch event {
	case "team_created":
		return "created"
	case "pipeline_started":
		return "pipeline start"
	case "pipeline_completed":
		return "pipeline done"
	case "phase_started":
		return "phase start"
	case "task_claimed":
		return "task claimed"
	case "task_completed":
		return "task done"
	case "task_failed":
		return "task failed"
	default:
		if len(event) > 20 {
			return event[:17] + "..."
		}
		return event
	}
}
