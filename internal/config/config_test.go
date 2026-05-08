package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowConfig_DefaultGetters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  *WorkflowConfig
		want struct {
			maxConcurrency    int
			pollIntervalMs    int
			maxRetryBackoffMs int
			agentTimeoutMs    int
			stallTimeoutMs    int
		}
	}{
		{
			name: "nil config uses defaults",
			cfg:  nil,
			want: struct {
				maxConcurrency    int
				pollIntervalMs    int
				maxRetryBackoffMs int
				agentTimeoutMs    int
				stallTimeoutMs    int
			}{
				maxConcurrency:    10,
				pollIntervalMs:    30_000,
				maxRetryBackoffMs: 300_000,
				agentTimeoutMs:    600_000,
				stallTimeoutMs:    120_000,
			},
		},
		{
			name: "non-positive values use defaults",
			cfg: &WorkflowConfig{
				MaxConcurrencyRaw:    0,
				PollIntervalMsRaw:    -1,
				MaxRetryBackoffMsRaw: 0,
				AgentTimeoutMsRaw:    -30,
				StallTimeoutMsRaw:    0,
			},
			want: struct {
				maxConcurrency    int
				pollIntervalMs    int
				maxRetryBackoffMs int
				agentTimeoutMs    int
				stallTimeoutMs    int
			}{
				maxConcurrency:    10,
				pollIntervalMs:    30_000,
				maxRetryBackoffMs: 300_000,
				agentTimeoutMs:    600_000,
				stallTimeoutMs:    120_000,
			},
		},
		{
			name: "explicit values are returned",
			cfg: &WorkflowConfig{
				MaxConcurrencyRaw:    4,
				PollIntervalMsRaw:    1_000,
				MaxRetryBackoffMsRaw: 42_000,
				AgentTimeoutMsRaw:    9_999,
				StallTimeoutMsRaw:    8_888,
			},
			want: struct {
				maxConcurrency    int
				pollIntervalMs    int
				maxRetryBackoffMs int
				agentTimeoutMs    int
				stallTimeoutMs    int
			}{
				maxConcurrency:    4,
				pollIntervalMs:    1_000,
				maxRetryBackoffMs: 42_000,
				agentTimeoutMs:    9_999,
				stallTimeoutMs:    8_888,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want.maxConcurrency, tt.cfg.MaxConcurrency())
			assert.Equal(t, tt.want.pollIntervalMs, tt.cfg.PollIntervalMs())
			assert.Equal(t, tt.want.maxRetryBackoffMs, tt.cfg.MaxRetryBackoffMs())
			assert.Equal(t, tt.want.agentTimeoutMs, tt.cfg.AgentTimeoutMs())
			assert.Equal(t, tt.want.stallTimeoutMs, tt.cfg.StallTimeoutMs())
		})
	}
}

func TestWorkflowConfig_RequiredFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		cfg            *WorkflowConfig
		wantModelErr   error
		wantProjectErr error
	}{
		{
			name:           "missing required fields",
			cfg:            &WorkflowConfig{},
			wantModelErr:   ErrModelRequired,
			wantProjectErr: ErrProjectURLRequired,
		},
		{
			name: "fields provided",
			cfg: &WorkflowConfig{
				ModelRaw:      "gpt-5",
				ProjectURLRaw: "https://example.com/project",
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			model, modelErr := tt.cfg.Model()
			if tt.wantModelErr != nil {
				require.ErrorIs(t, modelErr, tt.wantModelErr)
			} else {
				require.NoError(t, modelErr)
				assert.NotEmpty(t, model)
			}

			projectURL, projectErr := tt.cfg.ProjectURL()
			if tt.wantProjectErr != nil {
				require.ErrorIs(t, projectErr, tt.wantProjectErr)
			} else {
				require.NoError(t, projectErr)
				assert.NotEmpty(t, projectURL)
			}
		})
	}
}

