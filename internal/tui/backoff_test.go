package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBackoffEmpty(t *testing.T) {
	b := NewBackoff()
	assert.Equal(t, "", b.View())
}

func TestBackoffWithRows(t *testing.T) {
	rows := []BackoffRow{
		{IssueID: "ISSUE-789", Attempt: 3, RetryIn: "45s", Error: "agent timeout"},
		{IssueID: "ISSUE-012", Attempt: 1, RetryIn: "10s", Error: "rate limited"},
	}
	b := NewBackoff().Update(rows)
	out := b.View()
	assert.Contains(t, out, "ISSUE-789")
	assert.Contains(t, out, "ISSUE-012")
	assert.Contains(t, out, "attempt 3")
	assert.Contains(t, out, "retry in")
}

func TestBackoffContainsError(t *testing.T) {
	rows := []BackoffRow{
		{IssueID: "ERR-1", Attempt: 2, RetryIn: "30s", Error: "server overload"},
	}
	b := NewBackoff().Update(rows)
	out := b.View()
	assert.Contains(t, out, "server overload")
}

func TestBackoffSetWidth(t *testing.T) {
	b := NewBackoff().SetWidth(80)
	rows := []BackoffRow{{IssueID: "W-1", Attempt: 1, RetryIn: "5s", Error: "err"}}
	b = b.Update(rows)
	out := b.View()
	assert.Contains(t, out, "W-1")
}
