// Package config provides workflow configuration parsing and composition for
// Contrabass.
//
// # Composition model
//
// A RootConfig holds four named sections:
//
//   - defaults  — scalar and nested fields that apply to every preset unless
//     overridden.
//   - trackers  — named tracker-credential templates (type, project_url, team_id …).
//   - agents    — named agent-runner templates (type, binary_path …).
//   - presets   — lightweight overrides that reference a tracker and/or agent
//     template by name and then layer their own field values on top.
//
// Calling Resolve("presetName", loader) performs a deterministic deep-merge in
// this order:
//
//  1. defaults
//  2. trackers[preset.Tracker]   (if set)
//  3. agents[preset.AgentRef]    (if set; mutually exclusive with InlineAgent)
//  4. preset's own field overrides
//
// Merge semantics:
//   - Scalars (string, int, bool): later layer wins when the field is non-nil
//     (pointer-presence). nil means absent; non-nil (even zero/false/empty)
//     means "explicitly set this value".
//   - Nested structs: merged field-by-field using the same pointer-presence
//     approach for scalars within the struct.
//   - Slices: replaced wholesale by the later layer when the field is non-nil
//     (pointer-presence). nil means absent; non-nil (even empty slice) means
//     "replace with this value, including clearing to empty".
//   - Nested maps: merged by key — later layer wins per key.
//
// # Pointer-presence approach
//
// To distinguish "field not set" from "field explicitly set to zero/false/empty",
// TrackerTemplate, AgentTemplate, and PresetConfig use pointer fields (*string,
// *int, *int64, *bool, *[]string) for every scalar/slice that a later layer
// might want to override with a zero/empty/false value. nil = absent (skip
// merge); non-nil = apply (even if the pointed-to value is zero). Only fields
// used exclusively as lookup keys (tracker/agent template names) remain as
// plain strings because empty-as-absent is correct semantics there.
//
// When copying merged pointer values into the final WorkflowConfig (which uses
// plain value types for downstream consumers), nil pointers fall back to zero
// values, matching the existing LoadWorkflow behavior.
//
// # ENV interpolation
//
// After the full merge, $VAR placeholders in string fields are resolved against
// the process environment exactly once, reusing the same resolveEnvReferencesValue
// walker used by LoadWorkflow. Resolution runs on the merged struct — not on each
// layer individually — so there is never ambiguity about which layer "won" when a
// field carries an env placeholder.
//
// # SaaS portability
//
// LoadRoot accepts an io.Reader, not a file path. CLI callers wrap *os.File; SaaS
// callers supply a bytes.Reader over YAML derived from DB rows, or skip YAML
// entirely and construct *RootConfig directly. The composition and ENV-resolution
// logic is storage-agnostic by design.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// PromptLoader loads a prompt body by reference string.
// The reference is an opaque key whose meaning is defined by the caller
// (file path, DB key, etc.).  Resolve passes the preset's PromptRef field.
// Returning an error from Load causes Resolve to fail and wrap the error.
type PromptLoader interface {
	Load(ref string) (string, error)
}

// FilePromptLoader is the default PromptLoader.  It reads the prompt body from
// a file under Root.  Root defaults to "." when empty.
type FilePromptLoader struct {
	Root string
}

// Load reads the file at Root/ref and returns its contents as a string.
func (f FilePromptLoader) Load(ref string) (string, error) {
	root := f.Root
	if root == "" {
		root = "."
	}
	path := root + string(os.PathSeparator) + ref
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("prompt loader: read %q: %w", path, err)
	}
	return string(data), nil
}

// RootConfig is the top-level structure for the composition YAML format.
//
// Field placement rule: anything that has a sensible global value belongs on
// Defaults; anything that is inherently per-preset (tracker credentials, agent
// runner selection) belongs on TrackerTemplate / AgentTemplate / PresetConfig.
//
// Workflow YAML must contain only the keys {defaults, trackers, agents, presets};
// LoadRoot rejects documents with unknown top-level keys to catch typos early.
type RootConfig struct {
	Defaults WorkflowDefaults           `yaml:"defaults"`
	Trackers map[string]TrackerTemplate `yaml:"trackers"`
	Agents   map[string]AgentTemplate   `yaml:"agents"`
	Presets  map[string]PresetConfig    `yaml:"presets"`
}

