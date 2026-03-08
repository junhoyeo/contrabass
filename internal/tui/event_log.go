package tui

import "time"

// defaultEventLogSize is the maximum number of events stored per agent.
const defaultEventLogSize = 200

// EventLogEntry stores one event for the per-agent event timeline.
type EventLogEntry struct {
	Timestamp time.Time
	Type      string
	Detail    string
}

// EventLog is a fixed-size ring buffer of events.
// It keeps the most recent maxSize entries, silently dropping older ones.
type EventLog struct {
	entries []EventLogEntry
	head    int // next write position
	count   int
	maxSize int
}

// NewEventLog creates an event log with the given capacity.
func NewEventLog(maxSize int) *EventLog {
	if maxSize <= 0 {
		maxSize = defaultEventLogSize
	}
	return &EventLog{
		entries: make([]EventLogEntry, maxSize),
		maxSize: maxSize,
	}
}

// Push appends an entry, overwriting the oldest if full.
func (l *EventLog) Push(entry EventLogEntry) {
	l.entries[l.head] = entry
	l.head = (l.head + 1) % l.maxSize
	if l.count < l.maxSize {
		l.count++
	}
}

// Entries returns all stored entries in chronological order (oldest first).
func (l *EventLog) Entries() []EventLogEntry {
	if l.count == 0 {
		return nil
	}
	result := make([]EventLogEntry, l.count)
	start := (l.head - l.count + l.maxSize) % l.maxSize
	for i := 0; i < l.count; i++ {
		result[i] = l.entries[(start+i)%l.maxSize]
	}
	return result
}

// Len returns the number of stored entries.
func (l *EventLog) Len() int {
	return l.count
}

// Last returns the most recent N entries in chronological order.
// If n > Len(), returns all entries.
func (l *EventLog) Last(n int) []EventLogEntry {
	if n <= 0 || l.count == 0 {
		return nil
	}
	if n > l.count {
		n = l.count
	}
	result := make([]EventLogEntry, n)
	start := (l.head - n + l.maxSize) % l.maxSize
	for i := 0; i < n; i++ {
		result[i] = l.entries[(start+i)%l.maxSize]
	}
	return result
}
