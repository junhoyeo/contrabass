## Context

`internal/config/config.go` parses one workflow YAML per file. The 8 testdata files duplicate ~80% of fields. Defaults live in Go const. Tracker/agent blocks are copied 3-5×.

The next major direction is SaaS migration. SaaS config will live in a database, not yaml on disk. `--config <path>`, `--preset` flags, `scripts/start.sh`, and the testdata fixtures don't follow into SaaS — but the **composition model** does (defaults → tracker template → agent template → preset).

This change extracts the schema + resolution logic only, so SaaS can reuse `Resolve(presetName)` against data fetched from DB instead of disk. CLI surface and file-organization work is deferred until the SaaS architecture is decided, to avoid building dead code.

## Goals / Non-Goals

**Goals**:
- Define the composition schema: `defaults` + named `trackers` + named `agents` + `presets` that pick + override.
- Implement deterministic deep-merge resolution.
- ENV interpolation runs once, on the merged result.
- Storage-agnostic loader (`io.Reader` in, no path knowledge).
- Pluggable prompt loader so SaaS can swap disk for DB.

**Non-Goals**:
- Shipping `config/contrabass.yaml` in the repo (defer until SaaS schema is decided).
- New CLI flags or subcommands (`--preset`, `list-presets`).
- Modifying `scripts/start.sh`.
- Removing or migrating `testdata/workflow.*.md`.
- ENV var renames (`CONTRABASS_*`).
- Equivalence test locking the current testdata layout.
- Hot-reload of the new tree.

## Decisions

### D1: Layered composition over inheritance trees or YAML anchors

Each preset SHALL deep-merge `defaults → trackers[<name>] → agents[<name>] → preset overrides`. Later layers win on scalar conflict; nested maps merge by key; slices are replaced wholesale.

**Alternative**: YAML anchors (`<<: *base`). Rejected — clunky composition, doesn't survive a DB swap.

### D2: ENV interpolation runs on the merged result, not per-layer

After merge, the parser walks the final struct and resolves `$VAR` placeholders. Avoids "did defaults or preset win?" ambiguity for env-bearing fields. Reuses the existing `resolveEnvReferencesValue` semantics from PR #14 — same struct walker, same map-non-recursion limitation.

### D3: Storage-agnostic loader

`LoadRoot(io.Reader)` takes a stream, not a path. CLI callers wrap a `*os.File`; SaaS callers wrap a `bytes.Reader` over yaml derived from DB rows, or skip yaml entirely and construct `*RootConfig` directly. The composition logic doesn't know or care.

### D4: Prompt loading is pluggable

`PromptLoader` interface lets prompts come from disk now and DB later. Default `FilePromptLoader` keeps current behavior. `Resolve` takes the loader as a parameter (not as a `RootConfig` field) so `RootConfig` stays a pure data struct.

### D5: No CLI surface in this change

`--preset` flag, `list-presets`, `scripts/start.sh` rewrites all live in a follow-up if the OSS CLI keeps mattering post-SaaS. Deliberately deferred to avoid building code that SaaS will throw away.

### D6: No legacy removal

`testdata/workflow.*.md` stay as dev fixtures. The new composition model coexists with the old single-file path. `LoadWorkflow` and `LoadRoot` are siblings, not stages of a migration.

## Risks / Trade-offs

- **Risk**: Adding `RootConfig` without a real consumer creates orphan code. → **Mitigation**: ship at least one consumer in the same change — table-driven tests that exercise `Resolve` end-to-end on a representative preset set inline in the test file. SaaS becomes the production consumer when it lands.
- **Risk**: Deep-merge precedence is subtle for nested maps (e.g. `tracker.linear.assignee_id` in defaults vs preset). → **Mitigation**: document precedence rules (maps merge by key, scalars replace, slices replace) in the package doc comment; cover with table-driven tests including a nested-map case.
- **Risk**: SaaS data model may diverge from this composition shape. → **Mitigation**: keep the composition model small (4 sections, no extras); if SaaS adds tenant scoping, that wraps `RootConfig`, not replaces it.
- **Trade-off**: Without a `config/contrabass.yaml` shipping in the repo, the new model is harder to demo. → **Mitigation**: tests + doc comment + one example yaml string inline in tests are sufficient demo until SaaS becomes the real consumer.

### D7: Pointer-presence for scalar/slice/map merge semantics

Scalar fields (`string`, `int`, `bool`) and slice fields (`[]string`) in `TrackerTemplate`, `AgentTemplate` (via `CodexTemplateConfig`, `OMXTemplateConfig`, etc.), and `PresetConfig` use pointer types (`*string`, `*int`, `*bool`, `*[]string`). nil = absent (do not override the merged value); non-nil = explicitly set (apply, even if the pointed-to value is zero, false, or empty). This eliminates the "non-zero overlay" bug where a later layer could not set a boolean to false, an int to 0, or an empty list to clear prior labels.

When writing the final `WorkflowConfig` (which retains plain value types for downstream consumers), nil pointers fall back to zero values, matching today's `LoadWorkflow` behavior.

**Rationale**: value types give no way to distinguish "field absent" from "field set to zero" in a YAML-parsed struct. Pointer types give presence tracking for free, and Go's `gopkg.in/yaml.v3` supports `*T` natively (nil if key absent; populated pointer if key present, even with a zero value).

### D8: Inline agent uses `agent:` key; named reference uses `agent_ref:` key

The YAML shape for presets distinguishes inline agent templates from named references:

- `agent: <AgentTemplate struct>` — inline override, no named lookup. The preferred shape is flat, e.g. `agent: { type: omc, omc: { binary_path: omc } }`.
- `agent_ref: <name>` — named reference into `RootConfig.Agents`.

This matches the spec scenario wording ("inline data under `agent:`", "named ref under `agent_ref:`") and removes the prior ambiguity where `agent` was a string ref and `agent_inline` was the struct. Setting both is still a fail-fast error.

### D9: AgentTemplate is flat and pointer-presence aware

`AgentTemplate` carries `type` and runner blocks (`codex`, `opencode`, `omx`, `omc`, `oh_my_opencode`) at the same level. Named templates under top-level `agents:` and inline templates under preset `agent:` therefore share the same shape. `type` is `*string`, so an explicit `type: ""` can clear a default agent type while an omitted `type` inherits the prior layer.

## Open Questions

- Should `Resolve` take a `PromptLoader` parameter, or read it from a `RootConfig.PromptLoader` field set at construction time? **Initial cut**: parameter — keeps `RootConfig` a pure data struct.
- Should the merge handle slice append vs replace? **Initial cut**: slice replace. Document and revisit only if a real use case for append emerges.
- Does SaaS need per-tenant `defaults` and per-project `presets`? Probably yes, but that wraps `RootConfig` (e.g., `OrgConfig { Defaults; map[projectID]RootConfig }`) rather than changing it. Out of scope here.
