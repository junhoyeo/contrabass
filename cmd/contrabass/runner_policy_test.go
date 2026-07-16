package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/junhoyeo/contrabass/internal/config"
)

func TestRunnerPolicyFromConfig(t *testing.T) {
	t.Parallel()

	defaults := runnerPolicyFromConfig(nil)
	assert.Equal(t, 30*time.Second, defaults.startupTimeout)
	assert.Equal(t, 2*time.Second, defaults.tmuxPollInterval)

	cfg := &config.WorkflowConfig{
		Agent: config.AgentConfig{StartupTimeoutMs: 1234},
		Team:  config.TeamSectionConfig{WorkerPollIntervalMs: 5678},
	}
	custom := runnerPolicyFromConfig(cfg)
	assert.Equal(t, 1234*time.Millisecond, custom.startupTimeout)
	assert.Equal(t, 5678*time.Millisecond, custom.tmuxPollInterval)
}
