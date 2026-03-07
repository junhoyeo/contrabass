package tui

import (
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/junhoyeo/contrabass/internal/orchestrator"
	"github.com/junhoyeo/contrabass/internal/types"
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
	// cmd may return tea.QuitMsg or tea.sequenceMsg (when native image cleanup
	// is prepended via tea.Sequence). Both ultimately quit the program.
	model := updated.(Model)
	assert.True(t, model.quitting)
}

func TestModelCtrlCQuit(t *testing.T) {
	m := NewModel()
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
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

func TestModelTeamEventsPopulateBoardLinkedTeamState(t *testing.T) {
	m := NewModel()
	now := time.Now()

	updated, _ := m.Update(TeamEventMsg{Event: types.TeamEvent{
		Type:      "team_created",
		TeamName:  "team-alpha",
		Timestamp: now,
		Data: map[string]interface{}{
			"max_workers":    2,
			"board_issue_id": "CB-7",
		},
	}})
	model := updated.(Model)

	row, ok := model.teams["team-alpha"]
	require.True(t, ok)
	assert.Equal(t, "CB-7", row.BoardIssueID)
	assert.Equal(t, 2, row.Workers)
	assert.Equal(t, 2, model.stats.MaxAgents)
	assert.Equal(t, 0, model.stats.RunningAgents)

	updated, _ = model.Update(TeamEventMsg{Event: types.TeamEvent{
		Type:      "pipeline_started",
		TeamName:  "team-alpha",
		Timestamp: now.Add(time.Second),
		Data: map[string]interface{}{
			"task_count": 3,
		},
	}})
	model = updated.(Model)
	assert.Equal(t, 3, model.teams["team-alpha"].Tasks)

	updated, _ = model.Update(TeamEventMsg{Event: types.TeamEvent{
		Type:      "task_claimed",
		TeamName:  "team-alpha",
		Timestamp: now.Add(2 * time.Second),
		Data: map[string]interface{}{
			"worker_id": "worker-1",
			"task_id":   "003-cb-7-exec",
			"task":      "Implement CB-7",
		},
	}})
	model = updated.(Model)
	assert.Equal(t, 1, model.teams["team-alpha"].ActiveWorkers)
	assert.Equal(t, 1, model.stats.RunningAgents)
	require.Len(t, model.teamWorkers["team-alpha"], 1)
	assert.Equal(t, "working", model.teamWorkers["team-alpha"][0].Status)
	assert.Equal(t, "003-cb-7-exec", model.teamWorkers["team-alpha"][0].CurrentTask)

	updated, _ = model.Update(TeamEventMsg{Event: types.TeamEvent{
		Type:      "task_completed",
		TeamName:  "team-alpha",
		Timestamp: now.Add(3 * time.Second),
		Data: map[string]interface{}{
			"worker_id": "worker-1",
			"task_id":   "003-cb-7-exec",
		},
	}})
	model = updated.(Model)
	assert.Equal(t, 1, model.teams["team-alpha"].CompletedTasks)
	assert.Equal(t, 0, model.teams["team-alpha"].ActiveWorkers)
	assert.Equal(t, 0, model.stats.RunningAgents)
	assert.Equal(t, "idle", model.teamWorkers["team-alpha"][0].Status)
	assert.Empty(t, model.teamWorkers["team-alpha"][0].CurrentTask)

	updated, _ = model.Update(TeamEventMsg{Event: types.TeamEvent{
		Type:      "pipeline_completed",
		TeamName:  "team-alpha",
		Timestamp: now.Add(4 * time.Second),
		Data: map[string]interface{}{
			"phase": "failed",
		},
	}})
	model = updated.(Model)
	assert.Equal(t, "failed", model.teams["team-alpha"].Phase)

	view := stripANSI(model.teamTable.View())
	assert.Contains(t, view, "team-alpha · CB-7")
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

	view := stripANSI(updated.(Model).View().Content)
	assert.Contains(t, view, "CONTRABASS STATUS")
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

func TestTableView_NarrowWidthNoOverflow(t *testing.T) {
	tests := []struct {
		name  string
		width int
	}{
		{
			name:  "narrow 40-char terminal",
			width: 40,
		},
		{
			name:  "standard 80-char terminal",
			width: 80,
		},
		{
			name:  "zero width uses auto sizing",
			width: 0,
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
			tbl := NewTable().SetWidth(tt.width).Update(rows, "●")
			out := stripANSI(tbl.View())
			assert.NotEmpty(t, out)

			if tt.width > 0 {
				for _, line := range strings.Split(out, "\n") {
					if line == "" {
						continue
					}
					assert.LessOrEqual(t, len([]rune(line)), tt.width)
				}
			}
		})
	}
}

// TestModel_StatusUpdatePopulatesHeaderMetadata verifies that tracker metadata
// from StatusUpdate events is mapped to HeaderData.
func TestModel_StatusUpdatePopulatesHeaderMetadata(t *testing.T) {
	tests := []struct {
		name         string
		modelName    string
		projectURL   string
		trackerType  string
		trackerScope string
	}{
		{
			name:         "all fields populated",
			modelName:    "gpt-4o",
			projectURL:   "https://github.com/example/project",
			trackerType:  "github",
			trackerScope: "https://github.com/example/project",
		},
		{
			name:         "only model name",
			modelName:    "claude-3",
			projectURL:   "",
			trackerType:  "",
			trackerScope: "",
		},
		{
			name:         "only project URL",
			modelName:    "",
			projectURL:   "https://github.com/example/other",
			trackerType:  "",
			trackerScope: "",
		},
		{
			name:         "internal tracker scope",
			modelName:    "",
			projectURL:   "",
			trackerType:  "internal",
			trackerScope: ".contrabass/board",
		},
		{
			name:         "empty fields preserve existing values",
			modelName:    "",
			projectURL:   "",
			trackerType:  "",
			trackerScope: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewModel()
			// Pre-set values to verify empty strings don't overwrite.
			m.stats.ModelName = "existing-model"
			m.stats.ProjectURL = "https://existing.url"
			m.stats.TrackerType = "existing-tracker"
			m.stats.TrackerScope = "existing-scope"

			event := orchestrator.OrchestratorEvent{
				Type:      orchestrator.EventStatusUpdate,
				Timestamp: time.Now(),
				Data: orchestrator.StatusUpdate{
					Stats:        orchestrator.Stats{Running: 1, MaxAgents: 4},
					ModelName:    tt.modelName,
					ProjectURL:   tt.projectURL,
					TrackerType:  tt.trackerType,
					TrackerScope: tt.trackerScope,
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

			if tt.trackerType != "" {
				assert.Equal(t, tt.trackerType, model.stats.TrackerType)
			} else {
				assert.Equal(t, "existing-tracker", model.stats.TrackerType,
					"empty TrackerType should not overwrite existing value")
			}

			if tt.trackerScope != "" {
				assert.Equal(t, tt.trackerScope, model.stats.TrackerScope)
			} else {
				assert.Equal(t, "existing-scope", model.stats.TrackerScope,
					"empty TrackerScope should not overwrite existing value")
			}

			// Verify header data is synced.
			assert.Equal(t, model.stats.ModelName, model.header.data.ModelName)
			assert.Equal(t, model.stats.ProjectURL, model.header.data.ProjectURL)
			assert.Equal(t, model.stats.TrackerType, model.header.data.TrackerType)
			assert.Equal(t, model.stats.TrackerScope, model.header.data.TrackerScope)

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

// TestModel_UnknownMessage verifies that unknown tea.Msg types increment
// unknownEvents without corrupting model state or returning a command.
func TestModel_UnknownMessage(t *testing.T) {
	type customMsg struct{}

	m := NewModel()
	// Pre-populate state to verify it survives an unknown message.
	m.agents["ISSUE-1"] = AgentRow{IssueID: "ISSUE-1", PID: 100}
	m.backoffs["ISSUE-2"] = BackoffRow{IssueID: "ISSUE-2", Attempt: 1}
	m.stats.TokensIn = 42

	updated, cmd := m.Update(customMsg{})
	model := updated.(Model)

	assert.Nil(t, cmd, "unknown message should not produce a command")
	assert.Equal(t, 1, model.unknownEvents)
	assert.Len(t, model.agents, 1, "agents map should be unchanged")
	assert.Len(t, model.backoffs, 1, "backoffs map should be unchanged")
	assert.Contains(t, model.agents, "ISSUE-1")
	assert.Contains(t, model.backoffs, "ISSUE-2")
	assert.Equal(t, int64(42), model.stats.TokensIn, "stats should be unchanged")
}

// TestModel_EventWrongDataType verifies that each orchestrator event handler
// gracefully handles receiving a valid EventPayload type that doesn't match
// what the handler expects (no crash, no unknownEvents increment).
func TestModel_EventWrongDataType(t *testing.T) {
	tests := []struct {
		name      string
		eventType orchestrator.EventType
		data      orchestrator.EventPayload
	}{
		{
			name:      "StatusUpdate handler gets AgentStarted payload",
			eventType: orchestrator.EventStatusUpdate,
			data:      orchestrator.AgentStarted{PID: 1},
		},
		{
			name:      "AgentStarted handler gets StatusUpdate payload",
			eventType: orchestrator.EventAgentStarted,
			data:      orchestrator.StatusUpdate{ModelName: "wrong"},
		},
		{
			name:      "AgentFinished handler gets BackoffEnqueued payload",
			eventType: orchestrator.EventAgentFinished,
			data:      orchestrator.BackoffEnqueued{Attempt: 1},
		},
		{
			name:      "BackoffEnqueued handler gets IssueReleased payload",
			eventType: orchestrator.EventBackoffEnqueued,
			data:      orchestrator.IssueReleased{Attempt: 1},
		},
		{
			name:      "IssueReleased handler gets AgentFinished payload",
			eventType: orchestrator.EventIssueReleased,
			data:      orchestrator.AgentFinished{Attempt: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewModel()
			event := orchestrator.OrchestratorEvent{
				Type:      tt.eventType,
				IssueID:   "ISSUE-MISMATCH",
				Timestamp: time.Now(),
				Data:      tt.data,
			}

			updated, cmd := m.Update(OrchestratorEventMsg{Event: event})
			model := updated.(Model)

			assert.Nil(t, cmd)
			assert.Equal(t, 0, model.unknownEvents,
				"type mismatch in known event handler should not increment unknownEvents")
			assert.Empty(t, model.agents, "mismatched payload should not create agent rows")
			assert.Empty(t, model.backoffs, "mismatched payload should not create backoff rows")
		})
	}
}

// TestModel_IssueReleasedClearsBoth verifies that an IssueReleased event
// clears both the active agent row and any backoff row for that issue,
// leaving unrelated issues untouched.
func TestModel_IssueReleasedClearsBoth(t *testing.T) {
	m := NewModel()
	issueID := "ISSUE-BOTH"
	now := time.Now()

	// Set up active agent for this issue.
	m.agents[issueID] = AgentRow{IssueID: issueID, PID: 999, Phase: types.StreamingTurn}
	m.agentStartTime[issueID] = now.Add(-10 * time.Second)

	// Set up backoff for the same issue.
	m.backoffs[issueID] = BackoffRow{IssueID: issueID, Attempt: 2, Error: "overloaded"}
	m.backoffRetryAt[issueID] = now.Add(30 * time.Second)

	// Set up a different issue that should NOT be affected.
	m.agents["ISSUE-OTHER"] = AgentRow{IssueID: "ISSUE-OTHER", PID: 888}
	m.agentStartTime["ISSUE-OTHER"] = now

	event := orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventIssueReleased,
		IssueID:   issueID,
		Timestamp: now,
		Data:      orchestrator.IssueReleased{Attempt: 2},
	}

	updated, _ := m.Update(OrchestratorEventMsg{Event: event})
	model := updated.(Model)

	// The released issue should be completely gone from all maps.
	_, hasAgent := model.agents[issueID]
	assert.False(t, hasAgent, "agent row should be cleared for released issue")
	_, hasStart := model.agentStartTime[issueID]
	assert.False(t, hasStart, "agent start time should be cleared for released issue")
	_, hasBackoff := model.backoffs[issueID]
	assert.False(t, hasBackoff, "backoff row should be cleared for released issue")
	_, hasRetry := model.backoffRetryAt[issueID]
	assert.False(t, hasRetry, "backoff retry time should be cleared for released issue")

	// The other issue should remain intact.
	_, hasOther := model.agents["ISSUE-OTHER"]
	assert.True(t, hasOther, "unrelated issue should not be affected")
}

// TestModel_RefreshDerivedFields_ZeroStartTime verifies that when the model's
// startTime is zero, refreshDerivedFields replaces it with the provided time.
func TestModel_RefreshDerivedFields_ZeroStartTime(t *testing.T) {
	m := NewModel()
	m.startTime = time.Time{} // explicit zero value

	now := time.Now()
	m = m.refreshDerivedFields(now)

	assert.Equal(t, now, m.startTime,
		"zero startTime should be replaced with current time")
	assert.Equal(t, 0, m.stats.RuntimeSeconds,
		"runtime should be 0 when startTime equals now")
}

// TestDurationString_Negative verifies that negative and zero durations
// are clamped to "0s".
func TestDurationString_Negative(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{name: "negative 5 seconds", duration: -5 * time.Second, want: "0s"},
		{name: "negative 1 nanosecond", duration: -1, want: "0s"},
		{name: "zero duration", duration: 0, want: "0s"},
		{name: "positive 10 seconds", duration: 10 * time.Second, want: "10s"},
		{name: "positive sub-second truncated", duration: 1500 * time.Millisecond, want: "1s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, durationString(tt.duration))
		})
	}
}

// TestStartEventBridge_NilInputs verifies that StartEventBridge handles
// various nil input combinations gracefully without panicking.
func TestStartEventBridge_NilInputs(t *testing.T) {
	t.Run("nil context replaced with background", func(t *testing.T) {
		ch := make(chan orchestrator.OrchestratorEvent)
		p := tea.NewProgram(NewModel())

		assert.NotPanics(t, func() {
			StartEventBridge(nil, p, ch)
		})

		close(ch) // cleanup goroutine started by bridge
	})

	t.Run("all three arguments nil", func(t *testing.T) {
		assert.NotPanics(t, func() {
			StartEventBridge(nil, nil, nil)
		})
	})

	t.Run("nil program and nil events together", func(t *testing.T) {
		assert.NotPanics(t, func() {
			StartEventBridge(context.Background(), nil, nil)
		})
	})
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

func TestHelpToggleRefreshesViewportContent(t *testing.T) {
	m := NewModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(Model)

	m.agents["ISSUE-1"] = AgentRow{IssueID: "ISSUE-1", Stage: "StreamingTurn", PID: 1, Phase: types.StreamingTurn}
	m = m.syncTables()

	before := stripANSI(m.View().Content)
	assert.Contains(t, before, "ISSUE-1")
	assert.NotContains(t, before, "ISSUE-NEW")

	m.agents["ISSUE-NEW"] = AgentRow{IssueID: "ISSUE-NEW", Stage: "StreamingTurn", PID: 2, Phase: types.StreamingTurn}

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "?", Code: '?'})
	assert.Nil(t, cmd)
	m = updated.(Model)

	after := stripANSI(m.View().Content)
	assert.Contains(t, after, "ISSUE-NEW")
}

func TestRegressionQuit(t *testing.T) {
	m := NewModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "q", Code: 'q'})
	require.NotNil(t, cmd)
	model := updated.(Model)
	assert.True(t, model.quitting)

	m2 := NewModel()
	updated2, _ := m2.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m2 = updated2.(Model)
	updated2, cmd2 := m2.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.NotNil(t, cmd2)
	model2 := updated2.(Model)
	assert.True(t, model2.quitting)
}

func TestSpinnerTickReturnsCmd(t *testing.T) {
	m := NewModel()
	updated, cmd := m.Update(spinner.TickMsg{})
	require.NotNil(t, cmd)
	_ = updated
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
	m = m.syncTables()

	assert.Equal(t, 0, m.viewport.YOffset())
	updated, _ = m.Update(tea.KeyPressMsg{Text: "j", Code: 'j'})
	model := updated.(Model)
	assert.Greater(t, model.viewport.YOffset(), 0)
}

func TestViewportWindowResize(t *testing.T) {
	m := NewModel()
	sizes := []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 120, Height: 40}, {Width: 60, Height: 15}}

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

	m.agents["ISSUE-1"] = AgentRow{IssueID: "ISSUE-1", Stage: "StreamingTurn", PID: 1, Phase: types.StreamingTurn}
	m.agents["ISSUE-2"] = AgentRow{IssueID: "ISSUE-2", Stage: "Finishing", PID: 2, Phase: types.Finishing}
	m = m.syncTables()

	updated, _ = m.Update(tea.KeyPressMsg{Text: "j", Code: 'j'})
	model := updated.(Model)
	assert.Equal(t, 0, model.viewport.YOffset())
}
