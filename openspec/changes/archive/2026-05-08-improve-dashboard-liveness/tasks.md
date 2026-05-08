# Tasks: Improve dashboard liveness signals

## T1 — Extend `RunningIssue` snapshot type with liveness fields

**Files**: `internal/orchestrator/snapshot.go` (or wherever the
`RunningIssue` struct lives — verify with `grep -n "RunningIssue"
internal/orchestrator/`); `internal/types/types.go` for the
`Phase int → string` table.

**Contract**: Add to the existing `RunningIssue` struct (new fields,
no removals, no renames):

```go
PhaseLabel       string `json:"phase_label"`
LastActivityAt   string `json:"last_activity_at"`
LastActivityKind string `json:"last_activity_kind"`
LastHeartbeatAt  string `json:"last_heartbeat_at"`
Iteration        int    `json:"iteration"`
IterationMax     int    `json:"iteration_max"`
DiffAdded        int    `json:"diff_added"`
DiffRemoved      int    `json:"diff_removed"`
DiffFiles        int    `json:"diff_files"`
DiffStatus       string `json:"diff_status"`
```

Add a single helper `func (p Phase) Label() string` next to the
existing `Phase` enum that returns the human-readable label per
the table in design.md. Empty string for unknown values.

**Acceptance**:
- All field names + JSON tags match exactly.
- `(Phase).Label()` returns at least the labels for the 5 enum
  values currently used in code (`PreparingWorkspace`,
  `RunningAgent`, `Releasing`, `Done`, `Failed` — verify the
  exact set).
- `go build ./...` clean. No test added in this task.

**Depends on**: none.
**Blocks**: T2, T3, T4, T6.

---

## T2 — Track `last_activity_at` / `last_heartbeat_at` per running issue

**Files**: `internal/agent/teamcli.go`, plus the orchestrator's
event sink that funnels agent events into the snapshot
(`internal/orchestrator/orchestrator_runtime.go` likely — verify by
grepping for the existing `tokens_in` accumulator path).

**Contract**: Define a single source of truth for "is this event a
heartbeat":

```go
var heartbeatEventTypes = map[string]struct{}{
    "team/stalled": {},
}

func isHeartbeatEvent(t string) bool {
    _, ok := heartbeatEventTypes[t]
    return ok
}
```

Wherever the orchestrator currently records "last seen agent event
for this run" (likely on the `RunAttempt` or per-run state struct),
split into two fields. On every event:

- update `lastHeartbeatAt = event.Timestamp`,
- if `!isHeartbeatEvent(event.Type)`,
  also update `lastActivityAt` and `lastActivityKind`.

Snapshot assembly copies both timestamps to the new T1 fields as
RFC3339 strings (empty string when unset).

**Acceptance**:
- `isHeartbeatEvent` is exported-or-package-internal but reused by
  T5 (the SSE filter), so define it once.
- Run state initialized with empty `lastActivityAt` until the first
  non-heartbeat event arrives.
- `go vet ./...` clean.

**Depends on**: T1.
**Blocks**: T6.

---

## T3 — Add diff-stat probe to the snapshotter

**Files**: `internal/orchestrator/snapshot.go` (the function that
walks running issues building the snapshot — add a helper,
do not modify the runner code).

**Contract**:

```go
// diffStat runs `git diff --stat HEAD` in the workspace and returns
// (added, removed, files, status). status is "ok" / "timeout" / "error".
// Hard 1-second timeout via context. On any failure the int triple is
// (0, 0, 0) and status is the failure mode.
func diffStat(ctx context.Context, workspace string) (added, removed, files int, status string)
```

Parsing strategy: run `git -C <ws> diff --shortstat HEAD` and parse
the single line `" 2 files changed, 47 insertions(+), 3 deletions(-)"`
with a small regexp. Missing fields parse as 0 (e.g. shortstat
omits `insertions(+)` when added==0).

The snapshot loop calls this for each running issue, with a 1-second
context timeout. Empty workspace or non-git path returns `("error", 0, 0, 0)`.

