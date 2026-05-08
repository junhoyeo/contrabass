# Tasks: Surface task phase and completion estimate

## T1 — Add 4 wire-format fields to `RunningIssue`

**Files**: `internal/types/types.go`.

**Contract**: Add 4 additive fields to the `RunningIssue` struct
(or wherever the snapshot payload entry lives — verify with `grep
-n RunningIssue internal/`):

```go
AgentStage       string `json:"agent_stage"`
AgentStageStep   int    `json:"agent_stage_step"`
ETACompletionAt  string `json:"eta_completion_at,omitempty"`
ETAConfidence    string `json:"eta_confidence,omitempty"`
```

**Acceptance**:
- Field names + JSON tags exact.
- `omitempty` on the two ETA fields keeps the wire shape unchanged
  when ETA is suppressed.
- `go build ./...` clean.

**Depends on**: none.
**Blocks**: T2, T3, T4.

---

## T2 — Implement stage classifier `classifyAgentStage`

**Files**: `internal/orchestrator/snapshot.go` (production only).

**Contract**: Add a per-run state struct + helper:

```go
// agentStageState carries the rolling state classifyAgentStage needs
// across snapshot ticks.
type agentStageState struct {
    LastDiffAdded   int
    LastDiffRemoved int
    LastDiffChange  time.Time
    PrevStep        int
}

// classifyAgentStage returns (stage, step) per the rules in
// design.md Decision 1, then applies the monotonic clamp from
// Decision 2: step is max(state.PrevStep, computed).
func classifyAgentStage(
    state *agentStageState,
    lastActivityKind string,
    diffAdded, diffRemoved int,
    tokensPerMin float64,
    now time.Time,
) (string, int)
```

Stage strings exactly: `"Exploration"`, `"Editing"`, `"Testing"`,
`"Reviewing"`, `"Wrapping"`. Step values 1, 2, 3, 4, 5.

The function SHALL update `state` in place at the end of each call
so the next call sees the latest diff snapshot.

**Acceptance**:
- All five rules from design.md Decision 1 implemented in the
  documented priority order.
- Monotonic clamp applied: returned step ≥ `state.PrevStep`.
- No call site allocates a new state per tick — caller owns the
  per-run struct.
- `go vet ./...` clean.

**Depends on**: T1.
**Blocks**: T5.

---

## T3 — Implement completion-time estimator `estimateCompletionAt`

**Files**: `internal/orchestrator/snapshot.go`.

**Contract**:

```go
// estimateCompletionAt returns (rfc3339, confidence) per design.md
// Decision 3. Empty rfc3339 means "do not display a timestamp"; the
// confidence string is still set so the dashboard can show the
// elapsed-text fallback.
func estimateCompletionAt(
    startedAt time.Time,
    now time.Time,
    diffFiles int,
    tokensIn, tokensOut int64,
    stageStep int,
    estimatedTotalFiles int,
) (string, string)
```

Confidence values: `"low"`, `"medium"`, `"high"`. The function
SHALL never return a non-empty rfc3339 with `confidence == "low"`.

Defaults inside the function:
- `estimatedTotalFiles` <= current files → use `max(currentFiles*1.2, 11)`
- review buffer: `linear_remaining * 1.35`

**Acceptance**:
- All four confidence-band rules from Decision 3 implemented.
- elapsed < 3 min returns `("", "low")`.
- `files_per_min < 0.05` OR `tokens_per_min < 1000` returns
  `("", "low")` regardless of elapsed.
- elapsed > 8 min AND stageStep ≥ 3 → confidence `"high"`.
- elapsed > 5 min otherwise → `"medium"`.
- Returned rfc3339 (when non-empty) parses cleanly with
  `time.Parse(time.RFC3339, ...)`.

**Depends on**: T1.
**Blocks**: T5.

---

## T4 — Wire stage + ETA into snapshot construction

**Files**: `internal/orchestrator/snapshot.go`,
`internal/orchestrator/state.go` (the per-run state map — verify
where to attach `agentStageState`).

**Contract**: Inside the snapshot loop, for each running issue:

1. Look up or create the per-run `agentStageState` keyed on
   `issue.ID`. The orchestrator already has a `running` map; add
   the field there.
2. Compute `tokensPerMin` from `(tokens_in + tokens_out)` and
   `elapsed`.
3. Call `stage, step := classifyAgentStage(state, …)` and assign
   to the `RunningIssue` entry (T1 fields).
4. Call `etaAt, conf := estimateCompletionAt(…)` and assign.

The state struct MUST be cleaned up on run release (existing
`o.completeRun` / `o.deleteRun` paths). Add the cleanup line in
the same change.

**Acceptance**:
- Every snapshot tick assigns all four T1 fields on every running
  entry.
- Per-run state survives across snapshot ticks until the run
  finishes.
- No state leak after a run is released
  (verified by counting `len(running)` after release in tests).

**Depends on**: T1, T2, T3.
**Blocks**: T5.

---

## T5 — Producer-side tests

**Files**: `internal/orchestrator/snapshot_test.go` (extend; the
file already exists from `improve-dashboard-liveness` T6).

**Contract**: Add the following table-driven tests:

1. `TestClassifyAgentStage_RuleTable` — one sub-test per stage
   transition rule listed in the spec scenarios. Each sub-test
   sets the previous step to `0` (so the monotonic clamp does not
   suppress the rule under test).

2. `TestClassifyAgentStage_MonotonicClamp` — feed a sequence of
   inputs that would naively return step 4 then step 2; assert the
   returned step is `4, 4`.

