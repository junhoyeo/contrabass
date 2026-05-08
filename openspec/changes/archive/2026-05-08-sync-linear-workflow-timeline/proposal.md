## Why

Contrabass currently treats Linear issue metadata, dashboard runtime state, and Linear comments as separate surfaces: the dashboard can show only the normalized issue cache and live run snapshot, while Linear receives only coarse final-run comments. Operators need a reliable two-way visibility model where the dashboard can load complete Linear issue details and each durable workflow-node completion is reflected back into the Linear issue thread without making Linear the runtime source of truth.

## What Changes

- Add a durable local `WorkflowTimeline` as the canonical source for single-agent workflow history, node summaries, and Linear sync state.
- Add backend-only Linear issue detail reads so the dashboard can display richer issue metadata without browser-side Linear credentials.
- Add an opt-in asynchronous Linear comment syncer that projects completed workflow nodes to one root comment per run plus reply/top-level node summaries.
- Add idempotency, restart recovery, rate-limit retry, and duplicate-suppression behavior for Linear comment writes.
- Suppress legacy direct Linear completion comments when comment sync is enabled; preserve existing legacy comment behavior when sync is disabled or the tracker is not Linear.
- Keep team-to-Linear workflow summaries out of V1 because current team execution is restricted to internal/local trackers.

## Capabilities

### New Capabilities
- `workflow-timeline`: durable local workflow run/node timeline and sync-state storage used by dashboard and outbound projections.
- `linear-comment-sync`: opt-in asynchronous projection of workflow-node summaries into Linear issue comments/replies.
- `linear-issue-detail`: backend-only richer Linear issue detail retrieval for dashboard issue detail views.

### Modified Capabilities
- None.

## Impact

- Affected Go packages: `internal/config`, `internal/timeline` (new), `internal/tracker`, `internal/orchestrator`, `internal/web`, `cmd/contrabass`.
- Affected dashboard files: `packages/dashboard/src/types.ts`, `packages/dashboard/src/hooks/useSSE.ts`, `packages/dashboard/src/components/IssueDetailSheet.tsx`.
- Affected external system: Linear GraphQL API for richer issue reads and comment create/reply/update support.
- New local state: append-only workflow timeline JSONL files under `.contrabass/state/workflow-timeline` by default.
- New web APIs: issue detail and timeline endpoints under `/api/v1/issues/{issue_id}/...`.
