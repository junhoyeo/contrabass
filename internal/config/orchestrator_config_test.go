package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowConfig_OrchestratorPolicyDefaults(t *testing.T) {
	t.Parallel()

	var cfg *WorkflowConfig

	assert.Equal(t, 256, cfg.OrchestratorEventBufferSize())
	assert.Equal(t, 256, cfg.OrchestratorRunSignalBufferSize())
	assert.Equal(t, 1000, cfg.OrchestratorIssueCacheSize())
	assert.Equal(t, 5000, cfg.OrchestratorRunShutdownTimeoutMs())
	assert.Equal(t, 5000, cfg.OrchestratorStopGraceTimeoutMs())
	assert.Equal(t, 2000, cfg.OrchestratorGitCommandTimeoutMs())
	assert.Equal(t, 30_000, cfg.OrchestratorShutdownDrainTimeoutMs())
	assert.Equal(t, 10_000, cfg.OrchestratorShutdownCleanupTimeoutMs())
	assert.Equal(t, 10, cfg.OrchestratorShutdownPollIntervalMs())
	assert.Equal(t, "exponential", cfg.PollingBackoffStrategy())
	assert.Equal(t, 1000, cfg.OrchestratorContinuationBackoffMs())
	assert.Equal(t, 10_000, cfg.OrchestratorFailureBackoffBaseMs())
	assert.Equal(t, 2, cfg.OrchestratorFailureBackoffMultiplier())
	assert.Equal(t, 10, cfg.OrchestratorBackoffJitterPercent())
	assert.Equal(t, 1000, cfg.OrchestratorSnapshotDiffTimeoutMs())
	assert.Equal(t, 30_000, cfg.OrchestratorStageTestingAfterMs())
	assert.Equal(t, 60_000, cfg.OrchestratorStageReviewingAfterMs())
	assert.Equal(t, 50_000.0, cfg.OrchestratorStageReviewingMaxTokensPerMinute())
	assert.Equal(t, 180_000, cfg.OrchestratorETAMinElapsedMs())
	assert.Equal(t, 0.05, cfg.OrchestratorETAMinFilesPerMinute())
	assert.Equal(t, 1000.0, cfg.OrchestratorETAMinTokensPerMinute())
	assert.Equal(t, 300_000, cfg.OrchestratorETAMediumConfidenceAfterMs())
	assert.Equal(t, 480_000, cfg.OrchestratorETAHighConfidenceAfterMs())
	assert.Equal(t, 3, cfg.OrchestratorETAHighConfidenceMinStage())
	assert.Equal(t, 1.2, cfg.OrchestratorETAEstimatedFilesMultiplier())
	assert.Equal(t, 11, cfg.OrchestratorETAMinEstimatedFiles())
	assert.Equal(t, 5.0, cfg.OrchestratorETAFallbackRemainingMinutes())
	assert.Equal(t, 1.35, cfg.OrchestratorETAUncertaintyMultiplier())
	assert.Equal(t, 30_000, cfg.AgentStartupTimeoutMs())
	assert.Equal(t, 2_000, cfg.TeamWorkerPollIntervalMs())
}

