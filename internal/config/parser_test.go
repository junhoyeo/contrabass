package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseWorkflow(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "testdata", "workflow.md")

	tests := []struct {
		name           string
		path           string
		content        string
		env            map[string]string
		wantErr        error
		assertionsFunc func(t *testing.T, cfg *WorkflowConfig)
	}{
		{
			name:    "parses realistic workflow fixture",
			path:    fixturePath,
			wantErr: nil,
			assertionsFunc: func(t *testing.T, cfg *WorkflowConfig) {
				require.NotNil(t, cfg)
				assert.Equal(t, 8, cfg.MaxConcurrencyRaw)
				assert.Equal(t, 15_000, cfg.PollIntervalMsRaw)
				assert.Equal(t, 240_000, cfg.MaxRetryBackoffMsRaw)
				assert.Equal(t, 900_000, cfg.AgentTimeoutMsRaw)
				assert.Equal(t, 60_000, cfg.StallTimeoutMsRaw)

				model, err := cfg.Model()
				require.NoError(t, err)
				assert.Equal(t, "openai/gpt-5-codex", model)

				projectURL, err := cfg.ProjectURL()
				require.NoError(t, err)
				assert.Equal(t, "https://linear.app/contrabass/project/contrabass-symphony-demo-4bded72b77d3", projectURL)

				assert.Contains(t, cfg.PromptTemplate, "{{ issue.title }}")
				assert.Contains(t, cfg.PromptTemplate, "{{ issue.description }}")
				assert.Contains(t, cfg.PromptTemplate, "{{ issue.url }}")
			},
		},
		{
			name: "resolves env var indirection",
			content: `---
model: $WORKFLOW_MODEL
project_url: $WORKFLOW_PROJECT_URL
---
Task prompt body.
`,
			env: map[string]string{
				"WORKFLOW_MODEL":       "openai/gpt-5.3-codex",
				"WORKFLOW_PROJECT_URL": "https://linear.app/example/project/env",
			},
			wantErr: nil,
			assertionsFunc: func(t *testing.T, cfg *WorkflowConfig) {
				model, err := cfg.Model()
				require.NoError(t, err)
				assert.Equal(t, "openai/gpt-5.3-codex", model)

				projectURL, err := cfg.ProjectURL()
				require.NoError(t, err)
				assert.Equal(t, "https://linear.app/example/project/env", projectURL)
			},
		},
		{
			name: "accepts prompt-only workflow",
			content: `You are working on a ticket.

Use issue context and prepare a plan.
`,
			wantErr: nil,
			assertionsFunc: func(t *testing.T, cfg *WorkflowConfig) {
				require.NotNil(t, cfg)
				assert.Contains(t, cfg.PromptTemplate, "You are working on a ticket")
			},
		},
		{
			name: "accepts unterminated front matter with an empty prompt",
			content: `---
model: openai/gpt-5-codex
project_url: https://linear.app/example/project/unterminated
max_concurrency: 3
`,
			wantErr: ErrUnterminatedFrontMatter,
			assertionsFunc: func(t *testing.T, cfg *WorkflowConfig) {
				require.NotNil(t, cfg)
				assert.Equal(t, "", cfg.PromptTemplate)
				assert.Equal(t, 3, cfg.MaxConcurrencyRaw)
			},
		},
		{
			name: "invalid yaml returns typed error",
			content: `---
model: [
project_url: https://linear.app/example/project/bad
---
prompt
`,
			wantErr: ErrInvalidYAML,
		},
		{
			name: "front matter must be map",
			content: `---
- item
- item2
---
prompt
`,
			wantErr: ErrFrontMatterNotMap,
		},
		{
			name:           "empty file parses as empty prompt",
			content:        "",
			wantErr:        nil,
			assertionsFunc: func(t *testing.T, cfg *WorkflowConfig) { assert.Equal(t, "", cfg.PromptTemplate) },
		},
		{
			name:    "minimal front matter: just opening delimiter",
			content: "---",
			wantErr: nil,
			assertionsFunc: func(t *testing.T, cfg *WorkflowConfig) {
				require.NotNil(t, cfg)
				assert.Equal(t, "", cfg.PromptTemplate)
			},
		},
		{
			name:    "minimal front matter: opening delimiter with newline",
			content: "---\n",
			wantErr: nil,
			assertionsFunc: func(t *testing.T, cfg *WorkflowConfig) {
				require.NotNil(t, cfg)
				assert.Equal(t, "", cfg.PromptTemplate)
			},
		},
		{
			name:    "minimal front matter: empty YAML block",
			content: "---\n---",
			wantErr: nil,
			assertionsFunc: func(t *testing.T, cfg *WorkflowConfig) {
				require.NotNil(t, cfg)
				assert.Equal(t, "", cfg.PromptTemplate)
			},
		},
		{
			name:    "minimal front matter: empty YAML block with trailing newline",
			content: "---\n---\n",
			wantErr: nil,
			assertionsFunc: func(t *testing.T, cfg *WorkflowConfig) {
				require.NotNil(t, cfg)
				assert.Equal(t, "", cfg.PromptTemplate)
			},
		},
		{
			name: "prompt template preserves literal $VARIABLE patterns",
			content: `---
model: openai/gpt-5-codex
project_url: https://linear.app/example/project/test
---
Fix $ISSUE_ID in the codebase.
`,
			wantErr: nil,
			assertionsFunc: func(t *testing.T, cfg *WorkflowConfig) {
				require.NotNil(t, cfg)
				assert.Contains(t, cfg.PromptTemplate, "Fix $ISSUE_ID")
				assert.NotContains(t, cfg.PromptTemplate, "Fix  in")
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			path := tt.path
			if tt.content != "" || tt.path == "" {
				tempFile := filepath.Join(t.TempDir(), "WORKFLOW.md")
				err := os.WriteFile(tempFile, []byte(tt.content), 0o644)
				require.NoError(t, err)
				path = tempFile
			}

			cfg, err := ParseWorkflow(path)
			if tt.wantErr != nil {
				require.Error(t, err)
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}

			if tt.assertionsFunc != nil {
				tt.assertionsFunc(t, cfg)
			}
		})
	}
}

