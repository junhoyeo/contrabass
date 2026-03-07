package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// BackoffRow holds display data for one retry-backoff entry.
type BackoffRow struct {
	IssueID string
	Attempt int
	RetryIn string
	Error   string
}

// Backoff renders a list of issues currently in retry backoff.
type Backoff struct {
	width int
	rows  []BackoffRow
}

func NewBackoff() Backoff                          { return Backoff{} }
func (b Backoff) Update(rows []BackoffRow) Backoff { b.rows = rows; return b }
func (b Backoff) SetWidth(w int) Backoff           { b.width = w; return b }

func (b Backoff) View() string {
	if len(b.rows) == 0 {
		return ""
	}
	arrow := lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render("↻")
	idStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	retryStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	dimStyle := lipgloss.NewStyle().Faint(true)
	maxWidth := b.width
	if maxWidth <= 0 {
		maxWidth = 120
	}
	var b2 strings.Builder
	for i, r := range b.rows {
		if i > 0 {
			b2.WriteByte('\n')
		}
		errMsg := r.Error
		prefix := fmt.Sprintf("  %s %s  attempt %d  retry in %s  error: ",
			arrow, idStyle.Render(r.IssueID), r.Attempt, retryStyle.Render(r.RetryIn))
		maxErr := maxWidth - lipgloss.Width(prefix)
		if maxErr < 0 {
			maxErr = 0
		}
		errMsg = truncateRunesWithEllipsis(errMsg, maxErr)
		line := prefix + dimStyle.Render(errMsg)
		b2.WriteString(line)
	}
	return b2.String()
}

func truncateRunesWithEllipsis(input string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(input)
	if len(runes) <= max {
		return input
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}
