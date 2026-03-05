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
				assert.Equal(t, "https://linear.app/example/project/symphony", projectURL)

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
