package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventLogPushAndEntries(t *testing.T) {
	log := NewEventLog(5)
	assert.Equal(t, 0, log.Len())
	assert.Nil(t, log.Entries())

	now := time.Now()
	log.Push(EventLogEntry{Timestamp: now, Type: "start", Detail: "agent started"})
	assert.Equal(t, 1, log.Len())

	entries := log.Entries()
	require.Len(t, entries, 1)
	assert.Equal(t, "start", entries[0].Type)
	assert.Equal(t, "agent started", entries[0].Detail)
}

func TestEventLogRingBufferOverflow(t *testing.T) {
	log := NewEventLog(3)

	for i := 0; i < 5; i++ {
		log.Push(EventLogEntry{
			Timestamp: time.Now(),
			Type:      fmt.Sprintf("event-%d", i),
		})
	}

	assert.Equal(t, 3, log.Len())

	entries := log.Entries()
	require.Len(t, entries, 3)
	// Should have events 2, 3, 4 (oldest 0, 1 dropped)
	assert.Equal(t, "event-2", entries[0].Type)
	assert.Equal(t, "event-3", entries[1].Type)
	assert.Equal(t, "event-4", entries[2].Type)
}

func TestEventLogChronologicalOrder(t *testing.T) {
	log := NewEventLog(4)
	base := time.Now()

	for i := 0; i < 6; i++ {
		log.Push(EventLogEntry{
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Type:      fmt.Sprintf("e%d", i),
		})
	}

	entries := log.Entries()
	require.Len(t, entries, 4)

	// Verify chronological order
	for i := 1; i < len(entries); i++ {
		assert.True(t, entries[i].Timestamp.After(entries[i-1].Timestamp),
			"entry %d should be after entry %d", i, i-1)
	}
}

func TestEventLogLast(t *testing.T) {
	log := NewEventLog(10)
	for i := 0; i < 7; i++ {
		log.Push(EventLogEntry{Type: fmt.Sprintf("e%d", i)})
	}

	tests := []struct {
		name string
		n    int
		want int
		last string
	}{
		{"last 3", 3, 3, "e6"},
		{"last 1", 1, 1, "e6"},
		{"last more than count", 20, 7, "e6"},
		{"last 0", 0, 0, ""},
		{"last negative", -1, 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := log.Last(tt.n)
			assert.Len(t, entries, tt.want)
			if tt.want > 0 {
				assert.Equal(t, tt.last, entries[len(entries)-1].Type)
			}
		})
	}
}

func TestEventLogLastChronological(t *testing.T) {
	log := NewEventLog(5)
	for i := 0; i < 8; i++ {
		log.Push(EventLogEntry{
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
			Type:      fmt.Sprintf("e%d", i),
		})
	}

	entries := log.Last(3)
	require.Len(t, entries, 3)
	assert.Equal(t, "e5", entries[0].Type)
	assert.Equal(t, "e6", entries[1].Type)
	assert.Equal(t, "e7", entries[2].Type)
}

func TestEventLogDefaultSize(t *testing.T) {
	log := NewEventLog(0)
	assert.Equal(t, defaultEventLogSize, log.maxSize)

	log2 := NewEventLog(-5)
	assert.Equal(t, defaultEventLogSize, log2.maxSize)
}

func TestEventLogEmptyLast(t *testing.T) {
	log := NewEventLog(5)
	assert.Nil(t, log.Last(3))
}
