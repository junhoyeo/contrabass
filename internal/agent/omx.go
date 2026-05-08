package agent

import (
	"time"

	"github.com/junhoyeo/contrabass/internal/config"
)

type OMXRunner struct {
	*teamCLIRunner
}

func NewOMXRunner(cfg *config.WorkflowConfig, timeout time.Duration) *OMXRunner {
	pollInterval := time.Duration(cfg.OMXPollIntervalMs()) * time.Millisecond
	startupTimeout := timeout
	if startupTimeout <= 0 {
		startupTimeout = time.Duration(cfg.OMXStartupTimeoutMs()) * time.Millisecond
	}

	inner := newTeamCLIRunner(&teamCLIRunner{
		name:           "omx",
		binaryPath:     cfg.OMXBinaryPath(),
		teamSpec:       cfg.OMXTeamSpec(),
		pollInterval:   pollInterval,
		startupTimeout: startupTimeout,
		// omx v0.16+ runs its own worker supervisor (heartbeat, restart,
		// quarantine), so contrabass should only observe — driving its own
		// restart/quarantine on top of omx kills healthy codex turns.
		selfHealing: true,
		// omx v0.16 removed the inline `omx team ralph ...` form (ralph must
		// now run as a separate `omx ralph ...` invocation), so the workflow
		// `omx.ralph` flag is no longer threaded into team launch. Setting it
		// has no effect on contrabass-driven team runs; ralph cycles need to
		// be invoked outside the runner.
		startArgs: func(teamSpec, task string) []string {
			return []string{"team", teamSpec, task}
		},
		shutdownArgs: func(teamName string) []string {
			return []string{"team", "shutdown", teamName, "--force"}
		},
	})

	return &OMXRunner{teamCLIRunner: inner}
}
