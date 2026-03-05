package types

import (
	"time"
)

// IssueState represents the orchestrator's internal claim state for an issue.
type IssueState int

const (
	Unclaimed IssueState = iota
	Claimed
	Running
	RetryQueued
	Released
)

// String returns the human-readable name of the IssueState.
func (s IssueState) String() string {
	switch s {
	case Unclaimed:
		return "Unclaimed"
	case Claimed:
		return "Claimed"
	case Running:
		return "Running"
	case RetryQueued:
		return "RetryQueued"
	case Released:
		return "Released"
	default:
		return "Unknown"
	}
}

// RunPhase represents the phase of a run attempt.
type RunPhase int

const (
	PreparingWorkspace RunPhase = iota
	BuildingPrompt
	LaunchingAgentProcess
	InitializingSession
	StreamingTurn
	Finishing
	Succeeded
	Failed
	TimedOut
	Stalled
	CanceledByReconciliation
)

// String returns the human-readable name of the RunPhase.
func (p RunPhase) String() string {
	switch p {
	case PreparingWorkspace:
		return "PreparingWorkspace"
	case BuildingPrompt:
		return "BuildingPrompt"
	case LaunchingAgentProcess:
		return "LaunchingAgentProcess"
	case InitializingSession:
		return "InitializingSession"
	case StreamingTurn:
		return "StreamingTurn"
	case Finishing:
		return "Finishing"
	case Succeeded:
		return "Succeeded"
	case Failed:
		return "Failed"
	case TimedOut:
		return "TimedOut"
	case Stalled:
		return "Stalled"
	case CanceledByReconciliation:
		return "CanceledByReconciliation"
	default:
		return "Unknown"
	}
}

// Issue represents a normalized issue record from the tracker.
type Issue struct {
	ID          string                 `json:"id"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	State       IssueState             `json:"state"`
	Labels      []string               `json:"labels"`
	URL         string                 `json:"url"`
	TrackerMeta map[string]interface{} `json:"tracker_meta"`
}

// RunAttempt represents one execution attempt for one issue.
type RunAttempt struct {
	IssueID   string    `json:"issue_id"`
	Phase     RunPhase  `json:"phase"`
	Attempt   int       `json:"attempt"`
	PID       int       `json:"pid"`
	StartTime time.Time `json:"start_time"`
	TokensIn  int64     `json:"tokens_in"`
	TokensOut int64     `json:"tokens_out"`
	SessionID string    `json:"session_id"`
	LastEvent string    `json:"last_event"`
	Error     string    `json:"error"`
}

// BackoffEntry represents a scheduled retry state for an issue.
type BackoffEntry struct {
	IssueID string    `json:"issue_id"`
	Attempt int       `json:"attempt"`
	RetryAt time.Time `json:"retry_at"`
	Error   string    `json:"error"`
}

// AgentEvent represents an event emitted by the coding agent.
type AgentEvent struct {
	Type      string                 `json:"type"`
	Data      map[string]interface{} `json:"data"`
	Timestamp time.Time              `json:"timestamp"`
}
