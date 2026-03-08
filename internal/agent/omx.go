package agent

import (
	"time"

	"github.com/junhoyeo/contrabass/internal/config"
)

const (
	defaultOMXBinaryPath       = "omx"
	defaultOMXTeamSpec         = "1:executor"
	defaultOMXPollIntervalMs   = 1000
	defaultOMXStartupTimeoutMs = 15000
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

	inner := newTeamCLIRunner(teamCLIRunner{
		name:           "omx",
		binaryPath:     cfg.OMXBinaryPath(),
		teamSpec:       cfg.OMXTeamSpec(),
		pollInterval:   pollInterval,
		startupTimeout: startupTimeout,
		startArgs: func(teamSpec, task string) []string {
			args := []string{"team"}
			if cfg.OMXRalph() {
				args = append(args, "ralph")
			}
			args = append(args, teamSpec, task)
			return args
		},
		shutdownArgs: func(teamName string) []string {
			args := []string{"team", "shutdown", teamName, "--force"}
			if cfg.OMXRalph() {
				args = append(args, "--ralph")
			}
			return args
		},
	})

	return &OMXRunner{teamCLIRunner: inner}
}
