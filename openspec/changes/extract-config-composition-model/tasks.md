## 1. Schema types

- [x] 1.1 Define `RootConfig`, `PresetConfig`, `TrackerTemplate`, `AgentTemplate` in `internal/config/composition.go`. `RootConfig` has `Defaults WorkflowDefaults`, `Trackers map[string]TrackerTemplate`, `Agents map[string]AgentTemplate`, `Presets map[string]PresetConfig`. Reuse existing `WorkflowConfig` field types so the merged result is the same struct downstream code already consumes.
- [x] 1.2 Decide which fields belong on `WorkflowDefaults` vs a preset (rule of thumb: anything that has a sensible global value goes on defaults; anything that varies per preset stays per-preset). Document the split in a doc comment on `RootConfig`.

## 2. Loader

- [x] 2.1 Implement `LoadRoot(r io.Reader) (*RootConfig, error)` that yaml-unmarshals into `RootConfig`. Strict mode: reject unknown top-level sections. No file path knowledge in this function — pure stream-in.
- [x] 2.2 Unit-test `LoadRoot` with valid input, missing required sections, unknown extra section (rejected), empty input.

## 3. Resolve / merge

- [x] 3.1 Implement `(*RootConfig).Resolve(presetName string) (*WorkflowConfig, error)`. Order: start from `Defaults`, deep-merge `Trackers[preset.Tracker]`, then `Agents[preset.Agent]` (or inline agent override on the preset), then preset's own field overrides. Later layers MUST win on scalar conflict; nested maps MUST merge by key; slices are replaced wholesale (document this).
- [x] 3.2 Run existing `resolveEnvReferencesValue` on the final merged struct (not on each layer — env resolves once, against the merged result).
- [x] 3.3 Fail fast on: unknown preset name, unknown tracker reference, unknown agent reference, conflicting inline-agent + named-agent on same preset.
- [x] 3.4 Unit-test merge with table-driven cases: defaults-only preset, preset overrides defaults scalar, preset adds new tracker fields on top of template, inline agent override beats named agent, ENV resolution at defaults level survives merge, missing tracker reference fails fast, missing agent reference fails fast, slice-replace semantics on a list field.

## 4. Prompt loader interface

- [x] 4.1 Define `type PromptLoader interface { Load(ref string) (string, error) }` so prompt bodies can come from disk (`FilePromptLoader`) or DB (SaaS later). `Resolve` accepts a `PromptLoader` parameter; passing nil falls back to `FilePromptLoader{Root: "."}`.
- [x] 4.2 Unit-test with a fake loader that returns canned content; verify the resolved `WorkflowConfig.Prompt` matches and a missing prompt-ref surfaces a clear error.

## 5. Documentation

- [x] 5.1 Add a package doc comment (top of `composition.go`) describing the composition model, merge order, ENV interpolation timing, and the SaaS portability rationale. No README changes.
