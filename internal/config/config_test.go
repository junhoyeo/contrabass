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
