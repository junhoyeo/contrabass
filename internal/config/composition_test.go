package config

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePromptLoader is a test double that returns canned content keyed by ref.
type fakePromptLoader struct {
	content map[string]string
}

func (f fakePromptLoader) Load(ref string) (string, error) {
	body, ok := f.content[ref]
	if !ok {
		return "", fmt.Errorf("fakePromptLoader: no content for ref %q", ref)
	}
	return body, nil
}

// ---- LoadRoot tests ---------------------------------------------------------

func TestLoadRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		wantErr  string
		assertFn func(t *testing.T, rc *RootConfig)
	}{
		{
			name: "valid minimal input",
			input: `
defaults:
  model: openai/gpt-4o
presets:
  demo:
    max_concurrency: 3
`,
			assertFn: func(t *testing.T, rc *RootConfig) {
				require.NotNil(t, rc)
				assert.Equal(t, "openai/gpt-4o", rc.Defaults.ModelRaw)
				require.Contains(t, rc.Presets, "demo")
				require.NotNil(t, rc.Presets["demo"].MaxConcurrencyRaw)
				assert.Equal(t, 3, *rc.Presets["demo"].MaxConcurrencyRaw)
			},
		},
		{
			name: "valid full sections",
			input: `
defaults:
  model: openai/gpt-5
  agent_timeout_ms: 600000
trackers:
  linear:
    type: linear
    project_url: https://linear.app/test
agents:
  codex:
    type: codex
presets:
  prod:
    tracker: linear
    agent_ref: codex
`,
			assertFn: func(t *testing.T, rc *RootConfig) {
				require.NotNil(t, rc)
				assert.Equal(t, "openai/gpt-5", rc.Defaults.ModelRaw)
				assert.Equal(t, 600000, rc.Defaults.AgentTimeoutMsRaw)
				require.Contains(t, rc.Trackers, "linear")
				require.NotNil(t, rc.Trackers["linear"].Type)
				assert.Equal(t, "linear", *rc.Trackers["linear"].Type)
				require.Contains(t, rc.Agents, "codex")
				require.NotNil(t, rc.Agents["codex"].Type)
				assert.Equal(t, "codex", *rc.Agents["codex"].Type)
				require.Contains(t, rc.Presets, "prod")
				assert.Equal(t, "linear", rc.Presets["prod"].Tracker)
				assert.Equal(t, "codex", rc.Presets["prod"].AgentRef)
			},
		},
		{
			name:    "empty input rejected",
			input:   "   \n  ",
			wantErr: "empty",
		},
		{
			name: "unknown top-level section rejected",
			input: `
defaults:
  model: gpt-4
unknown_section:
  foo: bar
`,
			wantErr: "unknown top-level section",
		},
		{
			name: "another unknown top-level key",
			input: `
presets:
  demo: {}
extra_key: value
`,
			wantErr: "unknown top-level section",
		},
		{
			name:    "invalid yaml rejected",
			input:   "defaults: [\nbad yaml",
			wantErr: "invalid yaml",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rc, err := LoadRoot(bytes.NewReader([]byte(tc.input)))
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				assert.Nil(t, rc)
				return
			}
			require.NoError(t, err)
			if tc.assertFn != nil {
				tc.assertFn(t, rc)
			}
		})
	}
}

// ---- Resolve tests ----------------------------------------------------------

// baseRootYAML is a representative RootConfig used by multiple test cases.
// Note: inline agent uses `agent:` key; named agent ref uses `agent_ref:` key.
const baseRootYAML = `
defaults:
  model: openai/gpt-4o
  agent_timeout_ms: 900000
  max_concurrency: 5
trackers:
  linear:
    type: linear
    project_url: $CONTRABASS_PROJECT_URL
    team_id: team-abc
  github:
    type: github
    owner: acme
    repo: myrepo
agents:
  codex_runner:
    type: codex
    codex:
      binary_path: codex app-server
  omc_runner:
    type: omc
    omc:
      binary_path: omc
      team_spec: "1:claude"
presets:
  defaults_only: {}
  hardening:
    tracker: linear
    agent_ref: codex_runner
    agent_timeout_ms: 1800000
    max_concurrency: 2
    model: openai/o3
  omc:
    agent:
      type: omc
      omc:
        binary_path: omc
  with_prompt:
    tracker: linear
    prompt_ref: standard
  bad_tracker:
    tracker: bogus_tracker
  bad_agent:
    agent_ref: bogus_agent
  conflict:
    agent_ref: codex_runner
    agent:
      type: omc
  github_preset:
    tracker: github
    agent_ref: codex_runner
    max_concurrency: 8
`

