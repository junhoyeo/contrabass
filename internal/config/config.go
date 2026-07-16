package config

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

const (
	defaultMaxConcurrency    = 10
	defaultPollIntervalMs    = 30_000
	defaultMaxRetryBackoffMs = 300_000
	defaultAgentTimeoutMs    = 600_000
	defaultStallTimeoutMs    = 120_000
	defaultTrackerType       = "internal"
	defaultBackoffStrategy   = "exponential"
	defaultWorkspaceBaseDir  = "."
	defaultBranchPrefix      = "symphony/"
	defaultCodexBinaryPath   = "codex app-server"
	// defaultApprovalPolicy and defaultSandbox are intentionally empty.
	// Earlier values ("auto-edit" / "docker") were not valid codex TOML
	// settings — codex accepts approval_policy values like "never" /
	// "on-request" and sandbox_mode values like "workspace-write". Keeping
	// these empty means contrabass injects nothing when a workflow does not
	// set them, so codex falls back to whatever lives in
	// ~/.codex/config.toml. Operators opt in via codex.approval_policy and
	// codex.sandbox in their workflow file.
	defaultApprovalPolicy        = ""
	defaultSandbox               = ""
	defaultAgentType             = "codex"
	defaultAgentStartupTimeoutMs = 30_000
	defaultOpenCodeBinaryPath    = "opencode serve"
	defaultOMXBinaryPath         = "omx"
	defaultOMXTeamSpec           = "1:executor"
	defaultOMXPollIntervalMs     = 1000
	defaultOMXStartupTimeoutMs   = 15000
	defaultOMCBinaryPath         = "omc"
	defaultOMCTeamSpec           = "1:claude"
	defaultOMCPollIntervalMs     = 1000
	defaultOMCStartupTimeoutMs   = 15000
	defaultGitHubEndpoint        = "https://api.github.com"
	defaultLocalBoardDir         = ".contrabass/board"
	defaultLocalIssuePrefix      = "CB"

	defaultOhMyOpenCodePluginVersion = "oh-my-opencode"

	defaultTeamMaxWorkers           = 5
	defaultTeamMaxFixLoops          = 3
	defaultTeamClaimLeaseSeconds    = 300
	defaultTeamStateDir             = ".contrabass/state/team"
	defaultTeamExecutionMode        = TeamExecutionModeTeam
	defaultTeamWorkerMode           = "tmux"
	defaultTeamWorkerPollIntervalMs = 2_000
	defaultWorkflowTimelineDir      = ".contrabass/state/workflow-timeline"

	defaultOrchestratorEventBufferSize                  = 256
	defaultOrchestratorRunSignalBufferSize              = 256
	defaultOrchestratorIssueCacheSize                   = 1000
	defaultOrchestratorRunShutdownTimeoutMs             = 5_000
	defaultOrchestratorStopGraceTimeoutMs               = 5_000
	defaultOrchestratorGitCommandTimeoutMs              = 2_000
	defaultOrchestratorShutdownDrainTimeoutMs           = 30_000
	defaultOrchestratorShutdownCleanupTimeoutMs         = 10_000
	defaultOrchestratorShutdownPollIntervalMs           = 10
	defaultOrchestratorContinuationBackoffMs            = 1_000
	defaultOrchestratorFailureBackoffBaseMs             = 10_000
	defaultOrchestratorFailureBackoffMultiplier         = 2
	defaultOrchestratorBackoffJitterPercent             = 10
	defaultOrchestratorSnapshotDiffTimeoutMs            = 1_000
	defaultOrchestratorStageTestingAfterMs              = 30_000
	defaultOrchestratorStageReviewingAfterMs            = 60_000
	defaultOrchestratorStageReviewingMaxTokensPerMinute = 50_000.0
	defaultOrchestratorETAMinElapsedMs                  = 180_000
	defaultOrchestratorETAMinFilesPerMinute             = 0.05
	defaultOrchestratorETAMinTokensPerMinute            = 1_000.0
	defaultOrchestratorETAMediumConfidenceAfterMs       = 300_000
	defaultOrchestratorETAHighConfidenceAfterMs         = 480_000
	defaultOrchestratorETAHighConfidenceMinStage        = 3
	defaultOrchestratorETAEstimatedFilesMultiplier      = 1.2
	defaultOrchestratorETAMinEstimatedFiles             = 11
	defaultOrchestratorETAFallbackRemainingMinutes      = 5.0
	defaultOrchestratorETAUncertaintyMultiplier         = 1.35
)

const (
	TeamExecutionModeTeam   = "team"
	TeamExecutionModeSingle = "single"
	TeamExecutionModeAuto   = "auto"
)

const (
	LinearSyncCommentsModeReplyThread = "reply_thread"
	LinearSyncCommentsModeTopLevel    = "top_level"
)

var (
	ErrModelRequired                     = errors.New("model is required")
	ErrProjectURLRequired                = errors.New("project_url is required")
	ErrLinearSyncCommentsModeInvalid     = errors.New("invalid linear.sync_comments.mode")
	ErrLinearSyncCommentsModeUnsupported = errors.New("unsupported linear.sync_comments.mode")
)

