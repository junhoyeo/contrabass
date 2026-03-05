package logging

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogToFile(t *testing.T) {
	// Create a temporary file for logging
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	opts := LogOptions{
		Level:  log.InfoLevel,
		Output: logFile,
		Prefix: "test",
	}

	logger := NewLogger(opts)
	require.NotNil(t, logger)

	// Write a log entry
	logger.Info("test message", "key", "value")

	// Verify file exists and contains content
	content, err := os.ReadFile(logFile)
	require.NoError(t, err)
	assert.NotEmpty(t, content)
	assert.Contains(t, string(content), "test message")
}

func TestLogToStdout(t *testing.T) {
	// Empty output means stderr
	opts := LogOptions{
		Level:  log.InfoLevel,
		Output: "",
		Prefix: "test",
	}

	logger := NewLogger(opts)
	require.NotNil(t, logger)

	// Should not panic when logging to stderr
	assert.NotPanics(t, func() {
		logger.Info("test message")
	})
}

func TestLogLevels(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	opts := LogOptions{
		Level:  log.InfoLevel,
		Output: logFile,
		Prefix: "test",
	}

	logger := NewLogger(opts)
	require.NotNil(t, logger)

	// Log at different levels
	logger.Debug("debug message")
	logger.Info("info message")
	logger.Warn("warn message")
	logger.Error("error message")

	// Read file content
	content, err := os.ReadFile(logFile)
	require.NoError(t, err)
	contentStr := string(content)

	// Debug should not appear at Info level
	assert.NotContains(t, contentStr, "debug message")
	// Info and above should appear
	assert.Contains(t, contentStr, "info message")
	assert.Contains(t, contentStr, "warn message")
	assert.Contains(t, contentStr, "error message")
}

func TestLogIssueEvent(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	opts := LogOptions{
		Level:  log.InfoLevel,
		Output: logFile,
		Prefix: "test",
	}

	logger := NewLogger(opts)
	require.NotNil(t, logger)

	// Log an issue event
	LogIssueEvent(logger, "issue-123", "created", "title", "Test Issue", "priority", "high")

	// Verify structured fields appear in output
	content, err := os.ReadFile(logFile)
	require.NoError(t, err)
	contentStr := string(content)

	assert.Contains(t, contentStr, "issue event")
	assert.Contains(t, contentStr, "issue_id")
	assert.Contains(t, contentStr, "issue-123")
	assert.Contains(t, contentStr, "event")
	assert.Contains(t, contentStr, "created")
	assert.Contains(t, contentStr, "title")
	assert.Contains(t, contentStr, "Test Issue")
}

func TestLogAgentEvent(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	opts := LogOptions{
		Level:  log.InfoLevel,
		Output: logFile,
		Prefix: "test",
	}

	logger := NewLogger(opts)
	require.NotNil(t, logger)

	// Log an agent event
	LogAgentEvent(logger, "issue-456", "started", "agent_type", "analyzer", "status", "running")

	// Verify structured fields appear in output
	content, err := os.ReadFile(logFile)
	require.NoError(t, err)
	contentStr := string(content)

	assert.Contains(t, contentStr, "agent event")
	assert.Contains(t, contentStr, "issue_id")
	assert.Contains(t, contentStr, "issue-456")
	assert.Contains(t, contentStr, "agent_event")
	assert.Contains(t, contentStr, "started")
	assert.Contains(t, contentStr, "agent_type")
	assert.Contains(t, contentStr, "analyzer")
}

func TestLogOrchestratorEvent(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	opts := LogOptions{
		Level:  log.InfoLevel,
		Output: logFile,
		Prefix: "test",
	}

	logger := NewLogger(opts)
	require.NotNil(t, logger)

	// Log an orchestrator event
	LogOrchestratorEvent(logger, "initialized", "version", "1.0.0", "mode", "tui")

	// Verify structured fields appear in output
	content, err := os.ReadFile(logFile)
	require.NoError(t, err)
	contentStr := string(content)

	assert.Contains(t, contentStr, "orchestrator")
	assert.Contains(t, contentStr, "event")
	assert.Contains(t, contentStr, "initialized")
	assert.Contains(t, contentStr, "version")
	assert.Contains(t, contentStr, "1.0.0")
	assert.Contains(t, contentStr, "mode")
	assert.Contains(t, contentStr, "tui")
}

func TestLoggerPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	opts := LogOptions{
		Level:  log.InfoLevel,
		Output: logFile,
		Prefix: "myapp",
	}

	logger := NewLogger(opts)
	require.NotNil(t, logger)

	logger.Info("test message")

	// Verify prefix appears in output
	content, err := os.ReadFile(logFile)
	require.NoError(t, err)
	contentStr := string(content)

	assert.Contains(t, contentStr, "myapp")
}

func TestLoggerFallbackToStderr(t *testing.T) {
	// Use an invalid path that can't be created
	opts := LogOptions{
		Level:  log.InfoLevel,
		Output: "/invalid/path/that/does/not/exist/test.log",
		Prefix: "test",
	}

	logger := NewLogger(opts)
	require.NotNil(t, logger)

	// Should not panic even though file can't be opened
	assert.NotPanics(t, func() {
		logger.Info("test message")
	})
}