func mustLoadRoot(t *testing.T, yamlStr string) *RootConfig {
	t.Helper()
	rc, err := LoadRoot(bytes.NewReader([]byte(yamlStr)))
	require.NoError(t, err)
	return rc
}

func TestResolve(t *testing.T) {
	// Cannot use t.Parallel here: some subtests call t.Setenv, which is
	// incompatible with parallel ancestors in Go 1.21+.
	rc := mustLoadRoot(t, baseRootYAML)

	tests := []struct {
		name       string
		presetName string
		loader     PromptLoader
		env        map[string]string
		wantErr    string
		assertFn   func(t *testing.T, cfg *WorkflowConfig)
	}{
		// --- Spec scenario: Unknown preset name fails fast ---
		{
			name:       "unknown preset fails fast",
			presetName: "does-not-exist",
			wantErr:    `unknown preset "does-not-exist"`,
		},
		// --- Spec scenario: defaults-only preset inherits all defaults ---
		{
			name:       "defaults-only preset",
			presetName: "defaults_only",
			assertFn: func(t *testing.T, cfg *WorkflowConfig) {
				assert.Equal(t, "openai/gpt-4o", cfg.ModelRaw)
				assert.Equal(t, 900000, cfg.AgentTimeoutMsRaw)
				assert.Equal(t, 5, cfg.MaxConcurrencyRaw)
				// No tracker set, so tracker fields are zero.
				assert.Empty(t, cfg.Tracker.Type)
			},
		},
		// --- Spec scenario: Preset overrides a defaults field ---
		{
			name:       "preset overrides agent_timeout_ms",
			presetName: "hardening",
			assertFn: func(t *testing.T, cfg *WorkflowConfig) {
				assert.Equal(t, 1800000, cfg.AgentTimeoutMsRaw)
				assert.Equal(t, 2, cfg.MaxConcurrencyRaw)
				assert.Equal(t, "openai/o3", cfg.ModelRaw)
			},
		},
		// --- Spec scenario: Preset selects a tracker template by name ---
		{
			name:       "preset selects linear tracker",
			presetName: "hardening",
			env:        map[string]string{"CONTRABASS_PROJECT_URL": "https://linear.app/test/project/abc"},
			assertFn: func(t *testing.T, cfg *WorkflowConfig) {
				assert.Equal(t, "linear", cfg.Tracker.Type)
				assert.Equal(t, "https://linear.app/test/project/abc", cfg.Tracker.ProjectURL)
				assert.Equal(t, "team-abc", cfg.Tracker.TeamID)
			},
		},
		// --- ENV var defined at defaults level resolves in merged preset ---
		{
			name:       "env var at defaults level resolves after merge",
			presetName: "with_prompt",
			env:        map[string]string{"CONTRABASS_PROJECT_URL": "https://linear.app/env-resolved"},
			loader:     fakePromptLoader{content: map[string]string{"standard": "hello {{ issue.title }}"}},
			assertFn: func(t *testing.T, cfg *WorkflowConfig) {
				assert.Equal(t, "https://linear.app/env-resolved", cfg.Tracker.ProjectURL)
			},
		},
		// --- ENV var unset surfaces as empty string ---
		{
			name:       "unset env var resolves to empty string",
			presetName: "with_prompt",
			loader:     fakePromptLoader{content: map[string]string{"standard": "body"}},
			// CONTRABASS_PROJECT_URL not set → resolves to ""
			assertFn: func(t *testing.T, cfg *WorkflowConfig) {
				assert.Equal(t, "", cfg.Tracker.ProjectURL)
			},
		},
		// --- Spec scenario: Preset inlines an agent override ---
		{
			name:       "inline agent override",
			presetName: "omc",
			assertFn: func(t *testing.T, cfg *WorkflowConfig) {
				assert.Equal(t, "omc", cfg.Agent.Type)
				assert.Equal(t, "omc", cfg.OMC.BinaryPath)
			},
		},
		// --- Spec scenario: Named agent template applied ---
		{
			name:       "named agent template applied",
			presetName: "hardening",
			assertFn: func(t *testing.T, cfg *WorkflowConfig) {
				assert.Equal(t, "codex", cfg.Agent.Type)
				assert.Equal(t, "codex app-server", cfg.Codex.BinaryPath)
			},
		},
		// --- Spec scenario: Missing tracker reference fails fast ---
		{
			name:       "missing tracker reference fails fast",
			presetName: "bad_tracker",
			wantErr:    `unknown tracker "bogus_tracker"`,
		},
		// --- Missing agent reference fails fast ---
		{
			name:       "missing agent reference fails fast",
			presetName: "bad_agent",
			wantErr:    `unknown agent "bogus_agent"`,
		},
		// --- Spec scenario: Conflicting inline+named agent fails fast ---
		{
			name:       "conflicting inline and named agent fails fast",
			presetName: "conflict",
			wantErr:    `sets both`,
		},
		// --- Custom PromptLoader supplies prompt body ---
		{
			name:       "custom prompt loader supplies prompt body",
			presetName: "with_prompt",
			loader:     fakePromptLoader{content: map[string]string{"standard": "hello {{ issue.title }}"}},
			assertFn: func(t *testing.T, cfg *WorkflowConfig) {
				assert.Equal(t, "hello {{ issue.title }}", cfg.PromptTemplate)
			},
		},
		// --- PromptLoader error propagates out of Resolve ---
		{
			name:       "prompt loader error propagates",
			presetName: "with_prompt",
			loader:     fakePromptLoader{content: map[string]string{}}, // "standard" not present
			wantErr:    `prompt loader failed for ref "standard"`,
		},
		// --- Slice-replace semantics: tracker labels ---
		{
			name:       "slice replace — github tracker labels",
			presetName: "github_preset",
			assertFn: func(t *testing.T, cfg *WorkflowConfig) {
				// github tracker has no labels set, so cfg.Tracker.Labels should be nil/empty.
				assert.Empty(t, cfg.Tracker.Labels)
				assert.Equal(t, "github", cfg.Tracker.Type)
				assert.Equal(t, "acme", cfg.Tracker.Owner)
				assert.Equal(t, "myrepo", cfg.Tracker.Repo)
			},
		},
		// --- Nested map merge: preset adds fields on top of tracker template ---
		{
			name:       "nested struct merge tracker then preset scalar",
			presetName: "github_preset",
			assertFn: func(t *testing.T, cfg *WorkflowConfig) {
				// Tracker fields from template.
				assert.Equal(t, "acme", cfg.Tracker.Owner)
				// Agent fields from codex_runner template.
				assert.Equal(t, "codex", cfg.Agent.Type)
				// Preset scalar override.
				assert.Equal(t, 8, cfg.MaxConcurrencyRaw)
				// Default still present for non-overridden field.
				assert.Equal(t, 900000, cfg.AgentTimeoutMsRaw)
			},
		},
		// --- nil loader falls back to FilePromptLoader (no prompt_ref, so no I/O) ---
		{
			name:       "nil loader uses default (no prompt_ref, no IO)",
			presetName: "defaults_only",
			loader:     nil,
			assertFn: func(t *testing.T, cfg *WorkflowConfig) {
				assert.Empty(t, cfg.PromptTemplate)
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Set env vars for this test case.
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			cfg, err := rc.Resolve(tc.presetName, tc.loader)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				assert.Nil(t, cfg)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, cfg)
			if tc.assertFn != nil {
				tc.assertFn(t, cfg)
			}
		})
	}
}

