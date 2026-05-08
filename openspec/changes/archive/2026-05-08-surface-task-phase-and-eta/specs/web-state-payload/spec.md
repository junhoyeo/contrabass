# Web state payload — agent stage + completion estimate

## ADDED Requirements

### Requirement: Snapshot `Running` entries SHALL carry agent stage classification

Each `OrchestratorSnapshot.Running[i]` entry SHALL include two
additive fields describing what the codex agent is doing inside
the orchestrator's `AgentRunning` phase:

| field | type | zero | semantics |
|---|---|---|---|
| `agent_stage` | string | `""` | One of `Exploration` / `Editing` / `Testing` / `Reviewing` / `Wrapping`, derived per design.md Decision 1. Empty when no rule has yet fired (very early in a run) |
| `agent_stage_step` | int | `0` | 1–5 corresponding to the five stages in order. `0` when stage is empty. Monotonically non-decreasing across snapshot ticks for the same run |

#### Scenario: Stage 1 (Exploration) before any diff

- GIVEN a freshly claimed run with `diff_added=0`, `diff_removed=0`,
  `last_activity_kind="item/agentMessage"`, elapsed = 30 s
- WHEN the snapshotter runs
- THEN `agent_stage == "Exploration"` and `agent_stage_step == 1`.

#### Scenario: Stage 2 (Editing) once diff grows

- GIVEN a run whose `diff_added` increased from 12 to 47 between
  the previous snapshot tick and this one
- WHEN the snapshotter runs
- THEN `agent_stage == "Editing"` and `agent_stage_step == 2`.

#### Scenario: Stage 3 (Testing) when diff plateaus and tokens flow

- GIVEN a run whose `diff_added` and `diff_removed` have not changed
  for 35 seconds, `tokens_per_min ≈ 100k`,
  `last_activity_kind="hook/started"`
- WHEN the snapshotter runs
- THEN `agent_stage == "Testing"` and `agent_stage_step == 3`.

#### Scenario: Stage 4 (Reviewing) when diff plateaus and tokens slow

- GIVEN a run whose `diff_added`/`removed` have been static for 90 s
  AND `tokens_per_min ≈ 20k`
- WHEN the snapshotter runs
- THEN `agent_stage == "Reviewing"` and `agent_stage_step == 4`.

#### Scenario: Stage 5 (Wrapping) on terminal turn event

- GIVEN `last_activity_kind == "turn/completed"`
- WHEN the snapshotter runs
- THEN `agent_stage == "Wrapping"` and `agent_stage_step == 5`.

#### Scenario: Stage step never decreases

- GIVEN a run that has previously been classified as
  `agent_stage_step == 4`
- AND a late-arriving `turn/diff/updated` raises `diff_added` again
- WHEN the snapshotter re-runs
- THEN `agent_stage_step` stays `4`; it is NOT reset to `2`.

### Requirement: Snapshot `Running` entries SHALL carry a completion-time estimate with confidence

Each running entry SHALL include two additive fields describing the
estimated completion clock time and the runner's confidence in it:

| field | type | zero | semantics |
|---|---|---|---|
| `eta_completion_at` | string | `""` | Estimated wall-clock completion time in RFC3339. Empty whenever `eta_confidence == "low"` |
| `eta_confidence` | string | `"low"` | One of `low` / `medium` / `high` per design.md Decision 3 |

#### Scenario: Confidence is low for the first 3 minutes

- GIVEN a run whose elapsed time is 90 s
- WHEN the snapshotter runs
- THEN `eta_confidence == "low"` AND `eta_completion_at == ""`.

#### Scenario: Medium confidence after warm-up

- GIVEN a run whose elapsed time is 6 minutes, `agent_stage_step == 2`,
  and rate metrics fit the heuristic
- WHEN the snapshotter runs
- THEN `eta_confidence == "medium"` AND `eta_completion_at` is a
  non-empty RFC3339 timestamp strictly after `started_at`.

#### Scenario: High confidence late in the run

- GIVEN a run whose elapsed is 12 min and `agent_stage_step >= 3`
- WHEN the snapshotter runs
- THEN `eta_confidence == "high"` AND `eta_completion_at` is
  populated.

#### Scenario: Quiet run downgrades to low confidence

- GIVEN a run whose token rate has dropped below 1000/min
  (`files_per_min < 0.05` AND `tokens_per_min < 1000`)
- WHEN the snapshotter runs
- THEN `eta_confidence == "low"` AND `eta_completion_at == ""`,
  regardless of elapsed time. (Quiet means we cannot estimate.)

#### Scenario: Backwards compatibility — old consumers ignore new fields

- GIVEN an external consumer that decodes only the existing
  `Running` JSON shape
- WHEN it decodes a snapshot from the upgraded server
- THEN decoding succeeds and the new fields are silently ignored.