// WorkflowDefaults carries fields that apply to every preset unless overridden.
// The field names and yaml tags mirror WorkflowConfig so the deep-merge can
// copy them field-by-field without reflection gymnastics.
type WorkflowDefaults struct {
	MaxConcurrencyRaw    int                 `yaml:"max_concurrency"`
	PollIntervalMsRaw    int                 `yaml:"poll_interval_ms"`
	MaxRetryBackoffMsRaw int                 `yaml:"max_retry_backoff_ms"`
	ModelRaw             string              `yaml:"model"`
	AgentTimeoutMsRaw    int                 `yaml:"agent_timeout_ms"`
	StallTimeoutMsRaw    int                 `yaml:"stall_timeout_ms"`
	Polling              PollingConfig       `yaml:"polling"`
	Workspace            WorkspaceConfig     `yaml:"workspace"`
	Hooks                HooksConfig         `yaml:"hooks"`
	Codex                CodexConfig         `yaml:"codex"`
	Agent                AgentConfig         `yaml:"agent"`
	OpenCode             OpenCodeConfig      `yaml:"opencode"`
	OMX                  OMXConfig           `yaml:"omx"`
	OMC                  OMCConfig           `yaml:"omc"`
	Linear               LinearConfigSection `yaml:"linear"`
	OhMyOpenCode         OhMyOpenCodeConfig  `yaml:"oh_my_opencode"`
	Team                 TeamSectionConfig   `yaml:"team"`
	Timeline             TimelineConfig      `yaml:"timeline"`
	Web                  WebConfig           `yaml:"web"`
}

// TrackerTemplate holds tracker-credential fields that can be shared across
// presets.  Fields correspond to the Tracker sub-struct of WorkflowConfig.
//
// Pointer fields use pointer-presence semantics: nil = absent (do not override);
// non-nil = apply (even if the pointed-to value is zero/false/empty).
// Only pure lookup-key fields (Type is also used to select the tracker adapter,
// so setting it empty is meaningful) are pointers; the rest follow the same rule.
type TrackerTemplate struct {
	Type                           *string   `yaml:"type"`
	ProjectURL                     *string   `yaml:"project_url"`
	TeamID                         *string   `yaml:"team_id"`
	AssigneeID                     *string   `yaml:"assignee_id"`
	BoardDir                       *string   `yaml:"board_dir"`
	IssuePrefix                    *string   `yaml:"issue_prefix"`
	Owner                          *string   `yaml:"owner"`
	Repo                           *string   `yaml:"repo"`
	Labels                         *[]string `yaml:"labels"`
	Assignee                       *string   `yaml:"assignee"`
	Token                          *string   `yaml:"token"`
	Endpoint                       *string   `yaml:"endpoint"`
	HTTPTimeoutMsRaw               *int      `yaml:"http_timeout_ms"`
	MainRefRaw                     *string   `yaml:"main_ref"`
	AutoCloseAlreadyImplementedRaw *bool     `yaml:"auto_close_already_implemented"`
}

// AgentTemplate holds agent-runner fields that can be shared across presets.
// The YAML shape is flat (type/codex/opencode/omx/omc) so preset inline
// `agent:` blocks match named entries under the top-level `agents:` map.
//
// Pointer fields use pointer-presence semantics: nil = absent, non-nil = apply.
// Map fields (OhMyOpenCode.Agents, OhMyOpenCode.Categories) use merge-by-key
// semantics: later layer wins per key. Documented in the package doc comment.
type AgentTemplate struct {
	Type         *string                `yaml:"type"`
	Codex        CodexTemplateConfig    `yaml:"codex"`
	OpenCode     OpenCodeTemplateConfig `yaml:"opencode"`
	OMX          OMXTemplateConfig      `yaml:"omx"`
	OMC          OMCTemplateConfig      `yaml:"omc"`
	OhMyOpenCode OhMyOpenCodeTemplate   `yaml:"oh_my_opencode"`
}

// CodexTemplateConfig is the pointer-presence version of CodexConfig for use
// in agent templates and presets.
type CodexTemplateConfig struct {
	BinaryPath              *string `yaml:"binary_path"`
	Model                   *string `yaml:"model"`
	ApprovalPolicy          *string `yaml:"approval_policy"`
	Sandbox                 *string `yaml:"sandbox"`
	HandshakeTimeoutMsRaw   *int    `yaml:"handshake_timeout_ms"`
	OverloadRetryCapMsRaw   *int    `yaml:"overload_retry_cap_ms"`
	OverloadStartDelayMsRaw *int    `yaml:"overload_start_delay_ms"`
}