type WorkflowConfig struct {
	SchemaVersionRaw     int                 `yaml:"schema_version"`
	MaxConcurrencyRaw    int                 `yaml:"max_concurrency"`
	PollIntervalMsRaw    int                 `yaml:"poll_interval_ms"`
	MaxRetryBackoffMsRaw int                 `yaml:"max_retry_backoff_ms"`
	ModelRaw             string              `yaml:"model"`
	ProjectURLRaw        string              `yaml:"project_url"`
	AgentTimeoutMsRaw    int                 `yaml:"agent_timeout_ms"`
	StallTimeoutMsRaw    int                 `yaml:"stall_timeout_ms"`
	Tracker              TrackerConfig       `yaml:"tracker"`
	Polling              PollingConfig       `yaml:"polling"`
	Workspace            WorkspaceConfig     `yaml:"workspace"`
	Hooks                HooksConfig         `yaml:"hooks"`
	Codex                CodexConfig         `yaml:"codex"`
	Agent                AgentConfig         `yaml:"agent"`
	OpenCode             OpenCodeConfig      `yaml:"opencode"`
	OMX                  OMXConfig           `yaml:"omx"`
	OMC                  OMCConfig           `yaml:"omc"`
	Linear               LinearConfigSection `yaml:"linear"`
	OhMyOpenCode         OhMyOpenCodeConfig  `yaml:"oh_my_opencode"`
	Team                 TeamSectionConfig   `yaml:"team"`
	Timeline             TimelineConfig      `yaml:"timeline"`
	Orchestrator         OrchestratorConfig  `yaml:"orchestrator"`
	PromptTemplate       string              `yaml:"-"`
	presentFields        map[string]struct{} `yaml:"-"`
}

type TrackerConfig struct {
	Type        string   `yaml:"type"`
	ProjectURL  string   `yaml:"project_url"`
	TeamID      string   `yaml:"team_id"`
	AssigneeID  string   `yaml:"assignee_id"`
	BoardDir    string   `yaml:"board_dir"`
	IssuePrefix string   `yaml:"issue_prefix"`
	Owner       string   `yaml:"owner"`
	Repo        string   `yaml:"repo"`
	Labels      []string `yaml:"labels"`
	Assignee    string   `yaml:"assignee"`
	Token       string   `yaml:"token"`
	Endpoint    string   `yaml:"endpoint"`
}

type TimelineConfig struct {
	Dir string `yaml:"dir"`
}

type PollingConfig struct {
	IntervalMs      int    `yaml:"interval_ms"`
	BackoffStrategy string `yaml:"backoff_strategy"`
}

// OrchestratorConfig contains operator-tunable runtime policy. Protocol
// vocabulary and state-machine invariants intentionally remain code-defined.
type OrchestratorConfig struct {
	EventBufferSize      int                        `yaml:"event_buffer_size"`
	RunSignalBufferSize  int                        `yaml:"run_signal_buffer_size"`
	IssueCacheSize       int                        `yaml:"issue_cache_size"`
	RunShutdownTimeoutMs int                        `yaml:"run_shutdown_timeout_ms"`
	StopGraceTimeoutMs   int                        `yaml:"stop_grace_timeout_ms"`
	GitCommandTimeoutMs  int                        `yaml:"git_command_timeout_ms"`
	Shutdown             OrchestratorShutdownConfig `yaml:"shutdown"`
	Backoff              OrchestratorBackoffConfig  `yaml:"backoff"`
	Snapshot             OrchestratorSnapshotConfig `yaml:"snapshot"`
}

type OrchestratorShutdownConfig struct {
	DrainTimeoutMs   int `yaml:"drain_timeout_ms"`
	CleanupTimeoutMs int `yaml:"cleanup_timeout_ms"`
	PollIntervalMs   int `yaml:"poll_interval_ms"`
}

type OrchestratorBackoffConfig struct {
	ContinuationMs int  `yaml:"continuation_ms"`
	FailureBaseMs  int  `yaml:"failure_base_ms"`
	Multiplier     int  `yaml:"multiplier"`
	JitterPercent  *int `yaml:"jitter_percent"`
}

type OrchestratorSnapshotConfig struct {
	DiffTimeoutMs int                     `yaml:"diff_timeout_ms"`
	Stage         OrchestratorStageConfig `yaml:"stage"`
	ETA           OrchestratorETAConfig   `yaml:"eta"`
}

type OrchestratorStageConfig struct {
	TestingAfterMs              int     `yaml:"testing_after_ms"`
	ReviewingAfterMs            int     `yaml:"reviewing_after_ms"`
	ReviewingMaxTokensPerMinute float64 `yaml:"reviewing_max_tokens_per_minute"`
}

type OrchestratorETAConfig struct {
	MinElapsedMs             int     `yaml:"min_elapsed_ms"`
	MinFilesPerMinute        float64 `yaml:"min_files_per_minute"`
	MinTokensPerMinute       float64 `yaml:"min_tokens_per_minute"`
	MediumConfidenceAfterMs  int     `yaml:"medium_confidence_after_ms"`
	HighConfidenceAfterMs    int     `yaml:"high_confidence_after_ms"`
	HighConfidenceMinStage   int     `yaml:"high_confidence_min_stage"`
	EstimatedFilesMultiplier float64 `yaml:"estimated_files_multiplier"`
	MinEstimatedFiles        int     `yaml:"min_estimated_files"`
	FallbackRemainingMinutes float64 `yaml:"fallback_remaining_minutes"`
	UncertaintyMultiplier    float64 `yaml:"uncertainty_multiplier"`
}

