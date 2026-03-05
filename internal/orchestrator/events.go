package orchestrator

import (
	"time"

	"github.com/junhoyeo/symphony-charm/internal/types"
)

type EventType int

const (
	EventStatusUpdate EventType = iota
	EventAgentStarted
	EventAgentFinished
	EventBackoffEnqueued
	EventIssueReleased
)

func (t EventType) String() string {
	switch t {
	case EventStatusUpdate:
		return "StatusUpdate"
	case EventAgentStarted:
		return "AgentStarted"
	case EventAgentFinished:
		return "AgentFinished"
	case EventBackoffEnqueued:
		return "BackoffEnqueued"
	case EventIssueReleased:
		return "IssueReleased"
	default:
		return "Unknown"
	}
}

type OrchestratorEvent struct {
	Type      EventType
	IssueID   string
	Data      interface{}
	Timestamp time.Time
}

type StatusUpdate struct {
	Stats        Stats
	BackoffQueue int
	ModelName    string
	ProjectURL   string
}

type AgentStarted struct {
	Attempt   int
	PID       int
	SessionID string
	Workspace string
}

type AgentFinished struct {
	Attempt   int
	Phase     types.RunPhase
	TokensIn  int64
	TokensOut int64
	Error     string
}

type BackoffEnqueued struct {
	Attempt int
	RetryAt time.Time
	Error   string
}

type IssueReleased struct {
	Attempt int
}
