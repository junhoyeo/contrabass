package agent

import (
	"context"
	"fmt"
	"time"
)

// TeamEvent represents a team activity event
type TeamEvent struct {
	EventID   string                 `json:"event_id"`
	Team      string                 `json:"team"`
	Type      string                 `json:"type"`
	Worker    string                 `json:"worker"`
	TaskID    string                 `json:"task_id,omitempty"`
	MessageID string                 `json:"message_id,omitempty"`
	Reason    string                 `json:"reason,omitempty"`
	State     string                 `json:"state,omitempty"`
	PrevState string                 `json:"prev_state,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

// EventFilter defines filters for querying events
type EventFilter struct {
	AfterEventID string
	Type         string
	Worker       string
	TaskID       string
	WakeableOnly bool
}

// ReadEvents retrieves events from the team event log
func (r *teamCLIRunner) ReadEvents(ctx context.Context, workspace, teamName string, filter *EventFilter) ([]TeamEvent, error) {
	if filter == nil {
		filter = &EventFilter{}
	}

	input := map[string]interface{}{
		"team_name": teamName,
	}

	if filter.AfterEventID != "" {
		input["after_event_id"] = filter.AfterEventID
	}
	if filter.Type != "" {
		input["type"] = filter.Type
	}
	if filter.Worker != "" {
		input["worker"] = filter.Worker
	}
	if filter.TaskID != "" {
		input["task_id"] = filter.TaskID
	}
	if filter.WakeableOnly {
		input["wakeable_only"] = true
	}

	var resp struct {
		Count  int         `json:"count"`
		Cursor string      `json:"cursor"`
		Events []TeamEvent `json:"events"`
	}

	if err := r.runTeamAPI(ctx, workspace, "read-events", input, &resp); err != nil {
		return nil, fmt.Errorf("read team events: %w", err)
	}

	return resp.Events, nil
}

// AwaitEvent waits for a specific event to occur
func (r *teamCLIRunner) AwaitEvent(ctx context.Context, workspace, teamName string, filter *EventFilter, timeout time.Duration) (*TeamEvent, error) {
	if filter == nil {
		filter = &EventFilter{}
	}

	input := map[string]interface{}{
		"team_name":  teamName,
		"timeout_ms": timeout.Milliseconds(),
	}

	if filter.AfterEventID != "" {
		input["after_event_id"] = filter.AfterEventID
	}
	if filter.Type != "" {
		input["type"] = filter.Type
	}
	if filter.Worker != "" {
		input["worker"] = filter.Worker
	}
	if filter.TaskID != "" {
		input["task_id"] = filter.TaskID
	}
	if filter.WakeableOnly {
		input["wakeable_only"] = true
	}

	var resp struct {
		Status string     `json:"status"`
		Cursor string     `json:"cursor"`
		Event  *TeamEvent `json:"event"`
	}

	if err := r.runTeamAPI(ctx, workspace, "await-event", input, &resp); err != nil {
		return nil, fmt.Errorf("await team event: %w", err)
	}

	if resp.Status == "timeout" {
		return nil, fmt.Errorf("timeout waiting for event")
	}

	if resp.Event == nil {
		return nil, fmt.Errorf("no event received")
	}

	return resp.Event, nil
}

// AppendEvent adds an event to the team event log
func (r *teamCLIRunner) AppendEvent(ctx context.Context, workspace, teamName string, event *TeamEvent) (*TeamEvent, error) {
	input := map[string]interface{}{
		"team_name": teamName,
		"type":      event.Type,
		"worker":    event.Worker,
	}

	if event.TaskID != "" {
		input["task_id"] = event.TaskID
	}
	if event.MessageID != "" {
		input["message_id"] = event.MessageID
	}
	if event.Reason != "" {
		input["reason"] = event.Reason
	}
	if event.State != "" {
		input["state"] = event.State
	}
	if event.PrevState != "" {
		input["prev_state"] = event.PrevState
	}

	var resp struct {
		Event TeamEvent `json:"event"`
	}

	if err := r.runTeamAPI(ctx, workspace, "append-event", input, &resp); err != nil {
		return nil, fmt.Errorf("append team event: %w", err)
	}

	return &resp.Event, nil
}

// IdleState represents the idle state of the team
type IdleState struct {
	TeamName         string   `json:"team_name"`
	WorkerCount      int      `json:"worker_count"`
	IdleWorkerCount  int      `json:"idle_worker_count"`
	IdleWorkers      []string `json:"idle_workers"`
	NonIdleWorkers   []string `json:"non_idle_workers"`
	AllWorkersIdle   bool     `json:"all_workers_idle"`
	LastAllIdleEvent *struct {
		EventID   string    `json:"event_id"`
		Type      string    `json:"type"`
		CreatedAt time.Time `json:"created_at"`
	} `json:"last_all_workers_idle_event,omitempty"`
}

// ReadIdleState retrieves the current idle state of the team
func (r *teamCLIRunner) ReadIdleState(ctx context.Context, workspace, teamName string) (*IdleState, error) {
	var state IdleState

	if err := r.runTeamAPI(ctx, workspace, "read-idle-state", map[string]string{
		"team_name": teamName,
	}, &state); err != nil {
		return nil, fmt.Errorf("read idle state: %w", err)
	}

	return &state, nil
}

// StallState represents whether the team is stalled
type StallState struct {
	TeamName         string   `json:"team_name"`
	TeamStalled      bool     `json:"team_stalled"`
	LeaderStale      bool     `json:"leader_stale"`
	StalledWorkers   []string `json:"stalled_workers"`
	DeadWorkers      []string `json:"dead_workers"`
	PendingTaskCount int      `json:"pending_task_count"`
	AllWorkersIdle   bool     `json:"all_workers_idle"`
	IdleWorkers      []string `json:"idle_workers"`
	Reasons          []string `json:"reasons"`
}

// ReadStallState retrieves the current stall state of the team
func (r *teamCLIRunner) ReadStallState(ctx context.Context, workspace, teamName string) (*StallState, error) {
	var state StallState

	if err := r.runTeamAPI(ctx, workspace, "read-stall-state", map[string]string{
		"team_name": teamName,
	}, &state); err != nil {
		return nil, fmt.Errorf("read stall state: %w", err)
	}

	return &state, nil
}
