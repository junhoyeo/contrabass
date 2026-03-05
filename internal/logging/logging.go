package logging

import (
	"io"
	"os"

	"github.com/charmbracelet/log"
)

// LogOptions configures the logger
type LogOptions struct {
	Level  log.Level // Debug, Info, Warn, Error
	Output string    // File path or "" for stdout/stderr
	Prefix string    // Logger prefix (e.g., "orchestrator", "agent")
}

// NewLogger creates a configured logger
// If Output is empty, logs to stderr. Otherwise, logs to the specified file.
// If the file cannot be opened, falls back to stderr.
func NewLogger(opts LogOptions) *log.Logger {
	var w io.Writer

	if opts.Output != "" {
		f, err := os.OpenFile(opts.Output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			// Fallback to stderr if file can't be opened
			w = os.Stderr
		} else {
			w = f
		}
	} else {
		w = os.Stderr
	}

	logger := log.NewWithOptions(w, log.Options{
		Level:           opts.Level,
		Prefix:          opts.Prefix,
		ReportTimestamp: true,
	})

	return logger
}

// LogIssueEvent logs an event related to an issue
func LogIssueEvent(logger *log.Logger, issueID string, event string, fields ...interface{}) {
	args := append([]interface{}{"issue_id", issueID, "event", event}, fields...)
	logger.Info("issue event", args...)
}

// LogAgentEvent logs an agent-related event
func LogAgentEvent(logger *log.Logger, issueID string, eventType string, fields ...interface{}) {
	args := append([]interface{}{"issue_id", issueID, "agent_event", eventType}, fields...)
	logger.Info("agent event", args...)
}

// LogOrchestratorEvent logs an orchestrator lifecycle event
func LogOrchestratorEvent(logger *log.Logger, event string, fields ...interface{}) {
	args := append([]interface{}{"event", event}, fields...)
	logger.Info("orchestrator", args...)
}
