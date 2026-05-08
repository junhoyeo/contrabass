# Dashboard status rendering — stage pill + completion timestamp

## ADDED Requirements

### Requirement: Sessions table SHALL render a five-step stage pill instead of the bare phase label cell

The React `SessionsTable`
(`packages/dashboard/src/components/SessionsTable.tsx`) SHALL render
each running issue's `agent_stage` and `agent_stage_step` as a
five-step pill positioned where the previous bare `phase_label`
text used to be. The pill consists of five small box elements in a
row; the box at index `agent_stage_step - 1` is the current step
(filled with the accent color), boxes at lower indices are filled
in a muted color, boxes at higher indices are outlined only. The
human-readable stage name (`agent_stage`) appears above the pill
in subdued type.

When `agent_stage` is empty, the pill SHALL fall back to the
existing `phase_label` text rendering. This keeps the cell useful
during the very first poll cycle of a new run.

#### Scenario: Pill renders with current step highlighted

- GIVEN a row with `agent_stage_step == 3` and
  `agent_stage == "Testing"`
- WHEN the row renders
- THEN the cell contains 5 box elements with the third one
  highlighted, and the text `Testing` appears above the boxes.

#### Scenario: Pill never moves backwards across rerenders

- GIVEN a row that previously rendered with
  `agent_stage_step == 4`
- AND the next snapshot tick arrives with `agent_stage_step == 4`
  (server-side monotonic clamp guarantees this never decreases)
- WHEN the row rerenders
- THEN the highlighted box is still index 3 (4th of 5).

#### Scenario: Empty agent_stage falls back to phase_label

- GIVEN `agent_stage == ""` and `phase_label == "AgentRunning"`
- WHEN the row renders
- THEN the cell renders the text `AgentRunning` (not five boxes).

### Requirement: Sessions table SHALL render a "Done by" cell

The sessions table SHALL include a `Done by` column whose content
per row is one of:

| input | display |
|---|---|
| `eta_confidence` ∈ {`medium`, `high`} AND `eta_completion_at` non-empty | `~HH:MM` formatted from the RFC3339 timestamp in the user's local timezone |
| `eta_confidence == "low"` AND elapsed ≥ 60 s | `elapsed Xm, normal` where `X` is the elapsed minutes (rounded to nearest integer ≥ 1) |
| elapsed `< 60 s` | `—` (em-dash) |
| The run has finished (no longer in `Running`) | column is empty (the row is gone anyway) |

The `~` prefix is mandatory — it is the visible uncertainty marker
that distinguishes an estimate from a hard deadline.

#### Scenario: Medium-confidence ETA renders as ~HH:MM

- GIVEN `eta_completion_at == "2026-05-07T07:32:00+08:00"`,
  `eta_confidence == "medium"`, and the user's locale is `Asia/Shanghai`
- WHEN the row renders
- THEN the cell shows literal text `~07:32`.

#### Scenario: Low-confidence falls back to elapsed text

- GIVEN `eta_confidence == "low"`, `eta_completion_at == ""`,
  elapsed = 4 minutes
- WHEN the row renders
- THEN the cell shows `elapsed 4m, normal` (no shrinking timer, no
  countdown).

#### Scenario: Brand-new runs show em-dash

- GIVEN elapsed = 30 s
- WHEN the row renders
- THEN the cell shows `—`.

### Requirement: The dashboard SHALL NOT render any countdown or shrinking remaining-time number

The dashboard MUST NOT compute or display any value of the form
`X minutes remaining` or `X% complete` (other than what the
five-step pill represents). Specifically forbidden:

- A bare `(eta_completion_at - now)` countdown that decreases each
  poll tick.
- A continuous progress bar driven by elapsed/eta, tokens, or files
  changed.
- A percent-complete number anywhere in the running-issues table
  or its child components.

This is a hard UX rule grounded in the proposal: shrinking timers
under variance erode trust faster than they convey progress, and
percent bars lie about non-linear codex work. Any future change
that wants to add such an affordance MUST update this requirement
in the same change set with explicit justification.

#### Scenario: No countdown text in the rendered DOM

- GIVEN any running issue with any combination of stage / ETA
  fields
- WHEN the row renders
- THEN the rendered DOM contains zero substrings matching
  `/\d+\s?(min|m|sec|s) remaining/i` and zero substrings matching
  `/\d{1,3}%/`.

#### Scenario: No progress bar element

- GIVEN any running issue
- WHEN the row renders
- THEN the rendered DOM contains zero `<progress>` elements and
  zero elements with role="progressbar" inside the running-issues
  table.