**Acceptance**:
- Function compiles, no new dependencies.
- Returns `("ok", 0, 0, 0)` for a clean tree (shortstat empty).
- Returns the parsed counts for a tree with edits.
- Times out cleanly if the diff exceeds the 1s budget.
- Snapshot construction continues normally even when probes fail.

**Depends on**: T1.
**Blocks**: T6.

---

## T4 — Add iteration probe from `.omx/state/run-state.json`

**Files**: `internal/orchestrator/snapshot.go`.

**Contract**:

```go
// readIterationProgress returns (iteration, max) from
// <workspace>/.omx/state/run-state.json. Missing file or parse error
// returns (0, 0) silently — non-omx runners won't have this file.
func readIterationProgress(workspace string) (iteration, max int)
```

JSON shape: `{ "iteration": int, "max_iterations": int }`. Use stdlib
only.

**Acceptance**:
- Missing file → `(0, 0)`, no error logged.
- Well-formed file → parsed values.
- Corrupt JSON → `(0, 0)`.
- Snapshot loop calls this for each running issue.

**Depends on**: T1.
**Blocks**: T6.

---

## T5 — SSE: filter heartbeats and re-channel queue events

**Files**: `internal/web/sse.go` (or wherever the SSE encoder writes
to the response — verify with `grep -n 'event:' internal/web/`).

**Contract**:

- Before encoding any event to the wire, drop those for which
  `isHeartbeatEvent(event.Type)` returns true (re-using the helper
  from T2). The hub still delivers them to the SSE handler; the
  filter is at the encoder edge.
- For events whose Type matches `dispatch_skipped_blocked_by`,
  emit them with `event: queue` instead of the default
  `event: agent` channel. Payload unchanged.

**Acceptance**:
- TUI bridge tests still receive the full event stream (no change
  to the hub).
- New `internal/web/sse_test.go` (or addition to an existing test
  file) asserts: a `team/stalled` event does NOT appear in the
  encoded SSE buffer; a `dispatch_skipped_blocked_by` event appears
  with `event: queue`.
- A `tool_call` event still appears with the existing channel.

**Depends on**: T2 (uses `isHeartbeatEvent`).
**Blocks**: T6.

---

## T6 — Producer tests (snapshot + SSE)

**Files**: `internal/orchestrator/snapshot_test.go`,
`internal/orchestrator/orchestrator_test.go`,
`internal/web/sse_test.go` (or extend the closest existing test
file in each package).

**Contract**: Add focused unit tests for each of T1–T5:

1. `TestPhaseLabel_Coverage` — every numeric phase used by
   production code has a non-empty label.
2. `TestSnapshot_LastActivityIgnoresHeartbeats` — driving the
   orchestrator state with one `tool_call` then five
   `team/stalled` events leaves `LastActivityAt` pinned at the
   `tool_call` timestamp while `LastHeartbeatAt` advances.
3. `TestDiffStat_ParsesShortstat` — table-driven over `git diff
   --shortstat` outputs, including the empty-string case.
4. `TestDiffStat_TimeoutReturnsTimeoutStatus` — fake long-running
   command returns `status="timeout"`.
5. `TestReadIterationProgress` — table over (missing, well-formed,
   corrupt, partial) JSON.
6. `TestSSE_FiltersHeartbeats` — encoded SSE stream contains no
   `team/stalled` event and a `dispatch_skipped_blocked_by` arrives
   with `event: queue`.

**Acceptance**:
- All listed tests pass: `go test ./internal/orchestrator/ ./internal/web/ -count=1`.
- No pre-existing test in those packages is modified.

**Depends on**: T1, T2, T3, T4, T5.
**Blocks**: none on the producer side.

---

## T7 — Dashboard: render phase label, last activity, diff, iteration

**Files**:
- `packages/dashboard/src/components/SessionsTable.tsx`
- `packages/dashboard/src/i18n/messages.ts` (Chinese strings for
  "上次活动", "差异", "迭代", and the relative-time formats)
- `packages/dashboard/src/i18n/format.ts` (relative-time helper if
  not already present)

**Contract**:

- Add columns in this order after the existing `phase` column:
  `Last activity`, `Diff`, `Iter`.