func TestWorkflowConfig_NewSectionDefaults(t *testing.T) {
	t.Parallel()

	var nilCfg *WorkflowConfig

	assert.Equal(t, defaultTrackerType, nilCfg.TrackerType())
	assert.Equal(t, "", nilCfg.TrackerProjectURL())
	assert.Equal(t, "", nilCfg.TrackerTeamID())
	assert.Equal(t, "", nilCfg.TrackerAssigneeID())
	assert.Equal(t, defaultLocalBoardDir, nilCfg.LocalBoardDir())
	assert.Equal(t, defaultLocalIssuePrefix, nilCfg.LocalIssuePrefix())
	assert.Equal(t, defaultPollIntervalMs, nilCfg.PollingIntervalMs())
	assert.Equal(t, defaultWorkspaceBaseDir, nilCfg.WorkspaceBaseDir())
	assert.Equal(t, "", nilCfg.HookBeforeRun())
	assert.Equal(t, "", nilCfg.HookAfterRun())
	assert.Equal(t, "", nilCfg.HookBeforeRemove())
	assert.Equal(t, defaultCodexBinaryPath, nilCfg.CodexBinaryPath())
	assert.Equal(t, "", nilCfg.CodexModel())
	assert.Equal(t, defaultApprovalPolicy, nilCfg.CodexApprovalPolicy())
	assert.Equal(t, defaultSandbox, nilCfg.CodexSandbox())
	assert.Equal(t, defaultTeamStateDir, nilCfg.TeamStateDir())
	assert.Equal(t, defaultTeamExecutionMode, nilCfg.TeamExecutionMode())
	assert.Equal(t, defaultTeamWorkerMode, nilCfg.WorkerMode())
	assert.Equal(t, defaultWorkflowTimelineDir, nilCfg.WorkflowTimelineDir())
	assert.False(t, nilCfg.LinearIssueDetailsEnabled())
	assert.False(t, nilCfg.LinearSyncCommentsEnabled())
	assert.Equal(t, LinearSyncCommentsModeReplyThread, nilCfg.LinearSyncCommentsMode())
	assert.Equal(t, 100, nilCfg.LinearSyncCommentsQueueSize())
	assert.Equal(t, 5000, nilCfg.LinearSyncCommentsPollIntervalMs())

	cfg := &WorkflowConfig{}
	assert.Equal(t, defaultTrackerType, cfg.TrackerType())
	assert.Equal(t, defaultLocalBoardDir, cfg.LocalBoardDir())
	assert.Equal(t, defaultLocalIssuePrefix, cfg.LocalIssuePrefix())
	assert.Equal(t, defaultPollIntervalMs, cfg.PollingIntervalMs())
	assert.Equal(t, defaultWorkspaceBaseDir, cfg.WorkspaceBaseDir())
	assert.Equal(t, "", cfg.Polling.BackoffStrategy)
	assert.Equal(t, "", cfg.Workspace.BranchPrefix)
	assert.Equal(t, defaultCodexBinaryPath, cfg.CodexBinaryPath())
	assert.Equal(t, defaultApprovalPolicy, cfg.CodexApprovalPolicy())
	assert.Equal(t, defaultSandbox, cfg.CodexSandbox())
	assert.Equal(t, defaultTeamStateDir, cfg.TeamStateDir())
	assert.Equal(t, defaultTeamExecutionMode, cfg.TeamExecutionMode())
	assert.Equal(t, defaultTeamWorkerMode, cfg.WorkerMode())
	assert.Equal(t, defaultWorkflowTimelineDir, cfg.WorkflowTimelineDir())

	legacyCfg := &WorkflowConfig{
		ModelRaw:      "openai/gpt-5-codex",
		ProjectURLRaw: "https://linear.app/example/project/legacy",
	}
	assert.Equal(t, "openai/gpt-5-codex", legacyCfg.CodexModel())
	assert.Equal(t, "https://linear.app/example/project/legacy", legacyCfg.TrackerProjectURL())
}

func TestWorkflowConfig_WorkerMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  *WorkflowConfig
		want string
	}{
		{
			name: "nil config defaults to tmux",
			cfg:  nil,
			want: defaultTeamWorkerMode,
		},
		{
			name: "empty config defaults to tmux",
			cfg:  &WorkflowConfig{},
			want: defaultTeamWorkerMode,
		},
		{
			name: "explicit tmux mode is preserved",
			cfg: &WorkflowConfig{
				Team: TeamSectionConfig{WorkerMode: "tmux"},
			},
			want: "tmux",
		},
		{
			name: "mode is normalized",
			cfg: &WorkflowConfig{
				Team: TeamSectionConfig{WorkerMode: " TMUX "},
			},
			want: "tmux",
		},
		{
			name: "unknown mode falls back to tmux",
			cfg: &WorkflowConfig{
				Team: TeamSectionConfig{WorkerMode: "custom"},
			},
			want: defaultTeamWorkerMode,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.cfg.WorkerMode())
		})
	}
}

