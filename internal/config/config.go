package config

import (
	"errors"
)

const (
	defaultMaxConcurrency    = 10
	defaultPollIntervalMs    = 30_000
	defaultMaxRetryBackoffMs = 300_000
	defaultAgentTimeoutMs    = 600_000
	defaultStallTimeoutMs    = 120_000
	defaultTrackerType       = "linear"
	defaultBackoffStrategy   = "exponential"
	defaultWorkspaceBaseDir  = "."
	defaultBranchPrefix      = "symphony/"
	defaultCodexBinaryPath   = "codex"
	defaultApprovalPolicy    = "auto-edit"
	defaultSandbox           = "docker"
)

var (
	ErrModelRequired      = errors.New("model is required")
	ErrProjectURLRequired = errors.New("project_url is required")
)

type WorkflowConfig struct {
	MaxConcurrencyRaw    int             `yaml:"max_concurrency"`
	PollIntervalMsRaw    int             `yaml:"poll_interval_ms"`
	MaxRetryBackoffMsRaw int             `yaml:"max_retry_backoff_ms"`
	ModelRaw             string          `yaml:"model"`
	ProjectURLRaw        string          `yaml:"project_url"`
	AgentTimeoutMsRaw    int             `yaml:"agent_timeout_ms"`
	StallTimeoutMsRaw    int             `yaml:"stall_timeout_ms"`
	Tracker              TrackerConfig   `yaml:"tracker"`
	Polling              PollingConfig   `yaml:"polling"`
	Workspace            WorkspaceConfig `yaml:"workspace"`
	Hooks                HooksConfig     `yaml:"hooks"`
	Codex                CodexConfig     `yaml:"codex"`
	PromptTemplate       string          `yaml:"-"`
}

type TrackerConfig struct {
	Type       string `yaml:"type"`
	ProjectURL string `yaml:"project_url"`
	TeamID     string `yaml:"team_id"`
	AssigneeID string `yaml:"assignee_id"`
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