// TestResolve_ENVModelFromDefaults exercises the spec scenario "ENV var defined
// at defaults level resolves in the merged preset" for the model field.
func TestResolve_ENVModelFromDefaults(t *testing.T) {
	// t.Setenv requires no t.Parallel on the same test.
	t.Setenv("CONTRABASS_MODEL", "openai/gpt-5.5")

	yaml := `
defaults:
  model: $CONTRABASS_MODEL
presets:
  demo:
    max_concurrency: 3
`
	rc := mustLoadRoot(t, yaml)
	cfg, err := rc.Resolve("demo", nil)
	require.NoError(t, err)
	assert.Equal(t, "openai/gpt-5.5", cfg.ModelRaw)
}

// TestResolve_ENVUnset exercises the spec scenario "ENV var unset surfaces as
// empty string".
func TestResolve_ENVUnset(t *testing.T) {
	// t.Setenv requires no t.Parallel on the same test.
	// Ensure var is not set in this process.
	t.Setenv("CONTRABASS_MODEL_UNSET_XYZ", "")

	yaml := `
defaults:
  model: $CONTRABASS_MODEL_UNSET_XYZ
presets:
  demo: {}
`
	rc := mustLoadRoot(t, yaml)
	cfg, err := rc.Resolve("demo", nil)
	require.NoError(t, err)
	assert.Equal(t, "", cfg.ModelRaw)
}