// OpenCodeTemplateConfig is the pointer-presence version of OpenCodeConfig for
// use in agent templates and presets.
type OpenCodeTemplateConfig struct {
	BinaryPath *string `yaml:"binary_path"`
	Port       *int    `yaml:"port"`
	Password   *string `yaml:"password"`
	Username   *string `yaml:"username"`
}

// OMXTemplateConfig is the pointer-presence version of OMXConfig for use in
// agent templates and presets.
type OMXTemplateConfig struct {
	BinaryPath       *string `yaml:"binary_path"`
	TeamSpec         *string `yaml:"team_spec"`
	PollIntervalMs   *int    `yaml:"poll_interval_ms"`
	StartupTimeoutMs *int    `yaml:"startup_timeout_ms"`
	Ralph            *bool   `yaml:"ralph"`
}

// OMCTemplateConfig is the pointer-presence version of OMCConfig for use in
// agent templates and presets.
type OMCTemplateConfig struct {
	BinaryPath       *string `yaml:"binary_path"`
	TeamSpec         *string `yaml:"team_spec"`
	PollIntervalMs   *int    `yaml:"poll_interval_ms"`
	StartupTimeoutMs *int    `yaml:"startup_timeout_ms"`
}

// OhMyOpenCodeTemplate holds OhMyOpenCode overrides for agent templates and
// presets.  Map fields (Agents, Categories) merge by key across layers.
type OhMyOpenCodeTemplate struct {
	PluginVersion *string                         `yaml:"plugin_version"`
	Plugins       *[]string                       `yaml:"plugins"`
	Agents        map[string]OhMyOpenCodeAgent    `yaml:"agents"`
	Categories    map[string]OhMyOpenCodeCategory `yaml:"categories"`
	Provider      *OhMyOpenCodeProviderTemplate   `yaml:"provider"`
}

// OhMyOpenCodeProviderTemplate holds OhMyOpenCodeProvider overrides with
// pointer-presence semantics.
type OhMyOpenCodeProviderTemplate struct {
	Name    *string `yaml:"name"`
	BaseURL *string `yaml:"base_url"`
	APIKey  *string `yaml:"api_key"`
}

// PresetConfig is a lightweight override that selects a named tracker and/or
// agent template and then layers its own field values on top.
//
// Exactly one of AgentRef (named reference) or Agent (inline struct) may be set.
// Setting both causes Resolve to fail fast with a clear error.
//
// YAML shape:
//
//	agent: <inline AgentTemplate>   — inline agent override, no named lookup
//	                                (flat shape: type/codex/opencode/omx/omc)
//	agent_ref: <name>               — named reference into RootConfig.Agents
type PresetConfig struct {
	// Tracker is the name of a TrackerTemplate defined in RootConfig.Trackers.
	// Leave empty to inherit only from Defaults.
	Tracker string `yaml:"tracker"`

	// AgentRef is the name of an AgentTemplate defined in RootConfig.Agents.
	// Mutually exclusive with Agent (inline).
	AgentRef string `yaml:"agent_ref"`

	// Agent provides agent-runner fields directly on the preset without
	// requiring a named template.  Mutually exclusive with AgentRef.
	Agent *AgentTemplate `yaml:"agent"`

	// PromptRef is an opaque reference passed to the PromptLoader.
	// When set, Resolve calls loader.Load(PromptRef) and stores the result in
	// WorkflowConfig.PromptTemplate.
	PromptRef string `yaml:"prompt_ref"`

	// Scalar overrides — these win over defaults, tracker, and agent layers.
	// Pointer-presence: nil = absent (inherit); non-nil = apply (even zero).
	MaxConcurrencyRaw    *int    `yaml:"max_concurrency"`
	PollIntervalMsRaw    *int    `yaml:"poll_interval_ms"`
	MaxRetryBackoffMsRaw *int    `yaml:"max_retry_backoff_ms"`
	AgentTimeoutMsRaw    *int    `yaml:"agent_timeout_ms"`
	StallTimeoutMsRaw    *int    `yaml:"stall_timeout_ms"`
	ModelRaw             *string `yaml:"model"`

	// Nested block overrides — any block that WorkflowDefaults carries can be
	// partially or fully overridden here.  Same pointer-presence rules apply
	// inside each nested block where individual scalar fields are pointers.
	Workspace    *WorkspaceTemplateConfig `yaml:"workspace"`
	Hooks        *HooksTemplateConfig     `yaml:"hooks"`
	Linear       *LinearTemplateConfig    `yaml:"linear"`
	Team         *TeamTemplateConfig      `yaml:"team"`
	Web          *WebTemplateConfig       `yaml:"web"`
	Codex        *CodexTemplateConfig     `yaml:"codex"`
	OpenCode     *OpenCodeTemplateConfig  `yaml:"opencode"`
	OMX          *OMXTemplateConfig       `yaml:"omx"`
	OMC          *OMCTemplateConfig       `yaml:"omc"`
	OhMyOpenCode *OhMyOpenCodeTemplate    `yaml:"oh_my_opencode"`
}