func TestParseWorkflow_FullSpecSections(t *testing.T) {
	t.Parallel()

	content := `---
max_concurrency: 5
poll_interval_ms: 17000
model: openai/gpt-5.3-codex
project_url: https://linear.app/example/project/legacy
tracker:
  type: jira
  project_url: https://linear.app/example/project/nested
  team_id: team-42
  assignee_id: user-77
polling:
  interval_ms: 12000
  backoff_strategy: linear
workspace:
  base_dir: /tmp/worktrees
  branch_prefix: task/
hooks:
  before_run: ./scripts/before.sh
  after_run: ./scripts/after.sh
  before_remove: ./scripts/cleanup.sh
codex:
  binary_path: /usr/local/bin/codex
  model: openai/gpt-5.3-codex-mini
  approval_policy: manual
  sandbox: none
---
Prompt body.
`

	path := filepath.Join(t.TempDir(), "WORKFLOW.md")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	cfg, err := ParseWorkflow(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "jira", cfg.TrackerType())
	assert.Equal(t, "https://linear.app/example/project/nested", cfg.TrackerProjectURL())
	assert.Equal(t, "team-42", cfg.TrackerTeamID())
	assert.Equal(t, "user-77", cfg.TrackerAssigneeID())
	assert.Equal(t, 12_000, cfg.PollingIntervalMs())
	assert.Equal(t, "linear", cfg.PollingBackoffStrategy())
	assert.Equal(t, "/tmp/worktrees", cfg.WorkspaceBaseDir())
	assert.Equal(t, "task/", cfg.WorkspaceBranchPrefix())
	assert.Equal(t, "./scripts/before.sh", cfg.HookBeforeRun())
	assert.Equal(t, "./scripts/after.sh", cfg.HookAfterRun())
	assert.Equal(t, "./scripts/cleanup.sh", cfg.HookBeforeRemove())
	assert.Equal(t, "/usr/local/bin/codex", cfg.CodexBinaryPath())
	assert.Equal(t, "openai/gpt-5.3-codex-mini", cfg.CodexModel())
	assert.Equal(t, "manual", cfg.CodexApprovalPolicy())
	assert.Equal(t, "none", cfg.CodexSandbox())

	model, modelErr := cfg.Model()
	require.NoError(t, modelErr)
	assert.Equal(t, "openai/gpt-5.3-codex", model)

	projectURL, projectURLErr := cfg.ProjectURL()
	require.NoError(t, projectURLErr)
	assert.Equal(t, "https://linear.app/example/project/legacy", projectURL)
}

