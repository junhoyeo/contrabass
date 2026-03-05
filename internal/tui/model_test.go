package tui

import (
	"fmt"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/junhoyeo/symphony-charm/internal/orchestrator"
	"github.com/junhoyeo/symphony-charm/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestHelpToggle(t *testing.T) {
	m := NewModel()
	assert.False(t, m.help.ShowAll)

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "?", Code: '?'})
	assert.Nil(t, cmd)
	model := updated.(Model)
	assert.True(t, model.help.ShowAll)

	updated, cmd = model.Update(tea.KeyPressMsg{Text: "?", Code: '?'})
	assert.Nil(t, cmd)
	model = updated.(Model)
	assert.False(t, model.help.ShowAll)
}

func TestHelpViewContainsBindings(t *testing.T) {
	m := NewModel()
	helpView := m.help.View(m.keys)
	stripped := stripANSI(helpView)
	assert.Contains(t, stripped, "q")
	assert.Contains(t, stripped, "↑/k")
	assert.Contains(t, stripped, "↓/j")
}

func TestViewportHeightWithHelp(t *testing.T) {
	m := NewModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(Model)
	heightOff := m.viewport.Height()

	updated, _ = m.Update(tea.KeyPressMsg{Text: "?", Code: '?'})
	m = updated.(Model)
	heightOn := m.viewport.Height()

	assert.Less(t, heightOn, heightOff)
}

func TestRegressionQuit(t *testing.T) {
	m := NewModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "q", Code: 'q'})
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
	model := updated.(Model)
	assert.True(t, model.quitting)

	m2 := NewModel()
	updated2, _ := m2.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m2 = updated2.(Model)
	updated2, cmd2 := m2.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.NotNil(t, cmd2)
	assert.IsType(t, tea.QuitMsg{}, cmd2())
	model2 := updated2.(Model)
	assert.True(t, model2.quitting)
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

func TestSpinnerTickReturnsCmd(t *testing.T) {
	m := NewModel()
	updated, cmd := m.Update(spinner.TickMsg{})
	require.NotNil(t, cmd)
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
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(Model)

	now := time.Now()
	updated, _ = m.Update(OrchestratorEventMsg{Event: orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventBackoffEnqueued,
		IssueID:   "ISSUE-2",
		Timestamp: now,
		Data:      orchestrator.BackoffEnqueued{Attempt: 1, RetryAt: now.Add(10 * time.Second), Error: "slow"},
	}})

	view := updated.(Model).View().Content
	assert.Contains(t, view, "SYMPHONY STATUS")
	assert.Contains(t, view, "ISSUE-2")
}

func TestViewportScrollBasic(t *testing.T) {
	m := NewModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = updated.(Model)

	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("ISSUE-%d", i)
		m.agents[id] = AgentRow{
			IssueID: id,
			Stage:   "StreamingTurn",
			PID:     1000 + i,
			Age:     "1m",
			Phase:   types.StreamingTurn,
		}
	}
	m.syncTables()

	assert.Equal(t, 0, m.viewport.YOffset())
	updated, _ = m.Update(tea.KeyPressMsg{Text: "j", Code: 'j'})
	model := updated.(Model)
	assert.Greater(t, model.viewport.YOffset(), 0)
}

func TestViewportWindowResize(t *testing.T) {
	m := NewModel()
	sizes := []tea.WindowSizeMsg{
		{Width: 80, Height: 24},
		{Width: 120, Height: 40},
		{Width: 60, Height: 15},
	}

	for _, size := range sizes {
		updated, _ := m.Update(size)
		m = updated.(Model)
		assert.Equal(t, size.Width, m.width)
		assert.Equal(t, size.Height, m.height)
	}
}

func TestViewportContentShorterThanHeight(t *testing.T) {
	m := NewModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(Model)

	m.agents["ISSUE-1"] = AgentRow{
		IssueID: "ISSUE-1",
		Stage:   "StreamingTurn",
		PID:     1,
		Phase:   types.StreamingTurn,
	}
	m.agents["ISSUE-2"] = AgentRow{
		IssueID: "ISSUE-2",
		Stage:   "Finishing",
		PID:     2,
		Phase:   types.Finishing,
	}
	m.syncTables()

	updated, _ = m.Update(tea.KeyPressMsg{Text: "j", Code: 'j'})
	model := updated.(Model)
	assert.Equal(t, 0, model.viewport.YOffset())
}