// WorkspaceTemplateConfig is the pointer-presence version of WorkspaceConfig.
type WorkspaceTemplateConfig struct {
	BaseDir      *string `yaml:"base_dir"`
	BranchPrefix *string `yaml:"branch_prefix"`
}

// HooksTemplateConfig is the pointer-presence version of HooksConfig.
type HooksTemplateConfig struct {
	BeforeRun    *string `yaml:"before_run"`
	AfterRun     *string `yaml:"after_run"`
	BeforeRemove *string `yaml:"before_remove"`
}

// LinearTemplateConfig is the pointer-presence version of LinearConfigSection
// for use in presets.
type LinearTemplateConfig struct {
	IssueDetails *LinearIssueDetailsTemplateConfig `yaml:"issue_details"`
	SyncComments *LinearSyncCommentsTemplateConfig `yaml:"sync_comments"`
}

// LinearIssueDetailsTemplateConfig is the pointer-presence version of
// LinearIssueDetailsConfig.
type LinearIssueDetailsTemplateConfig struct {
	Enabled *bool `yaml:"enabled"`
}

// LinearSyncCommentsTemplateConfig is the pointer-presence version of
// LinearSyncCommentsConfig.
type LinearSyncCommentsTemplateConfig struct {
	Enabled        *bool   `yaml:"enabled"`
	Mode           *string `yaml:"mode"`
	QueueSize      *int    `yaml:"queue_size"`
	PollIntervalMs *int    `yaml:"poll_interval_ms"`
}

// TeamTemplateConfig is the pointer-presence version of TeamSectionConfig.
type TeamTemplateConfig struct {
	MaxWorkers                *int    `yaml:"max_workers"`
	MaxFixLoops               *int    `yaml:"max_fix_loops"`
	ClaimLeaseSeconds         *int    `yaml:"claim_lease_seconds"`
	StateDir                  *string `yaml:"state_dir"`
	ExecutionMode             *string `yaml:"execution_mode"`
	WorkerMode                *string `yaml:"worker_mode"`
	RestartGracePeriodMsRaw   *int    `yaml:"restart_grace_period_ms"`
	GovernanceRetryDelayMsRaw *int    `yaml:"governance_retry_delay_ms"`
	HeartbeatIntervalMsRaw    *int    `yaml:"heartbeat_interval_ms"`
}

// WebTemplateConfig is the pointer-presence version of WebConfig.
type WebTemplateConfig struct {
	SSEKeepaliveIntervalMsRaw *int `yaml:"sse_keepalive_interval_ms"`
}

// LoadRoot parses a RootConfig from an io.Reader (YAML stream).
// It does not know or care about file paths — callers supply the stream.
//
// Strict mode: any top-level YAML key outside {defaults, trackers, agents,
// presets} causes an error so typos are caught early.
func LoadRoot(r io.Reader) (*RootConfig, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("LoadRoot: read: %w", err)
	}

	if strings.TrimSpace(string(data)) == "" {
		return nil, fmt.Errorf("LoadRoot: input is empty")
	}

	// Strict-mode check: decode into a raw map first.
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("LoadRoot: invalid yaml: %w", err)
	}

	allowed := map[string]bool{
		"defaults": true,
		"trackers": true,
		"agents":   true,
		"presets":  true,
	}
	for key := range raw {
		if !allowed[key] {
			return nil, fmt.Errorf("LoadRoot: unknown top-level section %q (allowed: defaults, trackers, agents, presets)", key)
		}
	}

	var cfg RootConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("LoadRoot: unmarshal: %w", err)
	}

	return &cfg, nil
}