type WorkspaceConfig struct {
	BaseDir      string `yaml:"base_dir"`
	BranchPrefix string `yaml:"branch_prefix"`
}

type HooksConfig struct {
	// TODO: Hook fields are parsed from workflow YAML, but execution is not
	// implemented yet.
	BeforeRun    string `yaml:"before_run"`
	AfterRun     string `yaml:"after_run"`
	BeforeRemove string `yaml:"before_remove"`
}

type CodexConfig struct {
	BinaryPath     string `yaml:"binary_path"`
	Model          string `yaml:"model"`
	ApprovalPolicy string `yaml:"approval_policy"`
	Sandbox        string `yaml:"sandbox"`
}

type AgentConfig struct {
	Type             string `yaml:"type"`
	StartupTimeoutMs int    `yaml:"startup_timeout_ms"`
}

type OpenCodeConfig struct {
	BinaryPath string `yaml:"binary_path"`
	Port       int    `yaml:"port"`
	Password   string `yaml:"password"`
	Username   string `yaml:"username"`
}

type OMXConfig struct {
	BinaryPath       string `yaml:"binary_path"`
	TeamSpec         string `yaml:"team_spec"`
	PollIntervalMs   int    `yaml:"poll_interval_ms"`
	StartupTimeoutMs int    `yaml:"startup_timeout_ms"`
	Ralph            bool   `yaml:"ralph"`
}

type OMCConfig struct {
	BinaryPath       string `yaml:"binary_path"`
	TeamSpec         string `yaml:"team_spec"`
	PollIntervalMs   int    `yaml:"poll_interval_ms"`
	StartupTimeoutMs int    `yaml:"startup_timeout_ms"`
}

// LinearConfigSection holds Linear-specific workflow behavior.
type LinearConfigSection struct {
	IssueDetails LinearIssueDetailsConfig `yaml:"issue_details"`
	SyncComments LinearSyncCommentsConfig `yaml:"sync_comments"`
}

// LinearIssueDetailsConfig controls whether the dashboard fetches rich Linear
// issue detail payloads.
type LinearIssueDetailsConfig struct {
	Enabled *bool `yaml:"enabled"`
}

// LinearSyncCommentsConfig controls best-effort projection of workflow
// timeline nodes back into Linear comments.
type LinearSyncCommentsConfig struct {
	Enabled        bool   `yaml:"enabled"`
	Mode           string `yaml:"mode"`
	QueueSize      int    `yaml:"queue_size"`
	PollIntervalMs int    `yaml:"poll_interval_ms"`
}

// TeamSectionConfig holds settings for multi-agent team coordination.
type TeamSectionConfig struct {
	MaxWorkers           int    `yaml:"max_workers"`
	MaxFixLoops          int    `yaml:"max_fix_loops"`
	ClaimLeaseSeconds    int    `yaml:"claim_lease_seconds"`
	StateDir             string `yaml:"state_dir"`
	ExecutionMode        string `yaml:"execution_mode"`
	WorkerMode           string `yaml:"worker_mode"`
	WorkerPollIntervalMs int    `yaml:"worker_poll_interval_ms"`
}

// OhMyOpenCodeConfig holds settings for the oh-my-opencode agent runner which
// wraps the OpenCode runner with the oh-my-opencode plugin and model routing.
type OhMyOpenCodeConfig struct {
	PluginVersion string                          `yaml:"plugin_version"`
	Plugins       []string                        `yaml:"plugins"`
	Agents        map[string]OhMyOpenCodeAgent    `yaml:"agents"`
	Categories    map[string]OhMyOpenCodeCategory `yaml:"categories"`
	Provider      OhMyOpenCodeProvider            `yaml:"provider"`
}

// OhMyOpenCodeAgent configures a named agent override in oh-my-opencode.
type OhMyOpenCodeAgent struct {
	Model string `yaml:"model"`
}

// OhMyOpenCodeCategory configures the model for a task category.
type OhMyOpenCodeCategory struct {
	Model string `yaml:"model"`
}

// OhMyOpenCodeProvider configures the LLM provider proxy used by opencode.
type OhMyOpenCodeProvider struct {
	Name    string `yaml:"name"`
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key"`
}

// Clone returns a deep copy of the workflow config so callers can safely
// mutate the result without affecting the original.
func (c *WorkflowConfig) Clone() *WorkflowConfig {
	if c == nil {
		return nil
	}

	cfg := *c
	cfg.Tracker.Labels = slices.Clone(c.Tracker.Labels)
	cfg.OhMyOpenCode.Plugins = slices.Clone(c.OhMyOpenCode.Plugins)
	cfg.OhMyOpenCode.Agents = maps.Clone(c.OhMyOpenCode.Agents)
	cfg.OhMyOpenCode.Categories = maps.Clone(c.OhMyOpenCode.Categories)
	cfg.presentFields = maps.Clone(c.presentFields)
	if c.Linear.IssueDetails.Enabled != nil {
		enabled := *c.Linear.IssueDetails.Enabled
		cfg.Linear.IssueDetails.Enabled = &enabled
	}
	if c.Orchestrator.Backoff.JitterPercent != nil {
		jitter := *c.Orchestrator.Backoff.JitterPercent
		cfg.Orchestrator.Backoff.JitterPercent = &jitter
	}
	return &cfg
}

