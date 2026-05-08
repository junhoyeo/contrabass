package tracker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseModelOverride(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "opus shorthand", body: "Fix bug\n<!-- model: opus -->", want: "opus"},
		{name: "sonnet shorthand", body: "<!-- model: sonnet -->\nDetails here", want: "sonnet"},
		{name: "haiku shorthand", body: "<!-- model: haiku -->", want: "haiku"},
		{name: "configured opus full name", body: "<!-- model: claude-opus-4-5 -->", want: "claude-opus-4-5"},
		{name: "full model name", body: "<!-- model: claude-opus-4-6 -->", want: "claude-opus-4-6"},
		{name: "full sonnet name", body: "<!-- model: claude-sonnet-4-6 -->", want: "claude-sonnet-4-6"},
		{name: "full haiku name", body: "<!-- model: claude-haiku-4-5 -->", want: "claude-haiku-4-5"},
		{name: "configured provider prefixed opus", body: "<!-- model: anthropic/claude-opus-4-5 -->", want: "anthropic/claude-opus-4-5"},
		{name: "with provider prefix", body: "<!-- model: anthropic/claude-opus-4-6 -->", want: "anthropic/claude-opus-4-6"},
		{name: "extra spaces", body: "<!--  model:  opus  -->", want: "opus"},
		{name: "no override", body: "Just a regular issue body", want: ""},
		{name: "empty body", body: "", want: ""},
		// The whitelist was removed: arbitrary model names are now passed through
		// so users can reference new providers/models without a release.
		{name: "non-claude model accepted", body: "<!-- model: gpt-4 -->", want: "gpt-4"},
		{name: "current crs default model", body: "<!-- model: gpt-5.5 -->", want: "gpt-5.5"},
		{name: "openai prefixed model", body: "<!-- model: openai/gpt-5-codex -->", want: "openai/gpt-5-codex"},
		{name: "malformed comment", body: "<!-- model: -->", want: ""},
		{name: "embedded in text", body: "Please use\n<!-- model: opus -->\nfor this task", want: "opus"},
		{name: "multiple overrides uses first", body: "<!-- model: opus -->\n<!-- model: sonnet -->", want: "opus"},
		{name: "first override wins regardless of vendor", body: "<!-- model: gpt-4 -->\n<!-- model: sonnet -->", want: "gpt-4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ParseModelOverride(tt.body)
			assert.Equal(t, tt.want, got)
		})
	}
}
