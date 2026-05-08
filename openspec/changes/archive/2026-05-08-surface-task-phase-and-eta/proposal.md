# Surface task phase and completion estimate

## Why

Today the dashboard shows two time signals per running issue: `started_at`
(elapsed since claim) and `last_activity_at` (recency of the last event).
Neither answers the question users actually have at minute 11 of a
24-minute codex run: **when can I move on to something else?**

The naïve answer would be a "remaining minutes" countdown or a percent
progress bar. Both are wrong for codex tasks specifically:

- **Linear "remaining time" misleads under high variance.** Live
  evidence from this project: ZII-45 finished in 2 min, ZII-44 took
  18 min, ZII-47 took 15 min, ZII-55 has crossed 11 min and is still
  in mid-edit. Any `total / rate * (1 - done)` extrapolation produces
  an ETA that swings by ±5 min between consecutive polls. A swinging
  number actively erodes user trust.

- **Linear progress bars lie about non-linear work.** codex runs
  spend ~30–50% of total time in the post-edit "review + run tests"
  loop, where files-changed plateaus but tokens keep flowing. A
  bar driven by file count or token count looks 80% complete during
  the cheap phase and stalls at 95% during the expensive one.

The accurate, low-anxiety answer for tasks longer than ~10 minutes
is a pair of signals that **acknowledge uncertainty**:

1. **Stage label** — what codex is doing right now
   (`Exploration` / `Editing` / `Testing` / `Reviewing` / `Wrapping`),
   driven by event-stream rules, not estimates. Never moves
   backwards. A 5-step pill beats a 0–100% bar because it carries
   information about *which slow step we're in*, not just how
   "done" we are.

2. **Completion timestamp** — `~06:32` (with the tilde) when
   confidence is reasonable, plain `elapsed 11m, normal range`
   when the run is too young or volatile to estimate. Anchored to
   the wall clock, not relative — so users can plan around it.
   The timestamp never **shrinks then grows**, which is what makes
   "remaining time" countdowns feel broken.

## What Changes

Producer side (`internal/orchestrator/snapshot.go`):

- Add a stage classifier `classifyAgentStage(...)` that maps
  `(last_activity_kind, recent_diff_delta, elapsed)` to one of the
  five stage strings + a numeric step (1–5). Rules-only, no
  heuristics that depend on historical data.
- Add a completion-time estimator `estimateCompletionAt(...)` that
  returns `(rfc3339_string, confidence)` where `confidence` ∈
  `{"low", "medium", "high"}`. Below "medium" the function returns
  empty timestamp, signaling the dashboard to hide the clock.
- Surface 4 new additive fields on `RunningIssue`:
  `agent_stage`, `agent_stage_step`, `eta_completion_at`,
  `eta_confidence`.

Consumer side (`packages/dashboard/src/components/SessionsTable.tsx`):

- Replace the bare `phase_label` cell with a 5-step stage pill that
  highlights the current stage and shows the stage name above the
  pill.
- Add a "Done by" cell that renders one of:
  - `~HH:MM` when `eta_confidence ∈ {medium, high}`
  - `elapsed Xm, normal` when low confidence
  - `—` when `eta_completion_at` is empty (very early or no rate
    data)
- Existing `Last activity` and `Diff` columns stay unchanged.

## Impact

- Affected capabilities: `web-state-payload` (extends existing
  liveness fields), `dashboard-status-rendering` (extends existing
  phase/diff/iteration rendering), both first defined in
  `improve-dashboard-liveness`.
- Affected code:
  - `internal/orchestrator/snapshot.go` (classifier + estimator +
    snapshot wiring)
  - `internal/orchestrator/snapshot_test.go`
  - `internal/types/types.go` (4 new fields on `RunningIssue`)
  - `packages/dashboard/src/components/SessionsTable.tsx`
  - `packages/dashboard/src/components/SessionsTable.test.tsx`
  - `packages/dashboard/src/i18n/messages.ts` for the stage names
- Out of scope:
  - Reference-class forecasting (using past-run medians as a Bayesian
    prior). The five-step pill plus current-run rate is enough; a
    cross-run prior is a separate change once we have a few dozen
    completed runs persisted.
  - Sub-stage breakdowns inside `Testing` / `Reviewing` (which exact
    test command, how many failures so far). Useful, but a separate
    change.
- Not addressed: stage detection for runners other than codex
  (omx, opencode, etc.). Their event vocabulary differs and is
  covered by their own follow-ups.
