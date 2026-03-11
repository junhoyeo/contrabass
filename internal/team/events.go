package team

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

const eventsFileName = "events.ndjson"

// EventLogger appends and reads team events as JSON Lines.
type EventLogger struct {
	paths *Paths
	mu    sync.Mutex
}

// EventFilter narrows event reads to matching fields.
type EventFilter struct {
	Type     string
	WorkerID string
	TaskID   string
}

// LoggedEvent is a persisted team event record.
// It extends types.TeamEvent with an ID and optional worker/task identifiers.
type LoggedEvent struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	TeamName  string                 `json:"team_name"`
	WorkerID  string                 `json:"worker_id,omitempty"`
	TaskID    string                 `json:"task_id,omitempty"`
	Data      map[string]interface{} `json:"data,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

// NewEventLogger creates a new append-only JSONL event logger.
func NewEventLogger(paths *Paths) *EventLogger {
	return &EventLogger{paths: paths}
}

// Log appends a single event JSON object as one line to events.ndjson.
func (l *EventLogger) Log(teamName string, event LoggedEvent) error {
	if l == nil || l.paths == nil {
		return errors.New("event logger is not initialized")
	}
	if teamName == "" {
		return errors.New("team name is required")
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if event.ID == "" {
		id, err := generateEventID()
		if err != nil {
			return err
		}
		event.ID = id
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	event.TeamName = teamName

	path := l.eventsPath(teamName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create events dir: %w", err)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open events file: %w", err)
	}
	defer file.Close()

	if err := json.NewEncoder(file).Encode(event); err != nil {
		return fmt.Errorf("append event: %w", err)
	}

	return nil
}

// Read returns all events matching the optional filter.
func (l *EventLogger) Read(teamName string, filter *EventFilter) ([]LoggedEvent, error) {
	events, _, err := l.ReadSince(teamName, 0, filter)
	if err != nil {
		return nil, err
	}
	return events, nil
}

// ReadSince returns filtered events from the given file offset cursor and the next cursor.
func (l *EventLogger) ReadSince(teamName string, cursor int64, filter *EventFilter) ([]LoggedEvent, int64, error) {
	if l == nil || l.paths == nil {
		return nil, cursor, errors.New("event logger is not initialized")
	}
	if teamName == "" {
		return nil, cursor, errors.New("team name is required")
	}
	if cursor < 0 {
		return nil, cursor, errors.New("cursor must be non-negative")
	}

	path := l.eventsPath(teamName)
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []LoggedEvent{}, 0, nil
		}
		return nil, cursor, fmt.Errorf("open events file: %w", err)
	}
	defer file.Close()

	if _, err := file.Seek(cursor, io.SeekStart); err != nil {
		return nil, cursor, fmt.Errorf("seek events file: %w", err)
	}

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var events []LoggedEvent
	for scanner.Scan() {
		var event LoggedEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, cursor, fmt.Errorf("decode event line: %w", err)
		}
		if matchesFilter(event, filter) {
			events = append(events, event)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, cursor, fmt.Errorf("scan events file: %w", err)
	}

	nextCursor, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, cursor, fmt.Errorf("read cursor position: %w", err)
	}

	return events, nextCursor, nil
}

// Close closes the logger.
// EventLogger opens files per operation, so Close is a no-op.
func (l *EventLogger) Close() error {
	return nil
}

func (l *EventLogger) eventsPath(teamName string) string {
	return filepath.Join(l.paths.EventsDir(teamName), eventsFileName)
}

func matchesFilter(event LoggedEvent, filter *EventFilter) bool {
	if filter == nil {
		return true
	}
	if filter.Type != "" && event.Type != filter.Type {
		return false
	}
	if filter.WorkerID != "" && event.WorkerID != filter.WorkerID {
		return false
	}
	if filter.TaskID != "" && event.TaskID != filter.TaskID {
		return false
	}
	return true
}

func generateEventID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate event id: %w", err)
	}

	// UUIDv4: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	h := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32]), nil
}

func loggedEventFromTeamEvent(event types.TeamEvent) LoggedEvent {
	return LoggedEvent{
		Type:      event.Type,
		TeamName:  event.TeamName,
		Data:      event.Data,
		Timestamp: event.Timestamp,
	}
}
