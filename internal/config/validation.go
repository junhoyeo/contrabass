package config

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

const (
	LegacySchemaVersion  = 1
	CurrentSchemaVersion = 1
)

var (
	ErrInvalidConfig            = errors.New("invalid workflow config")
	ErrUnsupportedSchemaVersion = errors.New("unsupported workflow schema version")
)

// SchemaVersion returns the effective workflow schema version. Workflows that
// predate schema versioning are treated as version 1 for compatibility.
func (c *WorkflowConfig) SchemaVersion() int {
	if c == nil || (c.SchemaVersionRaw == 0 && !c.hasField("schema_version")) {
		return LegacySchemaVersion
	}
	return c.SchemaVersionRaw
}

func (c *WorkflowConfig) hasField(path string) bool {
	if c == nil {
		return false
	}
	_, ok := c.presentFields[path]
	return ok
}

// Validate checks explicit workflow values before they reach runtime
// consumers. Zero values remain valid because existing getters use them to
// select defaults; only explicit invalid ranges and unsupported enums fail.
func (c *WorkflowConfig) Validate() error {
	if c == nil {
		return nil
	}

	var validationErrors []error
	add := func(err error) {
		if err != nil {
			validationErrors = append(validationErrors, err)
		}
	}

	if (c.SchemaVersionRaw != 0 || c.hasField("schema_version")) &&
		(c.SchemaVersionRaw < LegacySchemaVersion || c.SchemaVersionRaw > CurrentSchemaVersion) {
		add(fmt.Errorf("%w: %d (current: %d)", ErrUnsupportedSchemaVersion, c.SchemaVersionRaw, CurrentSchemaVersion))
	}

	for _, field := range []struct {
		name  string
		value int
	}{
		{"max_concurrency", c.MaxConcurrencyRaw},
		{"poll_interval_ms", c.PollIntervalMsRaw},
		{"max_retry_backoff_ms", c.MaxRetryBackoffMsRaw},
		{"agent_timeout_ms", c.AgentTimeoutMsRaw},
		{"stall_timeout_ms", c.StallTimeoutMsRaw},
		{"polling.interval_ms", c.Polling.IntervalMs},
		{"agent.startup_timeout_ms", c.Agent.StartupTimeoutMs},
		{"opencode.port", c.OpenCode.Port},
		{"omx.poll_interval_ms", c.OMX.PollIntervalMs},
		{"omx.startup_timeout_ms", c.OMX.StartupTimeoutMs},
		{"omc.poll_interval_ms", c.OMC.PollIntervalMs},
		{"omc.startup_timeout_ms", c.OMC.StartupTimeoutMs},
		{"linear.sync_comments.queue_size", c.Linear.SyncComments.QueueSize},
		{"linear.sync_comments.poll_interval_ms", c.Linear.SyncComments.PollIntervalMs},
		{"team.max_workers", c.Team.MaxWorkers},
		{"team.max_fix_loops", c.Team.MaxFixLoops},
		{"team.claim_lease_seconds", c.Team.ClaimLeaseSeconds},
		{"team.worker_poll_interval_ms", c.Team.WorkerPollIntervalMs},
		{"orchestrator.event_buffer_size", c.Orchestrator.EventBufferSize},
		{"orchestrator.run_signal_buffer_size", c.Orchestrator.RunSignalBufferSize},
		{"orchestrator.issue_cache_size", c.Orchestrator.IssueCacheSize},
		{"orchestrator.run_shutdown_timeout_ms", c.Orchestrator.RunShutdownTimeoutMs},
		{"orchestrator.stop_grace_timeout_ms", c.Orchestrator.StopGraceTimeoutMs},
		{"orchestrator.git_command_timeout_ms", c.Orchestrator.GitCommandTimeoutMs},
		{"orchestrator.shutdown.drain_timeout_ms", c.Orchestrator.Shutdown.DrainTimeoutMs},
		{"orchestrator.shutdown.cleanup_timeout_ms", c.Orchestrator.Shutdown.CleanupTimeoutMs},
		{"orchestrator.shutdown.poll_interval_ms", c.Orchestrator.Shutdown.PollIntervalMs},
		{"orchestrator.backoff.continuation_ms", c.Orchestrator.Backoff.ContinuationMs},
		{"orchestrator.backoff.failure_base_ms", c.Orchestrator.Backoff.FailureBaseMs},
		{"orchestrator.backoff.multiplier", c.Orchestrator.Backoff.Multiplier},
		{"orchestrator.snapshot.diff_timeout_ms", c.Orchestrator.Snapshot.DiffTimeoutMs},
		{"orchestrator.snapshot.stage.testing_after_ms", c.Orchestrator.Snapshot.Stage.TestingAfterMs},
		{"orchestrator.snapshot.stage.reviewing_after_ms", c.Orchestrator.Snapshot.Stage.ReviewingAfterMs},
		{"orchestrator.snapshot.eta.min_elapsed_ms", c.Orchestrator.Snapshot.ETA.MinElapsedMs},
		{"orchestrator.snapshot.eta.medium_confidence_after_ms", c.Orchestrator.Snapshot.ETA.MediumConfidenceAfterMs},
		{"orchestrator.snapshot.eta.high_confidence_after_ms", c.Orchestrator.Snapshot.ETA.HighConfidenceAfterMs},
		{"orchestrator.snapshot.eta.high_confidence_min_stage", c.Orchestrator.Snapshot.ETA.HighConfidenceMinStage},
		{"orchestrator.snapshot.eta.min_estimated_files", c.Orchestrator.Snapshot.ETA.MinEstimatedFiles},
	} {
		if field.value < 0 {
			add(fmt.Errorf("%s must be greater than or equal to zero", field.name))
		}
	}

	if c.OpenCode.Port > 65_535 {
		add(fmt.Errorf("opencode.port must be between 0 and 65535"))
	}
	if c.Orchestrator.Backoff.Multiplier != 0 && c.Orchestrator.Backoff.Multiplier < 1 {
		add(fmt.Errorf("orchestrator.backoff.multiplier must be at least 1"))
	}
	if jitter := c.Orchestrator.Backoff.JitterPercent; jitter != nil && (*jitter < 0 || *jitter > 100) {
		add(fmt.Errorf("orchestrator.backoff.jitter_percent must be between 0 and 100"))
	}

	for _, field := range []struct {
		name  string
		value float64
	}{
		{"orchestrator.snapshot.stage.reviewing_max_tokens_per_minute", c.Orchestrator.Snapshot.Stage.ReviewingMaxTokensPerMinute},
		{"orchestrator.snapshot.eta.min_files_per_minute", c.Orchestrator.Snapshot.ETA.MinFilesPerMinute},
		{"orchestrator.snapshot.eta.min_tokens_per_minute", c.Orchestrator.Snapshot.ETA.MinTokensPerMinute},
		{"orchestrator.snapshot.eta.estimated_files_multiplier", c.Orchestrator.Snapshot.ETA.EstimatedFilesMultiplier},
		{"orchestrator.snapshot.eta.fallback_remaining_minutes", c.Orchestrator.Snapshot.ETA.FallbackRemainingMinutes},
		{"orchestrator.snapshot.eta.uncertainty_multiplier", c.Orchestrator.Snapshot.ETA.UncertaintyMultiplier},
	} {
		if math.IsNaN(field.value) || math.IsInf(field.value, 0) {
			add(fmt.Errorf("%s must be finite", field.name))
		} else if field.value < 0 {
			add(fmt.Errorf("%s must be greater than or equal to zero", field.name))
		}
	}

	add(validateEnum("polling.backoff_strategy", c.Polling.BackoffStrategy, "linear", defaultBackoffStrategy))
	add(validateEnum("team.execution_mode", c.Team.ExecutionMode, TeamExecutionModeAuto, TeamExecutionModeTeam, TeamExecutionModeSingle, "agent", "orchestrator"))
	add(validateEnum("team.worker_mode", c.Team.WorkerMode, "goroutine", "tmux"))
	add(validateLinearConfig(c))

	if c.OrchestratorStageReviewingAfterMs() < c.OrchestratorStageTestingAfterMs() {
		add(fmt.Errorf("orchestrator.snapshot.stage.reviewing_after_ms must be greater than or equal to testing_after_ms"))
	}
	if c.OrchestratorETAHighConfidenceAfterMs() < c.OrchestratorETAMediumConfidenceAfterMs() {
		add(fmt.Errorf("orchestrator.snapshot.eta.high_confidence_after_ms must be greater than or equal to medium_confidence_after_ms"))
	}

	if len(validationErrors) == 0 {
		return nil
	}
	return errors.Join(append([]error{ErrInvalidConfig}, validationErrors...)...)
}

func validateEnum(name, value string, allowed ...string) error {
	normalized := strings.TrimSpace(strings.ToLower(value))
	if normalized == "" {
		return nil
	}
	for _, candidate := range allowed {
		if normalized == candidate {
			return nil
		}
	}
	return fmt.Errorf("%s has an unsupported value (valid values: %s)", name, strings.Join(allowed, ", "))
}