- `Phase` column renders `running.phase_label` if non-empty, else
  the result of a client-side enum-to-label mapping (TypeScript copy
  of T1's table).
- `Last activity` renders the traffic-light dot + relative time +
  subdued kind per the dashboard-status-rendering spec.
- `Diff` renders `+A -R (F files)`, `—`, or `?` per spec.
- `Iter` renders the `iter N/M` badge only when `iteration_max > 0`.

**Acceptance**:
- TypeScript compiles cleanly: `bun run typecheck` (or whatever
  `packages/dashboard/package.json` exposes as the type-check
  command).
- `bun test packages/dashboard` continues to pass.
- A `SessionsTable.test.tsx` case exists for each of the four
  rendering paths (server label, client fallback label, diff "ok"
  with edits, diff "timeout").

**Depends on**: T1 (server fields must exist in the type).
**Blocks**: none.

---

## T8 — Dashboard: Queue panel component

**Files**: `packages/dashboard/src/components/QueuePanel.tsx`
(new), `packages/dashboard/src/components/QueuePanel.test.tsx`
(new), `packages/dashboard/src/App.tsx` (mount the panel),
`packages/dashboard/src/hooks/useSSE.ts` (subscribe to the new
`event: queue` channel).

**Contract**:

- The hook must register a listener for SSE `event: queue`. The
  decoded payload shape is
  `{ issue_id: string, identifier: string, blockers: string }` (the
  `blockers` field is the comma-joined string emitted by the
  orchestrator gate; the panel splits on `,`).
- The panel keeps an in-memory map keyed by `issue_id`. On each
  event it refreshes a `lastSeen` timestamp. A 1-second `setInterval`
  evicts rows where `now - lastSeen > 5_000 ms`.
- Each visible row shows `<identifier> blocked by <X, Y, Z>`.

**Acceptance**:
- `QueuePanel.test.tsx` covers: single blocker, multiple blockers,
  eviction after 6 seconds, refresh-resets-eviction.
- `App.tsx` mounts the panel above or below the existing
  `RetryQueue`.
- No regression in `bun test`.

**Depends on**: T5 (server emits the `event: queue` channel).
**Blocks**: none.

---

## T9 — Dashboard: filter heartbeats and queue events from `AgentLogs`

**Files**: `packages/dashboard/src/components/AgentLogs.tsx`,
`packages/dashboard/src/components/AgentLogs.test.tsx`.

**Contract**: Drop log entries whose type is in the dashboard-side
heartbeat set (`{"team/stalled"}`) and entries that arrived through
the SSE `queue` channel. The heartbeats-and-queue suppression is
defense in depth; the server already filters via T5.

**Acceptance**:
- `AgentLogs.test.tsx` adds two cases: `team/stalled` produces no
  DOM entry; a queue-channel event produces no DOM entry; a
  `tool_call` event still does.
- No other component test is modified.

**Depends on**: T5 (server-side filter is the primary defense; T9
just hardens the client).
**Blocks**: none.

---

## Rejection rules (apply to ALL tasks)

A diff that satisfies any of the following MUST be rejected:

1. The diff modifies only `*_test.go` / `*.test.tsx` files for
   tasks T1, T2, T3, T4, T5, T7, T8 (production code is mandatory).
2. The diff renames or removes any existing `RunningIssue` field
   (additive only).
3. The diff modifies `internal/agent/teamcli.go` for any task other
   than T2.
4. The diff adds a new Go module dependency or npm package
   dependency. Stdlib + existing deps only.
5. The diff omits any acceptance bullet from the task it claims to
   implement.
6. The diff couples T7 and T8 in the same file change set; the
   sessions table changes and the queue panel are independent UI
   surfaces.

## Task graph

```
T1 ─┬─ T2 ─┐
    ├─ T3 ─┼─ T6 (producer tests)
    └─ T4 ─┤
T5 ─────────┘  (and feeds T8/T9 on the consumer side)

T1 ── T7 (dashboard rendering)
T5 ── T8 (queue panel)
T5 ── T9 (logs filter)
```

Maximum parallelism after T1 lands: T2/T3/T4/T5 in parallel; then
T6 waits on all four; T7 waits on T1; T8 / T9 wait on T5.