// Resolve deep-merges defaults → trackers[preset.Tracker] → agents[preset.AgentRef]
// (or preset.Agent inline) → preset overrides, runs ENV interpolation once on the
// merged result, and returns the final WorkflowConfig.
//
// loader is used to fetch prompt bodies when the preset's PromptRef is set.
// Passing nil falls back to FilePromptLoader{Root: "."}.
//
// Error cases (fail fast):
//   - rc is nil
//   - rc.Presets is nil (no presets defined)
//   - unknown preset name
//   - preset references an unknown tracker name
//   - preset references an unknown agent name
//   - preset has both AgentRef (named ref) and Agent (inline) set simultaneously
func (rc *RootConfig) Resolve(presetName string, loader PromptLoader) (*WorkflowConfig, error) {
	if rc == nil {
		return nil, errors.New("config: nil RootConfig")
	}
	if rc.Presets == nil {
		return nil, fmt.Errorf("config: no presets defined")
	}

	if loader == nil {
		loader = FilePromptLoader{Root: "."}
	}

	preset, ok := rc.Presets[presetName]
	if !ok {
		names := sortedKeys(rc.Presets)
		return nil, fmt.Errorf("Resolve: unknown preset %q (available: %s)", presetName, strings.Join(names, ", "))
	}

	// Validate: cannot have both named agent ref and inline agent.
	if preset.AgentRef != "" && preset.Agent != nil {
		return nil, fmt.Errorf("Resolve: preset %q sets both agent_ref (named ref %q) and agent (inline) — pick one", presetName, preset.AgentRef)
	}

	// Layer 1: start from defaults.
	result := workflowConfigFromDefaults(rc.Defaults)

	// Layer 2: merge tracker template (if preset specifies one).
	if preset.Tracker != "" {
		tmpl, ok := rc.Trackers[preset.Tracker]
		if !ok {
			names := sortedKeys(rc.Trackers)
			return nil, fmt.Errorf("Resolve: preset %q references unknown tracker %q (available: %s)", presetName, preset.Tracker, strings.Join(names, ", "))
		}
		mergeTrackerTemplate(&result.Tracker, tmpl)
	}

	// Layer 3: merge agent template (named or inline).
	if preset.AgentRef != "" {
		tmpl, ok := rc.Agents[preset.AgentRef]
		if !ok {
			names := sortedKeys(rc.Agents)
			return nil, fmt.Errorf("Resolve: preset %q references unknown agent %q (available: %s)", presetName, preset.AgentRef, strings.Join(names, ", "))
		}
		mergeAgentTemplate(result, tmpl)
	} else if preset.Agent != nil {
		mergeAgentTemplate(result, *preset.Agent)
	}

	// Layer 4: preset overrides.
	mergePresetOverrides(result, preset)

	// Prompt loading.
	if preset.PromptRef != "" {
		body, err := loader.Load(preset.PromptRef)
		if err != nil {
			return nil, fmt.Errorf("Resolve: preset %q: prompt loader failed for ref %q: %w", presetName, preset.PromptRef, err)
		}
		result.PromptTemplate = body
	}

	// ENV interpolation runs once on the final merged struct.
	resolveEnvReferencesValue(reflect.ValueOf(result).Elem())

	return result, nil
}

// workflowConfigFromDefaults initialises a WorkflowConfig from WorkflowDefaults.
func workflowConfigFromDefaults(d WorkflowDefaults) *WorkflowConfig {
	cfg := &WorkflowConfig{
		MaxConcurrencyRaw:    d.MaxConcurrencyRaw,
		PollIntervalMsRaw:    d.PollIntervalMsRaw,
		MaxRetryBackoffMsRaw: d.MaxRetryBackoffMsRaw,
		ModelRaw:             d.ModelRaw,
		AgentTimeoutMsRaw:    d.AgentTimeoutMsRaw,
		StallTimeoutMsRaw:    d.StallTimeoutMsRaw,
		Polling:              d.Polling,
		Workspace:            d.Workspace,
		Hooks:                d.Hooks,
		Codex:                d.Codex,
		Agent:                d.Agent,
		OpenCode:             d.OpenCode,
		OMX:                  d.OMX,
		OMC:                  d.OMC,
		Linear:               d.Linear,
		OhMyOpenCode:         d.OhMyOpenCode,
		Team:                 d.Team,
		Timeline:             d.Timeline,
		Web:                  d.Web,
	}
	// Deep-copy slices and maps from defaults so later mutations don't alias.
	if d.OhMyOpenCode.Plugins != nil {
		cfg.OhMyOpenCode.Plugins = append([]string(nil), d.OhMyOpenCode.Plugins...)
	}
	if d.OhMyOpenCode.Agents != nil {
		cfg.OhMyOpenCode.Agents = make(map[string]OhMyOpenCodeAgent, len(d.OhMyOpenCode.Agents))
		for k, v := range d.OhMyOpenCode.Agents {
			cfg.OhMyOpenCode.Agents[k] = v
		}
	}
	if d.OhMyOpenCode.Categories != nil {
		cfg.OhMyOpenCode.Categories = make(map[string]OhMyOpenCodeCategory, len(d.OhMyOpenCode.Categories))
		for k, v := range d.OhMyOpenCode.Categories {
			cfg.OhMyOpenCode.Categories[k] = v
		}
	}
	if d.Linear.IssueDetails.Enabled != nil {
		enabled := *d.Linear.IssueDetails.Enabled
		cfg.Linear.IssueDetails.Enabled = &enabled
	}
	return cfg
}

