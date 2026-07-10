package config

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowConfigValidate(t *testing.T) {
	t.Parallel()

	jitterAboveRange := 101
	tests := []struct {
		name    string
		cfg     *WorkflowConfig
		message string
	}{
		{
			name:    "rejects negative duration",
			cfg:     &WorkflowConfig{AgentTimeoutMsRaw: -1},
			message: "agent_timeout_ms",
		},
		{
			name:    "rejects invalid polling strategy",
			cfg:     &WorkflowConfig{Polling: PollingConfig{BackoffStrategy: "random"}},
			message: "polling.backoff_strategy",
		},
		{
			name:    "rejects invalid team execution mode",
			cfg:     &WorkflowConfig{Team: TeamSectionConfig{ExecutionMode: "distributed"}},
			message: "team.execution_mode",
		},
		{
			name:    "rejects invalid worker mode",
			cfg:     &WorkflowConfig{Team: TeamSectionConfig{WorkerMode: "process"}},
			message: "team.worker_mode",
		},
		{
			name:    "rejects invalid port",
			cfg:     &WorkflowConfig{OpenCode: OpenCodeConfig{Port: 70_000}},
			message: "opencode.port",
		},
		{
			name: "rejects jitter above one hundred",
			cfg: &WorkflowConfig{Orchestrator: OrchestratorConfig{
				Backoff: OrchestratorBackoffConfig{JitterPercent: &jitterAboveRange},
			}},
			message: "orchestrator.backoff.jitter_percent",
		},
		{
			name: "rejects reversed stage thresholds",
			cfg: &WorkflowConfig{Orchestrator: OrchestratorConfig{
				Snapshot: OrchestratorSnapshotConfig{Stage: OrchestratorStageConfig{
					TestingAfterMs:   60_000,
					ReviewingAfterMs: 30_000,
				}},
			}},
			message: "reviewing_after_ms",
		},
		{
			name: "rejects testing threshold above default review threshold",
			cfg: &WorkflowConfig{Orchestrator: OrchestratorConfig{
				Snapshot: OrchestratorSnapshotConfig{Stage: OrchestratorStageConfig{
					TestingAfterMs: 70_000,
				}},
			}},
			message: "reviewing_after_ms",
		},
		{
			name: "rejects medium confidence above default high confidence",
			cfg: &WorkflowConfig{Orchestrator: OrchestratorConfig{
				Snapshot: OrchestratorSnapshotConfig{ETA: OrchestratorETAConfig{
					MediumConfidenceAfterMs: 500_000,
				}},
			}},
			message: "high_confidence_after_ms",
		},
		{
			name: "rejects non-finite rates",
			cfg: &WorkflowConfig{Orchestrator: OrchestratorConfig{
				Snapshot: OrchestratorSnapshotConfig{ETA: OrchestratorETAConfig{
					MinFilesPerMinute: math.NaN(),
				}},
			}},
			message: "must be finite",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalidConfig)
			assert.Contains(t, err.Error(), tt.message)
		})
	}
}

func TestWorkflowConfigValidateAcceptsDefaultsAndExplicitZeroJitter(t *testing.T) {
	t.Parallel()

	zero := 0
	cfg := &WorkflowConfig{
		Orchestrator: OrchestratorConfig{
			Backoff: OrchestratorBackoffConfig{JitterPercent: &zero},
		},
	}

	require.NoError(t, cfg.Validate())
}