// TestResolve_SliceReplaceLabels confirms that tracker template labels replace
// (not append to) any prior labels.
func TestResolve_SliceReplaceLabels(t *testing.T) {
	t.Parallel()

	yamlStr := `
defaults: {}
trackers:
  gh:
    type: github
    labels:
      - bug
      - needs-triage
presets:
  p:
    tracker: gh
`
	rc := mustLoadRoot(t, yamlStr)
	cfg, err := rc.Resolve("p", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"bug", "needs-triage"}, cfg.Tracker.Labels)
}

// TestResolve_TrackerLabelReplaceByPreset confirms a second tracker with
// different labels replaces the first (no append).
func TestResolve_TrackerLabelReplaceByPreset(t *testing.T) {
	t.Parallel()

	yamlStr := `
defaults: {}
trackers:
  t1:
    type: github
    labels: [alpha, beta]
  t2:
    type: github
    labels: [gamma]
presets:
  use_t1:
    tracker: t1
  use_t2:
    tracker: t2
`
	rc := mustLoadRoot(t, yamlStr)

	cfg1, err := rc.Resolve("use_t1", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"alpha", "beta"}, cfg1.Tracker.Labels)

	cfg2, err := rc.Resolve("use_t2", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"gamma"}, cfg2.Tracker.Labels)
}

// TestResolve_PromptLoaderFakeContent verifies the fake loader path end-to-end.
func TestResolve_PromptLoaderFakeContent(t *testing.T) {
	t.Parallel()

	yamlStr := `
defaults: {}
presets:
  p:
    prompt_ref: my_prompt
`
	rc := mustLoadRoot(t, yamlStr)
	loader := fakePromptLoader{content: map[string]string{"my_prompt": "Implement {{ issue.title }}"}}
	cfg, err := rc.Resolve("p", loader)
	require.NoError(t, err)
	assert.Equal(t, "Implement {{ issue.title }}", cfg.PromptTemplate)
}

// TestResolve_PromptLoaderMissingRef verifies that a missing prompt ref returns
// a clear error.
func TestResolve_PromptLoaderMissingRef(t *testing.T) {
	t.Parallel()

	yamlStr := `
defaults: {}
presets:
  p:
    prompt_ref: missing_ref
`
	rc := mustLoadRoot(t, yamlStr)
	loader := fakePromptLoader{content: map[string]string{}}
	_, err := rc.Resolve("p", loader)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "prompt loader failed") || strings.Contains(err.Error(), "missing_ref"),
		"error should mention prompt loader or the ref: %v", err)
}

// TestLoadRoot_MissingRequiredSections verifies that a document with no presets
// still parses (no required sections enforced at load time — Resolve validates).
func TestLoadRoot_MissingRequiredSections(t *testing.T) {
	t.Parallel()

	// A document with only defaults and no presets is valid to load.
	yamlStr := `
defaults:
  model: gpt-4
`
	rc, err := LoadRoot(bytes.NewReader([]byte(yamlStr)))
	require.NoError(t, err)
	assert.Empty(t, rc.Presets)
}

// ---- HIGH-1: Scalar merge should be "replace", not "non-zero overlay" --------

