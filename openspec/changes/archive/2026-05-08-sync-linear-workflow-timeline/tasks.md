## 1. Linear schema and config foundation

- [x] 1.1 Verify the current Linear GraphQL schema for comment replies, comment update support, commentCreate return fields, and rate-limit behavior; document the exact mutation/query fields in `internal/tracker/linear.go` comments or tests.
- [x] 1.2 Add `linear.issue_details` and `linear.sync_comments` config structs, defaults, validation, and accessors in `internal/config`.
- [x] 1.3 Add config tests for default values, explicit enablement, invalid mode, unsupported explicit mode handling, and non-Linear tracker behavior.

## 2. Durable workflow timeline package

- [x] 2.1 Create `internal/timeline` with `WorkflowTimelineSnapshot`, `WorkflowRunSummary`, `WorkflowNodeSummary`, `RunSyncState`, and `NodeSyncState` types.
- [x] 2.2 Implement append-only per-issue JSONL storage with record types `run_upsert`, `node_upsert`, `run_sync_upsert`, and `node_sync_upsert`.
- [x] 2.3 Implement timeline-owned file locking adapted from `internal/team/flock.go` and require it for every append/upsert.
- [x] 2.4 Implement snapshot reduction, idempotent node writes by content hash, run sync upsert, node sync upsert, and comment body rendering with hidden Contrabass markers.
- [x] 2.5 Add timeline tests for append/reduce, duplicate same-hash writes, changed-hash replacement, restart load, malformed record handling, and concurrent valid JSONL writes.

## 3. Orchestrator timeline integration

- [x] 3.1 Inject timeline store/config into the single-agent orchestrator without changing non-Linear tracker behavior.
- [x] 3.2 Append dashboard-only run-started state and syncable pre-agent failure nodes for claim, workspace creation, prompt render, and agent start failures.
- [x] 3.3 Append syncable completion, failed, retry-queued, and needs-review nodes from `completeRun`, `enqueueBackoffFromRunResult`, and `pauseUnverifiedSuccess`.
- [x] 3.4 Preserve legacy direct comments when sync is disabled; suppress legacy direct Linear completion/unverified-success comments when sync is enabled.
- [x] 3.5 Add orchestrator tests for pre-agent failure nodes, terminal nodes, retry/needs-review nodes, legacy comment preservation, and sync-enabled duplicate suppression.

## 4. Linear comment writer and syncer

- [x] 4.1 Add optional ID-returning comment writer interfaces without changing `tracker.Tracker`.
- [x] 4.2 Update `LinearClient` comment mutations to return comment IDs/URLs and implement root comment, reply comment, update comment when schema-supported, and top-level fallback behavior.
- [x] 4.3 Implement `internal/timeline.LinearSyncer` with bounded queue, pending-node scan, one root per `(issue_id, run_id, target)`, node reply/top-level sync, retry state, and shutdown drain.
- [x] 4.4 Wire `LinearSyncer` lifecycle from `cmd/contrabass/main.go` so it starts with the orchestrator even when the web dashboard is disabled.
- [x] 4.5 Add fake Linear GraphQL tests for root/reply success, root-created reply-failed restart recovery, same-run root dedupe, node dedupe after restart, 429 Retry-After persistence, unsupported default reply fallback, explicit unsupported mode validation, and update-unsupported append fallback.

## 5. Linear issue details and web APIs

- [x] 5.1 Add an optional `IssueDetailProvider` implemented by Linear with a separate rich issue detail query that does not bloat `FetchIssues` polling.
- [x] 5.2 Add `GET /api/v1/issues/{issue_id}/details` returning normalized issue data, Linear detail data when available, and generation timestamp.
- [x] 5.3 Add `GET /api/v1/issues/{issue_id}/timeline` returning `WorkflowTimelineSnapshot` from the local timeline store.
- [x] 5.4 Add web handler tests for success, missing issue, provider unavailable, timeline success, and timeline errors.

## 6. Dashboard rendering

- [x] 6.1 Add TypeScript types for Linear issue details, workflow timeline snapshots, node summaries, run sync states, and node sync states.
- [x] 6.2 Update `IssueDetailSheet` to load issue details and timeline on open while preserving base snapshot data during loading.
- [x] 6.3 Render Linear metadata, workflow timeline rows, per-node sync badges, and inline non-blocking error states.
- [x] 6.4 Add dashboard tests for detail loading, timeline rendering, sync badge states, and failed detail/timeline fetches.

## 7. Documentation and verification

- [x] 7.1 Update README/config docs with `linear.issue_details` and opt-in `linear.sync_comments` settings, including the fact that sync is best-effort and off by default.
- [x] 7.2 Run `go test ./internal/config ./internal/timeline ./internal/tracker ./internal/orchestrator ./internal/web`.
- [x] 7.3 Run `go test ./...`.
- [x] 7.4 Run `make test-dashboard` or `cd packages/dashboard && bun test`, then `cd packages/dashboard && bun run build`.
- [x] 7.5 Perform a dashboard smoke test that opens a Linear issue detail sheet and verifies detail/timeline loading and non-secret payloads.
