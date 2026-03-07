package tui

import (
	"fmt"

	"charm.land/lipgloss/v2"
	ltable "charm.land/lipgloss/v2/table"
)

// TeamRow holds display data for one team in the team table.
type TeamRow struct {
	TeamName       string
	BoardIssueID   string
	Phase          string
	Workers        int
	ActiveWorkers  int
	Tasks          int
	CompletedTasks int
	FailedTasks    int
	FixLoops       int
	Age            string
}

// TeamWorkerRow holds display data for one worker in a team.
type TeamWorkerRow struct {
	WorkerID    string
	Status      string
	CurrentTask string
	PID         int
	Age         string
}

// TeamTable renders a static team status table using lipgloss.
type TeamTable struct {
	width   int
	teams   []TeamRow
	workers map[string][]TeamWorkerRow
	spinner string
	rowBuf  *teamTableRowBuffer
}

type teamTableRowBuffer struct {
	rows [][]string
}

func NewTeamTable() TeamTable {
	return TeamTable{
		workers: make(map[string][]TeamWorkerRow),
		rowBuf:  &teamTableRowBuffer{},
	}
}

func (t TeamTable) Update(teams []TeamRow, workers map[string][]TeamWorkerRow, spinnerView string) TeamTable {
	t.teams = teams
	t.workers = workers
	t.spinner = spinnerView
	if t.rowBuf == nil {
		t.rowBuf = &teamTableRowBuffer{}
	}
	return t
}

func (t TeamTable) SetWidth(w int) TeamTable {
	t.width = w
	return t
}

func (t TeamTable) buildRows() ([][]string, map[int]int) {
	if t.rowBuf == nil {
		t.rowBuf = &teamTableRowBuffer{}
	}
	rows := t.rowBuf.rows[:0]
	teamRowIndex := make(map[int]int, len(t.teams))

	for teamIdx, team := range t.teams {
		// Team summary row
		glyph := teamStatusGlyph(team.Phase, t.spinner)
		tasksStr := fmt.Sprintf("%d/%d", team.CompletedTasks, team.Tasks)
		if team.FailedTasks > 0 {
			tasksStr = fmt.Sprintf("%d/%d (%d!)", team.CompletedTasks, team.Tasks, team.FailedTasks)
		}
		workersStr := fmt.Sprintf("%d/%d", team.ActiveWorkers, team.Workers)
		teamLabel := team.TeamName
		if team.BoardIssueID != "" {
			teamLabel = fmt.Sprintf("%s · %s", team.TeamName, team.BoardIssueID)
		}

		teamRowIndex[len(rows)] = teamIdx
		rows = append(rows, []string{
			glyph,
			teamLabel,
			compactTeamPhase(team.Phase),
			workersStr,
			tasksStr,
			fmt.Sprintf("%d", team.FixLoops),
			team.Age,
		})

		// Worker sub-rows
		if teamWorkers, ok := t.workers[team.TeamName]; ok {
			for i, w := range teamWorkers {
				isLast := i == len(teamWorkers)-1
				connector := "├─"
				if isLast {
					connector = "└─"
				}

				taskDisplay := w.CurrentTask
				if len(taskDisplay) > 20 {
					taskDisplay = taskDisplay[:17] + "..."
				}

				workerID := w.WorkerID
				if len(workerID) > 12 {
					workerID = workerID[:9] + "..."
				}

				rows = append(rows, []string{
					"",
					fmt.Sprintf("  %s %s", connector, workerID),
					w.Status,
					taskDisplay,
					fmt.Sprintf("%d", w.PID),
					w.Age,
					"",
				})
			}
		}
	}
	t.rowBuf.rows = rows

	return rows, teamRowIndex
}

func (t TeamTable) View() string {
	if len(t.teams) == 0 {
		return ""
	}

	rows, teamRowIndex := t.buildRows()

	if len(rows) == 0 {
		return lipgloss.NewStyle().Faint(true).Render("  No teams running")
	}

	tbl := ltable.New().
		Headers("", "TEAM", "PHASE", "WORKERS", "TASKS", "FIX", "AGE").
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

			bg := "234"
			if row%2 == 1 {
				bg = "235"
			}

			style := lipgloss.NewStyle().Padding(0, 1).Background(lipgloss.Color(bg))

			if teamIdx, isTeamRow := teamRowIndex[row]; isTeamRow {
				// Team row styling
				team := t.teams[teamIdx]
				phaseColor := teamPhaseColor(team.Phase)
				style = style.Foreground(lipgloss.Color(phaseColor))
				if isActiveTeamPhase(team.Phase) {
					style = style.Bold(true).Foreground(lipgloss.Color("255"))
				} else {
					style = style.Foreground(lipgloss.Color("250"))
				}
			} else {
				// Worker row styling
				style = style.Foreground(lipgloss.Color("250"))
			}

			switch col {
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

func compactTeamPhase(phase string) string {
	switch phase {
	case "team-plan":
		return "Plan"
	case "team-prd":
		return "PRD"
	case "team-exec":
		return "Exec"
	case "team-verify":
		return "Verify"
	case "team-fix":
		return "Fix"
	case "complete":
		return "Done"
	case "failed":
		return "Failed"
	case "cancelled":
		return "Cancel"
	default:
		if len(phase) > 8 {
			return phase[:8]
		}
		return phase
	}
}

func teamPhaseColor(phase string) string {
	switch phase {
	case "team-plan", "team-prd":
		return "33" // blue
	case "team-exec":
		return "5" // magenta
	case "team-verify":
		return "42" // green
	case "team-fix":
		return "3" // yellow
	case "complete":
		return "42" // green
	case "failed", "cancelled":
		return "1" // red
	default:
		return "7"
	}
}

func isActiveTeamPhase(phase string) bool {
	switch phase {
	case "team-plan", "team-prd", "team-exec", "team-verify", "team-fix":
		return true
	default:
		return false
	}
}

func teamStatusGlyph(phase string, spinnerView string) string {
	if isActiveTeamPhase(phase) {
		return spinnerView
	}
	return "●"
}
