# Web state payload — liveness fields

## ADDED Requirements

### Requirement: Snapshot `Running` entries SHALL carry liveness fields

The `OrchestratorSnapshot.Running[i]` entry returned by the
`internal/orchestrator` package and serialized by `internal/web` at
`GET /api/v1/state` SHALL include the following additive fields. All
fields are required; runners that cannot compute a value supply the
indicated zero value.

| field | type | zero | semantics |
|---|---|---|---|
| `phase_label` | string | `""` | Human label for `phase`, taken from the `phase int → string` table next to the `Phase` enum |
| `last_activity_at` | RFC3339 string | `""` | Wall-clock time of the most recent **non-heartbeat** agent event for this run |
| `last_activity_kind` | string | `""` | Type of that event (e.g. `tool_call`, `turn/started`) |
| `last_heartbeat_at` | RFC3339 string | `""` | Wall-clock time of the most recent agent event of any kind, including `team/stalled` |
| `iteration` | int | `0` | Current iteration index from `.omx/state/run-state.json` if present, else `0` |
| `iteration_max` | int | `0` | `max_iterations` from the same file, else `0` |
| `diff_added` | int | `0` | Lines added in the workspace as reported by `git diff --stat HEAD`. `0` on timeout or no changes |
| `diff_removed` | int | `0` | Lines removed |
| `diff_files` | int | `0` | File count |
| `diff_status` | string | `"ok"` | `"ok"` for a successful diff, `"timeout"` for the 1-second deadline, `"error"` for any other failure (the dashboard renders `?` for non-`ok`) |

#### Scenario: Successful initial poll populates all liveness fields

- GIVEN a running issue whose runner has emitted a `turn/started`
  event 4 seconds ago
- AND the workspace has 47 added / 3 removed lines vs `HEAD` across 2 files
- AND `.omx/state/run-state.json` contains `{ "iteration": 0, "max_iterations": 50 }`
- WHEN the snapshotter assembles the `Running` entry
- THEN `phase_label != ""`, `last_activity_at` is within 5 seconds of
  now, `last_activity_kind == "turn/started"`, `last_heartbeat_at`
  is the same as `last_activity_at` (no heartbeats yet),
  `iteration == 0`, `iteration_max == 50`, `diff_added == 47`,
  `diff_removed == 3`, `diff_files == 2`, `diff_status == "ok"`.

#### Scenario: `team/stalled` does not advance `last_activity_at`

- GIVEN `last_activity_at` was set 30 seconds ago by a `tool_call`
  event
- AND four `team/stalled` heartbeats arrive in the next 30 seconds
- WHEN the next snapshot is taken
- THEN `last_activity_at` is still the original 30-seconds-ago timestamp
- AND `last_activity_kind` is still `"tool_call"`
- AND `last_heartbeat_at` advances on each stalled event.

#### Scenario: Runner with no `.omx` directory leaves iteration fields zero

- GIVEN a codex runner whose workspace contains no `.omx/state/`
- WHEN the snapshotter runs
- THEN `iteration == 0` and `iteration_max == 0`, no error is logged,
  and the rest of the fields are populated normally.

#### Scenario: Diff timeout reports `timeout` status with zero counts

- GIVEN a workspace large enough that `git diff --stat HEAD` exceeds
  the 1-second budget
- WHEN the snapshotter assembles the entry
- THEN `diff_added == 0`, `diff_removed == 0`, `diff_files == 0`,
  `diff_status == "timeout"`, and the snapshot itself completes (no
  panic, no fatal log).

#### Scenario: Snapshot serialization is backward-compatible

- GIVEN an external consumer that decodes the existing
  `Running` JSON into a struct that knows only the original
  `{issue_id, attempt, pid, session_id, workspace, started_at,
  phase, tokens_in, tokens_out}` fields
- WHEN the consumer decodes a snapshot from the upgraded server
- THEN decoding succeeds and the consumer ignores the new fields.

### Requirement: SSE event stream SHALL filter heartbeat events to dashboard subscribers

The HTTP SSE endpoint at `GET /api/v1/events` SHALL drop agent events
whose type is in the heartbeat set (currently `{"team/stalled"}`)
before encoding to the wire. The orchestrator event hub SHALL still
deliver these events to non-SSE subscribers (TUI bridge, IPC, tests)
unchanged.

#### Scenario: TUI bridge keeps receiving heartbeat events

- GIVEN both an SSE client and the TUI bridge are subscribed
- WHEN the runner emits a `team/stalled` event
- THEN the TUI bridge receives it and the SSE client does not.

#### Scenario: Non-heartbeat events still flow to SSE

- GIVEN an SSE client is subscribed
- WHEN the runner emits `tool_call`
- THEN the SSE client receives it within the normal flush window.

### Requirement: SSE event stream SHALL re-classify `dispatch_skipped_blocked_by` events

The SSE endpoint SHALL forward `dispatch_skipped_blocked_by` events
under a distinct event channel (e.g. `event: queue` instead of
`event: agent`) so dashboard consumers can route them to the Queue
panel without keyword-matching the payload. The underlying
`OrchestratorEvent` payload is unchanged; only the SSE `event:` line
differs.

#### Scenario: Skip events arrive on the queue channel

- GIVEN a candidate is gated by `dispatchUnclaimedIssues`
- WHEN the orchestrator logs `dispatch_skipped_blocked_by`
- THEN the SSE client receives one frame whose `event:` line is
  `queue` and whose data carries the original
  `{ issue_id, blockers }` payload.