// TestResolve_ScalarPresence_BoolFalseOverridesTrue verifies that a later layer
// can explicitly set a bool to false even when defaults (or tracker) set it true.
func TestResolve_ScalarPresence_BoolFalseOverridesTrue(t *testing.T) {
	t.Parallel()

	yamlStr := `
defaults: {}
trackers:
  t:
    auto_close_already_implemented: true
presets:
  override_false:
    tracker: t
  keep_true:
    tracker: t
`
	rc := mustLoadRoot(t, yamlStr)

	// The tracker sets auto_close = true; no preset override → should be true.
	cfgTrue, err := rc.Resolve("keep_true", nil)
	require.NoError(t, err)
	assert.True(t, cfgTrue.Tracker.AutoCloseAlreadyImplementedRaw, "tracker template true must propagate")

	// A second tracker with explicit false must be able to clear it.
	yamlStr2 := `
defaults: {}
trackers:
  t:
    auto_close_already_implemented: false
presets:
  p:
    tracker: t
`
	rc2 := mustLoadRoot(t, yamlStr2)
	cfgFalse, err := rc2.Resolve("p", nil)
	require.NoError(t, err)
	assert.False(t, cfgFalse.Tracker.AutoCloseAlreadyImplementedRaw, "explicit false must not be silently ignored")
}

// TestResolve_ScalarPresence_IntZeroOverridesNonZero verifies that a later layer
// can explicitly set an int to 0 (e.g. disable a timeout).
func TestResolve_ScalarPresence_IntZeroOverridesNonZero(t *testing.T) {
	t.Parallel()

	yamlStr := `
defaults:
  agent_timeout_ms: 900000
presets:
  disable_timeout:
    agent_timeout_ms: 0
  no_override: {}
`
	rc := mustLoadRoot(t, yamlStr)

	// Explicit 0 in preset must win over defaults 900000.
	cfgZero, err := rc.Resolve("disable_timeout", nil)
	require.NoError(t, err)
	assert.Equal(t, 0, cfgZero.AgentTimeoutMsRaw, "explicit 0 must override default")

	// No override: default must remain.
	cfgDefault, err := rc.Resolve("no_override", nil)
	require.NoError(t, err)
	assert.Equal(t, 900000, cfgDefault.AgentTimeoutMsRaw, "unset field must inherit default")
}

// TestResolve_ScalarPresence_EmptyStringOverridesNonEmpty verifies a preset can
// explicitly clear a string field back to empty.
func TestResolve_ScalarPresence_EmptyStringOverridesNonEmpty(t *testing.T) {
	t.Parallel()

	yamlStr := `
defaults:
  model: openai/gpt-4o
presets:
  clear_model:
    model: ""
  no_override: {}
`
	rc := mustLoadRoot(t, yamlStr)

	cfgCleared, err := rc.Resolve("clear_model", nil)
	require.NoError(t, err)
	assert.Equal(t, "", cfgCleared.ModelRaw, "explicit empty string must override non-empty default")

	cfgDefault, err := rc.Resolve("no_override", nil)
	require.NoError(t, err)
	assert.Equal(t, "openai/gpt-4o", cfgDefault.ModelRaw, "unset field must inherit default")
}

// TestResolve_ScalarPresence_OMXRalphFalseOverridesTrue verifies a later layer
// can set omx.ralph: false even when the agent template sets it true.
func TestResolve_ScalarPresence_OMXRalphFalseOverridesTrue(t *testing.T) {
	t.Parallel()

	yamlStr := `
defaults: {}
agents:
  ralph_agent:
    omx:
      ralph: true
presets:
  with_ralph:
    agent_ref: ralph_agent
`
	rc := mustLoadRoot(t, yamlStr)
	cfg, err := rc.Resolve("with_ralph", nil)
	require.NoError(t, err)
	assert.True(t, cfg.OMX.Ralph, "agent template ralph=true must propagate")

	// Now an inline agent with ralph: false must win.
	yamlStr2 := `
defaults: {}
presets:
  no_ralph:
    agent:
      omx:
        ralph: false
`
	rc2 := mustLoadRoot(t, yamlStr2)
	cfg2, err := rc2.Resolve("no_ralph", nil)
	require.NoError(t, err)
	assert.False(t, cfg2.OMX.Ralph, "explicit ralph=false must not be silently ignored")
}

