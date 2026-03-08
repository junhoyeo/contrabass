package agent

import (
	"time"

	"github.com/junhoyeo/contrabass/internal/config"
)

type OMCRunner struct {
	*teamCLIRunner
}

func NewOMCRunner(cfg *config.WorkflowConfig, timeout time.Duration) *OMCRunner {
	pollInterval := time.Duration(cfg.OMCPollIntervalMs()) * time.Millisecond
	startupTimeout := timeout
	if startupTimeout <= 0 {
		startupTimeout = time.Duration(cfg.OMCStartupTimeoutMs()) * time.Millisecond
	}

	inner := newTeamCLIRunner(&teamCLIRunner{
		name:           "omc",
		binaryPath:     cfg.OMCBinaryPath(),
		teamSpec:       cfg.OMCTeamSpec(),
		pollInterval:   pollInterval,
		startupTimeout: startupTimeout,
		startArgs: func(teamSpec, task string) []string {
			return []string{"team", teamSpec, task}
		},
	})

	return &OMCRunner{teamCLIRunner: inner}
}
