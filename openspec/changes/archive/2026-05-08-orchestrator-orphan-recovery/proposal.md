## Why

When the orchestrator process restarts (graceful or crash), its in-memory `managedIssues` map is empty. Issues that were "In Progress" in Linear before the restart are mapped to `types.Claimed` by `issueStateFromLinearStateType`, causing `dispatchUnclaimedIssues` to skip them permanently. The result is zero running agents despite issues waiting for work — a silent stall that requires manual operator intervention to resolve.

## What Changes

- New `recoverOrphanedClaims` step added to the orchestrator poll loop: any issue whose Linear state is "started" (`Claimed`) but whose ID is absent from `managedIssues` is overridden to `Unclaimed` before dispatch evaluation.
- No persistent state, no shutdown hooks, no distributed coordination needed — the managed-issues map is the authoritative liveness source.
- New log event: `orphan_claim_recovered` (issue_id, identifier) emitted once per recovered issue per restart cycle.

## Capabilities

### New Capabilities

- `orphan-claim-recovery`: Per-poll cross-reference of Linear "started" issues against the live managed-issues set, with automatic Unclaimed override for issues not actively managed.

### Modified Capabilities

<!-- none — dispatch logic and Linear state mapping are unchanged; recovery is a pre-dispatch override step -->

## Impact

- **Backend**: `internal/orchestrator/orchestrator.go` (new `recoverOrphanedClaims` method, called before `dispatchUnclaimedIssues` each tick).
- **Tests**: `internal/orchestrator/orchestrator_test.go` (restart-scenario table cases: stale In-Progress issue is re-dispatched after restart).
- **No API, wire-format, or tracker changes.**
