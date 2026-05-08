# Team Runtime — token usage propagation

## ADDED Requirements

### Requirement: Team CLI runner SHALL surface omx token totals to the orchestrator

`teamCLIRunner` SHALL read the omx-maintained metrics file at
`<workspace>/.omx/metrics.json` on every poll cycle of `monitorProcess`
and emit a `session.usage` agent event whenever any of the cumulative
token totals strictly increase since the last emission.

#### Scenario: First successful read produces an event

- GIVEN a workspace whose `.omx/metrics.json` contains
  `{"session_input_tokens": 1234, "session_output_tokens": 56,
  "session_total_tokens": 1290}`
- AND `monitorProcess` has not previously emitted `session.usage` for
  this run
- WHEN the next poll cycle reads the file
- THEN exactly one `session.usage` event is emitted whose
  `Data["usage"]` map contains
  `input_tokens=1234`, `output_tokens=56`, `total_tokens=1290`.

#### Scenario: Unchanged metrics suppress further events

- GIVEN one `session.usage` event has already been emitted with
  `total_tokens=1290`
- AND the next poll cycle reads `metrics.json` whose
  `session_total_tokens` is still `1290`
- WHEN the cycle completes
- THEN no further `session.usage` event is emitted.

#### Scenario: Increasing metrics produce a new event

- GIVEN the previously emitted total was `1290`
- AND the next poll cycle reads `session_total_tokens: 2500`
- WHEN the cycle completes
- THEN exactly one new `session.usage` event is emitted with
  the updated cumulative values.

#### Scenario: Missing metrics file is silently tolerated

- GIVEN `<workspace>/.omx/metrics.json` does not exist (e.g. the runner
  is opencode/omc, or the omx team has not finished its first turn)
- WHEN the poll cycle attempts to read the file
- THEN no error is propagated, no log line above debug is produced,
  and no `session.usage` event is emitted.

#### Scenario: Partial / corrupt JSON is silently tolerated

- GIVEN the metrics file is mid-write and JSON parsing fails
- WHEN the poll cycle attempts to read the file
- THEN the runner does not crash, no `session.usage` event is emitted
  for that cycle, and the next successful read still emits.

#### Scenario: Cumulative totals match orchestrator delta accounting

- GIVEN `parseUsageTokens` in the orchestrator credits
  `tokensIn - entry.attempt.TokensIn` as the delta
- WHEN this runner emits cumulative values
- THEN `parseUsageTokens` records correct deltas and the dashboard
  `Stats.TotalTokensIn` / `TotalTokensOut` advance monotonically.
