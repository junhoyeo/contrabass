package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/types"
)

func TestRenderPrompt(t *testing.T) {
	t.Parallel()

	issue := types.Issue{
		Title:       "Implement workflow parser",
		Description: "Add parser, defaults, and tests for WORKFLOW.md",
		URL:         "https://linear.app/example/issue/SYM-101",
	}

	tests := []struct {
		name     string
		template string
		want     string
		wantErr  bool
	}{
		{
			name:     "renders known issue fields",
			template: "Title: {{ issue.title }}\nDescription: {{ issue.description }}\nURL: {{ issue.url }}",
			want:     "Title: Implement workflow parser\nDescription: Add parser, defaults, and tests for WORKFLOW.md\nURL: https://linear.app/example/issue/SYM-101",
			wantErr:  false,
		},
		{
			name:     "fails on unknown variable",
			template: "{{ issue.missing_field }}",
			wantErr:  true,
		},
		{
			name:     "fails on unknown filter",
			template: "{{ issue.title | no_such_filter }}",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, err := RenderPrompt(tt.template, issue)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, out)
		})
	}
}
