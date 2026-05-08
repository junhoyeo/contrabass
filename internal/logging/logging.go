package logging

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/log"
)

// LogOptions configures the logger
type LogOptions struct {
	Level   log.Level // Debug, Info, Warn, Error
	Output  string    // File path or "" for stdout/stderr
	Prefix  string    // Logger prefix (e.g., "orchestrator", "agent")
	Session string    // Optional session ID; if set, splice into Output before its extension
}

// ResolveLogPath splices a session ID into a file path before its final extension.
// Returns the input unchanged if either argument is empty. Used so concurrent
// contrabass instances do not interleave entries into the same log file.
//
//	ResolveLogPath("/tmp/contrabass.log", "9f3a2b1c") == "/tmp/contrabass-9f3a2b1c.log"
//	ResolveLogPath("contrabass",          "9f3a2b1c") == "contrabass-9f3a2b1c"
func ResolveLogPath(output, session string) string {
	if output == "" || session == "" {
		return output
	}
	if strings.HasSuffix(output, "-"+session) ||
		strings.Contains(filepath.Base(output), "-"+session+".") {
		return output
	}
	dir, base := filepath.Split(output)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	return filepath.Join(dir, stem+"-"+session+ext)
}

// NewLogger creates a configured logger
// If Output is empty, logs to stderr. Otherwise, logs to the specified file.
// If the file cannot be opened, falls back to stderr.
func NewLogger(opts LogOptions) *log.Logger {
	var w io.Writer

	output := ResolveLogPath(opts.Output, opts.Session)
	if output != "" {
		f, err := os.OpenFile(output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
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