3. `TestEstimateCompletionAt_LowEarly` — elapsed = 90s → `("", "low")`.

4. `TestEstimateCompletionAt_LowQuiet` — elapsed = 10 min but
   tokensPerMin = 200 → `("", "low")`.

5. `TestEstimateCompletionAt_Medium` — elapsed = 6 min, healthy
   rate, stage = 2 → confidence "medium", non-empty rfc3339 in
   the future.

6. `TestEstimateCompletionAt_High` — elapsed = 12 min, stage = 4
   → confidence "high".

7. `TestSnapshot_StagePropagatesAcrossTicks` — drive the
   snapshotter over two ticks with synthetic state + diff; assert
   the second tick's snapshot entry has step ≥ first tick's.

8. `TestSnapshot_ReleasesAgentStageState` — start a run, advance
   one tick, release the run, advance another tick; assert
   `len(o.running)` is 0 (state cleaned up).

**Acceptance**:
- `go test ./internal/orchestrator/ -run "TestClassifyAgentStage|TestEstimateCompletionAt|TestSnapshot" -count=1 -race -v` passes.
- No pre-existing test in the package is modified.

**Depends on**: T2, T3, T4.
**Blocks**: T6, T7.

---

## T6 — Render the five-step stage pill in `SessionsTable`

**Files**:
- `packages/dashboard/src/components/SessionsTable.tsx`
- `packages/dashboard/src/components/SessionsTable.test.tsx`
- `packages/dashboard/src/i18n/messages.ts` (add Chinese stage
  labels: 探索 / 编辑 / 测试 / 复核 / 收尾, indexed in the same
  order as the English `agent_stage` strings).

**Contract**:

- Replace the cell currently rendering `running.phase_label` with
  a small `StagePill` sub-component (define inline in the same
  file unless it grows past ~50 lines).
- The pill renders 5 box elements in a horizontal row. Box
  `index === agent_stage_step - 1` gets the active style; lower
  indices get the filled-past style; higher indices get the
  outline-future style. Use Tailwind utility classes plus the
  shadcn `Badge` primitive for consistency with the rest of the
  neo-brutalism theme.
- The localized stage name (`zhCN.stages[step - 1]`) renders in
  subdued type on the line above the boxes.
- When `agent_stage === ""` and `agent_stage_step === 0`, fall
  back to rendering the existing `phase_label` text — no boxes.
- Tests cover: stage 1 → first box highlighted, stage 5 → last
  box highlighted, empty stage → fallback `phase_label` text
  visible.

**Acceptance**:
- `bun test packages/dashboard` passes.
- `pnpm run build` succeeds with no new TypeScript errors.
- No `console.log` left behind.

**Depends on**: T1 (server fields must exist), T5 (so the wire
contract is verified before the consumer ships).
**Blocks**: none.

---

## T7 — Render the "Done by" cell in `SessionsTable`

**Files**:
- `packages/dashboard/src/components/SessionsTable.tsx`
- `packages/dashboard/src/components/SessionsTable.test.tsx`
- `packages/dashboard/src/i18n/messages.ts` (add `doneBy` label
  and `elapsedNormal: '已运行 {minutes}m，正常'`).

**Contract**:

- Add a column titled "Done by" (i18n key `doneBy`) after the
  existing `Diff` column.
- Per row content rules from `dashboard-status-rendering` spec:
  - `eta_completion_at` non-empty AND `eta_confidence ∈
    {medium, high}` → render `~HH:MM` in user's locale TZ.
  - `eta_confidence === "low"` AND elapsed ≥ 60s → render the
    localized `elapsedNormal` template.
  - elapsed < 60s → render `—`.
- Implement the time format with the existing `format.ts` helper
  module rather than new date libraries.
- The literal `~` prefix MUST be present in the medium/high
  branch.
- Tests cover: medium ETA produces `~HH:MM`; low confidence
  produces the elapsed-fallback string; under-60s shows the dash;
  no rendered substring matches `/\d+\s?(min|m|sec|s) remaining/i`
  in any of the three rendered states.

**Acceptance**:
- `bun test packages/dashboard` passes.
- `pnpm run build` succeeds.
- The rejected-form regex test (no countdown text) is part of
  the test file as a visible assertion.

**Depends on**: T1, T5.
**Blocks**: none.

---

## Rejection rules (apply to ALL tasks)

A diff that satisfies any of the following MUST be rejected:

1. The diff modifies only `*_test.go` / `*.test.tsx` files for
   T1, T2, T3, T4, T6, T7 (production code is mandatory).
2. The diff modifies any file outside `internal/types/**`,
   `internal/orchestrator/**`, or `packages/dashboard/**`.
3. The diff adds a new Go module dependency or new npm package.
   Stdlib + already-installed shadcn/Tailwind primitives only.
4. The diff introduces any "remaining time" countdown, percent
   bar, or `<progress>` element in `packages/dashboard/**`. The
   dashboard-status-rendering spec forbids these explicitly.
5. The diff omits any acceptance bullet from the task it claims
   to implement.

## Task graph

```
T1 ──┬── T2 ─┐
     ├── T3 ─┼── T4 ── T5 ──┬── T6
     │      │              └── T7
     └────────────────────────┘ (T6/T7 also depend on T1 directly)
```

T2, T3 may proceed in parallel after T1. T4 follows both. T5
follows T4. T6 and T7 may run in parallel after T5 lands.