// mergeTrackerTemplate overlays a TrackerTemplate onto the TrackerConfig in the
// WorkflowConfig.  Fields use pointer-presence: nil = absent, non-nil = apply.
// Slice field Labels: nil = absent (skip), non-nil = replace wholesale (even if
// the pointed-to slice is empty, which clears prior labels).
func mergeTrackerTemplate(dst *TrackerConfig, src TrackerTemplate) {
	if src.Type != nil {
		dst.Type = *src.Type
	}
	if src.ProjectURL != nil {
		dst.ProjectURL = *src.ProjectURL
	}
	if src.TeamID != nil {
		dst.TeamID = *src.TeamID
	}
	if src.AssigneeID != nil {
		dst.AssigneeID = *src.AssigneeID
	}
	if src.BoardDir != nil {
		dst.BoardDir = *src.BoardDir
	}
	if src.IssuePrefix != nil {
		dst.IssuePrefix = *src.IssuePrefix
	}
	if src.Owner != nil {
		dst.Owner = *src.Owner
	}
	if src.Repo != nil {
		dst.Repo = *src.Repo
	}
	if src.Labels != nil {
		// Replace wholesale — even an explicit empty list clears prior labels.
		// Use make to ensure non-nil result even when the source is empty,
		// so callers can distinguish "explicitly cleared" from "never set".
		dst.Labels = make([]string, len(*src.Labels))
		copy(dst.Labels, *src.Labels)
	}
	if src.Assignee != nil {
		dst.Assignee = *src.Assignee
	}
	if src.Token != nil {
		dst.Token = *src.Token
	}
	if src.Endpoint != nil {
		dst.Endpoint = *src.Endpoint
	}
	if src.HTTPTimeoutMsRaw != nil {
		dst.HTTPTimeoutMsRaw = *src.HTTPTimeoutMsRaw
	}
	if src.MainRefRaw != nil {
		dst.MainRefRaw = *src.MainRefRaw
	}
	if src.AutoCloseAlreadyImplementedRaw != nil {
		dst.AutoCloseAlreadyImplementedRaw = *src.AutoCloseAlreadyImplementedRaw
	}
}

// mergeAgentTemplate overlays an AgentTemplate onto the WorkflowConfig's
// agent-runner fields.  Uses pointer-presence for scalars; nested maps merge
// by key (later wins per key).
func mergeAgentTemplate(dst *WorkflowConfig, src AgentTemplate) {
	if src.Type != nil {
		dst.Agent.Type = *src.Type
	}
	mergeCodexTemplateConfig(&dst.Codex, src.Codex)
	mergeOpenCodeTemplateConfig(&dst.OpenCode, src.OpenCode)
	mergeOMXTemplateConfig(&dst.OMX, src.OMX)
	mergeOMCTemplateConfig(&dst.OMC, src.OMC)
	mergeOhMyOpenCodeTemplate(&dst.OhMyOpenCode, src.OhMyOpenCode)
}

