# Design: Improve dashboard liveness signals

## Context

`/api/v1/state` returns an `OrchestratorSnapshot` whose `Running []RunningIssue`
slice today exposes only:
`{issue_id, attempt, pid, session_id, workspace, started_at, phase, tokens_in, tokens_out}`.

The dashboard renders these directly. `phase` is an integer enum, the
token fields are zero for omx (until `surface-omx-token-usage` lands),
and there is no per-run aliveness clock. The result is a visually
noisy dashboard whose state signals are mostly hollow.

The orchestrator already has access to richer signals, just not in
the snapshot:

- `internal/agent/teamcli.go` consumes a stream of agent events. The
  `monitorProcess` poll loop sees `team/stalled`, `team/event`,
  `turn/started`, `turn/completed`, `tool_call`, `task_completed`,
  `task_failed`, etc.
- The orchestrator stores per-run state under `internal/orchestrator/state.go`
  including the last event timestamp.
- The git worktree at `proc.workspace` can be queried with
  `git diff --stat HEAD` for live edit progress.
- The omx team's `.omx/state/run-state.json` exposes
  `iteration` and `max_iterations`.

This change wires a curated subset of those signals through the
existing snapshot/SSE pipeline.

## Goals / Non-Goals

**Goals**

- Producer adds 8 derived fields per running issue without breaking
  any existing consumer.
- Consumer renders one new column ("Last activity") and one new
  panel ("Queue") that re-frame the existing skip events as informative
  rather than alarming.
- Heartbeat events (`team/stalled`) stop polluting the agent log
  stream that drives the SSE feed; their information is preserved as
  a `last_heartbeat_at` clock instead of a discrete event.
- Implementation works for every runner (codex, omx, opencode,
  ohmyopencode, omc, mock). No runner-specific code paths.

**Non-Goals**

- Token usage from omx (covered by `surface-omx-token-usage`).
- A separate anomaly-detection pass that fires on
  "Succeeded with zero diff" — flagged in the proposal as future
  work; this change provides the *signals* an anomaly detector would
  read but not the detector itself.
- Replacing the SSE protocol or breaking the existing event
  vocabulary. New fields are additive on the snapshot type only.

## Decisions

### Decision 1: Keep `phase` numeric, add `phase_label`

Numeric `phase` is load-bearing for orchestrator tests and for the
JSON contract. Adding a sibling `phase_label` string lets the
dashboard render human text without breaking machine consumers.
Mapping table lives next to the `Phase` enum in
`internal/types/types.go`.

### Decision 2: `last_activity_at` reflects the last *non-heartbeat* event

`team/stalled` is the steady state. Counting it as activity defeats
the purpose. The runner SHALL update `last_activity_at` only when
emitting any event whose type is **not** in the heartbeat set
(currently just `team/stalled`; extensible).

`last_activity_kind` carries the event type string (e.g.
`tool_call`, `turn/started`). Together they answer the user's real
question: "what was the agent doing most recently, and how long ago".

### Decision 3: `last_heartbeat_at` is a separate clock

Even when no real activity is happening, the runner is still polling
omx and getting back stalled responses. That clock is useful: if it
also stops, the runner itself is dead. So we keep both:

- `last_activity_at` advances on real events; stale → agent stuck.
- `last_heartbeat_at` advances on every poll; stale → runner dead.

The dashboard renders the activity clock as the prominent indicator
and the heartbeat clock as a tooltip / second-line subtle pulse.

### Decision 4: Diff stats are computed in the snapshotter, not the runner

`git diff --stat HEAD` is cheap (under 50 ms for a small worktree)
and works for any runner. Putting it in the snapshot assembly path
(`internal/orchestrator/snapshot.go`) means we don't need to touch
five different runner implementations. The cost is one fork+exec per
running issue per snapshot tick — bounded by `MaxConcurrency` (≤8 per
the project default).

The diff is computed against `HEAD` of the per-issue branch, which
is the orchestrator's claim point. A non-empty diff means the agent
is editing files in its sandbox; a continuously-zero diff during a
"Succeeded" run is the smoking gun for the false-success failure
mode.

### Decision 5: Iteration fields read from `.omx/state/run-state.json` when available

omx-specific signal, but the field names on the snapshot
(`iteration`, `iteration_max`) are runner-agnostic. Other runners
populate them with `0` / `0` and the dashboard renders nothing. The
same pattern as token counters before `surface-omx-token-usage`.

### Decision 6: Filter heartbeats inside the SSE fanout edge, not at the source

The orchestrator's event hub fans out to the TUI bridge AND the SSE
endpoint. TUI tests assert on the full event vocabulary. SSE clients
get a filtered view. We add a small `eventIsHeartbeat(event)`
predicate at the SSE handler in `internal/web/sse.go` (or wherever
the SSE encoder lives). No changes to the hub itself.

### Decision 7: Surface `dispatch_skipped_blocked_by` as a Queue panel, not as agent logs

These events are currently rendered as agent log entries because
that's where everything ends up. They aren't agent activity at all —
they're orchestrator-level scheduling decisions. The dashboard
already has a `RetryQueue` panel; we add a sibling `QueuePanel`
that groups skipped issues by their reported `blockers` list. Each
row shows `<identifier> blocked by <X, Y, Z>`. Skip events stop
flowing into the agent log stream (filter at the SSE edge again).

## Risks / Trade-offs

- **Risk**: `git diff --stat` on a large worktree could spike CPU
  during the snapshot tick. Mitigation: runs in a 1-second timeout;
  on timeout the diff fields are zero and a `diff_status="timeout"`
  string is included so the dashboard can show `?` instead of `+0
  -0`.
- **Risk**: `last_activity_at` regression — if a runner forgets to
  emit anything that isn't a heartbeat, the activity clock stays
  pinned at run-start forever. Mitigation: tests assert that every
  runner emits at least one non-heartbeat event during a normal
  successful run.
- **Trade-off**: The Queue panel re-frames a *skip* event as a
  *waiting* state. This is a conscious UX shift away from the
  "everything is a log line" model. It costs frontend-only work but
  changes user perception from "errors are happening" to "items are
  queued correctly".

## Migration Plan

- Producer fields are purely additive on the snapshot. Old SSE
  clients (none, in practice) continue to work.
- The dashboard ships in the same binary embed; no out-of-band
  rollout.
- The `team/stalled` filter at SSE only changes what is sent over the
  wire to the dashboard. Tests that assert on the hub or on raw
  orchestrator events (e.g. `internal/orchestrator/orchestrator_test.go`)
  are unaffected.
