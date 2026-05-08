## Why

Today `internal/config/config.go` parses one workflow YAML per file. The 8 testdata files share ~80% of fields (model, agent_timeout_ms, prompt body, tracker block). Defaults live in Go const, not yaml. Tracker/agent blocks duplicate 3-5×.

The next major direction is SaaS, where config will live in a database keyed by org/project, not a yaml on disk. The CLI-time pain points (file sprawl, `--preset` ergonomics, `start.sh` env validation, deletion of `testdata/*.md`) become irrelevant under SaaS. But the **composition model** — `defaults` + named `trackers` + named `agents` + lightweight `presets` that override only what differs — maps directly to the SaaS data model: `organization.defaults`, `tracker_credentials[]`, `agent_configs[]`, `project.preset` references both.

This change extracts the durable part — schema types, deep-merge resolution, ENV interpolation through the merged result — and skips the file-organization and CLI-surface work that SaaS would throw away.

## What Changes

- New types in `internal/config/`: `RootConfig` (with `defaults`, `trackers`, `agents`, `presets` sections), `PresetConfig`, `TrackerTemplate`, `AgentTemplate`. Existing `WorkflowConfig` stays as the merge target, unchanged.
- `(*RootConfig).Resolve(presetName) (*WorkflowConfig, error)`: deep-merges `defaults → trackers[<name>] → agents[<name>] → preset overrides`, runs ENV interpolation on the merged struct.
- `LoadRoot(io.Reader) (*RootConfig, error)`: storage-agnostic stream parser. SaaS swaps in a DB-backed loader later by constructing `RootConfig` directly or feeding a DB-derived yaml stream.
- Optional `PromptLoader` interface so prompt bodies can come from disk now and DB later without changing `Resolve`.

## Capabilities

### New Capabilities

- `config-composition-model`: schema + resolution semantics for layered config (defaults → tracker template → agent template → preset). Storage-agnostic.

### Modified Capabilities

(none — additive; existing `LoadWorkflow` path is untouched)

## Impact

- **Code added**: `internal/config/composition.go`, `internal/config/composition_test.go`, plus a doc comment on the new types.
- **Code unchanged**: `internal/config/config.go`'s existing `LoadWorkflow` path keeps working. Orchestrator, tracker adapters, agent runners, TUI, web, hub all unaffected.
- **No CLI changes**: no `--preset` flag, no `list-presets` subcommand, no `scripts/start.sh` rewrite.
- **No file deletions**: `testdata/workflow.*.md` stay as dev fixtures.
- **No fsnotify changes**: existing per-file watch unchanged.
- **No tracker/agent adapter changes.**
- **SaaS portability**: when SaaS lands, it imports `Resolve` and calls it on data fetched from DB. The `LoadRoot` stream loader becomes one of several entry points.

## Out of Scope

- Shipping `config/contrabass.yaml` in the repo (defer until SaaS schema is decided).
- `--preset` CLI flag, `list-presets` subcommand, `scripts/start.sh` integration.
- Removal or migration of `testdata/workflow.*.md`.
- ENV var rename to `CONTRABASS_*` prefix.
- Equivalence test that locks in the current 8-preset layout.
- Hot-reload semantics for the new tree (revisit when there is a real consumer).