func TestWorkflowConfig_LinearConfig(t *testing.T) {
	t.Parallel()

	enabled := true
	disabled := false

	tests := []struct {
		name string
		cfg  *WorkflowConfig
		want struct {
			issueDetails bool
			syncEnabled  bool
			syncMode     string
			wantErr      bool
		}
	}{
		{
			name: "nil config uses safe defaults",
			cfg:  nil,
			want: struct {
				issueDetails bool
				syncEnabled  bool
				syncMode     string
				wantErr      bool
			}{syncMode: LinearSyncCommentsModeReplyThread},
		},
		{
			name: "Linear tracker defaults issue details on and comment sync off",
			cfg: &WorkflowConfig{
				Tracker: TrackerConfig{Type: "linear"},
			},
			want: struct {
				issueDetails bool
				syncEnabled  bool
				syncMode     string
				wantErr      bool
			}{
				issueDetails: true,
				syncMode:     LinearSyncCommentsModeReplyThread,
			},
		},
		{
			name: "explicit enablement and top-level mode are preserved for Linear tracker",
			cfg: &WorkflowConfig{
				Tracker: TrackerConfig{Type: "linear"},
				Linear: LinearConfigSection{
					IssueDetails: LinearIssueDetailsConfig{Enabled: &enabled},
					SyncComments: LinearSyncCommentsConfig{Enabled: true, Mode: "top_level"},
				},
			},
			want: struct {
				issueDetails bool
				syncEnabled  bool
				syncMode     string
				wantErr      bool
			}{
				issueDetails: true,
				syncEnabled:  true,
				syncMode:     LinearSyncCommentsModeTopLevel,
			},
		},
		{
			name: "explicit issue details disablement is preserved for Linear tracker",
			cfg: &WorkflowConfig{
				Tracker: TrackerConfig{Type: "linear"},
				Linear: LinearConfigSection{
					IssueDetails: LinearIssueDetailsConfig{Enabled: &disabled},
				},
			},
			want: struct {
				issueDetails bool
				syncEnabled  bool
				syncMode     string
				wantErr      bool
			}{
				syncMode: LinearSyncCommentsModeReplyThread,
			},
		},
		{
			name: "non-Linear tracker ignores explicit Linear enablement",
			cfg: &WorkflowConfig{
				Tracker: TrackerConfig{Type: "github"},
				Linear: LinearConfigSection{
					IssueDetails: LinearIssueDetailsConfig{Enabled: &enabled},
					SyncComments: LinearSyncCommentsConfig{Enabled: true},
				},
			},
			want: struct {
				issueDetails bool
				syncEnabled  bool
				syncMode     string
				wantErr      bool
			}{
				syncMode: LinearSyncCommentsModeReplyThread,
			},
		},
		{
			name: "invalid sync mode is surfaced",
			cfg: &WorkflowConfig{
				Tracker: TrackerConfig{Type: "linear"},
				Linear: LinearConfigSection{
					SyncComments: LinearSyncCommentsConfig{Enabled: true, Mode: "unsupported"},
				},
			},
			want: struct {
				issueDetails bool
				syncEnabled  bool
				syncMode     string
				wantErr      bool
			}{
				issueDetails: true,
				syncEnabled:  true,
				syncMode:     "unsupported",
				wantErr:      true,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want.issueDetails, tt.cfg.LinearIssueDetailsEnabled())
			assert.Equal(t, tt.want.syncEnabled, tt.cfg.LinearSyncCommentsEnabled())
			assert.Equal(t, tt.want.syncMode, tt.cfg.LinearSyncCommentsMode())
			if tt.want.wantErr {
				require.Error(t, tt.cfg.ValidateLinearSyncCommentsMode())
			} else {
				require.NoError(t, tt.cfg.ValidateLinearSyncCommentsMode())
			}
		})
	}
}

func TestWorkflowConfig_LinearSyncCommentsEffectiveMode(t *testing.T) {
	t.Parallel()

	defaultCfg := &WorkflowConfig{Tracker: TrackerConfig{Type: "linear"}}
	mode, err := defaultCfg.LinearSyncCommentsEffectiveMode(false)
	require.NoError(t, err)
	assert.Equal(t, LinearSyncCommentsModeTopLevel, mode)

	explicitReply := &WorkflowConfig{
		Tracker: TrackerConfig{Type: "linear"},
		Linear: LinearConfigSection{
			SyncComments: LinearSyncCommentsConfig{Mode: LinearSyncCommentsModeReplyThread},
		},
	}
	_, err = explicitReply.LinearSyncCommentsEffectiveMode(false)
	require.ErrorIs(t, err, ErrLinearSyncCommentsModeUnsupported)

	explicitTopLevel := &WorkflowConfig{
		Tracker: TrackerConfig{Type: "linear"},
		Linear: LinearConfigSection{
			SyncComments: LinearSyncCommentsConfig{Mode: LinearSyncCommentsModeTopLevel},
		},
	}
	mode, err = explicitTopLevel.LinearSyncCommentsEffectiveMode(false)
	require.NoError(t, err)
	assert.Equal(t, LinearSyncCommentsModeTopLevel, mode)
}