func (c *WorkflowConfig) MaxConcurrency() int {
	if c == nil || c.MaxConcurrencyRaw <= 0 {
		return defaultMaxConcurrency
	}
	return c.MaxConcurrencyRaw
}

func (c *WorkflowConfig) PollIntervalMs() int {
	if c == nil {
		return defaultPollIntervalMs
	}
	if c.PollIntervalMsRaw > 0 {
		return c.PollIntervalMsRaw
	}
	if c.Polling.IntervalMs > 0 {
		return c.Polling.IntervalMs
	}
	return defaultPollIntervalMs
}

func (c *WorkflowConfig) TrackerType() string {
	if c == nil || c.Tracker.Type == "" {
		return defaultTrackerType
	}
	return c.Tracker.Type
}

func (c *WorkflowConfig) TrackerProjectURL() string {
	if c == nil {
		return ""
	}
	if c.Tracker.ProjectURL != "" {
		return c.Tracker.ProjectURL
	}
	return c.ProjectURLRaw
}

func (c *WorkflowConfig) TrackerTeamID() string {
	if c == nil {
		return ""
	}
	return c.Tracker.TeamID
}

func (c *WorkflowConfig) TrackerAssigneeID() string {
	if c == nil {
		return ""
	}
	return c.Tracker.AssigneeID
}

func (c *WorkflowConfig) LocalBoardDir() string {
	if c == nil || c.Tracker.BoardDir == "" {
		return defaultLocalBoardDir
	}
	return c.Tracker.BoardDir
}

func (c *WorkflowConfig) LocalIssuePrefix() string {
	if c == nil || c.Tracker.IssuePrefix == "" {
		return defaultLocalIssuePrefix
	}
	return c.Tracker.IssuePrefix
}

func (c *WorkflowConfig) PollingIntervalMs() int {
	if c == nil || c.Polling.IntervalMs <= 0 {
		return defaultPollIntervalMs
	}
	return c.Polling.IntervalMs
}

func (c *WorkflowConfig) WorkspaceBaseDir() string {
	if c == nil || c.Workspace.BaseDir == "" {
		return defaultWorkspaceBaseDir
	}
	return c.Workspace.BaseDir
}

func (c *WorkflowConfig) HookBeforeRun() string {
	if c == nil {
		return ""
	}
	return c.Hooks.BeforeRun
}

func (c *WorkflowConfig) HookAfterRun() string {
	if c == nil {
		return ""
	}
	return c.Hooks.AfterRun
}

func (c *WorkflowConfig) HookBeforeRemove() string {
	if c == nil {
		return ""
	}
	return c.Hooks.BeforeRemove
}

func (c *WorkflowConfig) CodexBinaryPath() string {
	if c == nil || c.Codex.BinaryPath == "" {
		return defaultCodexBinaryPath
	}
	return c.Codex.BinaryPath
}

func (c *WorkflowConfig) CodexModel() string {
	if c == nil {
		return ""
	}
	if c.Codex.Model != "" {
		return c.Codex.Model
	}
	return c.ModelRaw
}

func (c *WorkflowConfig) CodexApprovalPolicy() string {
	if c == nil || c.Codex.ApprovalPolicy == "" {
		return defaultApprovalPolicy
	}
	return c.Codex.ApprovalPolicy
}

func (c *WorkflowConfig) CodexSandbox() string {
	if c == nil || c.Codex.Sandbox == "" {
		return defaultSandbox
	}
	return c.Codex.Sandbox
}

func (c *WorkflowConfig) AgentType() string {
	if c == nil || c.Agent.Type == "" {
		return defaultAgentType
	}
	return c.Agent.Type
}

func (c *WorkflowConfig) AgentStartupTimeoutMs() int {
	if c == nil {
		return defaultAgentStartupTimeoutMs
	}
	return positiveInt(c.Agent.StartupTimeoutMs, defaultAgentStartupTimeoutMs)
}

func (c *WorkflowConfig) OpenCodeBinaryPath() string {
	if c == nil || c.OpenCode.BinaryPath == "" {
		return defaultOpenCodeBinaryPath
	}
	return c.OpenCode.BinaryPath
}

func (c *WorkflowConfig) OpenCodePort() int {
	if c == nil || c.OpenCode.Port <= 0 {
		return 0
	}
	return c.OpenCode.Port
}

func (c *WorkflowConfig) OpenCodePassword() string {
	if c == nil {
		return ""
	}
	return c.OpenCode.Password
}

func (c *WorkflowConfig) OpenCodeUsername() string {
	if c == nil {
		return ""
	}
	return c.OpenCode.Username
}

func (c *WorkflowConfig) OMXBinaryPath() string {
	if c == nil || c.OMX.BinaryPath == "" {
		return defaultOMXBinaryPath
	}
	return c.OMX.BinaryPath
}

func (c *WorkflowConfig) OMXTeamSpec() string {
	if c == nil || c.OMX.TeamSpec == "" {
		return defaultOMXTeamSpec
	}
	return c.OMX.TeamSpec
}

func (c *WorkflowConfig) OMXPollIntervalMs() int {
	if c == nil || c.OMX.PollIntervalMs <= 0 {
		return defaultOMXPollIntervalMs
	}
	return c.OMX.PollIntervalMs
}