// TestResolve_AgentTemplateTypeEmptyOverridesDefault verifies that agent.type
// uses pointer-presence, so an explicit empty string clears defaults.
func TestResolve_AgentTemplateTypeEmptyOverridesDefault(t *testing.T) {
	t.Parallel()

	yamlStr := `
defaults:
  agent:
    type: codex
agents:
  clear_type:
    type: ""
presets:
  p:
    agent_ref: clear_type
`
	rc := mustLoadRoot(t, yamlStr)
	cfg, err := rc.Resolve("p", nil)
	require.NoError(t, err)
	assert.Equal(t, "", cfg.Agent.Type, "explicit empty agent.type must clear defaults")
}

// TestResolve_AgentTemplateTypeAbsentKeepsDefault verifies that omitting
// agent.type leaves the defaults value intact.
func TestResolve_AgentTemplateTypeAbsentKeepsDefault(t *testing.T) {
	t.Parallel()

	yamlStr := `
defaults:
  agent:
    type: codex
agents:
  no_type:
    codex:
      binary_path: codex app-server
presets:
  p:
    agent_ref: no_type
`
	rc := mustLoadRoot(t, yamlStr)
	cfg, err := rc.Resolve("p", nil)
	require.NoError(t, err)
	assert.Equal(t, "codex", cfg.Agent.Type, "absent agent.type must inherit defaults")
	assert.Equal(t, "codex app-server", cfg.Codex.BinaryPath)
}

// TestResolve_InlineAgentTypeOverridesNamedTemplate verifies that a preset with
// an inline agent can replace the type that a named template would have used.
func TestResolve_InlineAgentTypeOverridesNamedTemplate(t *testing.T) {
	t.Parallel()

	yamlStr := `
defaults:
  agent:
    type: codex
agents:
  named_omc:
    type: omc
presets:
  named:
    agent_ref: named_omc
  inline:
    agent:
      type: opencode
`
	rc := mustLoadRoot(t, yamlStr)

	cfgNamed, err := rc.Resolve("named", nil)
	require.NoError(t, err)
	assert.Equal(t, "omc", cfgNamed.Agent.Type)

	cfgInline, err := rc.Resolve("inline", nil)
	require.NoError(t, err)
	assert.Equal(t, "opencode", cfgInline.Agent.Type, "inline agent.type must override the default/named baseline")
}

// ---- HIGH-2: Slice replacement fires even for explicit empty list -------------

// TestResolve_SliceReplaceEmptyListClearsLabels verifies that an explicit
// empty labels list in a tracker template clears any prior labels (including
// those from defaults, though defaults don't have labels in practice).
func TestResolve_SliceReplaceEmptyListClearsLabels(t *testing.T) {
	t.Parallel()

	// Two trackers: one with labels, one with explicit empty list.
	yamlStr := `
defaults: {}
trackers:
  with_labels:
    type: github
    labels: [bug, triage]
  clear_labels:
    type: github
    labels: []
presets:
  use_labels:
    tracker: with_labels
  use_clear:
    tracker: clear_labels
`
	rc := mustLoadRoot(t, yamlStr)

	cfgLabels, err := rc.Resolve("use_labels", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"bug", "triage"}, cfgLabels.Tracker.Labels, "labels must propagate")

	cfgClear, err := rc.Resolve("use_clear", nil)
	require.NoError(t, err)
	assert.NotNil(t, cfgClear.Tracker.Labels, "explicit empty labels must be non-nil (was set)")
	assert.Empty(t, cfgClear.Tracker.Labels, "explicit empty list must clear prior labels")
}

// TestResolve_SliceNilAbsentLeavesPrior verifies that a tracker template that
// does NOT set labels leaves any prior value untouched.
func TestResolve_SliceNilAbsentLeavesPrior(t *testing.T) {
	t.Parallel()

	// defaults has no labels; tracker has no labels field; labels should stay nil.
	yamlStr := `
defaults: {}
trackers:
  t:
    type: github
presets:
  p:
    tracker: t
`
	rc := mustLoadRoot(t, yamlStr)
	cfg, err := rc.Resolve("p", nil)
	require.NoError(t, err)
	// No labels were set anywhere, so the result should be nil/empty.
	assert.Empty(t, cfg.Tracker.Labels)
}

// ---- HIGH-3: Nested map merge across layers ----------------------------------

