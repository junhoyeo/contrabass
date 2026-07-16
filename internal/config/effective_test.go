package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestResolveConfigBuildsCanonicalRuntimeValues(t *testing.T) {
	t.Parallel()

	cfg := &WorkflowConfig{
		PollIntervalMsRaw: 2_500,
		ModelRaw:          "legacy-model",
		ProjectURLRaw:     "https://example.test/project",
		Tracker: TrackerConfig{
			Type:  "github",
			Token: "github-secret",
		},
		OpenCode: OpenCodeConfig{Password: "opencode-secret"},
		OhMyOpenCode: OhMyOpenCodeConfig{
			Provider: OhMyOpenCodeProvider{APIKey: "provider-secret"},
		},
	}

	resolved, err := Resolve(cfg)
	require.NoError(t, err)
	require.NotNil(t, resolved)
	assert.Equal(t, CurrentSchemaVersion, resolved.SchemaVersion)
	unredacted := resolved.UnredactedValues()
	assert.Equal(t, 2_500, nestedValue(t, unredacted, "polling", "interval_ms"))
	assert.Equal(t, "legacy-model", nestedValue(t, unredacted, "model"))
	assert.Equal(t, ConfigSourceDeprecatedAlias, resolved.Metadata["polling.interval_ms"].Source)
	assert.Equal(t, StartupOnly, resolved.Metadata["polling.interval_ms"].ReloadPolicy)
	assert.Equal(t, StartupOnly, resolved.Metadata["tracker.type"].ReloadPolicy)
	assert.Equal(t, StartupOnly, resolved.Metadata["codex.model"].ReloadPolicy)
	assert.Contains(t, resolved.Warnings, "codex.model falls back to model; configure codex.model to make the runner model explicit")
	assert.Contains(t, resolved.Warnings, "tracker.project_url falls back to project_url; configure tracker.project_url to make tracker scope explicit")

	effective := resolved.Effective()
	require.NotNil(t, effective)
	assert.Equal(t, RedactedValue, nestedValue(t, effective.Values, "tracker", "token"))
	assert.Equal(t, RedactedValue, nestedValue(t, effective.Values, "opencode", "password"))
	assert.Equal(t, RedactedValue, nestedValue(t, effective.Values, "oh_my_opencode", "provider", "api_key"))
	assert.Equal(t, "github-secret", nestedValue(t, unredacted, "tracker", "token"))
}

func TestBuildEffectiveConfigDoesNotRedactEmptySecrets(t *testing.T) {
	t.Parallel()

	effective, err := BuildEffectiveConfig(&WorkflowConfig{})
	require.NoError(t, err)
	assert.Equal(t, "", nestedValue(t, effective.Values, "tracker", "token"))
	assert.Equal(t, "", nestedValue(t, effective.Values, "opencode", "password"))
	assert.Equal(t, "", nestedValue(t, effective.Values, "oh_my_opencode", "provider", "api_key"))
}

func TestResolvedConfigSerializationIsAlwaysRedacted(t *testing.T) {
	t.Parallel()

	resolved, err := Resolve(&WorkflowConfig{Tracker: TrackerConfig{Token: "secret-token"}})
	require.NoError(t, err)

	jsonOutput, err := json.Marshal(resolved)
	require.NoError(t, err)
	assert.Contains(t, string(jsonOutput), RedactedValue)
	assert.NotContains(t, string(jsonOutput), "secret-token")

	yamlOutput, err := yaml.Marshal(resolved)
	require.NoError(t, err)
	assert.Contains(t, string(yamlOutput), RedactedValue)
	assert.NotContains(t, string(yamlOutput), "secret-token")

	yamlValueOutput, err := yaml.Marshal(*resolved)
	require.NoError(t, err)
	assert.Contains(t, string(yamlValueOutput), RedactedValue)
	assert.NotContains(t, string(yamlValueOutput), "secret-token")

	jsonValueOutput, err := json.Marshal(*resolved)
	require.NoError(t, err)
	assert.Contains(t, string(jsonValueOutput), RedactedValue)
	assert.NotContains(t, string(jsonValueOutput), "secret-token")

	embeddedOutput, err := json.Marshal(struct {
		Config ResolvedConfig `json:"config"`
	}{Config: *resolved})
	require.NoError(t, err)
	assert.Contains(t, string(embeddedOutput), RedactedValue)
	assert.NotContains(t, string(embeddedOutput), "secret-token")
}

func TestWorkflowConfigClonePreservesFieldPresence(t *testing.T) {
	t.Parallel()

	content := `---
schema_version: 1
omx:
  ralph: false
---
prompt
`
	path := filepath.Join(t.TempDir(), "WORKFLOW.md")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	cfg, err := ParseWorkflow(path)
	require.NoError(t, err)

	clone := cfg.Clone()
	require.True(t, clone.hasField("schema_version"))
	require.True(t, clone.hasField("omx.ralph"))
	delete(clone.presentFields, "omx.ralph")
	assert.True(t, cfg.hasField("omx.ralph"))
}

func TestResolveConfigTracksExplicitFalsePresence(t *testing.T) {
	t.Parallel()

	content := `---
tracker:
  type: linear
omx:
  ralph: false
linear:
  sync_comments:
    enabled: false
---
prompt
`
	path := filepath.Join(t.TempDir(), "WORKFLOW.md")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	cfg, err := ParseWorkflow(path)
	require.NoError(t, err)

	resolved, err := Resolve(cfg)
	require.NoError(t, err)
	assert.Equal(t, ConfigSourceExplicit, resolved.Metadata["omx.ralph"].Source)
	assert.Equal(t, ConfigSourceExplicit, resolved.Metadata["linear.sync_comments.enabled"].Source)
}

func nestedValue(t *testing.T, values map[string]any, path ...string) any {
	t.Helper()
	var current any = values
	for _, part := range path {
		mapping, ok := current.(map[string]any)
		require.True(t, ok, "expected map at %q, got %T", part, current)
		current, ok = mapping[part]
		require.True(t, ok, "missing config path %v", path)
	}
	return current
}