func (c *WorkflowConfig) OMXStartupTimeoutMs() int {
	if c == nil || c.OMX.StartupTimeoutMs <= 0 {
		return defaultOMXStartupTimeoutMs
	}
	return c.OMX.StartupTimeoutMs
}

func (c *WorkflowConfig) OMXRalph() bool {
	if c == nil {
		return false
	}
	return c.OMX.Ralph
}

func (c *WorkflowConfig) OMCBinaryPath() string {
	if c == nil || c.OMC.BinaryPath == "" {
		return defaultOMCBinaryPath
	}
	return c.OMC.BinaryPath
}

func (c *WorkflowConfig) OMCTeamSpec() string {
	if c == nil || c.OMC.TeamSpec == "" {
		return defaultOMCTeamSpec
	}
	return c.OMC.TeamSpec
}

func (c *WorkflowConfig) OMCPollIntervalMs() int {
	if c == nil || c.OMC.PollIntervalMs <= 0 {
		return defaultOMCPollIntervalMs
	}
	return c.OMC.PollIntervalMs
}

func (c *WorkflowConfig) OMCStartupTimeoutMs() int {
	if c == nil || c.OMC.StartupTimeoutMs <= 0 {
		return defaultOMCStartupTimeoutMs
	}
	return c.OMC.StartupTimeoutMs
}

func (c *WorkflowConfig) LinearIssueDetailsEnabled() bool {
	if c == nil || !strings.EqualFold(strings.TrimSpace(c.TrackerType()), "linear") {
		return false
	}
	if c.Linear.IssueDetails.Enabled == nil {
		return true
	}
	return *c.Linear.IssueDetails.Enabled
}

func (c *WorkflowConfig) LinearSyncCommentsEnabled() bool {
	if c == nil || !strings.EqualFold(strings.TrimSpace(c.TrackerType()), "linear") {
		return false
	}
	return c.Linear.SyncComments.Enabled
}

func (c *WorkflowConfig) LinearSyncCommentsMode() string {
	if c == nil {
		return LinearSyncCommentsModeReplyThread
	}

	switch strings.TrimSpace(strings.ToLower(c.Linear.SyncComments.Mode)) {
	case "", LinearSyncCommentsModeReplyThread:
		return LinearSyncCommentsModeReplyThread
	case LinearSyncCommentsModeTopLevel:
		return LinearSyncCommentsModeTopLevel
	default:
		return strings.TrimSpace(strings.ToLower(c.Linear.SyncComments.Mode))
	}
}

func (c *WorkflowConfig) ValidateLinearSyncCommentsMode() error {
	_, err := c.LinearSyncCommentsEffectiveMode(true)
	return err
}

func (c *WorkflowConfig) LinearSyncCommentsEffectiveMode(replyThreadSupported bool) (string, error) {
	if c == nil {
		return LinearSyncCommentsModeReplyThread, nil
	}

	raw := strings.TrimSpace(strings.ToLower(c.Linear.SyncComments.Mode))
	switch raw {
	case "":
		if replyThreadSupported {
			return LinearSyncCommentsModeReplyThread, nil
		}
		return LinearSyncCommentsModeTopLevel, nil
	case LinearSyncCommentsModeReplyThread:
		if !replyThreadSupported {
			return "", fmt.Errorf("%w: reply_thread requires Linear threaded reply support", ErrLinearSyncCommentsModeUnsupported)
		}
		return LinearSyncCommentsModeReplyThread, nil
	case LinearSyncCommentsModeTopLevel:
		return LinearSyncCommentsModeTopLevel, nil
	default:
		return "", fmt.Errorf("%w: %s (valid values: reply_thread, top_level)", ErrLinearSyncCommentsModeInvalid, c.Linear.SyncComments.Mode)
	}
}

func (c *WorkflowConfig) LinearSyncCommentsModeExplicit() bool {
	return c != nil && strings.TrimSpace(c.Linear.SyncComments.Mode) != ""
}

func (c *WorkflowConfig) LinearSyncCommentsQueueSize() int {
	if c == nil || c.Linear.SyncComments.QueueSize <= 0 {
		return 100
	}
	return c.Linear.SyncComments.QueueSize
}

func (c *WorkflowConfig) LinearSyncCommentsPollIntervalMs() int {
	if c == nil || c.Linear.SyncComments.PollIntervalMs <= 0 {
		return 5000
	}
	return c.Linear.SyncComments.PollIntervalMs
}

func (c *WorkflowConfig) OhMyOpenCodePluginVersion() string {
	if c == nil || c.OhMyOpenCode.PluginVersion == "" {
		return defaultOhMyOpenCodePluginVersion
	}
	return c.OhMyOpenCode.PluginVersion
}

func (c *WorkflowConfig) OhMyOpenCodePlugins() []string {
	if c == nil || len(c.OhMyOpenCode.Plugins) == 0 {
		return []string{}
	}
	return c.OhMyOpenCode.Plugins
}

func (c *WorkflowConfig) OhMyOpenCodeAgents() map[string]OhMyOpenCodeAgent {
	if c == nil || len(c.OhMyOpenCode.Agents) == 0 {
		return map[string]OhMyOpenCodeAgent{}
	}
	return c.OhMyOpenCode.Agents
}

