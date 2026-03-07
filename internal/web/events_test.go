package web

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/orchestrator"
	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/types"
)

func TestWebEventJSONRoundTripByKind(t *testing.T) {
	orchestratorTimestamp := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	teamTimestamp := time.Date(2026, 3, 1, 11, 0, 0, 0, time.UTC)
	boardTimestamp := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	agentTimestamp := time.Date(2026, 3, 1, 13, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		event         WebEvent
		expectedKind  WebEventKind
		expectedType  string
		expectedTime  time.Time
		assertPayload func(t *testing.T, payload json.RawMessage)
	}{
		{
			name: "orchestrator event round-trip",
			event: WebEvent{
				Kind:      WebEventOrchestrator,
				Type:      "StatusUpdate",
				Timestamp: orchestratorTimestamp,
				Payload: orchestrator.OrchestratorEvent{
					Type:      orchestrator.EventStatusUpdate,
					IssueID:   "ISSUE-1",
					Data:      orchestrator.StatusUpdate{BackoffQueue: 2},
					Timestamp: orchestratorTimestamp,
				},
			},
			expectedKind: WebEventOrchestrator,
			expectedType: "StatusUpdate",
			expectedTime: orchestratorTimestamp,
			assertPayload: func(t *testing.T, payload json.RawMessage) {
				t.Helper()
				var got struct {
					Type      orchestrator.EventType `json:"Type"`
					IssueID   string                 `json:"IssueID"`
					Timestamp time.Time              `json:"Timestamp"`
				}
				require.NoError(t, json.Unmarshal(payload, &got))
				assert.Equal(t, orchestrator.EventStatusUpdate, got.Type)
				assert.Equal(t, "ISSUE-1", got.IssueID)
				assert.Equal(t, orchestratorTimestamp, got.Timestamp)
			},
		},
		{
			name: "team event round-trip",
			event: WebEvent{
				Kind:      WebEventTeam,
				Type:      "team_created",
				Timestamp: teamTimestamp,
				Payload: types.TeamEvent{
					Type:      "team_created",
					TeamName:  "alpha",
					Data:      map[string]interface{}{"phase": "team-plan"},
					Timestamp: teamTimestamp,
				},
			},
			expectedKind: WebEventTeam,
			expectedType: "team_created",
			expectedTime: teamTimestamp,
			assertPayload: func(t *testing.T, payload json.RawMessage) {
				t.Helper()
				var got types.TeamEvent
				require.NoError(t, json.Unmarshal(payload, &got))
				assert.Equal(t, "team_created", got.Type)
				assert.Equal(t, "alpha", got.TeamName)
				assert.Equal(t, "team-plan", got.Data["phase"])
				assert.Equal(t, teamTimestamp, got.Timestamp)
			},
		},
		{
			name: "board event round-trip",
			event: WebEvent{
				Kind:      WebEventBoard,
				Type:      "board_issue_updated",
				Timestamp: boardTimestamp,
				Payload: BoardEvent{
					Action: "updated",
					Issue: tracker.LocalBoardIssue{
						ID:         "CB-1",
						Identifier: "CB-1",
						Title:      "Wire web stream",
						State:      tracker.LocalBoardStateInProgress,
						CreatedAt:  boardTimestamp,
						UpdatedAt:  boardTimestamp,
					},
				},
			},
			expectedKind: WebEventBoard,
			expectedType: "board_issue_updated",
			expectedTime: boardTimestamp,
			assertPayload: func(t *testing.T, payload json.RawMessage) {
				t.Helper()
				var got BoardEvent
				require.NoError(t, json.Unmarshal(payload, &got))
				assert.Equal(t, "updated", got.Action)
				assert.Equal(t, "CB-1", got.Issue.ID)
				assert.Equal(t, tracker.LocalBoardStateInProgress, got.Issue.State)
				assert.Equal(t, boardTimestamp, got.Issue.UpdatedAt)
			},
		},
		{
			name: "agent log event round-trip",
			event: WebEvent{
				Kind:      WebEventAgentLog,
				Type:      "agent_log",
				Timestamp: agentTimestamp,
				Payload: AgentLogEvent{
					WorkerID:  "worker-1",
					Line:      "started",
					Stream:    "stdout",
					Timestamp: agentTimestamp,
				},
			},
			expectedKind: WebEventAgentLog,
			expectedType: "agent_log",
			expectedTime: agentTimestamp,
			assertPayload: func(t *testing.T, payload json.RawMessage) {
				t.Helper()
				var got AgentLogEvent
				require.NoError(t, json.Unmarshal(payload, &got))
				assert.Equal(t, "worker-1", got.WorkerID)
				assert.Equal(t, "started", got.Line)
				assert.Equal(t, "stdout", got.Stream)
				assert.Equal(t, agentTimestamp, got.Timestamp)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := json.Marshal(tt.event)
			require.NoError(t, err)

			var got struct {
				Kind      WebEventKind    `json:"kind"`
				Type      string          `json:"type"`
				Payload   json.RawMessage `json:"payload"`
				Timestamp time.Time       `json:"timestamp"`
			}
			require.NoError(t, json.Unmarshal(encoded, &got))

			assert.Equal(t, tt.expectedKind, got.Kind)
			assert.Equal(t, tt.expectedType, got.Type)
			assert.Equal(t, tt.expectedTime, got.Timestamp)
			tt.assertPayload(t, got.Payload)
		})
	}
}