func mergeCodexTemplateConfig(dst *CodexConfig, src CodexTemplateConfig) {
	if src.BinaryPath != nil {
		dst.BinaryPath = *src.BinaryPath
	}
	if src.Model != nil {
		dst.Model = *src.Model
	}
	if src.ApprovalPolicy != nil {
		dst.ApprovalPolicy = *src.ApprovalPolicy
	}
	if src.Sandbox != nil {
		dst.Sandbox = *src.Sandbox
	}
	if src.HandshakeTimeoutMsRaw != nil {
		dst.HandshakeTimeoutMsRaw = *src.HandshakeTimeoutMsRaw
	}
	if src.OverloadRetryCapMsRaw != nil {
		dst.OverloadRetryCapMsRaw = *src.OverloadRetryCapMsRaw
	}
	if src.OverloadStartDelayMsRaw != nil {
		dst.OverloadStartDelayMsRaw = *src.OverloadStartDelayMsRaw
	}
}

func mergeOpenCodeTemplateConfig(dst *OpenCodeConfig, src OpenCodeTemplateConfig) {
	if src.BinaryPath != nil {
		dst.BinaryPath = *src.BinaryPath
	}
	if src.Port != nil {
		dst.Port = *src.Port
	}
	if src.Password != nil {
		dst.Password = *src.Password
	}
	if src.Username != nil {
		dst.Username = *src.Username
	}
}

func mergeOMXTemplateConfig(dst *OMXConfig, src OMXTemplateConfig) {
	if src.BinaryPath != nil {
		dst.BinaryPath = *src.BinaryPath
	}
	if src.TeamSpec != nil {
		dst.TeamSpec = *src.TeamSpec
	}
	if src.PollIntervalMs != nil {
		dst.PollIntervalMs = *src.PollIntervalMs
	}
	if src.StartupTimeoutMs != nil {
		dst.StartupTimeoutMs = *src.StartupTimeoutMs
	}
	if src.Ralph != nil {
		dst.Ralph = *src.Ralph
	}
}

func mergeOMCTemplateConfig(dst *OMCConfig, src OMCTemplateConfig) {
	if src.BinaryPath != nil {
		dst.BinaryPath = *src.BinaryPath
	}
	if src.TeamSpec != nil {
		dst.TeamSpec = *src.TeamSpec
	}
	if src.PollIntervalMs != nil {
		dst.PollIntervalMs = *src.PollIntervalMs
	}
	if src.StartupTimeoutMs != nil {
		dst.StartupTimeoutMs = *src.StartupTimeoutMs
	}
}

// mergeOhMyOpenCodeTemplate merges an OhMyOpenCodeTemplate into the
// OhMyOpenCodeConfig in the WorkflowConfig.  Maps merge by key (later wins).
func mergeOhMyOpenCodeTemplate(dst *OhMyOpenCodeConfig, src OhMyOpenCodeTemplate) {
	if src.PluginVersion != nil {
		dst.PluginVersion = *src.PluginVersion
	}
	if src.Plugins != nil {
		// Replace wholesale — even an explicit empty list clears prior plugins.
		dst.Plugins = make([]string, len(*src.Plugins))
		copy(dst.Plugins, *src.Plugins)
	}
	// Maps merge by key: each key in src wins over the same key in dst.
	if len(src.Agents) > 0 {
		if dst.Agents == nil {
			dst.Agents = make(map[string]OhMyOpenCodeAgent, len(src.Agents))
		}
		for k, v := range src.Agents {
			dst.Agents[k] = v
		}
	}
	if len(src.Categories) > 0 {
		if dst.Categories == nil {
			dst.Categories = make(map[string]OhMyOpenCodeCategory, len(src.Categories))
		}
		for k, v := range src.Categories {
			dst.Categories[k] = v
		}
	}
	if src.Provider != nil {
		if src.Provider.Name != nil {
			dst.Provider.Name = *src.Provider.Name
		}
		if src.Provider.BaseURL != nil {
			dst.Provider.BaseURL = *src.Provider.BaseURL
		}
		if src.Provider.APIKey != nil {
			dst.Provider.APIKey = *src.Provider.APIKey
		}
	}
}