func (c *WorkflowConfig) OhMyOpenCodeCategories() map[string]OhMyOpenCodeCategory {
	if c == nil || len(c.OhMyOpenCode.Categories) == 0 {
		return map[string]OhMyOpenCodeCategory{}
	}
	return c.OhMyOpenCode.Categories
}

func (c *WorkflowConfig) OhMyOpenCodeProviderName() string {
	if c == nil || c.OhMyOpenCode.Provider.Name == "" {
		return ""
	}
	return c.OhMyOpenCode.Provider.Name
}

func (c *WorkflowConfig) OhMyOpenCodeProviderBaseURL() string {
	if c == nil {
		return ""
	}
	return c.OhMyOpenCode.Provider.BaseURL
}

func (c *WorkflowConfig) OhMyOpenCodeProviderAPIKey() string {
	if c == nil {
		return ""
	}
	return c.OhMyOpenCode.Provider.APIKey
}

func (c *WorkflowConfig) TeamMaxWorkers() int {
	if c == nil || c.Team.MaxWorkers <= 0 {
		return defaultTeamMaxWorkers
	}
	return c.Team.MaxWorkers
}

func (c *WorkflowConfig) TeamMaxFixLoops() int {
	if c == nil || c.Team.MaxFixLoops <= 0 {
		return defaultTeamMaxFixLoops
	}
	return c.Team.MaxFixLoops
}

func (c *WorkflowConfig) TeamClaimLeaseSeconds() int {
	if c == nil || c.Team.ClaimLeaseSeconds <= 0 {
		return defaultTeamClaimLeaseSeconds
	}
	return c.Team.ClaimLeaseSeconds
}

func (c *WorkflowConfig) TeamWorkerPollIntervalMs() int {
	if c == nil {
		return defaultTeamWorkerPollIntervalMs
	}
	return positiveInt(c.Team.WorkerPollIntervalMs, defaultTeamWorkerPollIntervalMs)
}

func (c *WorkflowConfig) TeamStateDir() string {
	if c == nil || c.Team.StateDir == "" {
		return defaultTeamStateDir
	}
	return c.Team.StateDir
}

func (c *WorkflowConfig) WorkflowTimelineDir() string {
	if c == nil || strings.TrimSpace(c.Timeline.Dir) == "" {
		return defaultWorkflowTimelineDir
	}
	return c.Timeline.Dir
}

func (c *WorkflowConfig) TeamExecutionMode() string {
	if c == nil {
		return defaultTeamExecutionMode
	}

	switch strings.TrimSpace(strings.ToLower(c.Team.ExecutionMode)) {
	case "", TeamExecutionModeAuto:
		switch c.TrackerType() {
		case "internal", "local":
			return TeamExecutionModeTeam
		default:
			return TeamExecutionModeSingle
		}
	case "agent", "orchestrator", TeamExecutionModeSingle:
		return TeamExecutionModeSingle
	case TeamExecutionModeTeam:
		return TeamExecutionModeTeam
	default:
		return strings.TrimSpace(strings.ToLower(c.Team.ExecutionMode))
	}
}

func (c *WorkflowConfig) WorkerMode() string {
	if c == nil {
		return defaultTeamWorkerMode
	}

	mode := strings.TrimSpace(strings.ToLower(c.Team.WorkerMode))
	if mode == "" {
		return defaultTeamWorkerMode
	}

	switch mode {
	case "goroutine":
		return "goroutine"
	default:
		return defaultTeamWorkerMode
	}
}

// ValidateWorkerMode checks if the worker mode is valid.
// Empty values are allowed (they default to "tmux").
// Valid modes are "goroutine" and "tmux".
// Returns an error for unknown values.
func (c *WorkflowConfig) ValidateWorkerMode() error {
	if c == nil {
		return nil
	}

	mode := strings.TrimSpace(strings.ToLower(c.Team.WorkerMode))
	if mode == "" {
		// Empty is fine, defaults to goroutine
		return nil
	}

	switch mode {
	case "goroutine", "tmux":
		return nil
	default:
		return errors.New("unknown worker_mode: " + c.Team.WorkerMode + " (valid values: goroutine, tmux)")
	}
}

func (c *WorkflowConfig) GitHubOwner() string {
	if c == nil {
		return ""
	}
	return c.Tracker.Owner
}

func (c *WorkflowConfig) GitHubRepo() string {
	if c == nil {
		return ""
	}
	return c.Tracker.Repo
}

func (c *WorkflowConfig) GitHubToken() string {
	if c == nil {
		return ""
	}
	return c.Tracker.Token
}

func (c *WorkflowConfig) GitHubAssignee() string {
	if c == nil {
		return ""
	}
	return c.Tracker.Assignee
}

func (c *WorkflowConfig) GitHubLabels() []string {
	if c == nil || len(c.Tracker.Labels) == 0 {
		return []string{}
	}
	return c.Tracker.Labels
}

func (c *WorkflowConfig) GitHubEndpoint() string {
	if c == nil || c.Tracker.Endpoint == "" {
		return defaultGitHubEndpoint
	}
	return c.Tracker.Endpoint
}

func (c *WorkflowConfig) MaxRetryBackoffMs() int {
	if c == nil || c.MaxRetryBackoffMsRaw <= 0 {
		return defaultMaxRetryBackoffMs
	}
	return c.MaxRetryBackoffMsRaw
}

