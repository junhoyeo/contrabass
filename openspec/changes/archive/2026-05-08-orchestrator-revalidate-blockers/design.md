## Context

The orchestrator poll loop calls `dispatchUnclaimedIssues` on every tick, which already contains `unresolvedBlockers` — but only for `Unclaimed` issues. Managed (running) issues are tracked in `o.managedIssues` (a map guarded by a mutex). The poll loop receives a fresh `[]types.Issue` slice from the tracker each tick, which includes up-to-date `BlockedBy` fields. The missing piece is a symmetric check that runs _after_ dispatch and inspects currently-managed issues against the same `openIDs` set.

## Goals / Non-Goals

**Goals:**
- Re-evaluate `BlockedBy` for every managed issue on each poll tick.
- Gracefully stop the agent (existing stop-signal path) and reset the issue to `Todo` if a previously-absent blocker is now present.
- Log `running_released_blocked_by` analogous to the existing `dispatch_skipped_blocked_by`.

**Non-Goals:**
- Changing the dispatch-time check (it stays as-is).
- Force-killing agents (use the same graceful stop path used elsewhere).
- Retroactively preventing the race at the Linear API level (that is the short-term mitigation documented separately).

## Decisions

**1. Call site: after `dispatchUnclaimedIssues`, same tick**
Both checks share the same `openIDs` map built from the current issue snapshot, so they are consistent within a tick. Alternative (separate tick) was rejected to keep the invariant tight.

**2. Graceful stop, then tracker revert to Todo**
The agent may be mid-commit; SIGTERM + drain is safer than SIGKILL. After exit, call `tracker.MoveIssue(issueID, Todo)` so the issue re-enters the dispatch queue once blockers clear. Alternative (leave in current state, ignore) was rejected — it defeats the purpose.

**3. Skip if issue has no `BlockedBy`**
`unresolvedBlockers` already returns `nil` for empty slices; no extra guard needed. Short-circuits the hot path for the common case.

**4. Re-use `unresolvedBlockers` helper unchanged**
The helper is pure and already correct. No new logic needed — only a new call site.

## Risks / Trade-offs

- **Thrash risk** — if a blocker oscillates between open/done across ticks, the agent could be stopped and re-dispatched repeatedly. Mitigation: the blocker state in Linear is stable; oscillation only occurs if a user re-opens a done issue deliberately.
- **Work loss** — stopping a running agent discards in-progress work. Mitigation: this only triggers when a _new_ blocker relation appears on an already-running issue, which is rare and indicates an operator correction. The agent will re-run after the blocker resolves.
- **Revert latency** — `MoveIssue` is a tracker API call; failure should be logged and retried on the next tick (issue remains managed but will re-trigger the check).
