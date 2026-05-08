# Design: Surface task phase and completion estimate

## Context

The orchestrator already produces a per-run snapshot field
`phase_label` (added by `improve-dashboard-liveness`) that maps the
numeric `RunPhase` enum to a label such as `AgentRunning` or
`Releasing`. That label answers a different question — *what
high-level lifecycle phase is the run in?* — and is too coarse for
the user-facing question this change targets:

> "How long until the agent is actually finished?"

What's missing is a finer-grained classification of what codex is
doing **inside** the long `AgentRunning` phase, plus a clock-time
estimate the user can plan around.

## Goals / Non-Goals

**Goals**

- Five named stages, monotonically advancing (never backwards),
  derived purely from event-stream signals already present on the
  snapshot. No new event types, no runner-side changes.
- Completion timestamp is *honest* about uncertainty: empty until
  enough signal accumulates, plain `elapsed Xm, normal` when
  confidence is low, `~HH:MM` when confidence is medium-or-better.
- Tracker- and runner-agnostic: the stage classifier consults
  `last_activity_kind` and the diff/heartbeat clocks already on the
  snapshot, none of which are codex-specific.

**Non-Goals**

- Cross-run reference-class forecasting. We don't yet persist past
  runs in a queryable form. A separate follow-up after we have ~20
  completed runs of comparable shape.
- Sub-stage breakdowns inside `Testing` (which test framework, what
  the failing assertion was, etc.).
- Showing a "remaining minutes" countdown anywhere in the UI. The
  proposal explains why this is intentionally avoided; design must
  not regress on that.

## Decisions

### Decision 1: Five named stages, classifier on the snapshot side

Stages and their numeric step values:

| step | name | meaning |
|---|---|---|
| 1 | Exploration | agent reading code, no diff produced yet |
| 2 | Editing | diff is growing; files being created or modified |
| 3 | Testing | diff plateau; tokens still flowing fast (test loop) |
| 4 | Reviewing | tokens still flowing but slower; final pass before yield |
| 5 | Wrapping | terminal turn event observed, run finalizing |

Classifier rules are evaluated in priority order. The first rule
that matches wins; ties resolve to the higher-numbered stage to
preserve monotonic advancement:

```
if last_activity_kind in {"turn/completed", "turn/failed",
                          "turn/cancelled"}:                 → Wrapping (5)
elif diff_added == 0 and diff_removed == 0
     and elapsed_since_last_diff_change > 60s
     and tokens_per_min < 50_000:                            → Reviewing (4)
elif diff_added == 0 and diff_removed == 0
     and elapsed_since_last_diff_change > 30s:               → Testing (3)
elif (current_diff_added > prev_diff_added) or
     (current_diff_removed > prev_diff_removed):             → Editing (2)
else:                                                        → Exploration (1)
```

Inputs the classifier needs from the snapshot tick:

- `last_activity_kind`, `last_activity_at`, `last_heartbeat_at`
  (already on snapshot per `improve-dashboard-liveness`)
- `diff_added`, `diff_removed`, `diff_files`, `diff_status`
  (already there)
- `elapsed = now - started_at`
- one new piece of state: per-issue snapshot of
  `(diff_added_at_last_change, diff_removed_at_last_change,
  ts_at_last_change)`. Stored on the orchestrator's per-run state
  struct, not on the wire.

### Decision 2: Monotonic clamp for `agent_stage_step`

The classifier rules above can in principle return a lower step
than was previously seen if events arrive out of order or a
late-arriving `turn/diff/updated` reverses a `Reviewing` decision.
The wire-level field `agent_stage_step` SHALL be clamped to
`max(prev, computed)` per run, so the dashboard's pill never goes
backwards. The string `agent_stage` follows the same monotonic
clamp.

### Decision 3: Completion timestamp via current-run rate, with a confidence ladder

