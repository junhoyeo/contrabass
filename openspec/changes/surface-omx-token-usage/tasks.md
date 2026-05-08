# Tasks: Surface omx token usage

## T1 — Add `OmxMetrics` type in `internal/agent/teamcli.go`

**Files**: `internal/agent/teamcli.go` (production only — no test changes here)

**Contract**:

```go
// OmxMetrics mirrors the cumulative session token / quota counters that
// omx writes to <workspace>/.omx/metrics.json after every codex turn.
// Fields are intentionally lenient (omx may add or rename keys); JSON
// tags use snake_case to match omx's notify-hook output.
type OmxMetrics struct {
    SessionInputTokens  int64   `json:"session_input_tokens"`
    SessionOutputTokens int64   `json:"session_output_tokens"`
    SessionTotalTokens  int64   `json:"session_total_tokens"`
    FiveHourLimitPct    float64 `json:"five_hour_limit_pct"`
    WeeklyLimitPct      float64 `json:"weekly_limit_pct"`
}
```

**Acceptance**:
- Type added with JSON tags exactly as above.
- No other production-code change.
- `go build ./...` clean.

**Depends on**: none.
**Blocks**: T2, T4.

---

## T2 — Add `readOmxMetrics(workspace string) (*OmxMetrics, error)` helper

**Files**: `internal/agent/teamcli.go`

**Contract**:

```go
// readOmxMetrics reads <workspace>/.omx/metrics.json and returns the
// parsed OmxMetrics. Missing file returns (nil, nil) so callers can
// poll without log spam; partial / unparseable JSON returns (nil, err)
// and the caller should skip the cycle.
func readOmxMetrics(workspace string) (*OmxMetrics, error)
```

**Acceptance**:
- Function returns `(*OmxMetrics, nil)` for a well-formed file.
- Returns `(nil, nil)` when the file is absent (`os.IsNotExist`).
- Returns `(nil, err)` for parse failures.
- Uses stdlib only (`os` + `encoding/json`).

**Depends on**: T1.
**Blocks**: T3, T4.

---

## T3 — Emit `session.usage` event in `monitorProcess` poll loop

**Files**: `internal/agent/teamcli.go`

**Contract**:
- New field on `teamCLIProcess`:
  ```go
  lastUsage struct {
      input  int64
      output int64
      total  int64
      seen   bool
  }
  ```
- Inside `monitorProcess` poll loop (after `handleSnapshot`):
  ```go
  if metrics, err := readOmxMetrics(proc.workspace); err == nil && metrics != nil {
      changed := !proc.lastUsage.seen ||
          metrics.SessionInputTokens  > proc.lastUsage.input  ||
          metrics.SessionOutputTokens > proc.lastUsage.output ||
          metrics.SessionTotalTokens  > proc.lastUsage.total
      if changed {
          emit("session.usage", map[string]interface{}{
              "team_name": proc.teamName,
              "usage": map[string]interface{}{
                  "input_tokens":  metrics.SessionInputTokens,
                  "output_tokens": metrics.SessionOutputTokens,
                  "total_tokens":  metrics.SessionTotalTokens,
              },
          })
          proc.lastUsage.input  = metrics.SessionInputTokens
          proc.lastUsage.output = metrics.SessionOutputTokens
          proc.lastUsage.total  = metrics.SessionTotalTokens
          proc.lastUsage.seen   = true
      }
  }
  ```
- No change to `handleSnapshot`, no change to other emit calls, no
  change to `parseUsageTokens` or anything in `internal/orchestrator/`.

**Acceptance**:
- Event fires once per change boundary, never twice for the same totals.
- Read errors logged at debug only (or silently dropped); never fatal.
- `go build ./...` and `go vet ./...` clean.

**Depends on**: T2.
**Blocks**: T5.

---

## T4 — Unit test `TestReadOmxMetrics`

**Files**: `internal/agent/teamcli_test.go`

**Contract**:
- Table-driven test covering at least:
  - well-formed file → expected struct values
  - missing file → `(nil, nil)`
  - corrupt JSON → `(nil, non-nil err)`
  - file with extra unknown fields → ignores them, parses known fields
- Uses `t.TempDir()`; no fixture files committed.

**Acceptance**:
- `go test ./internal/agent/ -run TestReadOmxMetrics -count=1 -v` passes.

**Depends on**: T1, T2.
**Blocks**: none.

---

## T5 — Unit test for `session.usage` emission semantics

**Files**: `internal/agent/teamcli_test.go`

**Contract**:
- Test name `TestMonitorProcess_EmitsSessionUsageOnMetricsChange`.
- Drive `monitorProcess` (or extract a small helper if needed) past two
  poll boundaries:
  - boundary 1: write metrics file with `total_tokens=1000` → assert
    exactly one `session.usage` event with `total_tokens=1000`.
  - boundary 2: same totals → assert no new emission.
  - boundary 3: write `total_tokens=2500` → assert exactly one new
    emission with the updated value.
- Must not modify any production code beyond what T1/T2/T3 already added.

**Acceptance**:
- `go test ./internal/agent/ -run TestMonitorProcess_EmitsSessionUsageOnMetricsChange -count=1 -v` passes.

**Depends on**: T3.
**Blocks**: none.

---

## Rejection rules (apply to ALL tasks)

A diff that satisfies any of the following MUST be rejected and the task
re-issued; do not commit:

1. The diff modifies only `*_test.go` or `packages/dashboard/**` files.
   Producer code in `internal/agent/teamcli.go` is mandatory for T1, T2,
   T3.
2. The diff modifies `internal/orchestrator/**` or `internal/web/**`.
   This change is producer-only; downstream is already wired.
3. The diff adds a new go module dependency. Use stdlib only.
4. The diff omits any acceptance bullet from the task it claims to
   implement.

## Task graph (visual)

```
T1 ── T2 ── T3 ── T5
  └─── T4 (also needs T2)
```

Maximum parallelism after T2 is done: T3 and T4 in parallel. T5 waits
on T3.
