package config

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
)

const RedactedValue = "[REDACTED]"

type ConfigSource string

const (
	ConfigSourceDefault         ConfigSource = "default"
	ConfigSourceExplicit        ConfigSource = "explicit"
	ConfigSourceDerived         ConfigSource = "derived"
	ConfigSourceDeprecatedAlias ConfigSource = "deprecated_alias"
)

type ReloadPolicy string

const (
	StartupOnly ReloadPolicy = "startup_only"
	Reloadable  ReloadPolicy = "reloadable"
)

// FieldMetadata explains how an effective value was selected and whether a
// running process can observe changes without being restarted.
type FieldMetadata struct {
	Source       ConfigSource `json:"source" yaml:"source"`
	SourcePath   string       `json:"source_path,omitempty" yaml:"source_path,omitempty"`
	ReloadPolicy ReloadPolicy `json:"reload_policy" yaml:"reload_policy"`
	Sensitive    bool         `json:"sensitive,omitempty" yaml:"sensitive,omitempty"`
	Note         string       `json:"note,omitempty" yaml:"note,omitempty"`
}

// ResolvedConfig is a canonical snapshot of values consumed by the current
// runtime. Its secret-bearing value map is private; internal callers must opt
// in through UnredactedValues, while all serialization is redacted.
type ResolvedConfig struct {
	SchemaVersion int                      `json:"schema_version" yaml:"schema_version"`
	Metadata      map[string]FieldMetadata `json:"metadata" yaml:"metadata"`
	Warnings      []string                 `json:"warnings,omitempty" yaml:"warnings,omitempty"`
	values        map[string]any
	sensitive     []string
}