func (c *WorkflowConfig) AgentTimeoutMs() int {
	if c == nil || c.AgentTimeoutMsRaw <= 0 {
		return defaultAgentTimeoutMs
	}
	return c.AgentTimeoutMsRaw
}

func (c *WorkflowConfig) StallTimeoutMs() int {
	if c == nil || c.StallTimeoutMsRaw <= 0 {
		return defaultStallTimeoutMs
	}
	return c.StallTimeoutMsRaw
}

func (c *WorkflowConfig) PollingBackoffStrategy() string {
	if c == nil {
		return defaultBackoffStrategy
	}
	strategy := strings.TrimSpace(strings.ToLower(c.Polling.BackoffStrategy))
	switch strategy {
	case "linear", defaultBackoffStrategy:
		return strategy
	default:
		return defaultBackoffStrategy
	}
}

func (c *WorkflowConfig) OrchestratorEventBufferSize() int {
	if c == nil {
		return defaultOrchestratorEventBufferSize
	}
	return positiveInt(c.Orchestrator.EventBufferSize, defaultOrchestratorEventBufferSize)
}

func (c *WorkflowConfig) OrchestratorRunSignalBufferSize() int {
	if c == nil {
		return defaultOrchestratorRunSignalBufferSize
	}
	return positiveInt(c.Orchestrator.RunSignalBufferSize, defaultOrchestratorRunSignalBufferSize)
}

func (c *WorkflowConfig) OrchestratorIssueCacheSize() int {
	if c == nil {
		return defaultOrchestratorIssueCacheSize
	}
	return positiveInt(c.Orchestrator.IssueCacheSize, defaultOrchestratorIssueCacheSize)
}

func (c *WorkflowConfig) OrchestratorRunShutdownTimeoutMs() int {
	if c == nil {
		return defaultOrchestratorRunShutdownTimeoutMs
	}
	return positiveInt(c.Orchestrator.RunShutdownTimeoutMs, defaultOrchestratorRunShutdownTimeoutMs)
}

func (c *WorkflowConfig) OrchestratorStopGraceTimeoutMs() int {
	if c == nil {
		return defaultOrchestratorStopGraceTimeoutMs
	}
	return positiveInt(c.Orchestrator.StopGraceTimeoutMs, defaultOrchestratorStopGraceTimeoutMs)
}

func (c *WorkflowConfig) OrchestratorGitCommandTimeoutMs() int {
	if c == nil {
		return defaultOrchestratorGitCommandTimeoutMs
	}
	return positiveInt(c.Orchestrator.GitCommandTimeoutMs, defaultOrchestratorGitCommandTimeoutMs)
}

func (c *WorkflowConfig) OrchestratorShutdownDrainTimeoutMs() int {
	if c == nil {
		return defaultOrchestratorShutdownDrainTimeoutMs
	}
	return positiveInt(c.Orchestrator.Shutdown.DrainTimeoutMs, defaultOrchestratorShutdownDrainTimeoutMs)
}

func (c *WorkflowConfig) OrchestratorShutdownCleanupTimeoutMs() int {
	if c == nil {
		return defaultOrchestratorShutdownCleanupTimeoutMs
	}
	return positiveInt(c.Orchestrator.Shutdown.CleanupTimeoutMs, defaultOrchestratorShutdownCleanupTimeoutMs)
}

func (c *WorkflowConfig) OrchestratorShutdownPollIntervalMs() int {
	if c == nil {
		return defaultOrchestratorShutdownPollIntervalMs
	}
	return positiveInt(c.Orchestrator.Shutdown.PollIntervalMs, defaultOrchestratorShutdownPollIntervalMs)
}

func (c *WorkflowConfig) OrchestratorContinuationBackoffMs() int {
	if c == nil {
		return defaultOrchestratorContinuationBackoffMs
	}
	return positiveInt(c.Orchestrator.Backoff.ContinuationMs, defaultOrchestratorContinuationBackoffMs)
}

func (c *WorkflowConfig) OrchestratorFailureBackoffBaseMs() int {
	if c == nil {
		return defaultOrchestratorFailureBackoffBaseMs
	}
	return positiveInt(c.Orchestrator.Backoff.FailureBaseMs, defaultOrchestratorFailureBackoffBaseMs)
}

func (c *WorkflowConfig) OrchestratorFailureBackoffMultiplier() int {
	if c == nil {
		return defaultOrchestratorFailureBackoffMultiplier
	}
	return positiveInt(c.Orchestrator.Backoff.Multiplier, defaultOrchestratorFailureBackoffMultiplier)
}

func (c *WorkflowConfig) OrchestratorBackoffJitterPercent() int {
	if c == nil || c.Orchestrator.Backoff.JitterPercent == nil {
		return defaultOrchestratorBackoffJitterPercent
	}
	jitter := *c.Orchestrator.Backoff.JitterPercent
	if jitter < 0 {
		return defaultOrchestratorBackoffJitterPercent
	}
	if jitter > 100 {
		return 100
	}
	return jitter
}

func (c *WorkflowConfig) OrchestratorSnapshotDiffTimeoutMs() int {
	if c == nil {
		return defaultOrchestratorSnapshotDiffTimeoutMs
	}
	return positiveInt(c.Orchestrator.Snapshot.DiffTimeoutMs, defaultOrchestratorSnapshotDiffTimeoutMs)
}

