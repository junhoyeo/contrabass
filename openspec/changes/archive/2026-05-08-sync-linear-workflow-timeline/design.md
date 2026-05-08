## Context

Contrabass currently has three separate information surfaces:

- Linear tracker polling reads a lean candidate issue set and normalizes it into `types.Issue` in `internal/tracker/linear.go`.
- Runtime state is local and mostly transient: `StateSnapshot` exposes live `running`, `backoff`, and cached `issues`, but no durable completed-node history.
- Linear comments are written synchronously through `Tracker.PostComment` only at coarse terminal points, and that API returns no comment IDs.

The desired behavior is bidirectional visibility, not bidirectional ownership. Linear remains the collaboration system for issues. Contrabass owns workflow runtime history and exposes it to the dashboard. Linear comments receive durable workflow-node summaries as a best-effort projection of Contrabass state.

## Goals / Non-Goals

**Goals:**

- Add a durable local `WorkflowTimeline` for single-agent run history, node summaries, and Linear sync state.
- Expose timeline and richer Linear issue details through backend APIs consumed by the dashboard.
- Add an opt-in async Linear syncer that writes workflow-node summaries to Linear root comments and replies/top-level fallback comments.
- Make Linear comment writes idempotent across retries and restarts.
- Preserve legacy comment behavior when sync is disabled or the tracker is not Linear.
- Ensure Linear sync runs even when the web dashboard is disabled.

**Non-Goals:**

- Do not make Linear comments the source of truth for runtime history.
- Do not expose Linear API keys to browser code.
- Do not sync heartbeats, token-stream chunks, or transient agent events to Linear.
- Do not support direct Linear-backed team execution in V1; current team execution remains internal/local only.
- Do not change the core `tracker.Tracker` interface for existing trackers.

## Decisions

### Decision 1: Durable local timeline is canonical

Add `internal/timeline` as the canonical store for single-agent workflow history. The store writes one append-only JSONL file per issue under `.contrabass/state/workflow-timeline` by default.

Records are tagged as:

- `run_upsert`
- `node_upsert`
- `run_sync_upsert`
- `node_sync_upsert`

The reducer returns `WorkflowTimelineSnapshot` containing runs, nodes, run-level sync state, and node-level sync state. Node writes are idempotent by `(issue_id, run_id, node_id, attempt)` and content hash.

Alternative considered: use Linear comments as restart source. Rejected because it makes runtime observability dependent on Linear availability, comment schema, and rate limits.

### Decision 2: Separate run/root sync from node/reply sync

Use `RunSyncState` keyed by `(issue_id, run_id, target)` for root comments, and `NodeSyncState` keyed by `(issue_id, run_id, node_id, target)` for replies/top-level node comments.

This prevents duplicate root comments when multiple pending nodes belong to the same run and allows restart recovery after root creation but before reply creation.

### Decision 3: Timeline writes use explicit file locking

`internal/timeline` owns a file-lock wrapper copied/adapted from `internal/team/flock.go`. Every append/upsert against a per-issue JSONL file MUST acquire the lock before opening the file for append.

Alternative considered: rely on process-local mutexes. Rejected because syncer and runtime paths can run concurrently and future CLI/debug tools may write the same files.

### Decision 4: Linear syncer is an application-runtime component, not web

Place the syncer type in `internal/timeline` as `LinearSyncer`, but own its lifecycle from `cmd/contrabass/main.go` in the single-agent path. Main constructs the timeline store and syncer after creating the Linear tracker and before optional web startup, then runs orchestrator and syncer under one context/errgroup.

The syncer starts when `linear.sync_comments.enabled=true` even if `port == 0`. The web server remains read-only for timeline and detail state.

### Decision 5: Linear writer APIs are optional and ID-returning

Keep `tracker.Tracker` unchanged. Add optional Linear-capability interfaces that return comment IDs:

- `CreateRootComment(ctx, input) (CommentRef, error)`
- `CreateReplyComment(ctx, input) (CommentRef, error)`
- `UpdateComment(ctx, commentID, body) (CommentRef, error)` when supported

Linear `commentCreate` must return `comment { id url }`, not just `success`.

Before implementation, verify current Linear GraphQL schema for reply parent field and update mutation. If the default reply-thread mode is unsupported, effective mode falls back to top-level comments. If the operator explicitly configures an unsupported mode, config validation fails.

### Decision 6: Legacy Linear comments are suppressed only when sync is enabled

When `linear.sync_comments.enabled=false`, existing completion and unverified-success `PostComment` behavior remains unchanged.

When sync is enabled and tracker type is Linear, direct completion/unverified-success comments are suppressed and replaced by timeline nodes consumed by the syncer. There is no separate duplicate-enabling flag.

Non-Linear trackers keep legacy behavior.

### Decision 7: V1 node coverage includes terminal and pre-agent failures

V1 creates syncable timeline nodes for:

- claim failure: `run:attempt-<n>:claim-failed`
- workspace creation failure: `run:attempt-<n>:workspace-failed`
- prompt render failure: `run:attempt-<n>:prompt-failed`
- agent start failure: `run:attempt-<n>:agent-start-failed`
- run completion: `run:attempt-<n>:complete`
- run failure: `run:attempt-<n>:failed`
- retry queued: `run:attempt-<n>:retry-queued`
- unverified success paused: `run:attempt-<n>:needs-review`

Run-started nodes may exist for dashboard only and do not sync to Linear.

### Decision 8: Dashboard reads backend detail/timeline endpoints

Add endpoints:

- `GET /api/v1/issues/{issue_id}/details`
- `GET /api/v1/issues/{issue_id}/timeline`

Do not extend the broad existing `GET /api/v1/{identifier}` route. The dashboard loads details/timeline when the issue detail sheet opens and shows non-blocking loading/error states.

## Risks / Trade-offs

- Linear reply/update schema mismatch → Perform schema verification first; support deterministic top-level fallback for unsupported default reply mode.
- Duplicate comments after restart → Use separate run/root sync and node sync state plus hidden content-hash markers.
- Timeline JSONL corruption under concurrent writes → Require file locking for every append/upsert and test concurrent writes.
- Comment noise → Sync only durable completion/failure/retry/needs-review nodes; no heartbeats or stream events.
- More local persistence surface → Keep V1 scope single-agent only and use append-only JSONL before adding compaction.
- Sync failures hiding from operators → Surface pending/synced/failed/skipped sync status in the dashboard timeline.

## Migration Plan

1. Add config defaults with comment sync disabled by default.
2. Implement timeline store and tests before wiring runtime paths.
3. Wire timeline into single-agent orchestrator while keeping legacy comments for sync-disabled mode.
4. Add Linear schema gate, writer interfaces, and syncer behind opt-in config.
5. Add backend detail/timeline endpoints and dashboard rendering.
6. Update docs to explain that comment sync is best-effort and off by default.

Rollback strategy: disable `linear.sync_comments.enabled` to restore legacy direct completion comments. Local timeline files are additive and can remain on disk unused.

## Open Questions

- Exact Linear GraphQL field for threaded replies and comment update must be verified against current schema before implementation.
- Whether to add timeline compaction is deferred until append-only storage proves useful.
- Team-to-Linear projection requires a future `ExternalIssueRef` mapping and is out of V1 scope.