func TestWorkflowConfig_TeamExecutionMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  *WorkflowConfig
		want string
	}{
		{
			name: "nil config defaults to team mode",
			cfg:  nil,
			want: TeamExecutionModeTeam,
		},
		{
			name: "internal tracker defaults to team mode",
			cfg: &WorkflowConfig{
				Tracker: TrackerConfig{Type: "internal"},
			},
			want: TeamExecutionModeTeam,
		},
		{
			name: "github tracker defaults to single mode",
			cfg: &WorkflowConfig{
				Tracker: TrackerConfig{Type: "github"},
			},
			want: TeamExecutionModeSingle,
		},
		{
			name: "explicit single alias agent maps to single mode",
			cfg: &WorkflowConfig{
				Team: TeamSectionConfig{ExecutionMode: "agent"},
			},
			want: TeamExecutionModeSingle,
		},
		{
			name: "explicit orchestrator alias maps to single mode",
			cfg: &WorkflowConfig{
				Team: TeamSectionConfig{ExecutionMode: "orchestrator"},
			},
			want: TeamExecutionModeSingle,
		},
		{
			name: "explicit team mode is preserved",
			cfg: &WorkflowConfig{
				Tracker: TrackerConfig{Type: "github"},
				Team:    TeamSectionConfig{ExecutionMode: "team"},
			},
			want: TeamExecutionModeTeam,
		},
		{
			name: "explicit unknown mode is surfaced as-is",
			cfg: &WorkflowConfig{
				Team: TeamSectionConfig{ExecutionMode: "custom"},
			},
			want: "custom",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.cfg.TeamExecutionMode())
		})
	}
}

func TestWorkflowConfig_AgentTypeDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  *WorkflowConfig
		want string
	}{
		{
			name: "nil config returns default agent type",
			cfg:  nil,
			want: defaultAgentType,
		},
		{
			name: "empty AgentConfig returns default agent type",
			cfg:  &WorkflowConfig{},
			want: defaultAgentType,
		},
		{
			name: "explicit agent type is returned",
			cfg: &WorkflowConfig{
				Agent: AgentConfig{Type: "opencode"},
			},
			want: "opencode",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.cfg.AgentType())
		})
	}
}

func TestWorkflowConfig_OpenCodeDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		cfg            *WorkflowConfig
		wantBinaryPath string
		wantPort       int
		wantPassword   string
		wantUsername   string
	}{
		{
			name:           "nil config returns defaults",
			cfg:            nil,
			wantBinaryPath: defaultOpenCodeBinaryPath,
			wantPort:       0,
			wantPassword:   "",
			wantUsername:   "",
		},
		{
			name:           "empty OpenCodeConfig returns defaults",
			cfg:            &WorkflowConfig{},
			wantBinaryPath: defaultOpenCodeBinaryPath,
			wantPort:       0,
			wantPassword:   "",
			wantUsername:   "",
		},
		{
			name: "populated config returns configured values",
			cfg: &WorkflowConfig{
				OpenCode: OpenCodeConfig{
					BinaryPath: "custom-opencode",
					Port:       8080,
					Password:   "secret123",
					Username:   "testuser",
				},
			},
			wantBinaryPath: "custom-opencode",
			wantPort:       8080,
			wantPassword:   "secret123",
			wantUsername:   "testuser",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantBinaryPath, tt.cfg.OpenCodeBinaryPath())
			assert.Equal(t, tt.wantPort, tt.cfg.OpenCodePort())
			assert.Equal(t, tt.wantPassword, tt.cfg.OpenCodePassword())
			assert.Equal(t, tt.wantUsername, tt.cfg.OpenCodeUsername())
		})
	}
}

func TestWorkflowConfig_GitHubDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		cfg          *WorkflowConfig
		wantOwner    string
		wantRepo     string
		wantToken    string
		wantAssignee string
		wantLabels   []string
		wantEndpoint string
	}{
		{
			name:         "nil config returns defaults",
			cfg:          nil,
			wantOwner:    "",
			wantRepo:     "",
			wantToken:    "",
			wantAssignee: "",
			wantLabels:   []string{},
			wantEndpoint: defaultGitHubEndpoint,
		},
		{
			name:         "empty TrackerConfig returns defaults",
			cfg:          &WorkflowConfig{},
			wantOwner:    "",
			wantRepo:     "",
			wantToken:    "",
			wantAssignee: "",
			wantLabels:   []string{},
			wantEndpoint: defaultGitHubEndpoint,
		},
		{
			name: "populated config returns configured values",
			cfg: &WorkflowConfig{
				Tracker: TrackerConfig{
					Owner:    "myorg",
					Repo:     "myrepo",
					Token:    "ghp_token123",
					Assignee: "bot",
					Labels:   []string{"bug", "feature"},
					Endpoint: "https://github.enterprise.com/api/v3",
				},
			},
			wantOwner:    "myorg",
			wantRepo:     "myrepo",
			wantToken:    "ghp_token123",
			wantAssignee: "bot",
			wantLabels:   []string{"bug", "feature"},
			wantEndpoint: "https://github.enterprise.com/api/v3",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantOwner, tt.cfg.GitHubOwner())
			assert.Equal(t, tt.wantRepo, tt.cfg.GitHubRepo())
			assert.Equal(t, tt.wantToken, tt.cfg.GitHubToken())
			assert.Equal(t, tt.wantAssignee, tt.cfg.GitHubAssignee())
			assert.Equal(t, tt.wantLabels, tt.cfg.GitHubLabels())
			assert.Equal(t, tt.wantEndpoint, tt.cfg.GitHubEndpoint())
		})
	}
}

