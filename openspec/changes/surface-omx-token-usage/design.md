# Design: Surface omx token usage

## Context

`teamCLIRunner` already runs a polling loop in `monitorProcess` (every
`pollInterval`, default 1.5 s for omx) that calls `omx team api ...` for
state. It is the natural place to also read `metrics.json` because:

- It already runs at the right cadence.
- It has the workspace path (`proc.workspace`).
- Its `emit()` closure is the single channel through which all agent
  events flow into the orchestrator.

omx writes the file in `<workspace>/.omx/metrics.json`. The `notify-hook`
uses cumulative session totals (`session_input_tokens`,
`session_output_tokens`, `session_total_tokens`). `parseUsageTokens` in
the orchestrator already handles cumulative values via delta accounting
(`if tokensIn > entry.attempt.TokensIn { delta := tokensIn - entry.attempt.TokensIn ... }`),
so feeding the cumulative value directly is correct.

## Goals / Non-Goals

**Goals**
- Producer-side change only. Zero modifications to orchestrator, hub, web
  server, or frontend.
- Throttle emissions: at most one event per metrics-change boundary.
- Best-effort: missing/partial/old `metrics.json` must never crash the
  runner.
- Keep the existing event vocabulary; reuse the `usage` map shape.

**Non-Goals**
- 5-hour / weekly quota fields (deferred).
- Switching to fsnotify or any push-based file watcher.
- Token tracking for non-omx runners.

## Decisions

### Decision 1: Use polling, not fsnotify

Reading the file every poll cycle is bounded by `pollInterval` (1.5 s).
The bottleneck is omx's per-turn write cadence (~30–60 s); a 1.5 s polling
worst case is invisible in practice. fsnotify would add a watcher
lifecycle (registration, cleanup, restart on workspace mutation) that
this change does not need.

### Decision 2: Cumulative values stored on `teamCLIProcess`, deltas computed against previous emit

`teamCLIProcess` gains a `lastEmittedUsage` field (input/output/total
ints + bool `seen`). Emit only when any value strictly increased relative
to the last emit; otherwise skip. This matches `parseUsageTokens`'
"delta from previous attempt total" model exactly.

### Decision 3: Helper returns `(*OmxMetrics, error)`; missing file is `(nil, nil)`

A missing file is the normal state for non-omx runners and the early
moments of an omx run before its first turn ends. The helper must not
log spam or surface an error in that case. Parse failures (corrupt JSON
mid-write) return `(nil, err)` and the caller logs at debug only.

### Decision 4: Event shape mirrors codex-app-server

```go
emit("session.usage", map[string]interface{}{
    "team_name": proc.teamName,
    "usage": map[string]interface{}{
        "input_tokens":  metrics.SessionInputTokens,
        "output_tokens": metrics.SessionOutputTokens,
        "total_tokens":  metrics.SessionTotalTokens,
    },
})
```

`parseUsageTokens` reads `event.Data["usage"]` with the keys above. By
matching exactly, no orchestrator changes are required.

## Risks / Trade-offs

- **Risk**: omx changes its metrics schema in a future release. Helper
  must tolerate missing fields by returning zero — verified in tests.
- **Risk**: Concurrent write/read race on `metrics.json` produces
  partial JSON. Helper must catch the parse error and skip the cycle.
- **Trade-off**: Polling instead of fsnotify gives a fixed worst-case
  latency of `pollInterval`. Acceptable given omx's update cadence.

## Migration Plan

No schema or API changes. Deploy as a normal contrabass binary upgrade.
Dashboards already wired show non-zero counts on the next omx run.
