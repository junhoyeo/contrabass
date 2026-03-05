package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/junhoyeo/symphony-charm/internal/orchestrator"
	"github.com/junhoyeo/symphony-charm/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestModelInit(t *testing.T) {
	m := NewModel()
	assert.NotNil(t, m.Init())
}

func TestModelQuit(t *testing.T) {
	m := NewModel()
	updated, cmd := m.Update(tea.KeyPressMsg{Text: "q", Code: 'q'})
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
	model := updated.(Model)
	assert.True(t, model.quitting)
}

func TestModelCtrlCQuit(t *testing.T) {
	m := NewModel()
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
	model := updated.(Model)
	assert.True(t, model.quitting)
}

func TestModelWindowResize(t *testing.T) {
	m := NewModel()
	updated, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	assert.Nil(t, cmd)
	model := updated.(Model)
	assert.Equal(t, 120, model.width)
	assert.Equal(t, 40, model.height)
	assert.Equal(t, 120, model.header.width)
	assert.Equal(t, 120, model.table.width)
	assert.Equal(t, 120, model.backoff.width)
}

func TestModelTickReturnsCmd(t *testing.T) {
	m := NewModel()
	updated, cmd := m.Update(tickMsg(time.Now()))
	require.NotNil(t, cmd)
	assert.IsType(t, tickMsg{}, cmd())
	_ = updated
}

func TestModelStatusUpdate(t *testing.T) {
	m := NewModel()
	start := time.Now().Add(-5 * time.Second)
	event := orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventStatusUpdate,
		Timestamp: time.Now(),
		Data: orchestrator.StatusUpdate{Stats: orchestrator.Stats{
			Running:        2,
			MaxAgents:      8,
			TotalTokensIn:  120,
			TotalTokensOut: 80,
			StartTime:      start,
		}},
	}

	updated, _ := m.Update(OrchestratorEventMsg{Event: event})
	model := updated.(Model)
	assert.Equal(t, int64(120), model.stats.TokensIn)
	assert.Equal(t, int64(80), model.stats.TokensOut)
	assert.Equal(t, int64(200), model.stats.TokensTotal)
	assert.Equal(t, 2, model.stats.RunningAgents)
	assert.Equal(t, 8, model.stats.MaxAgents)
	assert.GreaterOrEqual(t, model.stats.RuntimeSeconds, 5)
	assert.Equal(t, model.stats, model.header.data)
}

func TestModelAgentStarted(t *testing.T) {
	m := NewModel()
	event := orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventAgentStarted,
		IssueID:   "ISSUE-1",
		Timestamp: time.Now(),
		Data: orchestrator.AgentStarted{
			Attempt:   2,
			PID:       321,
			SessionID: "sess-1",
		},
	}

	updated, _ := m.Update(OrchestratorEventMsg{Event: event})
	model := updated.(Model)
	row, ok := model.agents["ISSUE-1"]
	require.True(t, ok)
	assert.Equal(t, 321, row.PID)
	assert.Equal(t, 2, row.Turn)
	assert.Equal(t, "sess-1", row.SessionID)
	assert.Equal(t, types.InitializingSession, row.Phase)
	assert.Len(t, model.table.rows, 1)
}

func TestModelAgentFinished(t *testing.T) {
	m := NewModel()
	startEvent := orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventAgentStarted,
		IssueID:   "ISSUE-1",
		Timestamp: time.Now(),
		Data:      orchestrator.AgentStarted{Attempt: 1, PID: 321, SessionID: "sess-1"},
	}
	updated, _ := m.Update(OrchestratorEventMsg{Event: startEvent})

	finishEvent := orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventAgentFinished,
		IssueID:   "ISSUE-1",
		Timestamp: time.Now(),
		Data: orchestrator.AgentFinished{
			Attempt:   1,
			Phase:     types.Succeeded,
			TokensIn:  100,
			TokensOut: 40,
		},
	}

	updated, _ = updated.(Model).Update(OrchestratorEventMsg{Event: finishEvent})
	model := updated.(Model)
	_, exists := model.agents["ISSUE-1"]
	assert.False(t, exists)
	assert.Len(t, model.table.rows, 0)
}

func TestModelBackoffEnqueued(t *testing.T) {
	m := NewModel()
	now := time.Now()
	event := orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventBackoffEnqueued,
		IssueID:   "ISSUE-9",
		Timestamp: now,
		Data: orchestrator.BackoffEnqueued{
			Attempt: 3,
			RetryAt: now.Add(20 * time.Second),
			Error:   "retry later",
		},
	}

	updated, _ := m.Update(OrchestratorEventMsg{Event: event})
	model := updated.(Model)
	row, ok := model.backoffs["ISSUE-9"]
	require.True(t, ok)
	assert.Equal(t, 3, row.Attempt)
	assert.Equal(t, "retry later", row.Error)
	assert.Equal(t, "20s", row.RetryIn)
	assert.Len(t, model.backoff.rows, 1)
}

func TestModelViewComposition(t *testing.T) {
	m := NewModel()
	now := time.Now()
	updated, _ := m.Update(OrchestratorEventMsg{Event: orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventBackoffEnqueued,
		IssueID:   "ISSUE-2",
		Timestamp: now,
		Data:      orchestrator.BackoffEnqueued{Attempt: 1, RetryAt: now.Add(10 * time.Second), Error: "slow"},
	}})

	view := updated.(Model).View().Content
	assert.Contains(t, view, "SYMPHONY STATUS")
	assert.Contains(t, view, "No agents running")
	assert.Contains(t, view, "ISSUE-2")
}