func TestWorkflowConfig_LocalTrackerDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		cfg             *WorkflowConfig
		wantBoardDir    string
		wantIssuePrefix string
	}{
		{
			name:            "nil config returns defaults",
			cfg:             nil,
			wantBoardDir:    defaultLocalBoardDir,
			wantIssuePrefix: defaultLocalIssuePrefix,
		},
		{
			name:            "empty tracker config returns defaults",
			cfg:             &WorkflowConfig{},
			wantBoardDir:    defaultLocalBoardDir,
			wantIssuePrefix: defaultLocalIssuePrefix,
		},
		{
			name: "populated tracker config returns configured values",
			cfg: &WorkflowConfig{
				Tracker: TrackerConfig{
					BoardDir:    ".contrabass/custom-board",
					IssuePrefix: "OPS",
				},
			},
			wantBoardDir:    ".contrabass/custom-board",
			wantIssuePrefix: "OPS",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantBoardDir, tt.cfg.LocalBoardDir())
			assert.Equal(t, tt.wantIssuePrefix, tt.cfg.LocalIssuePrefix())
		})
	}
}

func TestParseWorkflow_GitHubConfig(t *testing.T) {
	const (
		wantOwner    = "test-org"
		wantRepo     = "test-repo"
		wantAssignee = "test-bot"
	)
	t.Setenv("GITHUB_OWNER", wantOwner)
	t.Setenv("GITHUB_REPO", wantRepo)
	t.Setenv("GITHUB_ASSIGNEE", wantAssignee)

	path := "../../testdata/workflow.github.md"

	cfg, err := ParseWorkflow(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Verify tracker type and agent type
	assert.Equal(t, "github", cfg.TrackerType())
	assert.Equal(t, "opencode", cfg.AgentType())

	// Verify GitHub tracker config
	assert.Equal(t, wantOwner, cfg.GitHubOwner())
	assert.Equal(t, wantRepo, cfg.GitHubRepo())
	assert.Equal(t, wantAssignee, cfg.GitHubAssignee())
	assert.Equal(t, []string{"bug", "agent"}, cfg.GitHubLabels())
	assert.Equal(t, "https://api.github.com", cfg.GitHubEndpoint())

	// Verify OpenCode config
	assert.Equal(t, "opencode serve", cfg.OpenCodeBinaryPath())
	assert.Equal(t, 4096, cfg.OpenCodePort())
	assert.Equal(t, "admin", cfg.OpenCodeUsername())

	// Token and Password use env var references - they will be empty unless env is set
	// The raw field contains the env var reference
	assert.Equal(t, "", cfg.GitHubToken())
	assert.Equal(t, "", cfg.OpenCodePassword())

	// Verify prompt template
	assert.Contains(t, cfg.PromptTemplate, "{{ issue.title }}")
	assert.Contains(t, cfg.PromptTemplate, "{{ issue.description }}")
	assert.Contains(t, cfg.PromptTemplate, "{{ issue.url }}")
}

func TestParseWorkflow_BackwardCompat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		path                string
		wantTrackerType     string
		wantAgentType       string
		wantMaxConcurrency  int
		wantCodexBinaryPath string
	}{
		{
			name:                "workflow.md uses default internal tracker and codex agent",
			path:                "../../testdata/workflow.md",
			wantTrackerType:     "internal",
			wantAgentType:       "codex",
			wantMaxConcurrency:  8,
			wantCodexBinaryPath: defaultCodexBinaryPath,
		},
		{
			name:                "workflow.demo.md uses linear tracker and codex agent",
			path:                "../../testdata/workflow.demo.md",
			wantTrackerType:     "linear",
			wantAgentType:       "codex",
			wantMaxConcurrency:  3,
			wantCodexBinaryPath: "codex app-server",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := ParseWorkflow(tt.path)
			require.NoError(t, err)
			require.NotNil(t, cfg)

			assert.Equal(t, tt.wantTrackerType, cfg.TrackerType())
			assert.Equal(t, tt.wantAgentType, cfg.AgentType())
			assert.Equal(t, tt.wantMaxConcurrency, cfg.MaxConcurrency())
			assert.Equal(t, tt.wantCodexBinaryPath, cfg.CodexBinaryPath())
		})
	}
}

