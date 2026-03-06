package agent

import (
	"context"

	"github.com/junhoyeo/contrabass/internal/types"
)

// AgentRunner defines the interface for running a coding agent.
type AgentRunner interface {
	// Start launches a coding agent process for the given issue.
	Start(ctx context.Context, issue types.Issue, workspace string, prompt string) (*AgentProcess, error)

	// Stop terminates a running agent process.
	Stop(proc *AgentProcess) error

	// Close releases any resources held by the runner (e.g. a managed
	// server subprocess). Callers should invoke Close during application
	// shutdown to prevent orphaned child processes.
	Close() error
}

// AgentProcess represents a running agent subprocess.
type AgentProcess struct {
	PID       int
	SessionID string
	Events    <-chan types.AgentEvent
	Done      <-chan error

	serverURL string
}
