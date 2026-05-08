# Improve dashboard liveness signals

## Why

The contrabass dashboard hides whether an agent is actually making progress.
Two recent failure modes made this visible:

1. **15-minute opaque wait on ZII-49** — `tokens_in/out=0`, `phase=4` (a bare
   integer), and the only event stream entry per cycle was
   `agent_event=team/stalled`. From the user's perspective this is
   indistinguishable from a stuck process. omx between turns is the steady
   state, but the dashboard renders steady-state heartbeats as foreground
   noise and doesn't surface anything else.

2. **False-success on ZII-49** — the run reported `status=Succeeded` and
   contrabass moved the Linear issue to `Done`, despite the underlying
   branch having **zero** commits (`git reflog` shows only the
   `branch: Created from HEAD` line and nothing more). Token counters,
   diff stats, and recent-tool-call signals would all have flagged this
   as a hollow run *during* the run, not after the fact. The dashboard
   currently shows none of them.

The pattern is the same in both cases: **the dashboard renders
implementation noise (phase numbers, stalled heartbeats, zeroed token
counters) instead of a small set of human-readable progress signals**.

## What Changes

Producer side (Go, `internal/web` + `internal/orchestrator`):

- Add a small set of derived progress fields to the
  `/api/v1/state` response per running issue: `phase_label`,
  `last_activity_at`, `last_activity_kind`, `iteration`,
  `iteration_max`, `diff_added`, `diff_removed`, `diff_files`.
- Filter `team/stalled` from being promoted into the agent-event ring
  buffer used by the SSE stream. Replace with a
  `last_heartbeat_at` field that updates on every poll, including
  stalled ones — that's the right semantic for a heartbeat (a clock,
  not an event).
- Keep the existing event types intact for non-dashboard consumers
  (orchestrator tests, IPC). The filtering is at the SSE/dashboard
  fan-out edge.

Consumer side (TypeScript, `packages/dashboard`):

- Render `phase_label` instead of `phase` in `SessionsTable` /
  `WorkerTable`.
- Add a "Last activity" column that shows
  `relative(now - last_activity_at) + ' · ' + last_activity_kind`,
  with a colored dot (green <30s, yellow 30–180s, red >180s).
- Add a "Diff" column showing `+N -M (F files)` when present, or `—`
  when the workspace has produced nothing yet.
- Add an iteration progress badge on running issues
  (`iter 3/50`).
- Re-classify `dispatch_skipped_blocked_by` skip events: instead of
  emitting them as agent log lines, surface them in a "Queue" panel
  grouping waiting issues by their unresolved blockers.

## Impact

- Affected capabilities: `web-state-payload` (NEW — payload shape used
  by the dashboard and any external consumer), `dashboard-status-rendering`
  (NEW — React rendering of the running-issue table and queue panel).
- Affected code:
  - `internal/web/server.go`, `internal/web/state.go` (or wherever the
    `/api/v1/state` payload is assembled),
  - `internal/orchestrator/snapshot.go` (the `Snapshot` type that the
    web layer marshals),
  - `internal/agent/teamcli.go` (filter the heartbeats coming out of
    omx; surface a `last_meaningful_activity` clock alongside the
    existing event ring),
  - `packages/dashboard/src/components/SessionsTable.tsx`,
    `MetricCards.tsx`, `RetryQueue.tsx` (and a new `QueuePanel.tsx`),
  - `packages/dashboard/src/i18n/messages.ts` for the phase label
    table and human-readable strings.
- Out of scope: token usage from omx (`surface-omx-token-usage`
  change covers that, T1–T5). This change must not assume the omx
  metrics file exists; the diff fields must work for *any* runner.
- Not addressed: anomaly detection ("succeeded with zero diff is
  suspicious") — captured as follow-up in design.md.
