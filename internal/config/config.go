package config

import (
	"errors"
)

const (
	defaultMaxConcurrency     = 10
	defaultPollIntervalMs     = 30_000
	defaultMaxRetryBackoffMs  = 300_000
	defaultAgentTimeoutMs     = 600_000
	defaultStallTimeoutMs     = 120_000
	defaultTrackerType        = "linear"
	defaultBackoffStrategy    = "exponential"
	defaultWorkspaceBaseDir   = "."
	defaultBranchPrefix       = "symphony/"
	defaultCodexBinaryPath    = "codex app-server"
	defaultApprovalPolicy     = "auto-edit"
	defaultSandbox            = "docker"
	defaultAgentType          = "codex"
	defaultOpenCodeBinaryPath = "opencode serve"
	defaultGitHubEndpoint     = "https://api.github.com"

	defaultOhMyOpenCodePluginVersion = "oh-my-opencode"
	defaultOhMyOpenCodeAgentModel    = "anthropic/claude-sonnet-4-6"
	defaultOhMyOpenCodeFallbackModel = "anthropic/claude-haiku-4-5"
)

var (
	ErrModelRequired      = errors.New("model is required")
	ErrProjectURLRequired = errors.New("project_url is required")
)

type WorkflowConfig struct {
	MaxConcurrencyRaw    int                `yaml:"max_concurrency"`
	PollIntervalMsRaw    int                `yaml:"poll_interval_ms"`
	MaxRetryBackoffMsRaw int                `yaml:"max_retry_backoff_ms"`
	ModelRaw             string             `yaml:"model"`
	ProjectURLRaw        string             `yaml:"project_url"`
	AgentTimeoutMsRaw    int                `yaml:"agent_timeout_ms"`
	StallTimeoutMsRaw    int                `yaml:"stall_timeout_ms"`
	Tracker              TrackerConfig      `yaml:"tracker"`
	Polling              PollingConfig      `yaml:"polling"`
	Workspace            WorkspaceConfig    `yaml:"workspace"`
	Hooks                HooksConfig        `yaml:"hooks"`
	Codex                CodexConfig        `yaml:"codex"`
	Agent                AgentConfig        `yaml:"agent"`
	OpenCode             OpenCodeConfig     `yaml:"opencode"`
	OhMyOpenCode         OhMyOpenCodeConfig `yaml:"oh_my_opencode"`
	PromptTemplate       string             `yaml:"-"`
}

type TrackerConfig struct {
	Type       string   `yaml:"type"`
	ProjectURL string   `yaml:"project_url"`
	TeamID     string   `yaml:"team_id"`
	AssigneeID string   `yaml:"assignee_id"`
	Owner      string   `yaml:"owner"`
	Repo       string   `yaml:"repo"`
	Labels     []string `yaml:"labels"`
	Assignee   string   `yaml:"assignee"`
	Token      string   `yaml:"token"`
	Endpoint   string   `yaml:"endpoint"`
}

type PollingConfig struct {
	IntervalMs      int    `yaml:"interval_ms"`
	BackoffStrategy string `yaml:"backoff_strategy"`
}

type WorkspaceConfig struct {
	BaseDir      string `yaml:"base_dir"`
	BranchPrefix string `yaml:"branch_prefix"`
}

type HooksConfig struct {
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
	Type string `yaml:"type"`
}

type OpenCodeConfig struct {
	BinaryPath string `yaml:"binary_path"`
	Port       int    `yaml:"port"`
	Password   string `yaml:"password"`
	Username   string `yaml:"username"`
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

// OhMyOpenCodeAgent configures a named agent's primary and fallback model.
type OhMyOpenCodeAgent struct {
	Model    string `yaml:"model"`
	Fallback string `yaml:"fallback"`
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

func (c *WorkflowConfig) PollingIntervalMs() int {
	if c == nil || c.Polling.IntervalMs <= 0 {
		return defaultPollIntervalMs
	}
	return c.Polling.IntervalMs
}

func (c *WorkflowConfig) PollingBackoffStrategy() string {
	if c == nil || c.Polling.BackoffStrategy == "" {
		return defaultBackoffStrategy
	}
	return c.Polling.BackoffStrategy
}

func (c *WorkflowConfig) WorkspaceBaseDir() string {
	if c == nil || c.Workspace.BaseDir == "" {
		return defaultWorkspaceBaseDir
	}
	return c.Workspace.BaseDir
}

func (c *WorkflowConfig) WorkspaceBranchPrefix() string {
	if c == nil || c.Workspace.BranchPrefix == "" {
		return defaultBranchPrefix
	}
	return c.Workspace.BranchPrefix
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