```
files_per_min  = current_diff_files  / elapsed_min
tokens_per_min = (tokens_in + tokens_out) / elapsed_min

if elapsed_min < 3:
    return ("", "low")
if files_per_min < 0.05 or tokens_per_min < 1000:
    return ("", "low")    # too quiet to call

# Heuristic remaining: codex runs spend ~30-50% time in stage 4-5
# (review + final). Add 35% as a fixed buffer to current-rate ETA.
linear_remaining_min = (estimated_total_files - current_files) / files_per_min
                       if estimated_total_files > current_files else 5
total_estimate_min = elapsed_min + linear_remaining_min * 1.35

# Confidence band:
if elapsed_min > 8 and stage_step >= 3:
    confidence = "high"
elif elapsed_min > 5:
    confidence = "medium"
else:
    confidence = "low"

if confidence == "low":
    return ("", "low")    # signal "show elapsed only"

eta_at = started_at + total_estimate_min minutes
return (eta_at.RFC3339, confidence)
```

`estimated_total_files` defaults to `max(current_files * 1.2, 11)`
on the assumption that a typical contrabass-hardening task touches
~10 files. This is intentionally crude — its only job is to
prevent absurdly low remaining estimates when the agent is mid-edit.

### Decision 4: Server-side computation, dashboard-side rendering only

The classifier and estimator both run inside the snapshot loop on
the orchestrator side. Reasons:

- The dashboard is stateless across reloads; per-run rate windows
  can't be computed there reliably.
- Putting it server-side keeps the rules testable in Go alongside
  every other liveness derivation.
- Future non-React consumers (CLI, TUI) get the same fields free.

The dashboard SHALL render exactly what's on the wire and not
re-derive stages or ETA. If `agent_stage` is empty, the dashboard
shows nothing for the pill (defensive; should not occur in normal
operation).

### Decision 5: Don't show shrinking timestamps

The completion timestamp returned to the wire is a clock time, not
a duration. By construction, a clock time can only shift forward
or backward by small amounts per tick. The dashboard SHALL NEVER
display a remaining duration in a way that visibly counts down or
counts up. This is a hard UX rule motivated by the proposal — any
follow-up that adds "X min remaining" must justify the regression
explicitly.

### Decision 6: Five-step pill, not a percent bar

The dashboard SHALL render `agent_stage` as a five-step pill where
each step is a small box and the current step is highlighted.
Steps before the current one are filled, steps after are outlined.
This avoids the "70% means we're 70% done" lie that a continuous
bar implies. It also makes the late-stage slowdown visible — when
a run sits on step 3 for 5 minutes, that's information, not a bug.

## Risks / Trade-offs

- **Risk**: classifier mis-detects an agent that legitimately writes
  more code in a late "Reviewing" pass, demoting back to step 2.
  Mitigation: monotonic clamp (Decision 2). The trade-off is that
  a real "started over" mid-run looks frozen on the pill; the diff
  column still shows movement, so the user gets the truth from the
  combination.
- **Risk**: the 35% review-buffer constant in Decision 3 doesn't
  fit very simple tasks (ZII-45 finished in 2 min and would
  otherwise be over-estimated by ~50s). Mitigation: the
  `elapsed_min < 3` early-return guarantees we don't show a
  timestamp for short tasks at all.
- **Trade-off**: clock-time timestamps anchor the user to absolute
  time, which can read as overconfident even with `~`. Mitigation:
  the prefix tilde + the medium-confidence band keep the
  uncertainty marker visible.

## Migration Plan

- All wire-format changes are additive; older dashboards ignore
  the new fields and keep showing the old `phase_label` cell.
- The new dashboard cell falls back gracefully:
  - empty `agent_stage` → render the existing `phase_label` cell
    unchanged
  - empty `eta_completion_at` AND `eta_confidence == "low"` → show
    the elapsed text, no timestamp
- The classifier's per-run state additions are in-memory only;
  no on-disk migration.
