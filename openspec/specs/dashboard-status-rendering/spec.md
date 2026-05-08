# dashboard-status-rendering Specification

## Purpose
TBD - created by archiving change improve-dashboard-liveness. Update Purpose after archive.
## Requirements
### Requirement: Sessions table SHALL render a phase label, not a phase number

The React `SessionsTable` (`packages/dashboard/src/components/SessionsTable.tsx`)
SHALL render `running.phase_label` from the snapshot payload in the
phase column. When `phase_label` is empty (older server), it SHALL
fall back to a client-side mapping table keyed on `running.phase` so
existing deployments don't show a bare integer.

#### Scenario: Server provides label

- GIVEN `running.phase_label == "AgentRunning · turn 3"`
- WHEN the row renders
- THEN the phase cell shows the string literally.

#### Scenario: Server provides only the integer

- GIVEN `running.phase_label == ""` and `running.phase == 4`
- WHEN the row renders
- THEN the phase cell shows the client-side mapping for `4`
  (e.g. `"AgentRunning"`); never the bare number `4`.

### Requirement: Sessions table SHALL render a "Last activity" indicator with traffic-light dot

The sessions table SHALL include a column whose content for each row is:

- a colored dot (green / yellow / red) reflecting the age of
  `last_activity_at` relative to the wall clock,
- followed by a relative-time string (`8s ago`, `2m ago`),
- followed by `last_activity_kind` in subdued text.

Color thresholds:
| age | dot |
|---|---|
| `< 30 s` | green |
| `30 – 180 s` | yellow |
| `> 180 s` | red |
| `last_activity_at == ""` | gray (no data yet) |

#### Scenario: Recently active

- GIVEN `last_activity_at` is 8 s ago and `last_activity_kind == "tool_call"`
- WHEN the row renders
- THEN it shows: green dot, `8s ago`, `tool_call`.

#### Scenario: Stale activity warning

- GIVEN `last_activity_at` is 4 minutes ago
- WHEN the row renders
- THEN the dot is red and the indicator includes the relative time.

### Requirement: Sessions table SHALL render a diff summary column

The sessions table SHALL include a `Diff` column whose content per row is:

- `+{diff_added} -{diff_removed} ({diff_files} files)` when
  `diff_status == "ok"` and any of the three counts is non-zero.
- `—` when all three counts are zero and status is `"ok"`.
- `?` (with a tooltip carrying the `diff_status` string) for
  `"timeout"` or `"error"`.

#### Scenario: Active edits visible

- GIVEN `diff_added=47, diff_removed=3, diff_files=2, diff_status="ok"`
- WHEN the row renders
- THEN the diff cell shows `+47 -3 (2 files)`.

#### Scenario: Quiet workspace

- GIVEN `diff_added=0, diff_removed=0, diff_files=0, diff_status="ok"`
- WHEN the row renders
- THEN the diff cell shows `—`.

#### Scenario: Diff probe failed

- GIVEN `diff_status="timeout"`
- WHEN the row renders
- THEN the diff cell shows `?` with a hover tooltip that contains
  the word `timeout`.

### Requirement: Sessions table SHALL render an iteration progress badge

When `iteration_max > 0`, the row SHALL display a small badge with
`iter {iteration}/{iteration_max}`. When `iteration_max == 0` no
badge is displayed (avoids `0/0` noise on non-omx runners).

#### Scenario: Badge shown

- GIVEN `iteration=3, iteration_max=50`
- WHEN the row renders
- THEN a badge with text `iter 3/50` is visible.

#### Scenario: Badge hidden

- GIVEN `iteration_max=0`
- WHEN the row renders
- THEN no iteration badge is in the DOM.

### Requirement: Dashboard SHALL render a "Queue" panel for blocked-by skips

The dashboard SHALL include a panel (component file
`packages/dashboard/src/components/QueuePanel.tsx`) that subscribes
to the SSE `event: queue` channel and renders one row per recently
skipped issue. Each row shows:

- the issue identifier,
- a short label `blocked by`,
- the comma-separated unresolved blockers from the event payload.

The panel keeps each row visible for at least 5 seconds *after the
most recent skip event for that issue* and then removes it
(eventual eviction; the gate keeps re-emitting on every poll cycle,
so an actively-blocked issue stays in the panel continuously).

#### Scenario: Single blocker rendered

- GIVEN a `queue` SSE event with payload
  `{ issue_id: "...", identifier: "ZII-50", blockers: "ZII-49" }`
- WHEN the panel processes the event
- THEN a row is visible with text containing both `ZII-50` and
  `ZII-49`.

#### Scenario: Multiple blockers rendered

- GIVEN payload `{ identifier: "ZII-52", blockers: "ZII-49,ZII-50" }`
- WHEN the panel processes the event
- THEN the row contains `ZII-49` and `ZII-50`, displayed as a
  comma-separated list.

#### Scenario: Stale entry expires

- GIVEN a row was last refreshed 6 seconds ago and no further skip
  events for that issue have arrived
- WHEN the panel re-renders
- THEN the row is no longer in the DOM.

### Requirement: Agent log stream SHALL exclude heartbeats and queue events

The dashboard's agent log component
(`packages/dashboard/src/components/AgentLogs.tsx`) SHALL not display
events whose type matches the heartbeat set (`team/stalled`) or whose
SSE channel is `queue`. Both classes are surfaced through dedicated
UI affordances (the activity dot, the queue panel) and would
otherwise pollute the log view.

#### Scenario: Heartbeat hidden

- GIVEN a `team/stalled` agent event arrives
- WHEN the agent log re-renders
- THEN no log entry for it appears.

#### Scenario: Real agent events still shown

- GIVEN a `tool_call` agent event arrives
- WHEN the agent log re-renders
- THEN a corresponding entry appears.