// TestModel_UnknownEventTypeHandled verifies that unknown tea.Msg types
// and unknown orchestrator event types increment the unknownEvents counter.
func TestModel_UnknownEventTypeHandled(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.Msg
		want int
	}{
		{
			name: "unknown tea.Msg type increments counter",
			msg:  struct{ tea.Msg }{},
			want: 1,
		},
		{
			name: "unknown orchestrator event type increments counter",
			msg: OrchestratorEventMsg{Event: orchestrator.OrchestratorEvent{
				Type:    orchestrator.EventType(999),
				IssueID: "ISSUE-X",
			}},
			want: 1,
		},
		{
			name: "bad type assertion on AgentStarted data increments zero",
			msg: OrchestratorEventMsg{Event: orchestrator.OrchestratorEvent{
				Type:    orchestrator.EventAgentStarted,
				IssueID: "ISSUE-Y",
				Data:    orchestrator.IssueReleased{Attempt: 1},
			}},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewModel()
			updated, _ := m.Update(tt.msg)
			model := updated.(Model)
			assert.Equal(t, tt.want, model.unknownEvents)
		})
	}
}

// TestTableView_NarrowWidthNoOverflow verifies that the table separator
// respects a narrow SetWidth and doesn't overflow.
func TestTableView_NarrowWidthNoOverflow(t *testing.T) {
	tests := []struct {
		name     string
		width    int
		expected int // expected separator rune count
	}{
		{
			name:     "narrow 40-char terminal",
			width:    40,
			expected: 36, // 40 - 4 (indent)
		},
		{
			name:     "standard 80-char terminal",
			width:    80,
			expected: 76, // 80 - 4
		},
		{
			name:     "zero width uses default 90",
			width:    0,
			expected: 90,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := []AgentRow{{
				IssueID: "X-1",
				Stage:   "StreamingTurn",
				PID:     1234,
				Age:     "10s",
				Phase:   types.StreamingTurn,
			}}
			tbl := NewTable().SetWidth(tt.width).Update(rows)
			out := stripANSI(tbl.View())

			// The output should contain the separator line.
			assert.Contains(t, out, strings.Repeat("\u2500", tt.expected))
			// But not a longer separator (unless default).
			if tt.width > 4 {
				assert.NotContains(t, out, strings.Repeat("\u2500", tt.expected+1))
			}
		})
	}
}

// TestModel_StatusUpdatePopulatesHeaderModelProject verifies that ModelName
// and ProjectURL from StatusUpdate events are mapped to HeaderData.
func TestModel_StatusUpdatePopulatesHeaderModelProject(t *testing.T) {
	tests := []struct {
		name       string
		modelName  string
		projectURL string
	}{
		{
			name:       "both fields populated",
			modelName:  "gpt-4o",
			projectURL: "https://github.com/example/project",
		},
		{
			name:       "only model name",
			modelName:  "claude-3",
			projectURL: "",
		},
		{
			name:       "only project URL",
			modelName:  "",
			projectURL: "https://github.com/example/other",
		},
		{
			name:       "empty fields preserve existing values",
			modelName:  "",
			projectURL: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewModel()
			// Pre-set values to verify empty strings don't overwrite.
			m.stats.ModelName = "existing-model"
			m.stats.ProjectURL = "https://existing.url"

			event := orchestrator.OrchestratorEvent{
				Type:      orchestrator.EventStatusUpdate,
				Timestamp: time.Now(),
				Data: orchestrator.StatusUpdate{
					Stats:      orchestrator.Stats{Running: 1, MaxAgents: 4},
					ModelName:  tt.modelName,
					ProjectURL: tt.projectURL,
				},
			}

			updated, _ := m.Update(OrchestratorEventMsg{Event: event})
			model := updated.(Model)

			if tt.modelName != "" {
				assert.Equal(t, tt.modelName, model.stats.ModelName)
			} else {
				assert.Equal(t, "existing-model", model.stats.ModelName,
					"empty ModelName should not overwrite existing value")
			}

			if tt.projectURL != "" {
				assert.Equal(t, tt.projectURL, model.stats.ProjectURL)
			} else {
				assert.Equal(t, "https://existing.url", model.stats.ProjectURL,
					"empty ProjectURL should not overwrite existing value")
			}

			// Verify header data is synced.
			assert.Equal(t, model.stats.ModelName, model.header.data.ModelName)
			assert.Equal(t, model.stats.ProjectURL, model.header.data.ProjectURL)

			// Verify other stats still mapped correctly.
			assert.Equal(t, 1, model.stats.RunningAgents)
			assert.Equal(t, 4, model.stats.MaxAgents)
		})
	}
}

// TestStartEventBridge_NilProgramNoOp verifies that nil program is handled gracefully.
func TestStartEventBridge_NilProgramNoOp(t *testing.T) {
	ctx := context.Background()
	events := make(chan orchestrator.OrchestratorEvent)

	// Should return immediately without starting goroutine
	StartEventBridge(ctx, nil, events)

	// No panic should occur
	assert.True(t, true)
}

// TestStartEventBridge_NilEventsNoOp verifies that nil events channel is handled gracefully.
func TestStartEventBridge_NilEventsNoOp(t *testing.T) {
	ctx := context.Background()
	p := tea.NewProgram(NewModel())

	// Should return immediately without starting goroutine
	StartEventBridge(ctx, p, nil)

	// No panic should occur
	assert.True(t, true)
}