func TestParseWorkflow_BackwardCompatibleMinimal(t *testing.T) {
	t.Parallel()

	content := `---
model: openai/gpt-5-codex
project_url: https://linear.app/example/project/minimal
---
Minimal prompt.
`

	path := filepath.Join(t.TempDir(), "WORKFLOW.md")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	cfg, err := ParseWorkflow(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	model, modelErr := cfg.Model()
	require.NoError(t, modelErr)
	assert.Equal(t, "openai/gpt-5-codex", model)

	projectURL, projectURLErr := cfg.ProjectURL()
	require.NoError(t, projectURLErr)
	assert.Equal(t, "https://linear.app/example/project/minimal", projectURL)

	assert.Equal(t, defaultTrackerType, cfg.TrackerType())
	assert.Equal(t, defaultPollIntervalMs, cfg.PollingIntervalMs())
	assert.Equal(t, defaultBackoffStrategy, cfg.PollingBackoffStrategy())
	assert.Equal(t, defaultWorkspaceBaseDir, cfg.WorkspaceBaseDir())
	assert.Equal(t, defaultBranchPrefix, cfg.WorkspaceBranchPrefix())
	assert.Equal(t, defaultCodexBinaryPath, cfg.CodexBinaryPath())
	assert.Equal(t, "openai/gpt-5-codex", cfg.CodexModel())
	assert.Equal(t, defaultApprovalPolicy, cfg.CodexApprovalPolicy())
	assert.Equal(t, defaultSandbox, cfg.CodexSandbox())
}

func TestSplitFrontMatter_CRLFLineEnding(t *testing.T) {
	t.Parallel()

	// Test that CRLF line endings (\r\n) are handled correctly in front matter splitting
	content := "---\r\nmodel: openai/gpt-5-codex\r\nproject_url: https://linear.app/example/project/crlf\r\n---\r\nPrompt with CRLF.\r\n"

	frontMatter, prompt, hasFrontMatter, terminated := splitFrontMatter(content)

	require.True(t, hasFrontMatter)
	require.True(t, terminated)
	assert.Contains(t, frontMatter, "model: openai/gpt-5-codex")
	assert.Contains(t, frontMatter, "project_url: https://linear.app/example/project/crlf")
	assert.Contains(t, prompt, "Prompt with CRLF")
}

func TestResolveEnvToken_InvalidPattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  string
		ok    bool
	}{
		{
			name:  "valid env var pattern",
			value: "$VALID_VAR",
			want:  "", // will be empty since env var not set
			ok:    true,
		},
		{
			name:  "invalid: starts with number",
			value: "$123INVALID",
			want:  "",
			ok:    false,
		},
		{
			name:  "invalid: contains hyphen",
			value: "$INVALID-VAR",
			want:  "",
			ok:    false,
		},
		{
			name:  "invalid: contains dot",
			value: "$INVALID.VAR",
			want:  "",
			ok:    false,
		},
		{
			name:  "no dollar sign",
			value: "PLAIN_STRING",
			want:  "",
			ok:    false,
		},
		{
			name:  "empty after dollar",
			value: "$",
			want:  "",
			ok:    false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			result, ok := resolveEnvToken(tt.value)
			assert.Equal(t, tt.ok, ok)
			if ok {
				assert.Equal(t, tt.want, result)
			}
		})
	}
}