// mergePresetOverrides applies the preset's own overrides (layer 4).
// All scalar and nested-block fields use pointer-presence: nil = absent (skip).
func mergePresetOverrides(dst *WorkflowConfig, p PresetConfig) {
	if p.MaxConcurrencyRaw != nil {
		dst.MaxConcurrencyRaw = *p.MaxConcurrencyRaw
	}
	if p.PollIntervalMsRaw != nil {
		dst.PollIntervalMsRaw = *p.PollIntervalMsRaw
	}
	if p.MaxRetryBackoffMsRaw != nil {
		dst.MaxRetryBackoffMsRaw = *p.MaxRetryBackoffMsRaw
	}
	if p.AgentTimeoutMsRaw != nil {
		dst.AgentTimeoutMsRaw = *p.AgentTimeoutMsRaw
	}
	if p.StallTimeoutMsRaw != nil {
		dst.StallTimeoutMsRaw = *p.StallTimeoutMsRaw
	}
	if p.ModelRaw != nil {
		dst.ModelRaw = *p.ModelRaw
	}

	// Nested block overrides.
	if p.Workspace != nil {
		if p.Workspace.BaseDir != nil {
			dst.Workspace.BaseDir = *p.Workspace.BaseDir
		}
		if p.Workspace.BranchPrefix != nil {
			dst.Workspace.BranchPrefix = *p.Workspace.BranchPrefix
		}
	}
	if p.Hooks != nil {
		if p.Hooks.BeforeRun != nil {
			dst.Hooks.BeforeRun = *p.Hooks.BeforeRun
		}
		if p.Hooks.AfterRun != nil {
			dst.Hooks.AfterRun = *p.Hooks.AfterRun
		}
		if p.Hooks.BeforeRemove != nil {
			dst.Hooks.BeforeRemove = *p.Hooks.BeforeRemove
		}
	}
	if p.Linear != nil {
		if p.Linear.IssueDetails != nil && p.Linear.IssueDetails.Enabled != nil {
			enabled := *p.Linear.IssueDetails.Enabled
			dst.Linear.IssueDetails.Enabled = &enabled
		}
		if p.Linear.SyncComments != nil {
			sc := p.Linear.SyncComments
			if sc.Enabled != nil {
				dst.Linear.SyncComments.Enabled = *sc.Enabled
			}
			if sc.Mode != nil {
				dst.Linear.SyncComments.Mode = *sc.Mode
			}
			if sc.QueueSize != nil {
				dst.Linear.SyncComments.QueueSize = *sc.QueueSize
			}
			if sc.PollIntervalMs != nil {
				dst.Linear.SyncComments.PollIntervalMs = *sc.PollIntervalMs
			}
		}
	}
	if p.Team != nil {
		t := p.Team
		if t.MaxWorkers != nil {
			dst.Team.MaxWorkers = *t.MaxWorkers
		}
		if t.MaxFixLoops != nil {
			dst.Team.MaxFixLoops = *t.MaxFixLoops
		}
		if t.ClaimLeaseSeconds != nil {
			dst.Team.ClaimLeaseSeconds = *t.ClaimLeaseSeconds
		}
		if t.StateDir != nil {
			dst.Team.StateDir = *t.StateDir
		}
		if t.ExecutionMode != nil {
			dst.Team.ExecutionMode = *t.ExecutionMode
		}
		if t.WorkerMode != nil {
			dst.Team.WorkerMode = *t.WorkerMode
		}
		if t.RestartGracePeriodMsRaw != nil {
			dst.Team.RestartGracePeriodMsRaw = *t.RestartGracePeriodMsRaw
		}
		if t.GovernanceRetryDelayMsRaw != nil {
			dst.Team.GovernanceRetryDelayMsRaw = *t.GovernanceRetryDelayMsRaw
		}
		if t.HeartbeatIntervalMsRaw != nil {
			dst.Team.HeartbeatIntervalMsRaw = *t.HeartbeatIntervalMsRaw
		}
	}
	if p.Web != nil {
		if p.Web.SSEKeepaliveIntervalMsRaw != nil {
			dst.Web.SSEKeepaliveIntervalMsRaw = *p.Web.SSEKeepaliveIntervalMsRaw
		}
	}
	if p.Codex != nil {
		mergeCodexTemplateConfig(&dst.Codex, *p.Codex)
	}
	if p.OpenCode != nil {
		mergeOpenCodeTemplateConfig(&dst.OpenCode, *p.OpenCode)
	}
	if p.OMX != nil {
		mergeOMXTemplateConfig(&dst.OMX, *p.OMX)
	}
	if p.OMC != nil {
		mergeOMCTemplateConfig(&dst.OMC, *p.OMC)
	}
	if p.OhMyOpenCode != nil {
		mergeOhMyOpenCodeTemplate(&dst.OhMyOpenCode, *p.OhMyOpenCode)
	}
}

// sortedKeys returns sorted keys of a map for deterministic error messages.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