// TestResolve_MapMergeByKey_AgentTemplate verifies that an agent template can
// add or override individual map entries in OhMyOpenCode.Agents and
// OhMyOpenCode.Categories relative to defaults.
func TestResolve_MapMergeByKey_AgentTemplate(t *testing.T) {
	t.Parallel()

	yamlStr := `
defaults:
  oh_my_opencode:
    agents:
      planner:
        model: gpt-4o
      executor:
        model: gpt-4o-mini
agents:
  override_agent:
    oh_my_opencode:
      agents:
        executor:
          model: claude-3-5-sonnet
        reviewer:
          model: claude-3-haiku
presets:
  p:
    agent_ref: override_agent
`
	rc := mustLoadRoot(t, yamlStr)
	cfg, err := rc.Resolve("p", nil)
	require.NoError(t, err)

	// planner from defaults must survive.
	assert.Equal(t, "gpt-4o", cfg.OhMyOpenCode.Agents["planner"].Model, "defaults key must survive merge")
	// executor from agent template must win over defaults.
	assert.Equal(t, "claude-3-5-sonnet", cfg.OhMyOpenCode.Agents["executor"].Model, "agent template key must override defaults")
	// reviewer was only in agent template.
	assert.Equal(t, "claude-3-haiku", cfg.OhMyOpenCode.Agents["reviewer"].Model, "new key from agent template must be added")
}

// TestResolve_MapMergeByKey_PresetAddsKey verifies that a preset can add a new
// key to a map defined in defaults without clearing the existing keys.
func TestResolve_MapMergeByKey_PresetAddsKey(t *testing.T) {
	t.Parallel()

	yamlStr := `
defaults:
  oh_my_opencode:
    categories:
      implementation:
        model: gpt-4o
presets:
  add_key:
    oh_my_opencode:
      categories:
        verification:
          model: claude-3-haiku
`
	rc := mustLoadRoot(t, yamlStr)
	cfg, err := rc.Resolve("add_key", nil)
	require.NoError(t, err)

	// Both keys must be present.
	assert.Equal(t, "gpt-4o", cfg.OhMyOpenCode.Categories["implementation"].Model, "defaults key must survive")
	assert.Equal(t, "claude-3-haiku", cfg.OhMyOpenCode.Categories["verification"].Model, "preset new key must be added")
}

// TestResolve_MapMergeByKey_PresetOverridesKey verifies that a preset can
// override a specific key in a map from defaults without affecting other keys.
func TestResolve_MapMergeByKey_PresetOverridesKey(t *testing.T) {
	t.Parallel()

	yamlStr := `
defaults:
  oh_my_opencode:
    agents:
      planner:
        model: gpt-4o
      executor:
        model: gpt-4o-mini
presets:
  override_executor:
    oh_my_opencode:
      agents:
        executor:
          model: claude-opus-4
`
	rc := mustLoadRoot(t, yamlStr)
	cfg, err := rc.Resolve("override_executor", nil)
	require.NoError(t, err)

	assert.Equal(t, "gpt-4o", cfg.OhMyOpenCode.Agents["planner"].Model, "untouched defaults key must survive")
	assert.Equal(t, "claude-opus-4", cfg.OhMyOpenCode.Agents["executor"].Model, "same-key preset must override defaults")
}

// ---- MEDIUM-4: PresetConfig nested block overrides ---------------------------

// TestResolve_PresetOverridesNestedBlock_Web verifies that a preset can override
// web.sse_keepalive_interval_ms which was previously only settable via defaults.
func TestResolve_PresetOverridesNestedBlock_Web(t *testing.T) {
	t.Parallel()

	yamlStr := `
defaults:
  web:
    sse_keepalive_interval_ms: 15000
presets:
  custom_web:
    web:
      sse_keepalive_interval_ms: 5000
  no_override: {}
`
	rc := mustLoadRoot(t, yamlStr)

	cfgCustom, err := rc.Resolve("custom_web", nil)
	require.NoError(t, err)
	assert.Equal(t, 5000, cfgCustom.Web.SSEKeepaliveIntervalMsRaw, "preset must override web block")

	cfgDefault, err := rc.Resolve("no_override", nil)
	require.NoError(t, err)
	assert.Equal(t, 15000, cfgDefault.Web.SSEKeepaliveIntervalMsRaw, "unset preset must inherit defaults web block")
}