func TestWorkflowConfig_OhMyOpenCodeDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		cfg               *WorkflowConfig
		wantPluginVersion string
		wantPlugins       []string
		wantAgents        map[string]OhMyOpenCodeAgent
		wantCategories    map[string]OhMyOpenCodeCategory
		wantProviderName  string
		wantBaseURL       string
		wantAPIKey        string
	}{
		{
			name:              "nil config returns defaults",
			cfg:               nil,
			wantPluginVersion: defaultOhMyOpenCodePluginVersion,
			wantPlugins:       []string{},
			wantAgents:        map[string]OhMyOpenCodeAgent{},
			wantCategories:    map[string]OhMyOpenCodeCategory{},
			wantProviderName:  "",
			wantBaseURL:       "",
			wantAPIKey:        "",
		},
		{
			name:              "empty OhMyOpenCodeConfig returns defaults",
			cfg:               &WorkflowConfig{},
			wantPluginVersion: defaultOhMyOpenCodePluginVersion,
			wantPlugins:       []string{},
			wantAgents:        map[string]OhMyOpenCodeAgent{},
			wantCategories:    map[string]OhMyOpenCodeCategory{},
			wantProviderName:  "",
			wantBaseURL:       "",
			wantAPIKey:        "",
		},
		{
			name: "populated config returns configured values",
			cfg: &WorkflowConfig{
				OhMyOpenCode: OhMyOpenCodeConfig{
					PluginVersion: "oh-my-opencode@3.10.0",
					Plugins:       []string{"opencode-antigravity-auth@1.2.7-beta.3"},
					Agents: map[string]OhMyOpenCodeAgent{
						"sisyphus": {Model: "anthropic/claude-opus-4-5"},
					},
					Categories: map[string]OhMyOpenCodeCategory{
						"quick": {Model: "anthropic/claude-haiku-4-5"},
					},
					Provider: OhMyOpenCodeProvider{
						Name:    "anthropic",
						BaseURL: "https://proxy.example.com/v1",
						APIKey:  "sk-test",
					},
				},
			},
			wantPluginVersion: "oh-my-opencode@3.10.0",
			wantPlugins:       []string{"opencode-antigravity-auth@1.2.7-beta.3"},
			wantAgents: map[string]OhMyOpenCodeAgent{
				"sisyphus": {Model: "anthropic/claude-opus-4-5"},
			},
			wantCategories: map[string]OhMyOpenCodeCategory{
				"quick": {Model: "anthropic/claude-haiku-4-5"},
			},
			wantProviderName: "anthropic",
			wantBaseURL:      "https://proxy.example.com/v1",
			wantAPIKey:       "sk-test",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantPluginVersion, tt.cfg.OhMyOpenCodePluginVersion())
			assert.Equal(t, tt.wantPlugins, tt.cfg.OhMyOpenCodePlugins())
			assert.Equal(t, tt.wantAgents, tt.cfg.OhMyOpenCodeAgents())
			assert.Equal(t, tt.wantCategories, tt.cfg.OhMyOpenCodeCategories())
			assert.Equal(t, tt.wantProviderName, tt.cfg.OhMyOpenCodeProviderName())
			assert.Equal(t, tt.wantBaseURL, tt.cfg.OhMyOpenCodeProviderBaseURL())
			assert.Equal(t, tt.wantAPIKey, tt.cfg.OhMyOpenCodeProviderAPIKey())
		})
	}
}

func TestParseWorkflow_OhMyOpenCodeConfig(t *testing.T) {
	const wantAPIKey = "sk-test-key"
	t.Setenv("OPENCODE_PROVIDER_API_KEY", wantAPIKey)
	t.Setenv("GITHUB_OWNER", "test-org")
	t.Setenv("GITHUB_REPO", "test-repo")
	t.Setenv("GITHUB_ASSIGNEE", "test-bot")

	cfg, err := ParseWorkflow("../../testdata/workflow.ohmyopencode.md")
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "github", cfg.TrackerType())
	assert.Equal(t, "oh-my-opencode", cfg.AgentType())
	assert.Equal(t, "oh-my-opencode@3.10.0", cfg.OhMyOpenCodePluginVersion())
	assert.Equal(t, []string{"opencode-antigravity-auth@1.2.7-beta.3"}, cfg.OhMyOpenCodePlugins())

	agents := cfg.OhMyOpenCodeAgents()
	require.Contains(t, agents, "sisyphus")
	assert.Equal(t, "anthropic/claude-sonnet-4-6", agents["sisyphus"].Model)

	categories := cfg.OhMyOpenCodeCategories()
	require.Contains(t, categories, "quick")
	assert.Equal(t, "anthropic/claude-haiku-4-5", categories["quick"].Model)
	require.Contains(t, categories, "visual-engineering")
	assert.Equal(t, "anthropic/claude-sonnet-4-6", categories["visual-engineering"].Model)

	assert.Equal(t, "anthropic", cfg.OhMyOpenCodeProviderName())
	assert.Equal(t, "https://proxy.example.com/v1", cfg.OhMyOpenCodeProviderBaseURL())
	assert.Equal(t, wantAPIKey, cfg.OhMyOpenCodeProviderAPIKey())

	assert.Equal(t, "opencode serve", cfg.OpenCodeBinaryPath())
	assert.Equal(t, 8787, cfg.OpenCodePort())
	assert.Contains(t, cfg.PromptTemplate, "{{ issue.title }}")
}

