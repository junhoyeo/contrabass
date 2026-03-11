package team

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventLogger_LogAndRead(t *testing.T) {
	tests := []struct {
		name   string
		filter *EventFilter
		want   int
	}{
		{
			name: "no filter returns all events",
			want: 3,
		},
		{
			name: "type filter",
			filter: &EventFilter{
				Type: "task_completed",
			},
			want: 1,
		},
		{
			name: "worker filter",
			filter: &EventFilter{
				WorkerID: "worker-1",
			},
			want: 2,
		},
		{
			name: "task filter",
			filter: &EventFilter{
				TaskID: "task-2",
			},
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths := NewPaths(t.TempDir())
			logger := NewEventLogger(paths)

			err := logger.Log("team-a", LoggedEvent{Type: "task_claimed", WorkerID: "worker-1", TaskID: "task-1"})
			require.NoError(t, err)
			err = logger.Log("team-a", LoggedEvent{Type: "task_completed", WorkerID: "worker-1", TaskID: "task-2"})
			require.NoError(t, err)
			err = logger.Log("team-a", LoggedEvent{Type: "worker_started", WorkerID: "worker-2"})
			require.NoError(t, err)

			events, err := logger.Read("team-a", tt.filter)
			require.NoError(t, err)
			assert.Len(t, events, tt.want)
		})
	}
}

func TestEventLogger_LogWritesJSONL(t *testing.T) {
	paths := NewPaths(t.TempDir())
	logger := NewEventLogger(paths)

	err := logger.Log("team-a", LoggedEvent{
		Type:     "task_claimed",
		WorkerID: "worker-1",
		TaskID:   "task-1",
		Data: map[string]interface{}{
			"foo": "bar",
		},
	})
	require.NoError(t, err)

	path := filepath.Join(paths.EventsDir("team-a"), eventsFileName)
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 1)

	var event LoggedEvent
	err = json.Unmarshal([]byte(lines[0]), &event)
	require.NoError(t, err)
	assert.Equal(t, "task_claimed", event.Type)
	assert.Equal(t, "team-a", event.TeamName)
	assert.Equal(t, "worker-1", event.WorkerID)
	assert.Equal(t, "task-1", event.TaskID)
	assert.Equal(t, "bar", event.Data["foo"])
	assert.False(t, event.Timestamp.IsZero())
	require.Regexp(t, regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`), event.ID)
}

func TestEventLogger_ReadSince(t *testing.T) {
	paths := NewPaths(t.TempDir())
	logger := NewEventLogger(paths)

	err := logger.Log("team-a", LoggedEvent{Type: "task_claimed", TaskID: "task-1"})
	require.NoError(t, err)
	err = logger.Log("team-a", LoggedEvent{Type: "task_claimed", TaskID: "task-2"})
	require.NoError(t, err)

	firstBatch, cursor, err := logger.ReadSince("team-a", 0, nil)
	require.NoError(t, err)
	require.Len(t, firstBatch, 2)
	assert.Greater(t, cursor, int64(0))

	err = logger.Log("team-a", LoggedEvent{Type: "task_completed", TaskID: "task-1"})
	require.NoError(t, err)

	secondBatch, nextCursor, err := logger.ReadSince("team-a", cursor, nil)
	require.NoError(t, err)
	require.Len(t, secondBatch, 1)
	assert.Equal(t, "task_completed", secondBatch[0].Type)
	assert.Equal(t, "task-1", secondBatch[0].TaskID)
	assert.Greater(t, nextCursor, cursor)
}

func TestEventLogger_ReadSinceWithFilter(t *testing.T) {
	paths := NewPaths(t.TempDir())
	logger := NewEventLogger(paths)

	err := logger.Log("team-a", LoggedEvent{Type: "task_claimed", WorkerID: "worker-1", TaskID: "task-1"})
	require.NoError(t, err)
	err = logger.Log("team-a", LoggedEvent{Type: "task_claimed", WorkerID: "worker-2", TaskID: "task-1"})
	require.NoError(t, err)

	events, _, err := logger.ReadSince("team-a", 0, &EventFilter{Type: "task_claimed", WorkerID: "worker-2"})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "worker-2", events[0].WorkerID)
}

func TestEventLogger_ReadSinceMissingFile(t *testing.T) {
	paths := NewPaths(t.TempDir())
	logger := NewEventLogger(paths)

	events, cursor, err := logger.ReadSince("team-a", 0, nil)
	require.NoError(t, err)
	assert.Empty(t, events)
	assert.Equal(t, int64(0), cursor)
}

func TestEventLogger_InvalidCursor(t *testing.T) {
	paths := NewPaths(t.TempDir())
	logger := NewEventLogger(paths)

	_, _, err := logger.ReadSince("team-a", -1, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cursor")
}

func TestLoggedEventFromTeamEvent(t *testing.T) {
	now := time.Now()
	base := loggedEventFromTeamEvent(types.TeamEvent{
		Type:      "task_claimed",
		TeamName:  "team-a",
		Data:      map[string]interface{}{"k": "v"},
		Timestamp: now,
	})

	assert.Equal(t, "task_claimed", base.Type)
	assert.Equal(t, "team-a", base.TeamName)
	assert.Equal(t, "v", base.Data["k"])
	assert.Equal(t, now, base.Timestamp)
}
