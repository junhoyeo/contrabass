package config

import (
	"testing"

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
	assert.Equal(t, defaultPollIntervalMs, nilCfg.PollingIntervalMs())
	assert.Equal(t, defaultBackoffStrategy, nilCfg.PollingBackoffStrategy())
	assert.Equal(t, defaultWorkspaceBaseDir, nilCfg.WorkspaceBaseDir())
	assert.Equal(t, defaultBranchPrefix, nilCfg.WorkspaceBranchPrefix())
	assert.Equal(t, "", nilCfg.HookBeforeRun())
	assert.Equal(t, "", nilCfg.HookAfterRun())
	assert.Equal(t, "", nilCfg.HookBeforeRemove())
	assert.Equal(t, defaultCodexBinaryPath, nilCfg.CodexBinaryPath())
	assert.Equal(t, "", nilCfg.CodexModel())
	assert.Equal(t, defaultApprovalPolicy, nilCfg.CodexApprovalPolicy())
	assert.Equal(t, defaultSandbox, nilCfg.CodexSandbox())

	cfg := &WorkflowConfig{}
	assert.Equal(t, defaultTrackerType, cfg.TrackerType())
	assert.Equal(t, defaultPollIntervalMs, cfg.PollingIntervalMs())
	assert.Equal(t, defaultBackoffStrategy, cfg.PollingBackoffStrategy())
	assert.Equal(t, defaultWorkspaceBaseDir, cfg.WorkspaceBaseDir())
	assert.Equal(t, defaultBranchPrefix, cfg.WorkspaceBranchPrefix())
	assert.Equal(t, defaultCodexBinaryPath, cfg.CodexBinaryPath())
	assert.Equal(t, defaultApprovalPolicy, cfg.CodexApprovalPolicy())
	assert.Equal(t, defaultSandbox, cfg.CodexSandbox())

	legacyCfg := &WorkflowConfig{
		ModelRaw:      "openai/gpt-5-codex",
		ProjectURLRaw: "https://linear.app/example/project/legacy",
	}
	assert.Equal(t, "openai/gpt-5-codex", legacyCfg.CodexModel())
	assert.Equal(t, "https://linear.app/example/project/legacy", legacyCfg.TrackerProjectURL())
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

func TestParseWorkflow_GitHubConfig(t *testing.T) {
	t.Parallel()

	path := "../../testdata/workflow.github.md"

	cfg, err := ParseWorkflow(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Verify tracker type and agent type
	assert.Equal(t, "github", cfg.TrackerType())
	assert.Equal(t, "opencode", cfg.AgentType())

	// Verify GitHub tracker config
	assert.Equal(t, "example-org", cfg.GitHubOwner())
	assert.Equal(t, "example-repo", cfg.GitHubRepo())
	assert.Equal(t, "bot-user", cfg.GitHubAssignee())
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
			name:                "workflow.md uses linear tracker and codex agent",
			path:                "../../testdata/workflow.md",
			wantTrackerType:     "linear",
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