func TestWorkflowConfig_CloneDeepCopy(t *testing.T) {
	t.Parallel()

	original := &WorkflowConfig{
		Tracker: TrackerConfig{
			Labels: []string{"bug", "agent"},
		},
		OhMyOpenCode: OhMyOpenCodeConfig{
			Plugins: []string{"plugin-a"},
			Agents: map[string]OhMyOpenCodeAgent{
				"sisyphus": {Model: "anthropic/claude-sonnet-4-6"},
			},
			Categories: map[string]OhMyOpenCodeCategory{
				"quick": {Model: "anthropic/claude-haiku-4-5"},
			},
		},
	}

	cloned := original.Clone()
	require.NotNil(t, cloned)

	cloned.Tracker.Labels[0] = "mutated"
	cloned.OhMyOpenCode.Plugins[0] = "plugin-b"
	cloned.OhMyOpenCode.Agents["sisyphus"] = OhMyOpenCodeAgent{Model: "openai/gpt-5"}
	cloned.OhMyOpenCode.Categories["quick"] = OhMyOpenCodeCategory{Model: "openai/gpt-5-mini"}

	assert.Equal(t, []string{"bug", "agent"}, original.Tracker.Labels)
	assert.Equal(t, []string{"plugin-a"}, original.OhMyOpenCode.Plugins)
	assert.Equal(t, "anthropic/claude-sonnet-4-6", original.OhMyOpenCode.Agents["sisyphus"].Model)
	assert.Equal(t, "anthropic/claude-haiku-4-5", original.OhMyOpenCode.Categories["quick"].Model)
}

func TestWorkflowConfig_OMXDefaults(t *testing.T) {
	t.Parallel()

	var nilCfg *WorkflowConfig
	assert.Equal(t, defaultOMXBinaryPath, nilCfg.OMXBinaryPath())
	assert.Equal(t, defaultOMXTeamSpec, nilCfg.OMXTeamSpec())
	assert.Equal(t, defaultOMXPollIntervalMs, nilCfg.OMXPollIntervalMs())
	assert.Equal(t, defaultOMXStartupTimeoutMs, nilCfg.OMXStartupTimeoutMs())
	assert.False(t, nilCfg.OMXRalph())

	cfg := &WorkflowConfig{}
	assert.Equal(t, defaultOMXBinaryPath, cfg.OMXBinaryPath())
	assert.Equal(t, defaultOMXTeamSpec, cfg.OMXTeamSpec())
	assert.Equal(t, defaultOMXPollIntervalMs, cfg.OMXPollIntervalMs())
	assert.Equal(t, defaultOMXStartupTimeoutMs, cfg.OMXStartupTimeoutMs())
	assert.False(t, cfg.OMXRalph())

	custom := &WorkflowConfig{OMX: OMXConfig{
		BinaryPath:       "/usr/local/bin/omx",
		TeamSpec:         "2:executor",
		PollIntervalMs:   1500,
		StartupTimeoutMs: 22000,
		Ralph:            true,
	}}
	assert.Equal(t, "/usr/local/bin/omx", custom.OMXBinaryPath())
	assert.Equal(t, "2:executor", custom.OMXTeamSpec())
	assert.Equal(t, 1500, custom.OMXPollIntervalMs())
	assert.Equal(t, 22000, custom.OMXStartupTimeoutMs())
	assert.True(t, custom.OMXRalph())
}

func TestWorkflowConfig_OMCDefaults(t *testing.T) {
	t.Parallel()

	var nilCfg *WorkflowConfig
	assert.Equal(t, defaultOMCBinaryPath, nilCfg.OMCBinaryPath())
	assert.Equal(t, defaultOMCTeamSpec, nilCfg.OMCTeamSpec())
	assert.Equal(t, defaultOMCPollIntervalMs, nilCfg.OMCPollIntervalMs())
	assert.Equal(t, defaultOMCStartupTimeoutMs, nilCfg.OMCStartupTimeoutMs())

	cfg := &WorkflowConfig{}
	assert.Equal(t, defaultOMCBinaryPath, cfg.OMCBinaryPath())
	assert.Equal(t, defaultOMCTeamSpec, cfg.OMCTeamSpec())
	assert.Equal(t, defaultOMCPollIntervalMs, cfg.OMCPollIntervalMs())
	assert.Equal(t, defaultOMCStartupTimeoutMs, cfg.OMCStartupTimeoutMs())

	custom := &WorkflowConfig{OMC: OMCConfig{
		BinaryPath:       "/usr/local/bin/omc",
		TeamSpec:         "2:claude",
		PollIntervalMs:   1200,
		StartupTimeoutMs: 21000,
	}}
	assert.Equal(t, "/usr/local/bin/omc", custom.OMCBinaryPath())
	assert.Equal(t, "2:claude", custom.OMCTeamSpec())
	assert.Equal(t, 1200, custom.OMCPollIntervalMs())
	assert.Equal(t, 21000, custom.OMCStartupTimeoutMs())
}