func TestWebEventConstructors(t *testing.T) {
	orchestratorTimestamp := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	teamTimestamp := time.Date(2026, 3, 2, 11, 0, 0, 0, time.UTC)

	orchestratorEvent := orchestrator.OrchestratorEvent{
		Type:      orchestrator.EventBackoffEnqueued,
		IssueID:   "ISSUE-2",
		Data:      orchestrator.BackoffEnqueued{Attempt: 2},
		Timestamp: orchestratorTimestamp,
	}
	teamEvent := types.TeamEvent{
		Type:      "task_claimed",
		TeamName:  "beta",
		Data:      map[string]interface{}{"task_id": "task-1"},
		Timestamp: teamTimestamp,
	}
	issue := tracker.LocalBoardIssue{
		ID:         "CB-2",
		Identifier: "CB-2",
		Title:      "Add team SSE payload",
		State:      tracker.LocalBoardStateTodo,
	}

	tests := []struct {
		name   string
		event  WebEvent
		kind   WebEventKind
		typeID string
	}{
		{
			name:   "orchestrator constructor sets kind and type",
			event:  NewOrchestratorWebEvent(orchestratorEvent),
			kind:   WebEventOrchestrator,
			typeID: "BackoffEnqueued",
		},
		{
			name:   "team constructor sets kind and type",
			event:  NewTeamWebEvent(teamEvent),
			kind:   WebEventTeam,
			typeID: "task_claimed",
		},
		{
			name:   "board constructor sets kind and type",
			event:  NewBoardWebEvent("moved", issue),
			kind:   WebEventBoard,
			typeID: "board_issue_moved",
		},
		{
			name:   "agent log constructor sets kind and type",
			event:  NewAgentLogWebEvent("worker-1", "hello", "stderr"),
			kind:   WebEventAgentLog,
			typeID: "agent_log",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.kind, tt.event.Kind)
			assert.Equal(t, tt.typeID, tt.event.Type)
		})
	}

	orchestratorWebEvent := NewOrchestratorWebEvent(orchestratorEvent)
	assert.Equal(t, orchestratorTimestamp, orchestratorWebEvent.Timestamp)

	teamWebEvent := NewTeamWebEvent(teamEvent)
	assert.Equal(t, teamTimestamp, teamWebEvent.Timestamp)

	boardWebEvent := NewBoardWebEvent("updated", issue)
	require.False(t, boardWebEvent.Timestamp.IsZero())

	boardPayload, ok := boardWebEvent.Payload.(BoardEvent)
	require.True(t, ok)
	assert.Equal(t, "updated", boardPayload.Action)
	assert.Equal(t, issue.ID, boardPayload.Issue.ID)

	agentWebEvent := NewAgentLogWebEvent("worker-2", "line", "stdout")
	require.False(t, agentWebEvent.Timestamp.IsZero())

	agentPayload, ok := agentWebEvent.Payload.(AgentLogEvent)
	require.True(t, ok)
	assert.Equal(t, "worker-2", agentPayload.WorkerID)
	assert.Equal(t, "line", agentPayload.Line)
	assert.Equal(t, "stdout", agentPayload.Stream)
	assert.Equal(t, agentWebEvent.Timestamp, agentPayload.Timestamp)
}