// EffectiveConfig is a display-safe resolved configuration. Sensitive values
// are always replaced with RedactedValue.
type EffectiveConfig struct {
	SchemaVersion int                      `json:"schema_version" yaml:"schema_version"`
	Values        map[string]any           `json:"values" yaml:"values"`
	Metadata      map[string]FieldMetadata `json:"metadata" yaml:"metadata"`
	Warnings      []string                 `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}

// MarshalJSON prevents accidental serialization of the unredacted resolved
// snapshot. Programmatic callers can still read Values intentionally.
func (r ResolvedConfig) MarshalJSON() ([]byte, error) {
	return json.Marshal((&r).Effective())
}

// MarshalYAML prevents accidental YAML serialization of unredacted secrets.
func (r ResolvedConfig) MarshalYAML() (any, error) {
	return (&r).Effective(), nil
}

// Resolve validates cfg and builds a canonical runtime snapshot without
// changing the legacy WorkflowConfig getters used by existing consumers.
func Resolve(cfg *WorkflowConfig) (*ResolvedConfig, error) {
	if cfg == nil {
		cfg = &WorkflowConfig{}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	resolved := &ResolvedConfig{
		SchemaVersion: cfg.SchemaVersion(),
		Metadata:      make(map[string]FieldMetadata),
		values:        make(map[string]any),
	}
	add := func(path string, value any, source ConfigSource, sourcePath string, policy ReloadPolicy, sensitive bool, note string) {
		setNestedValue(resolved.values, path, value)
		resolved.Metadata[path] = FieldMetadata{
			Source:       source,
			SourcePath:   sourcePath,
			ReloadPolicy: policy,
			Sensitive:    sensitive,
			Note:         note,
		}
		if sensitive {
			resolved.sensitive = append(resolved.sensitive, path)
		}
	}

	resolved.Metadata["schema_version"] = FieldMetadata{
		Source:       sourceFor(cfg.SchemaVersionRaw != 0 || cfg.hasField("schema_version")),
		SourcePath:   "schema_version",
		ReloadPolicy: StartupOnly,
	}

	model, _ := cfg.Model()
	projectURL, _ := cfg.ProjectURL()
	pollTopSource, pollTopSourcePath := ConfigSourceDefault, "default"
	if cfg.PollIntervalMsRaw > 0 {
		pollTopSource, pollTopSourcePath = ConfigSourceExplicit, "poll_interval_ms"
	} else if cfg.Polling.IntervalMs > 0 {
		pollTopSource, pollTopSourcePath = ConfigSourceDerived, "polling.interval_ms"
	}
	add("max_concurrency", cfg.MaxConcurrency(), sourceFor(cfg.MaxConcurrencyRaw != 0), "max_concurrency", Reloadable, false, "")
	add("poll_interval_ms", cfg.PollIntervalMs(), pollTopSource, pollTopSourcePath, StartupOnly, false, "poll ticker is constructed at startup")
	add("max_retry_backoff_ms", cfg.MaxRetryBackoffMs(), sourceFor(cfg.MaxRetryBackoffMsRaw != 0), "max_retry_backoff_ms", Reloadable, false, "")
	add("model", model, modelSource(cfg), modelSourcePath(cfg), Reloadable, false, "")
	add("project_url", projectURL, projectURLSource(cfg), projectURLSourcePath(cfg), StartupOnly, false, "tracker client changes require restart")
	add("agent_timeout_ms", cfg.AgentTimeoutMs(), sourceFor(cfg.AgentTimeoutMsRaw != 0), "agent_timeout_ms", Reloadable, false, "")
	add("stall_timeout_ms", cfg.StallTimeoutMs(), sourceFor(cfg.StallTimeoutMsRaw != 0), "stall_timeout_ms", Reloadable, false, "")

	add("tracker.type", cfg.TrackerType(), sourceFor(cfg.Tracker.Type != ""), "tracker.type", StartupOnly, false, "tracker implementation is constructed at startup")
	add("tracker.project_url", cfg.TrackerProjectURL(), trackerProjectURLSource(cfg), trackerProjectURLSourcePath(cfg), StartupOnly, false, "tracker client changes require restart")
	add("tracker.team_id", cfg.TrackerTeamID(), sourceFor(cfg.Tracker.TeamID != ""), "tracker.team_id", StartupOnly, false, "")
	add("tracker.assignee_id", cfg.TrackerAssigneeID(), sourceFor(cfg.Tracker.AssigneeID != ""), "tracker.assignee_id", StartupOnly, false, "")
	add("tracker.board_dir", cfg.LocalBoardDir(), sourceFor(cfg.Tracker.BoardDir != ""), "tracker.board_dir", StartupOnly, false, "")
	add("tracker.issue_prefix", cfg.LocalIssuePrefix(), sourceFor(cfg.Tracker.IssuePrefix != ""), "tracker.issue_prefix", StartupOnly, false, "")
	add("tracker.owner", cfg.GitHubOwner(), sourceFor(cfg.Tracker.Owner != ""), "tracker.owner", StartupOnly, false, "")
	add("tracker.repo", cfg.GitHubRepo(), sourceFor(cfg.Tracker.Repo != ""), "tracker.repo", StartupOnly, false, "")
	add("tracker.labels", slices.Clone(cfg.GitHubLabels()), sourceFor(len(cfg.Tracker.Labels) > 0), "tracker.labels", StartupOnly, false, "")
	add("tracker.assignee", cfg.GitHubAssignee(), sourceFor(cfg.Tracker.Assignee != ""), "tracker.assignee", StartupOnly, false, "")
	add("tracker.token", cfg.GitHubToken(), sourceFor(cfg.Tracker.Token != ""), "tracker.token", StartupOnly, true, "")
	add("tracker.endpoint", cfg.GitHubEndpoint(), sourceFor(cfg.Tracker.Endpoint != ""), "tracker.endpoint", StartupOnly, false, "")

	pollSource, pollSourcePath := ConfigSourceDefault, "default"
	if cfg.PollIntervalMsRaw > 0 {
		pollSource, pollSourcePath = ConfigSourceDeprecatedAlias, "poll_interval_ms"
		resolved.Warnings = append(resolved.Warnings, "poll_interval_ms is a compatibility alias; prefer polling.interval_ms")
		if cfg.Polling.IntervalMs > 0 {
			resolved.Warnings = append(resolved.Warnings, "poll_interval_ms overrides polling.interval_ms")
		}
	} else if cfg.Polling.IntervalMs > 0 {
		pollSource, pollSourcePath = ConfigSourceExplicit, "polling.interval_ms"
	}
	add("polling.interval_ms", cfg.PollIntervalMs(), pollSource, pollSourcePath, StartupOnly, false, "poll ticker is constructed at startup")
	add("polling.backoff_strategy", cfg.PollingBackoffStrategy(), sourceFor(cfg.Polling.BackoffStrategy != ""), "polling.backoff_strategy", Reloadable, false, "")

	add("workspace.base_dir", cfg.WorkspaceBaseDir(), sourceFor(cfg.Workspace.BaseDir != ""), "workspace.base_dir", StartupOnly, false, "workspace manager is constructed at startup")
	add("workspace.branch_prefix", cfg.Workspace.BranchPrefix, sourceFor(cfg.Workspace.BranchPrefix != ""), "workspace.branch_prefix", StartupOnly, false, "parsed for compatibility; current trackers provide branch names")
	add("hooks.before_run", cfg.HookBeforeRun(), sourceFor(cfg.Hooks.BeforeRun != ""), "hooks.before_run", StartupOnly, false, "parsed but hook execution is not implemented")
	add("hooks.after_run", cfg.HookAfterRun(), sourceFor(cfg.Hooks.AfterRun != ""), "hooks.after_run", StartupOnly, false, "parsed but hook execution is not implemented")
	add("hooks.before_remove", cfg.HookBeforeRemove(), sourceFor(cfg.Hooks.BeforeRemove != ""), "hooks.before_remove", StartupOnly, false, "parsed but hook execution is not implemented")

	add("codex.binary_path", cfg.CodexBinaryPath(), sourceFor(cfg.Codex.BinaryPath != ""), "codex.binary_path", StartupOnly, false, "runner process is constructed at startup")
	add("codex.model", cfg.CodexModel(), codexModelSource(cfg), codexModelSourcePath(cfg), Reloadable, false, "")
	add("codex.approval_policy", cfg.CodexApprovalPolicy(), sourceFor(cfg.Codex.ApprovalPolicy != ""), "codex.approval_policy", StartupOnly, false, "runner process is constructed at startup")
	add("codex.sandbox", cfg.CodexSandbox(), sourceFor(cfg.Codex.Sandbox != ""), "codex.sandbox", StartupOnly, false, "runner process is constructed at startup")
	add("agent.type", cfg.AgentType(), sourceFor(cfg.Agent.Type != ""), "agent.type", StartupOnly, false, "runner implementation is selected at startup")
	add("agent.startup_timeout_ms", cfg.AgentStartupTimeoutMs(), sourceFor(cfg.Agent.StartupTimeoutMs != 0), "agent.startup_timeout_ms", StartupOnly, false, "")

	add("opencode.binary_path", cfg.OpenCodeBinaryPath(), sourceFor(cfg.OpenCode.BinaryPath != ""), "opencode.binary_path", StartupOnly, false, "")
	add("opencode.port", cfg.OpenCodePort(), sourceFor(cfg.OpenCode.Port != 0), "opencode.port", StartupOnly, false, "")
	add("opencode.password", cfg.OpenCodePassword(), sourceFor(cfg.OpenCode.Password != ""), "opencode.password", StartupOnly, true, "")
	add("opencode.username", cfg.OpenCodeUsername(), sourceFor(cfg.OpenCode.Username != ""), "opencode.username", StartupOnly, false, "")

	add("omx.binary_path", cfg.OMXBinaryPath(), sourceFor(cfg.OMX.BinaryPath != ""), "omx.binary_path", StartupOnly, false, "")
	add("omx.team_spec", cfg.OMXTeamSpec(), sourceFor(cfg.OMX.TeamSpec != ""), "omx.team_spec", StartupOnly, false, "")
	add("omx.poll_interval_ms", cfg.OMXPollIntervalMs(), sourceFor(cfg.OMX.PollIntervalMs != 0), "omx.poll_interval_ms", StartupOnly, false, "")
	add("omx.startup_timeout_ms", cfg.OMXStartupTimeoutMs(), sourceFor(cfg.OMX.StartupTimeoutMs != 0), "omx.startup_timeout_ms", StartupOnly, false, "")
	add("omx.ralph", cfg.OMXRalph(), sourceFor(cfg.OMX.Ralph || cfg.hasField("omx.ralph")), "omx.ralph", StartupOnly, false, "")
	add("omc.binary_path", cfg.OMCBinaryPath(), sourceFor(cfg.OMC.BinaryPath != ""), "omc.binary_path", StartupOnly, false, "")
	add("omc.team_spec", cfg.OMCTeamSpec(), sourceFor(cfg.OMC.TeamSpec != ""), "omc.team_spec", StartupOnly, false, "")
	add("omc.poll_interval_ms", cfg.OMCPollIntervalMs(), sourceFor(cfg.OMC.PollIntervalMs != 0), "omc.poll_interval_ms", StartupOnly, false, "")
	add("omc.startup_timeout_ms", cfg.OMCStartupTimeoutMs(), sourceFor(cfg.OMC.StartupTimeoutMs != 0), "omc.startup_timeout_ms", StartupOnly, false, "")

	add("linear.issue_details.enabled", cfg.LinearIssueDetailsEnabled(), sourceFor(cfg.Linear.IssueDetails.Enabled != nil), "linear.issue_details.enabled", StartupOnly, false, "")
	add("linear.sync_comments.enabled", cfg.LinearSyncCommentsEnabled(), sourceFor(cfg.Linear.SyncComments.Enabled || cfg.hasField("linear.sync_comments.enabled")), "linear.sync_comments.enabled", StartupOnly, false, "")
	add("linear.sync_comments.mode", cfg.LinearSyncCommentsMode(), sourceFor(cfg.Linear.SyncComments.Mode != ""), "linear.sync_comments.mode", StartupOnly, false, "")
	add("linear.sync_comments.queue_size", cfg.LinearSyncCommentsQueueSize(), sourceFor(cfg.Linear.SyncComments.QueueSize != 0), "linear.sync_comments.queue_size", StartupOnly, false, "")
	add("linear.sync_comments.poll_interval_ms", cfg.LinearSyncCommentsPollIntervalMs(), sourceFor(cfg.Linear.SyncComments.PollIntervalMs != 0), "linear.sync_comments.poll_interval_ms", StartupOnly, false, "")

	add("oh_my_opencode.plugin_version", cfg.OhMyOpenCodePluginVersion(), sourceFor(cfg.OhMyOpenCode.PluginVersion != ""), "oh_my_opencode.plugin_version", StartupOnly, false, "")
	add("oh_my_opencode.plugins", slices.Clone(cfg.OhMyOpenCodePlugins()), sourceFor(len(cfg.OhMyOpenCode.Plugins) > 0), "oh_my_opencode.plugins", StartupOnly, false, "")
	add("oh_my_opencode.agents", agentValues(cfg.OhMyOpenCodeAgents()), sourceFor(len(cfg.OhMyOpenCode.Agents) > 0), "oh_my_opencode.agents", StartupOnly, false, "")
	add("oh_my_opencode.categories", categoryValues(cfg.OhMyOpenCodeCategories()), sourceFor(len(cfg.OhMyOpenCode.Categories) > 0), "oh_my_opencode.categories", StartupOnly, false, "")
	add("oh_my_opencode.provider.name", cfg.OhMyOpenCodeProviderName(), sourceFor(cfg.OhMyOpenCode.Provider.Name != ""), "oh_my_opencode.provider.name", StartupOnly, false, "")
	add("oh_my_opencode.provider.base_url", cfg.OhMyOpenCodeProviderBaseURL(), sourceFor(cfg.OhMyOpenCode.Provider.BaseURL != ""), "oh_my_opencode.provider.base_url", StartupOnly, false, "")
	add("oh_my_opencode.provider.api_key", cfg.OhMyOpenCodeProviderAPIKey(), sourceFor(cfg.OhMyOpenCode.Provider.APIKey != ""), "oh_my_opencode.provider.api_key", StartupOnly, true, "")

	add("team.max_workers", cfg.TeamMaxWorkers(), sourceFor(cfg.Team.MaxWorkers != 0), "team.max_workers", StartupOnly, false, "")
	add("team.max_fix_loops", cfg.TeamMaxFixLoops(), sourceFor(cfg.Team.MaxFixLoops != 0), "team.max_fix_loops", StartupOnly, false, "")
	add("team.claim_lease_seconds", cfg.TeamClaimLeaseSeconds(), sourceFor(cfg.Team.ClaimLeaseSeconds != 0), "team.claim_lease_seconds", StartupOnly, false, "")
	add("team.state_dir", cfg.TeamStateDir(), sourceFor(cfg.Team.StateDir != ""), "team.state_dir", StartupOnly, false, "")
	add("team.execution_mode", cfg.TeamExecutionMode(), sourceFor(cfg.Team.ExecutionMode != ""), "team.execution_mode", StartupOnly, false, "root execution mode is selected at startup")
	add("team.worker_mode", cfg.WorkerMode(), sourceFor(cfg.Team.WorkerMode != ""), "team.worker_mode", StartupOnly, false, "")
	add("team.worker_poll_interval_ms", cfg.TeamWorkerPollIntervalMs(), sourceFor(cfg.Team.WorkerPollIntervalMs != 0), "team.worker_poll_interval_ms", StartupOnly, false, "")
	add("timeline.dir", cfg.WorkflowTimelineDir(), sourceFor(cfg.Timeline.Dir != ""), "timeline.dir", StartupOnly, false, "timeline store is constructed at startup")

	addOrchestratorFields(cfg, add)
	if cfg.Codex.Model == "" && cfg.ModelRaw != "" {
		resolved.Warnings = append(resolved.Warnings, "codex.model falls back to model; configure codex.model to make the runner model explicit")
	}
	if cfg.Tracker.ProjectURL == "" && cfg.ProjectURLRaw != "" {
		resolved.Warnings = append(resolved.Warnings, "tracker.project_url falls back to project_url; configure tracker.project_url to make tracker scope explicit")
	}
	return resolved, nil
}

// Effective returns a deep-copied, display-safe snapshot.
func (r *ResolvedConfig) Effective() *EffectiveConfig {
	if r == nil {
		return nil
	}
	values := cloneStringMap(r.values)
	for _, path := range r.sensitive {
		if value, ok := nestedValueAt(values, path); ok && !isEmptyValue(value) {
			setNestedValue(values, path, RedactedValue)
		}
	}
	return &EffectiveConfig{
		SchemaVersion: r.SchemaVersion,
		Values:        values,
		Metadata:      maps.Clone(r.Metadata),
		Warnings:      slices.Clone(r.Warnings),
	}
}

// UnredactedValues returns a deep copy for internal callers that explicitly
// need secret-bearing runtime values. Display and serialization code should use
// Effective instead.
func (r *ResolvedConfig) UnredactedValues() map[string]any {
	if r == nil {
		return nil
	}
	return cloneStringMap(r.values)
}

func BuildEffectiveConfig(cfg *WorkflowConfig) (*EffectiveConfig, error) {
	resolved, err := Resolve(cfg)
	if err != nil {
		return nil, err
	}
	return resolved.Effective(), nil
}

type addResolvedField func(path string, value any, source ConfigSource, sourcePath string, policy ReloadPolicy, sensitive bool, note string)

func addOrchestratorFields(cfg *WorkflowConfig, add addResolvedField) {
	add("orchestrator.event_buffer_size", cfg.OrchestratorEventBufferSize(), sourceFor(cfg.Orchestrator.EventBufferSize != 0), "orchestrator.event_buffer_size", StartupOnly, false, "channel capacity is fixed at startup")
	add("orchestrator.run_signal_buffer_size", cfg.OrchestratorRunSignalBufferSize(), sourceFor(cfg.Orchestrator.RunSignalBufferSize != 0), "orchestrator.run_signal_buffer_size", StartupOnly, false, "channel capacity is fixed at startup")
	add("orchestrator.issue_cache_size", cfg.OrchestratorIssueCacheSize(), sourceFor(cfg.Orchestrator.IssueCacheSize != 0), "orchestrator.issue_cache_size", StartupOnly, false, "cache capacity is fixed at startup")
	add("orchestrator.run_shutdown_timeout_ms", cfg.OrchestratorRunShutdownTimeoutMs(), sourceFor(cfg.Orchestrator.RunShutdownTimeoutMs != 0), "orchestrator.run_shutdown_timeout_ms", Reloadable, false, "")
	add("orchestrator.stop_grace_timeout_ms", cfg.OrchestratorStopGraceTimeoutMs(), sourceFor(cfg.Orchestrator.StopGraceTimeoutMs != 0), "orchestrator.stop_grace_timeout_ms", Reloadable, false, "")
	add("orchestrator.git_command_timeout_ms", cfg.OrchestratorGitCommandTimeoutMs(), sourceFor(cfg.Orchestrator.GitCommandTimeoutMs != 0), "orchestrator.git_command_timeout_ms", Reloadable, false, "")
	add("orchestrator.shutdown.drain_timeout_ms", cfg.OrchestratorShutdownDrainTimeoutMs(), sourceFor(cfg.Orchestrator.Shutdown.DrainTimeoutMs != 0), "orchestrator.shutdown.drain_timeout_ms", Reloadable, false, "")
	add("orchestrator.shutdown.cleanup_timeout_ms", cfg.OrchestratorShutdownCleanupTimeoutMs(), sourceFor(cfg.Orchestrator.Shutdown.CleanupTimeoutMs != 0), "orchestrator.shutdown.cleanup_timeout_ms", Reloadable, false, "")
	add("orchestrator.shutdown.poll_interval_ms", cfg.OrchestratorShutdownPollIntervalMs(), sourceFor(cfg.Orchestrator.Shutdown.PollIntervalMs != 0), "orchestrator.shutdown.poll_interval_ms", Reloadable, false, "")
	add("orchestrator.backoff.continuation_ms", cfg.OrchestratorContinuationBackoffMs(), sourceFor(cfg.Orchestrator.Backoff.ContinuationMs != 0), "orchestrator.backoff.continuation_ms", Reloadable, false, "")
	add("orchestrator.backoff.failure_base_ms", cfg.OrchestratorFailureBackoffBaseMs(), sourceFor(cfg.Orchestrator.Backoff.FailureBaseMs != 0), "orchestrator.backoff.failure_base_ms", Reloadable, false, "")
	add("orchestrator.backoff.multiplier", cfg.OrchestratorFailureBackoffMultiplier(), sourceFor(cfg.Orchestrator.Backoff.Multiplier != 0), "orchestrator.backoff.multiplier", Reloadable, false, "")
	add("orchestrator.backoff.jitter_percent", cfg.OrchestratorBackoffJitterPercent(), sourceFor(cfg.Orchestrator.Backoff.JitterPercent != nil), "orchestrator.backoff.jitter_percent", Reloadable, false, "")
	add("orchestrator.snapshot.diff_timeout_ms", cfg.OrchestratorSnapshotDiffTimeoutMs(), sourceFor(cfg.Orchestrator.Snapshot.DiffTimeoutMs != 0), "orchestrator.snapshot.diff_timeout_ms", Reloadable, false, "")
	add("orchestrator.snapshot.stage.testing_after_ms", cfg.OrchestratorStageTestingAfterMs(), sourceFor(cfg.Orchestrator.Snapshot.Stage.TestingAfterMs != 0), "orchestrator.snapshot.stage.testing_after_ms", Reloadable, false, "")
	add("orchestrator.snapshot.stage.reviewing_after_ms", cfg.OrchestratorStageReviewingAfterMs(), sourceFor(cfg.Orchestrator.Snapshot.Stage.ReviewingAfterMs != 0), "orchestrator.snapshot.stage.reviewing_after_ms", Reloadable, false, "")
	add("orchestrator.snapshot.stage.reviewing_max_tokens_per_minute", cfg.OrchestratorStageReviewingMaxTokensPerMinute(), sourceFor(cfg.Orchestrator.Snapshot.Stage.ReviewingMaxTokensPerMinute != 0), "orchestrator.snapshot.stage.reviewing_max_tokens_per_minute", Reloadable, false, "")
	add("orchestrator.snapshot.eta.min_elapsed_ms", cfg.OrchestratorETAMinElapsedMs(), sourceFor(cfg.Orchestrator.Snapshot.ETA.MinElapsedMs != 0), "orchestrator.snapshot.eta.min_elapsed_ms", Reloadable, false, "")
	add("orchestrator.snapshot.eta.min_files_per_minute", cfg.OrchestratorETAMinFilesPerMinute(), sourceFor(cfg.Orchestrator.Snapshot.ETA.MinFilesPerMinute != 0), "orchestrator.snapshot.eta.min_files_per_minute", Reloadable, false, "")
	add("orchestrator.snapshot.eta.min_tokens_per_minute", cfg.OrchestratorETAMinTokensPerMinute(), sourceFor(cfg.Orchestrator.Snapshot.ETA.MinTokensPerMinute != 0), "orchestrator.snapshot.eta.min_tokens_per_minute", Reloadable, false, "")
	add("orchestrator.snapshot.eta.medium_confidence_after_ms", cfg.OrchestratorETAMediumConfidenceAfterMs(), sourceFor(cfg.Orchestrator.Snapshot.ETA.MediumConfidenceAfterMs != 0), "orchestrator.snapshot.eta.medium_confidence_after_ms", Reloadable, false, "")
	add("orchestrator.snapshot.eta.high_confidence_after_ms", cfg.OrchestratorETAHighConfidenceAfterMs(), sourceFor(cfg.Orchestrator.Snapshot.ETA.HighConfidenceAfterMs != 0), "orchestrator.snapshot.eta.high_confidence_after_ms", Reloadable, false, "")
	add("orchestrator.snapshot.eta.high_confidence_min_stage", cfg.OrchestratorETAHighConfidenceMinStage(), sourceFor(cfg.Orchestrator.Snapshot.ETA.HighConfidenceMinStage != 0), "orchestrator.snapshot.eta.high_confidence_min_stage", Reloadable, false, "")
	add("orchestrator.snapshot.eta.estimated_files_multiplier", cfg.OrchestratorETAEstimatedFilesMultiplier(), sourceFor(cfg.Orchestrator.Snapshot.ETA.EstimatedFilesMultiplier != 0), "orchestrator.snapshot.eta.estimated_files_multiplier", Reloadable, false, "")
	add("orchestrator.snapshot.eta.min_estimated_files", cfg.OrchestratorETAMinEstimatedFiles(), sourceFor(cfg.Orchestrator.Snapshot.ETA.MinEstimatedFiles != 0), "orchestrator.snapshot.eta.min_estimated_files", Reloadable, false, "")
	add("orchestrator.snapshot.eta.fallback_remaining_minutes", cfg.OrchestratorETAFallbackRemainingMinutes(), sourceFor(cfg.Orchestrator.Snapshot.ETA.FallbackRemainingMinutes != 0), "orchestrator.snapshot.eta.fallback_remaining_minutes", Reloadable, false, "")
	add("orchestrator.snapshot.eta.uncertainty_multiplier", cfg.OrchestratorETAUncertaintyMultiplier(), sourceFor(cfg.Orchestrator.Snapshot.ETA.UncertaintyMultiplier != 0), "orchestrator.snapshot.eta.uncertainty_multiplier", Reloadable, false, "")
}

func sourceFor(explicit bool) ConfigSource {
	if explicit {
		return ConfigSourceExplicit
	}
	return ConfigSourceDefault
}

func modelSource(cfg *WorkflowConfig) ConfigSource {
	if cfg.ModelRaw != "" {
		return ConfigSourceExplicit
	}
	if cfg.Codex.Model != "" {
		return ConfigSourceDerived
	}
	return ConfigSourceDefault
}

func modelSourcePath(cfg *WorkflowConfig) string {
	if cfg.ModelRaw != "" {
		return "model"
	}
	if cfg.Codex.Model != "" {
		return "codex.model"
	}
	return "default"
}

func projectURLSource(cfg *WorkflowConfig) ConfigSource {
	if cfg.ProjectURLRaw != "" {
		return ConfigSourceExplicit
	}
	if cfg.Tracker.ProjectURL != "" {
		return ConfigSourceDerived
	}
	return ConfigSourceDefault
}

func projectURLSourcePath(cfg *WorkflowConfig) string {
	if cfg.ProjectURLRaw != "" {
		return "project_url"
	}
	if cfg.Tracker.ProjectURL != "" {
		return "tracker.project_url"
	}
	return "default"
}

func trackerProjectURLSource(cfg *WorkflowConfig) ConfigSource {
	if cfg.Tracker.ProjectURL != "" {
		return ConfigSourceExplicit
	}
	if cfg.ProjectURLRaw != "" {
		return ConfigSourceDeprecatedAlias
	}
	return ConfigSourceDefault
}

func trackerProjectURLSourcePath(cfg *WorkflowConfig) string {
	if cfg.Tracker.ProjectURL != "" {
		return "tracker.project_url"
	}
	if cfg.ProjectURLRaw != "" {
		return "project_url"
	}
	return "default"
}

func codexModelSource(cfg *WorkflowConfig) ConfigSource {
	if cfg.Codex.Model != "" {
		return ConfigSourceExplicit
	}
	if cfg.ModelRaw != "" {
		return ConfigSourceDeprecatedAlias
	}
	return ConfigSourceDefault
}

func codexModelSourcePath(cfg *WorkflowConfig) string {
	if cfg.Codex.Model != "" {
		return "codex.model"
	}
	if cfg.ModelRaw != "" {
		return "model"
	}
	return "default"
}

func agentValues(agents map[string]OhMyOpenCodeAgent) map[string]any {
	values := make(map[string]any, len(agents))
	for name, agent := range agents {
		values[name] = map[string]any{"model": agent.Model}
	}
	return values
}

func categoryValues(categories map[string]OhMyOpenCodeCategory) map[string]any {
	values := make(map[string]any, len(categories))
	for name, category := range categories {
		values[name] = map[string]any{"model": category.Model}
	}
	return values
}

func setNestedValue(values map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	current := values
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok {
			next = make(map[string]any)
			current[part] = next
		}
		current = next
	}
	current[parts[len(parts)-1]] = value
}

func nestedValueAt(values map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var current any = values
	for _, part := range parts {
		mapping, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = mapping[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func cloneStringMap(values map[string]any) map[string]any {
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = cloneConfigValue(value)
	}
	return cloned
}

func cloneConfigValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneStringMap(typed)
	case []string:
		return slices.Clone(typed)
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneConfigValue(item)
		}
		return cloned
	default:
		return value
	}
}

func isEmptyValue(value any) bool {
	stringValue, ok := value.(string)
	return ok && stringValue == ""
}