func TestParseWorkflow_OMXConfig(t *testing.T) {
	t.Parallel()

	cfg, err := ParseWorkflow("../../testdata/workflow.omx.md")
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "omx", cfg.AgentType())
	assert.Equal(t, "omx", cfg.OMXBinaryPath())
	assert.Equal(t, "2:executor", cfg.OMXTeamSpec())
	assert.Equal(t, 1500, cfg.OMXPollIntervalMs())
	assert.Equal(t, 22000, cfg.OMXStartupTimeoutMs())
	assert.True(t, cfg.OMXRalph())
	assert.Contains(t, cfg.PromptTemplate, "OMX")
}

func TestParseWorkflow_OMCConfig(t *testing.T) {
	t.Parallel()

	cfg, err := ParseWorkflow("../../testdata/workflow.omc.md")
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "omc", cfg.AgentType())
	assert.Equal(t, "omc", cfg.OMCBinaryPath())
	assert.Equal(t, "2:claude", cfg.OMCTeamSpec())
	assert.Equal(t, 1200, cfg.OMCPollIntervalMs())
	assert.Equal(t, 21000, cfg.OMCStartupTimeoutMs())
	assert.Contains(t, cfg.PromptTemplate, "OMC")
}

func TestWorkflowConfig_TunableDefaults(t *testing.T) {
	t.Parallel()

	var nilCfg *WorkflowConfig

	// All nil-config calls must return hardcoded defaults.
	assert.Equal(t, 15_000*time.Millisecond, nilCfg.WebSSEKeepaliveInterval())
	assert.Equal(t, 30_000*time.Millisecond, nilCfg.CodexHandshakeTimeout())
	assert.Equal(t, 4_000*time.Millisecond, nilCfg.CodexOverloadRetryCap())
	assert.Equal(t, 100*time.Millisecond, nilCfg.CodexOverloadStartDelay())
	assert.Equal(t, 5_000*time.Millisecond, nilCfg.TeamRestartGracePeriod())
	assert.Equal(t, 500*time.Millisecond, nilCfg.TeamGovernanceRetryDelay())
	assert.Equal(t, 10_000*time.Millisecond, nilCfg.TeamHeartbeatInterval())
	assert.Equal(t, 30_000*time.Millisecond, nilCfg.TrackerHTTPTimeout())

	// Zero/negative values also return defaults.
	zeroCfg := &WorkflowConfig{}
	assert.Equal(t, 15_000*time.Millisecond, zeroCfg.WebSSEKeepaliveInterval())
	assert.Equal(t, 30_000*time.Millisecond, zeroCfg.CodexHandshakeTimeout())
	assert.Equal(t, 4_000*time.Millisecond, zeroCfg.CodexOverloadRetryCap())
	assert.Equal(t, 100*time.Millisecond, zeroCfg.CodexOverloadStartDelay())
	assert.Equal(t, 5_000*time.Millisecond, zeroCfg.TeamRestartGracePeriod())
	assert.Equal(t, 500*time.Millisecond, zeroCfg.TeamGovernanceRetryDelay())
	assert.Equal(t, 10_000*time.Millisecond, zeroCfg.TeamHeartbeatInterval())
	assert.Equal(t, 30_000*time.Millisecond, zeroCfg.TrackerHTTPTimeout())

	// Explicit values are honoured.
	custom := &WorkflowConfig{
		Web: WebConfig{SSEKeepaliveIntervalMsRaw: 5_000},
		Codex: CodexConfig{
			HandshakeTimeoutMsRaw:   60_000,
			OverloadRetryCapMsRaw:   8_000,
			OverloadStartDelayMsRaw: 200,
		},
		Team: TeamSectionConfig{
			RestartGracePeriodMsRaw:   10_000,
			GovernanceRetryDelayMsRaw: 1_000,
			HeartbeatIntervalMsRaw:    20_000,
		},
		Tracker: TrackerConfig{HTTPTimeoutMsRaw: 60_000},
	}
	assert.Equal(t, 5_000*time.Millisecond, custom.WebSSEKeepaliveInterval())
	assert.Equal(t, 60_000*time.Millisecond, custom.CodexHandshakeTimeout())
	assert.Equal(t, 8_000*time.Millisecond, custom.CodexOverloadRetryCap())
	assert.Equal(t, 200*time.Millisecond, custom.CodexOverloadStartDelay())
	assert.Equal(t, 10_000*time.Millisecond, custom.TeamRestartGracePeriod())
	assert.Equal(t, 1_000*time.Millisecond, custom.TeamGovernanceRetryDelay())
	assert.Equal(t, 20_000*time.Millisecond, custom.TeamHeartbeatInterval())
	assert.Equal(t, 60_000*time.Millisecond, custom.TrackerHTTPTimeout())
}
