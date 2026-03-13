package agent

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

func TestCLIEventToTeamEvent(t *testing.T) {
	event := &cliEvent{
		EventID:   "evt_123",
		Team:      "test-team",
		Type:      "task_completed",
		Worker:    "worker-1",
		TaskID:    "task-1",
		State:     "completed",
		PrevState: "in_progress",
		Metadata: map[string]interface{}{
			"duration_ms": float64(1500),
		},
		CreatedAt: time.Now(),
	}

	teamEvent := event.toTeamEvent()

	if teamEvent.Type != event.Type {
		t.Errorf("Type mismatch: got %s, want %s", teamEvent.Type, event.Type)
	}
	if teamEvent.TeamName != event.Team {
		t.Errorf("TeamName mismatch: got %s, want %s", teamEvent.TeamName, event.Team)
	}
	if teamEvent.Data["worker"] != event.Worker {
		t.Errorf("Worker mismatch: got %v, want %s", teamEvent.Data["worker"], event.Worker)
	}
	if teamEvent.Data["state"] != event.State {
		t.Errorf("State mismatch: got %v, want %s", teamEvent.Data["state"], event.State)
	}
	if teamEvent.Data["prev_state"] != event.PrevState {
		t.Errorf("PrevState mismatch: got %v, want %s", teamEvent.Data["prev_state"], event.PrevState)
	}
	if teamEvent.Data["event_id"] != event.EventID {
		t.Errorf("EventID mismatch: got %v, want %s", teamEvent.Data["event_id"], event.EventID)
	}
}

func TestTeamEventJSON(t *testing.T) {
	event := types.TeamEvent{
		Type:     "task_completed",
		TeamName: "test-team",
		Data: map[string]interface{}{
			"worker":  "worker-1",
			"task_id": "task-1",
		},
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event: %v", err)
	}

	var decoded types.TeamEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal event: %v", err)
	}

	if decoded.Type != event.Type {
		t.Errorf("Type mismatch: got %s, want %s", decoded.Type, event.Type)
	}
	if decoded.TeamName != event.TeamName {
		t.Errorf("TeamName mismatch: got %s, want %s", decoded.TeamName, event.TeamName)
	}
}

func TestEventFilter(t *testing.T) {
	filter := &EventFilter{
		AfterEventID: "evt_100",
		Type:         "worker_state_changed",
		Worker:       "worker-1",
		WakeableOnly: true,
	}

	if filter.AfterEventID != "evt_100" {
		t.Errorf("AfterEventID mismatch: got %s, want evt_100", filter.AfterEventID)
	}
	if filter.Type != "worker_state_changed" {
		t.Errorf("Type mismatch: got %s, want worker_state_changed", filter.Type)
	}
	if !filter.WakeableOnly {
		t.Error("WakeableOnly should be true")
	}
}

func TestIdleStateJSON(t *testing.T) {
	state := &IdleState{
		TeamName:        "test-team",
		WorkerCount:     3,
		IdleWorkerCount: 2,
		IdleWorkers:     []string{"worker-1", "worker-2"},
		NonIdleWorkers:  []string{"worker-3"},
		AllWorkersIdle:  false,
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Failed to marshal idle state: %v", err)
	}

	var decoded IdleState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal idle state: %v", err)
	}

	if decoded.TeamName != state.TeamName {
		t.Errorf("TeamName mismatch: got %s, want %s", decoded.TeamName, state.TeamName)
	}
	if decoded.IdleWorkerCount != state.IdleWorkerCount {
		t.Errorf("IdleWorkerCount mismatch: got %d, want %d", decoded.IdleWorkerCount, state.IdleWorkerCount)
	}
	if decoded.AllWorkersIdle != state.AllWorkersIdle {
		t.Errorf("AllWorkersIdle mismatch: got %v, want %v", decoded.AllWorkersIdle, state.AllWorkersIdle)
	}
}

func TestStallStateJSON(t *testing.T) {
	state := &StallState{
		TeamName:         "test-team",
		TeamStalled:      true,
		LeaderStale:      false,
		StalledWorkers:   []string{"worker-2"},
		DeadWorkers:      []string{"worker-3"},
		PendingTaskCount: 5,
		AllWorkersIdle:   false,
		IdleWorkers:      []string{"worker-1"},
		Reasons:          []string{"workers_non_reporting:worker-2", "dead_workers_with_pending_work:worker-3"},
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Failed to marshal stall state: %v", err)
	}

	var decoded StallState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal stall state: %v", err)
	}

	if decoded.TeamStalled != state.TeamStalled {
		t.Errorf("TeamStalled mismatch: got %v, want %v", decoded.TeamStalled, state.TeamStalled)
	}
	if len(decoded.Reasons) != len(state.Reasons) {
		t.Errorf("Reasons length mismatch: got %d, want %d", len(decoded.Reasons), len(state.Reasons))
	}
	if decoded.PendingTaskCount != state.PendingTaskCount {
		t.Errorf("PendingTaskCount mismatch: got %d, want %d", decoded.PendingTaskCount, state.PendingTaskCount)
	}
}
