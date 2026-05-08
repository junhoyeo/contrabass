## Why

The orchestrator only checks `BlockedBy` at claim time (`dispatchUnclaimedIssues`). If a blocking relation is added to Linear _after_ an issue is already claimed and running — which happens whenever issues and relations are created in separate API calls — the running agent is never interrupted, violating the expressed dependency order. This was observed in practice with ZII-72 being claimed before ZII-70/71's `blocks` relation was persisted.

## What Changes

- New `releaseBlockedRunning` step added to the orchestrator's main polling loop that re-evaluates `BlockedBy` for every currently-managed (running/claimed) issue against the fresh issue list.
- If a running issue is found to have an unresolved blocker, the orchestrator sends a stop signal to the agent, waits for it to exit, and reverts the issue state to `Todo` via the tracker so it re-enters the dispatch queue once its blockers resolve.
- New log event: `running_released_blocked_by` (mirrors the existing `dispatch_skipped_blocked_by`).

## Capabilities

### New Capabilities

- `running-blocker-revalidation`: Per-poll re-check of `BlockedBy` for managed issues, with graceful stop + requeue on violation.

### Modified Capabilities

<!-- none — dispatch-time blocker check is unchanged; this adds a complementary runtime check -->

## Impact

- **Backend**: `internal/orchestrator/orchestrator.go` (new `releaseBlockedRunning` method, called from the main loop alongside `dispatchUnclaimedIssues`), `internal/orchestrator/orchestrator_runtime.go` (stop + requeue plumbing).
- **Tests**: `internal/orchestrator/orchestrator_test.go` (new table-driven cases covering the late-relation race scenario).
- **No API or wire-format changes.**