// TestResolve_PresetOverridesNestedBlock_Team verifies that a preset can override
// team.execution_mode.
func TestResolve_PresetOverridesNestedBlock_Team(t *testing.T) {
	t.Parallel()

	yamlStr := `
defaults:
  team:
    execution_mode: team
    max_workers: 5
presets:
  single_mode:
    team:
      execution_mode: single
  no_override: {}
`
	rc := mustLoadRoot(t, yamlStr)

	cfgSingle, err := rc.Resolve("single_mode", nil)
	require.NoError(t, err)
	assert.Equal(t, "single", cfgSingle.Team.ExecutionMode, "preset must override team.execution_mode")
	assert.Equal(t, 5, cfgSingle.Team.MaxWorkers, "untouched team field must survive from defaults")

	cfgDefault, err := rc.Resolve("no_override", nil)
	require.NoError(t, err)
	assert.Equal(t, "team", cfgDefault.Team.ExecutionMode, "unset preset must inherit defaults team.execution_mode")
}

// TestResolve_PresetOverridesNestedBlock_Workspace verifies workspace block overrides.
func TestResolve_PresetOverridesNestedBlock_Workspace(t *testing.T) {
	t.Parallel()

	yamlStr := `
defaults:
  workspace:
    base_dir: workspaces
    branch_prefix: symphony/
presets:
  custom_ws:
    workspace:
      branch_prefix: custom/
`
	rc := mustLoadRoot(t, yamlStr)

	cfg, err := rc.Resolve("custom_ws", nil)
	require.NoError(t, err)
	assert.Equal(t, "workspaces", cfg.Workspace.BaseDir, "untouched workspace field must survive from defaults")
	assert.Equal(t, "custom/", cfg.Workspace.BranchPrefix, "preset must override workspace.branch_prefix")
}

// ---- MEDIUM-5: API shape — agent (inline) vs agent_ref (named) --------------

// TestResolve_InlineAgent_YAMLShape verifies that the yaml key `agent` holds an
// inline AgentTemplate and `agent_ref` holds a named reference.
func TestResolve_InlineAgent_YAMLShape(t *testing.T) {
	t.Parallel()

	yamlStr := `
defaults: {}
agents:
  named_codex:
    type: codex
    codex:
      binary_path: codex app-server
presets:
  use_inline:
    agent:
      type: omc
      omc:
        binary_path: /usr/local/bin/omc
  use_named:
    agent_ref: named_codex
`
	rc := mustLoadRoot(t, yamlStr)

	cfgInline, err := rc.Resolve("use_inline", nil)
	require.NoError(t, err)
	assert.Equal(t, "omc", cfgInline.Agent.Type, "inline agent must set agent type")
	assert.Equal(t, "/usr/local/bin/omc", cfgInline.OMC.BinaryPath, "inline agent omc.binary_path must propagate")

	cfgNamed, err := rc.Resolve("use_named", nil)
	require.NoError(t, err)
	assert.Equal(t, "codex", cfgNamed.Agent.Type, "named agent_ref must set agent type")
	assert.Equal(t, "codex app-server", cfgNamed.Codex.BinaryPath, "named agent_ref codex.binary_path must propagate")
}

// TestResolve_InlineAndNamedConflict verifies that setting both `agent` (inline)
// and `agent_ref` (named) in the same preset fails fast.
func TestResolve_InlineAndNamedConflict(t *testing.T) {
	t.Parallel()

	yamlStr := `
defaults: {}
agents:
  some_agent:
    type: codex
presets:
  conflict:
    agent_ref: some_agent
    agent:
      type: omc
`
	rc := mustLoadRoot(t, yamlStr)
	_, err := rc.Resolve("conflict", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sets both", "error must mention the conflict")
}

// ---- LOW-6: nil receiver / nil Presets panics --------------------------------

// TestResolve_NilReceiver verifies that calling Resolve on a nil *RootConfig
// returns a clear error instead of panicking.
func TestResolve_NilReceiver(t *testing.T) {
	t.Parallel()

	var rc *RootConfig
	_, err := rc.Resolve("any", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil RootConfig", "error must mention nil receiver")
}

// TestResolve_NilPresets verifies that calling Resolve on a *RootConfig with no
// Presets map returns a clear error instead of panicking.
func TestResolve_NilPresets(t *testing.T) {
	t.Parallel()

	rc := &RootConfig{} // Presets is nil
	_, err := rc.Resolve("any", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no presets defined", "error must mention missing presets")
}