func (c *WorkflowConfig) OrchestratorStageTestingAfterMs() int {
	if c == nil {
		return defaultOrchestratorStageTestingAfterMs
	}
	return positiveInt(c.Orchestrator.Snapshot.Stage.TestingAfterMs, defaultOrchestratorStageTestingAfterMs)
}

func (c *WorkflowConfig) OrchestratorStageReviewingAfterMs() int {
	if c == nil {
		return defaultOrchestratorStageReviewingAfterMs
	}
	return positiveInt(c.Orchestrator.Snapshot.Stage.ReviewingAfterMs, defaultOrchestratorStageReviewingAfterMs)
}

func (c *WorkflowConfig) OrchestratorStageReviewingMaxTokensPerMinute() float64 {
	if c == nil {
		return defaultOrchestratorStageReviewingMaxTokensPerMinute
	}
	return positiveFloat(c.Orchestrator.Snapshot.Stage.ReviewingMaxTokensPerMinute, defaultOrchestratorStageReviewingMaxTokensPerMinute)
}

func (c *WorkflowConfig) OrchestratorETAMinElapsedMs() int {
	if c == nil {
		return defaultOrchestratorETAMinElapsedMs
	}
	return positiveInt(c.Orchestrator.Snapshot.ETA.MinElapsedMs, defaultOrchestratorETAMinElapsedMs)
}

func (c *WorkflowConfig) OrchestratorETAMinFilesPerMinute() float64 {
	if c == nil {
		return defaultOrchestratorETAMinFilesPerMinute
	}
	return positiveFloat(c.Orchestrator.Snapshot.ETA.MinFilesPerMinute, defaultOrchestratorETAMinFilesPerMinute)
}

func (c *WorkflowConfig) OrchestratorETAMinTokensPerMinute() float64 {
	if c == nil {
		return defaultOrchestratorETAMinTokensPerMinute
	}
	return positiveFloat(c.Orchestrator.Snapshot.ETA.MinTokensPerMinute, defaultOrchestratorETAMinTokensPerMinute)
}

func (c *WorkflowConfig) OrchestratorETAMediumConfidenceAfterMs() int {
	if c == nil {
		return defaultOrchestratorETAMediumConfidenceAfterMs
	}
	return positiveInt(c.Orchestrator.Snapshot.ETA.MediumConfidenceAfterMs, defaultOrchestratorETAMediumConfidenceAfterMs)
}

func (c *WorkflowConfig) OrchestratorETAHighConfidenceAfterMs() int {
	if c == nil {
		return defaultOrchestratorETAHighConfidenceAfterMs
	}
	return positiveInt(c.Orchestrator.Snapshot.ETA.HighConfidenceAfterMs, defaultOrchestratorETAHighConfidenceAfterMs)
}

func (c *WorkflowConfig) OrchestratorETAHighConfidenceMinStage() int {
	if c == nil {
		return defaultOrchestratorETAHighConfidenceMinStage
	}
	return positiveInt(c.Orchestrator.Snapshot.ETA.HighConfidenceMinStage, defaultOrchestratorETAHighConfidenceMinStage)
}

func (c *WorkflowConfig) OrchestratorETAEstimatedFilesMultiplier() float64 {
	if c == nil {
		return defaultOrchestratorETAEstimatedFilesMultiplier
	}
	return positiveFloat(c.Orchestrator.Snapshot.ETA.EstimatedFilesMultiplier, defaultOrchestratorETAEstimatedFilesMultiplier)
}

func (c *WorkflowConfig) OrchestratorETAMinEstimatedFiles() int {
	if c == nil {
		return defaultOrchestratorETAMinEstimatedFiles
	}
	return positiveInt(c.Orchestrator.Snapshot.ETA.MinEstimatedFiles, defaultOrchestratorETAMinEstimatedFiles)
}

func (c *WorkflowConfig) OrchestratorETAFallbackRemainingMinutes() float64 {
	if c == nil {
		return defaultOrchestratorETAFallbackRemainingMinutes
	}
	return positiveFloat(c.Orchestrator.Snapshot.ETA.FallbackRemainingMinutes, defaultOrchestratorETAFallbackRemainingMinutes)
}

func (c *WorkflowConfig) OrchestratorETAUncertaintyMultiplier() float64 {
	if c == nil {
		return defaultOrchestratorETAUncertaintyMultiplier
	}
	return positiveFloat(c.Orchestrator.Snapshot.ETA.UncertaintyMultiplier, defaultOrchestratorETAUncertaintyMultiplier)
}

func positiveInt(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func positiveFloat(value, fallback float64) float64 {
	if value <= 0 {
		return fallback
	}
	return value
}

func (c *WorkflowConfig) Model() (string, error) {
	if c == nil {
		return "", ErrModelRequired
	}
	if c.ModelRaw != "" {
		return c.ModelRaw, nil
	}
	if c.Codex.Model != "" {
		return c.Codex.Model, nil
	}
	return "", ErrModelRequired
}

func (c *WorkflowConfig) ProjectURL() (string, error) {
	if c == nil {
		return "", ErrProjectURLRequired
	}
	if c.ProjectURLRaw != "" {
		return c.ProjectURLRaw, nil
	}
	if c.Tracker.ProjectURL != "" {
		return c.Tracker.ProjectURL, nil
	}
	return "", ErrProjectURLRequired
}
