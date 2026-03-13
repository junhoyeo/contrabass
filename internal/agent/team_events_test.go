package agent

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	assert.Equal(t, event.Type, teamEvent.Type)
	assert.Equal(t, event.Team, teamEvent.TeamName)
	assert.Equal(t, event.Worker, teamEvent.Data["worker"])
	assert.Equal(t, event.State, teamEvent.Data["state"])
	assert.Equal(t, event.PrevState, teamEvent.Data["prev_state"])
	assert.Equal(t, event.EventID, teamEvent.Data["event_id"])
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
	require.NoError(t, err)

	var decoded types.TeamEvent
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, event.Type, decoded.Type)
	assert.Equal(t, event.TeamName, decoded.TeamName)
}

func TestEventFilter(t *testing.T) {
	filter := &EventFilter{
		AfterEventID: "evt_100",
		Type:         "worker_state_changed",
		Worker:       "worker-1",
		WakeableOnly: true,
	}

	assert.Equal(t, "evt_100", filter.AfterEventID)
	assert.Equal(t, "worker_state_changed", filter.Type)
	assert.True(t, filter.WakeableOnly)
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
	require.NoError(t, err)

	var decoded IdleState
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, state.TeamName, decoded.TeamName)
	assert.Equal(t, state.IdleWorkerCount, decoded.IdleWorkerCount)
	assert.Equal(t, state.AllWorkersIdle, decoded.AllWorkersIdle)
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
	require.NoError(t, err)

	var decoded StallState
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, state.TeamStalled, decoded.TeamStalled)
	assert.Equal(t, len(state.Reasons), len(decoded.Reasons))
	assert.Equal(t, state.PendingTaskCount, decoded.PendingTaskCount)
}
