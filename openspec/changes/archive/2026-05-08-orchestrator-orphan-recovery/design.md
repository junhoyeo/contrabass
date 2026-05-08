## Context

`issueStateFromLinearStateType` is a pure function: `"started"` → `types.Claimed`. `dispatchUnclaimedIssues` skips any issue whose `State != Unclaimed`. `managedIssues` is an in-memory map, populated when the orchestrator dispatches an agent and cleared when it exits. On restart the map is empty, so all `"started"` issues are `Claimed` with no corresponding managed entry — orphaned forever.

## Goals / Non-Goals

**Goals:**
- Before each dispatch cycle, override `State` to `Unclaimed` for any issue that is `Claimed` in the snapshot but absent from `managedIssues`.
- Handle both clean restarts and crash recovery identically — no shutdown hook required.
- Work correctly in the steady state: genuinely-managed issues are never incorrectly overridden.

**Non-Goals:**
- Distributed/multi-instance deployments (single-process assumption; no distributed lock needed).
- Persisting managed-issues state across restarts to disk.
- Changing the Linear state mapping (`issueStateFromLinearStateType` stays as-is).

## Decisions

**1. Override in-place on the issues slice before dispatch, not inside `issueStateFromLinearStateType`**
Keeping the mapping function pure preserves its testability. The override is a post-fetch, pre-dispatch step that applies orchestrator-local knowledge (the managed set) to the tracker-sourced snapshot. Alternative (passing managedIssues into the mapper) was rejected — it couples two layers that have no other dependency.

**2. Operate on every poll tick, not only on first tick**
The managed set is always authoritative. If an issue disappears from the managed set mid-run (bug, external kill), the next tick recovers it without special startup logic. A `isFirstPoll` flag would miss mid-run cases and complicate the state machine.

**3. Log once per recovered issue per restart**
Because recovery fires every tick until the issue is dispatched, log at the tick the override is first applied (compare against a `recoveredSet` that is cleared on startup). Avoids log spam on slow-starting agents.

**4. No tracker write at recovery time**
The orchestrator does not move the issue back to "Todo" in Linear during recovery — it simply re-dispatches. When the agent claims the issue, it updates Linear state as normal. Writing to Linear at recovery time adds latency, a failure mode, and is unnecessary.

## Risks / Trade-offs

- **Double-dispatch if two orchestrator instances share a Linear project** — mitigation: single-instance deployment is the documented and tested topology; multi-instance is an explicit non-goal.
- **Issue genuinely "In Progress" on another machine restarted** — same as above; not in scope.
- **Agent re-runs work already done** — agent runners are expected to be idempotent (commit if changed, no-op if already done). This is an existing assumption throughout the codebase.
