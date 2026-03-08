package web

import (
	"time"

	"github.com/junhoyeo/contrabass/internal/orchestrator"
	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/types"
)

type WebEventKind string

const (
	WebEventOrchestrator WebEventKind = "orchestrator"
	WebEventTeam         WebEventKind = "team"
	WebEventBoard        WebEventKind = "board"
	WebEventAgentLog     WebEventKind = "agent_log"
)

type WebEvent struct {
	Kind      WebEventKind `json:"kind"`
	Type      string       `json:"type"`
	Payload   interface{}  `json:"payload"`
	Timestamp time.Time    `json:"timestamp"`
}

type BoardEvent struct {
	Action string                  `json:"action"`
	Issue  tracker.LocalBoardIssue `json:"issue"`
}

type AgentLogEvent struct {
	WorkerID  string    `json:"worker_id"`
	Line      string    `json:"line"`
	Stream    string    `json:"stream"`
	Timestamp time.Time `json:"timestamp"`
}

func NewOrchestratorWebEvent(event orchestrator.OrchestratorEvent) WebEvent {
	return WebEvent{
		Kind:      WebEventOrchestrator,
		Type:      event.Type.String(),
		Payload:   event,
		Timestamp: event.Timestamp,
	}
}

func NewTeamWebEvent(event types.TeamEvent) WebEvent {
	return WebEvent{
		Kind:      WebEventTeam,
		Type:      event.Type,
		Payload:   event,
		Timestamp: event.Timestamp,
	}
}

func NewBoardWebEvent(action string, issue tracker.LocalBoardIssue) WebEvent {
	now := time.Now().UTC()
	return WebEvent{
		Kind: WebEventBoard,
		Type: "board_issue_" + action,
		Payload: BoardEvent{
			Action: action,
			Issue:  issue,
		},
		Timestamp: now,
	}
}

func NewAgentLogWebEvent(workerID, line, stream string) WebEvent {
	now := time.Now().UTC()
	return WebEvent{
		Kind: WebEventAgentLog,
		Type: "agent_log",
		Payload: AgentLogEvent{
			WorkerID:  workerID,
			Line:      line,
			Stream:    stream,
			Timestamp: now,
		},
		Timestamp: now,
	}
}
