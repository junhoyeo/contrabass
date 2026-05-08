## ADDED Requirements

### Requirement: A composition schema SHALL describe workflow configs as layered templates

The system SHALL expose a `RootConfig` type with exactly four sections — `defaults`, `trackers`, `agents`, `presets` — such that a complete `WorkflowConfig` can be derived from any preset name by deep-merging, in order, `defaults → trackers[<preset.tracker>] → agents[<preset.agent>] → preset overrides`. Later layers MUST win on scalar conflict; nested maps MUST merge by key; slices MUST be replaced wholesale.

#### Scenario: Preset selects a tracker template by name

- **WHEN** `RootConfig` defines `trackers: { linear: { type: linear, project_url: $CONTRABASS_PROJECT_URL } }` and `presets: { demo: { tracker: linear, max_concurrency: 3 } }`
- **AND** the caller invokes `Resolve("demo")`
- **THEN** the resolved `WorkflowConfig` SHALL have `tracker.type = "linear"`, `tracker.project_url` populated from env, and `max_concurrency = 3`

#### Scenario: Preset overrides a defaults field

- **WHEN** `defaults.agent_timeout_ms = 900000` and `presets.hardening.agent_timeout_ms = 1800000`
- **THEN** `Resolve("hardening")` SHALL return `agent_timeout_ms = 1800000`

#### Scenario: Preset inlines an agent override instead of selecting a named template

- **WHEN** `presets.omc = { agent: { type: omc, omc: { binary_path: omc } } }` and `agents` does not contain an `omc` entry
- **THEN** `Resolve("omc")` SHALL succeed with `agent.type = "omc"` and `agent.omc.binary_path = "omc"`

#### Scenario: Conflicting inline + named agent on the same preset fails fast

- **WHEN** `presets.x = { agent: <inline override>, agent_ref: omc }` (or any equivalent shape carrying both)
- **THEN** `Resolve("x")` SHALL return an error citing the preset name and the conflict

#### Scenario: Missing tracker reference fails fast

- **WHEN** a preset says `tracker: bogus` but `trackers` does not define `bogus`
- **THEN** `Resolve(...)` SHALL fail with a clear error pointing at the preset name and the unknown reference

#### Scenario: Unknown preset name fails fast

- **WHEN** the caller invokes `Resolve("does-not-exist")`
- **THEN** the call SHALL fail with a clear error listing the available preset names

### Requirement: ENV interpolation SHALL run once, against the merged result

After deep-merge of defaults + tracker + agent + preset overrides, the resolver SHALL resolve `$VAR` placeholders against the process environment in the final merged config. Resolution SHALL reuse the existing `resolveEnvReferencesValue` semantics (struct fields recurse; raw map values do not).

#### Scenario: ENV var defined at defaults level resolves in the merged preset

- **WHEN** `defaults.model = $CONTRABASS_MODEL` and the env has `CONTRABASS_MODEL=openai/gpt-5.5`
- **AND** `Resolve("demo")` is called
- **THEN** the resolved config's `model` field SHALL be `openai/gpt-5.5`

#### Scenario: ENV var unset surfaces as empty string

- **WHEN** `defaults.model = $CONTRABASS_MODEL` and the env has no `CONTRABASS_MODEL`
- **THEN** the resolver SHALL set the field to an empty string (matching existing `LoadWorkflow` behavior)

### Requirement: The loader SHALL be storage-agnostic

The system SHALL expose `LoadRoot(r io.Reader) (*RootConfig, error)` that yaml-unmarshals into `RootConfig` from any byte stream — no file path knowledge inside the function. Callers (CLI, SaaS, tests) supply the stream.

#### Scenario: LoadRoot accepts a yaml stream

- **WHEN** a caller passes `bytes.NewReader([]byte(yamlString))` containing a valid `RootConfig`
- **THEN** `LoadRoot` SHALL return a populated `*RootConfig` with no error

#### Scenario: LoadRoot rejects unknown top-level sections

- **WHEN** the input yaml has a top-level key outside `{defaults, trackers, agents, presets}`
- **THEN** `LoadRoot` SHALL return an error citing the offending key

### Requirement: Prompt bodies SHALL load through a pluggable interface

The system SHALL expose a `PromptLoader` interface with `Load(ref string) (string, error)`. `Resolve` SHALL accept a `PromptLoader` parameter; passing `nil` SHALL fall back to a default `FilePromptLoader` rooted at the current directory. The interface lets SaaS substitute a DB-backed loader without changing `Resolve`.

#### Scenario: Custom PromptLoader supplies the prompt body

- **WHEN** the caller passes a `PromptLoader` whose `Load("standard")` returns `"hello {{ issue.title }}"`
- **AND** the resolved preset's `prompt_ref = standard`
- **THEN** the resolved `WorkflowConfig.Prompt` SHALL equal `"hello {{ issue.title }}"`

#### Scenario: PromptLoader error propagates out of Resolve

- **WHEN** the configured `PromptLoader.Load(ref)` returns an error
- **THEN** `Resolve` SHALL return an error wrapping that loader error and citing the preset that requested the prompt