func TestParseWorkflow_OrchestratorPolicyOverrides(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "WORKFLOW.md")
	content := `---
polling:
  backoff_strategy: linear
agent:
  startup_timeout_ms: 61
team:
  worker_poll_interval_ms: 62
orchestrator:
  event_buffer_size: 64
  run_signal_buffer_size: 65
  issue_cache_size: 66
  run_shutdown_timeout_ms: 67
  stop_grace_timeout_ms: 68
  git_command_timeout_ms: 69
  shutdown:
    drain_timeout_ms: 70
    cleanup_timeout_ms: 71
    poll_interval_ms: 72
  backoff:
    continuation_ms: 73
    failure_base_ms: 74
    multiplier: 3
    jitter_percent: 0
  snapshot:
    diff_timeout_ms: 75
    stage:
      testing_after_ms: 76
      reviewing_after_ms: 77
      reviewing_max_tokens_per_minute: 78.5
    eta:
      min_elapsed_ms: 79
      min_files_per_minute: 0.8
      min_tokens_per_minute: 81.5
      medium_confidence_after_ms: 82
      high_confidence_after_ms: 83
      high_confidence_min_stage: 4
      estimated_files_multiplier: 1.4
      min_estimated_files: 85
      fallback_remaining_minutes: 8.6
      uncertainty_multiplier: 1.7
---
Prompt.
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	cfg, err := ParseWorkflow(path)
	require.NoError(t, err)

	assert.Equal(t, 64, cfg.OrchestratorEventBufferSize())
	assert.Equal(t, 65, cfg.OrchestratorRunSignalBufferSize())
	assert.Equal(t, 66, cfg.OrchestratorIssueCacheSize())
	assert.Equal(t, 67, cfg.OrchestratorRunShutdownTimeoutMs())
	assert.Equal(t, 68, cfg.OrchestratorStopGraceTimeoutMs())
	assert.Equal(t, 69, cfg.OrchestratorGitCommandTimeoutMs())
	assert.Equal(t, 70, cfg.OrchestratorShutdownDrainTimeoutMs())
	assert.Equal(t, 71, cfg.OrchestratorShutdownCleanupTimeoutMs())
	assert.Equal(t, 72, cfg.OrchestratorShutdownPollIntervalMs())
	assert.Equal(t, "linear", cfg.PollingBackoffStrategy())
	assert.Equal(t, 73, cfg.OrchestratorContinuationBackoffMs())
	assert.Equal(t, 74, cfg.OrchestratorFailureBackoffBaseMs())
	assert.Equal(t, 3, cfg.OrchestratorFailureBackoffMultiplier())
	assert.Equal(t, 0, cfg.OrchestratorBackoffJitterPercent())
	assert.Equal(t, 75, cfg.OrchestratorSnapshotDiffTimeoutMs())
	assert.Equal(t, 76, cfg.OrchestratorStageTestingAfterMs())
	assert.Equal(t, 77, cfg.OrchestratorStageReviewingAfterMs())
	assert.Equal(t, 78.5, cfg.OrchestratorStageReviewingMaxTokensPerMinute())
	assert.Equal(t, 79, cfg.OrchestratorETAMinElapsedMs())
	assert.Equal(t, 0.8, cfg.OrchestratorETAMinFilesPerMinute())
	assert.Equal(t, 81.5, cfg.OrchestratorETAMinTokensPerMinute())
	assert.Equal(t, 82, cfg.OrchestratorETAMediumConfidenceAfterMs())
	assert.Equal(t, 83, cfg.OrchestratorETAHighConfidenceAfterMs())
	assert.Equal(t, 4, cfg.OrchestratorETAHighConfidenceMinStage())
	assert.Equal(t, 1.4, cfg.OrchestratorETAEstimatedFilesMultiplier())
	assert.Equal(t, 85, cfg.OrchestratorETAMinEstimatedFiles())
	assert.Equal(t, 8.6, cfg.OrchestratorETAFallbackRemainingMinutes())
	assert.Equal(t, 1.7, cfg.OrchestratorETAUncertaintyMultiplier())
	assert.Equal(t, 61, cfg.AgentStartupTimeoutMs())
	assert.Equal(t, 62, cfg.TeamWorkerPollIntervalMs())
}

func TestWorkflowConfig_CloneCopiesOrchestratorPointers(t *testing.T) {
	t.Parallel()

	jitter := 0
	original := &WorkflowConfig{
		Orchestrator: OrchestratorConfig{
			Backoff: OrchestratorBackoffConfig{JitterPercent: &jitter},
		},
	}

	cloned := original.Clone()
	require.NotNil(t, cloned.Orchestrator.Backoff.JitterPercent)
	*cloned.Orchestrator.Backoff.JitterPercent = 25

	assert.Equal(t, 0, *original.Orchestrator.Backoff.JitterPercent)
	assert.Equal(t, 25, *cloned.Orchestrator.Backoff.JitterPercent)
}
