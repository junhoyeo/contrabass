# Tasks: Fix codex app-server runner

## T1 — Inject `approvalPolicy` and `sandboxPolicy` into `thread/start` and `turn/start`

**Files**: `internal/agent/codex.go`.

**Contract**:

- Add a small helper:

  ```go
  // policyParams returns the approvalPolicy and sandboxPolicy values
  // that thread/start and turn/start must carry. Workflow override
  // wins; otherwise hardcoded defaults are returned.
  func (r *CodexRunner) policyParams() (approval string, sandbox interface{})
  ```

  Defaults: `approval = "never"`,
  `sandbox = map[string]interface{}{"type":"workspaceWrite", "networkAccess": false}`.

- Inside `Start`, when building the maps for `thread/start` and
  `turn/start`, splice the two keys in. The `cwd` and other existing
  keys stay where they are.

- `CodexRunnerOptions` already carries `ApprovalPolicy` and `Sandbox`
  per `team.go::createRunner`. If `Sandbox` is currently a string,
  keep accepting a string and pass it through; the helper SHALL also
  accept a `map[string]interface{}` for callers that want the
  structured form. (Verify the existing field type before editing.)

**Acceptance**:
- `thread/start` and `turn/start` both have non-empty `approvalPolicy`
  and `sandboxPolicy` for every wire encoding observed in tests.
- When workflow YAML omits both fields, the wire string contains
  `"approvalPolicy":"never"` and the sandbox object literal listed
  above.
- `go build ./...` clean.

**Depends on**: none.
**Blocks**: T7 (test that exercises the wire format).

---

## T2 — Sentinel `errCodexOverloaded` + retry in `awaitResponse`

**Files**: `internal/agent/codex.go`.

**Contract**:

- Add package-level sentinel:
  ```go
  var errCodexOverloaded = errors.New("codex app-server overloaded (-32001)")
  ```
- In `awaitResponse`, when the matching-id message contains an
  `error` whose `code` (parsed via the existing
  JSON-decoding) equals `-32001`, return
  `(nil, errCodexOverloaded)` instead of the generic formatted error.
- Other JSON-RPC error codes keep returning the existing formatted
  error string unchanged.
- Add helper `isOverloadError(err) bool` that returns
  `errors.Is(err, errCodexOverloaded)`.

**Acceptance**:
- Unit test for the overloaded path is in T7.
- Other error codes' behavior is byte-identical to today.
- `go vet ./...` clean.

**Depends on**: none.
**Blocks**: T3 (the retry wrapper relies on the sentinel).

---

## T3 — Retry handshake calls on `errCodexOverloaded`

**Files**: `internal/agent/codex.go`.

**Contract**:

- Add fields to `CodexRunner`:
  ```go
  overloadRetries  int            // default 5
  overloadRetryCap time.Duration  // default 4 * time.Second
  overloadStartDelay time.Duration  // default 100 * time.Millisecond
  ```
  Set in `NewCodexRunner`.
- Wrap the three call sites in `Start` (after each `sendMessage` +
  `awaitResponse` pair for `id=1`, `id=2`, `id=3`) with:
  ```go
  result, err := r.handshakeStep(reader, writer, msg, id)
  ```
  where `handshakeStep`:
  - sends the message,
  - waits for response,
  - if `isOverloadError(err)`, sleeps with exponential backoff
    capped at `overloadRetryCap`, increments retry count,
    re-sends the same message,
  - returns either the result or the wrapped error after the budget
    is exhausted.

- Backoff schedule: `100, 200, 400, 800, 4000` ms (default budget = 5
  retries; total wallclock ≤ ~5.5 s on max overload).

**Acceptance**:
- The three handshake call sites in `Start` go through `handshakeStep`.
- Overload retries are budgeted per-handshake-step (each of `id=1`,
  `id=2`, `id=3` gets its own budget).
- Existing happy-path behavior: zero added latency.

**Depends on**: T2.
**Blocks**: T7.

---

## T4 — Replace `bufio.Scanner` with `bufio.Reader.ReadBytes('\n')` + 32 MB cap

**Files**: `internal/agent/codex.go`.

**Contract**:

- Add constant:
  ```go
  const maxStreamLineSize = 32 * 1024 * 1024 // 32 MB
  ```
- In `streamEventsAndWait`, replace the `bufio.NewScanner(reader)` +
  `scanner.Buffer(...)` + `for scanner.Scan()` block with a
  `bufio.NewReader(reader)` (or reuse the existing one) and a loop
  that calls `reader.ReadBytes('\n')`. Trim the trailing `\n`. Decode
  identically to today.
- Add a length guard: if `len(line) > maxStreamLineSize`, log WARN
  with `event="maxStreamLineSize_exceeded"` and the first 256 bytes,
  drop the line, and `continue`.
- Treat `io.EOF` as the natural end-of-stream just like the previous
  `scanner.Scan() == false` branch.
- Keep the existing `process.cmd.Wait()` and finish-error logic
  unchanged.

**Acceptance**:
- Lines up to 32 MB pass through and reach the events channel.
- Lines beyond 32 MB are skipped with a single WARN log line; the
  loop continues processing the next line.
- `bufio.ErrTooLong` is no longer reachable from this code path.

**Depends on**: none.
**Blocks**: T7.

---

## T5 — Block on terminal events, drop only item-level events

**Files**: `internal/agent/codex.go`.

**Contract**:

- Add package-level set:
  ```go
  var terminalCodexEventTypes = map[string]struct{}{
      "turn/completed": {},
      "turn/failed":    {},
      "turn/cancelled": {},
  }
  ```
- Inside `streamEventsAndWait`, after constructing `event`, branch:
  ```go
  if _, terminal := terminalCodexEventTypes[event.Type]; terminal {
      select {
      case events <- event:
      case <-streamCtx.Done():
          // logged at debug; loop exits naturally on next read.
      }
  } else {
      select {
      case events <- event:
      default:
          // existing drop-on-full semantics for non-terminal events
      }
  }
  ```
  `streamCtx` is the context plumbed in from `Start` (today the
  process context). Add it as a field on `codexProcess` if it isn't
  already.
- Bump default events buffer in `Start` from 128 to 512:
  ```go
  events := make(chan types.AgentEvent, 512)
  ```

**Acceptance**:
- Terminal events go through unconditional blocking send guarded by
  context cancellation.
- Non-terminal events keep the existing best-effort semantics.
- `go vet ./...` clean.

**Depends on**: T4.
**Blocks**: T7.

---

## T6 — Split `r.timeout` into `handshakeTimeout` + `streamReadTimeout`

**Files**: `internal/agent/codex.go`,
`cmd/contrabass/team.go`.

**Contract**:

- Rename the field `timeout` on `CodexRunner` to `handshakeTimeout`
  internally. The constructor `NewCodexRunner(binaryPath, timeout
  time.Duration)` keeps its signature; the parameter is now
  interpreted as `handshakeTimeout`.
- Add field:
  ```go
  streamReadTimeout time.Duration  // 0 == no per-line deadline (default)
  ```
- Add chainable setter:
  ```go
  func (r *CodexRunner) WithStreamReadTimeout(d time.Duration) *CodexRunner {
      r.streamReadTimeout = d
      return r
  }
  ```
- Wire `streamEventsAndWait` to use a per-line deadline when
  `streamReadTimeout > 0`. Reuse the existing
  `readLineWithTimeout` helper if applicable. When 0, fall through to
  no per-line deadline.
- In `cmd/contrabass/team.go::createRunner`, in the `case "codex":`
  branch, if `cfg.StallTimeoutMs() > 0`, call
  `runner.WithStreamReadTimeout(cfg.StallTimeoutMs())`.

**Acceptance**:
- `awaitResponse` uses `handshakeTimeout`; not the per-line value.
- `streamEventsAndWait` uses `streamReadTimeout` (or no deadline).
- Existing tests that don't set `stall_timeout_ms` keep passing.
- Public constructor signature unchanged.

**Depends on**: none.
**Blocks**: T7.

---

## T7 — Unit + integration tests

**Files**: `internal/agent/codex_test.go`.

**Contract**: Drive the runner against an in-process **stub
app-server** that speaks the same JSONL protocol. Use stdlib only
(an `os.Pipe` + a goroutine that reads requests and writes responses
is sufficient — see existing `MockRunner` for inspiration). Tests:

1. `TestCodexRunner_PolicyDefaults_OnWire`
   - Stub captures every request line; assert `thread/start.params`
     and `turn/start.params` contain `approvalPolicy="never"` and
     `sandboxPolicy={"type":"workspaceWrite","networkAccess":false}`.

2. `TestCodexRunner_PolicyOverride_OnWire`
   - Construct runner with explicit overrides; assert wire format
     matches.

3. `TestCodexRunner_OverloadRetried`
   - Stub returns `-32001` once for `id=2` then a real result; assert
     `Start` succeeds, retry counter == 1.

4. `TestCodexRunner_OverloadBudgetExhausted`
   - Set `overloadRetries=2`; stub returns `-32001` forever for `id=2`;
     assert `Start` returns error wrapping `errCodexOverloaded` and
     subprocess is cleaned up.

5. `TestCodexRunner_LargeAgentMessage`
   - Stub emits a 5 MB `item/agentMessage` line followed by
     `turn/completed`; assert both events reach `events`.

6. `TestCodexRunner_OversizedLineSkipped`
   - Stub emits a 64 MB malformed line followed by `turn/completed`;
     assert only `turn/completed` reaches `events`, exactly one
     `maxStreamLineSize_exceeded` log entry.

7. `TestCodexRunner_TerminalEventsNotDropped`
   - Construct runner with events buffer 4; emit 50 `item/*` events
     followed by `turn/completed`; consumer reads with a delay long
     enough to fill the buffer; assert the last event read is
     `turn/completed`.

8. `TestCodexRunner_FlushErrorSurfaced`
   - Close the stdin pipe inside the stub before `Start` writes; assert
     `Start` returns an error containing `flush`.

9. `TestCodexRunner_HandshakeAndStreamTimeoutsIndependent`
   - Two sub-tests:
     - handshake stalls → fail in `~handshakeTimeout`,
       `streamReadTimeout` ignored.
     - handshake OK, stream stalls → fail in
       `~streamReadTimeout` (when set), regardless of how high
       `handshakeTimeout` is.

**Acceptance**:
- `go test ./internal/agent/ -run TestCodexRunner -count=1 -race -v`
  passes.
- Tests use only `stdlib` + `stretchr/testify` (already a project
  dep).
- No real `codex` binary is invoked.

**Depends on**: T1, T2, T3, T4, T5, T6.
**Blocks**: none.

---

## T8 — Close codex stdin after the first terminal turn event

**Files**: `internal/agent/codex.go`, `internal/agent/codex_test.go`.

**Contract**:

- Add a `sync.Once` field to `codexProcess` named `stdinCloseOnce`.
- All sites that currently call `process.stdin.Close()` (today:
  `cleanupOnStartFailure`, `Stop()`'s force-shutdown path) SHALL be
  routed through a small helper:

  ```go
  func (p *codexProcess) closeStdin() {
      p.stdinCloseOnce.Do(func() {
          if p.stdin != nil {
              _ = p.stdin.Close()
          }
      })
  }
  ```
- Inside `streamEventsAndWait`, after the existing
  `terminal := terminalCodexEventTypes[event.Type]` branch (added in
  T5) — i.e. on the same turn that the runner blocks-or-drops the
  event — call `process.closeStdin()`. Place the call *after* the
  blocking send succeeds (or after the context-cancel branch fires)
  so the consumer is guaranteed to have seen the terminal event
  before codex is told to exit.
- No change to `process.cmd.Wait()` semantics: it remains the
  single source of truth for the runner's exit status. Tests added
  in T7 keep passing.

**Acceptance**:
- `codexProcess` has a `stdinCloseOnce sync.Once` field.
- `closeStdin()` helper exists and is the only path that calls
  `process.stdin.Close()` going forward.
- New unit test
  `TestCodexRunner_StdinClosesAfterTerminalEvent` (file
  `internal/agent/codex_test.go`) drives a stub that emits
  `turn/completed`, sits idle on stdin, exits 0 only on EOF. Asserts
  that the runner exits cleanly within a small budget (e.g. 2 s)
  *without* relying on any `streamReadTimeout` deadline.
- New unit test
  `TestCodexRunner_StdinCloseIsIdempotent` constructs a runner whose
  stub emits `turn/completed` AND `turn/cancelled` in sequence;
  asserts no panic, no goroutine leak, runner exits cleanly.
- `go test ./internal/agent/ -run TestCodexRunner -count=1 -race -v`
  remains green for every test added in T7.
- `go vet ./...` clean.

**Depends on**: T5 (uses `terminalCodexEventTypes`).
**Blocks**: none.

---

## Rejection rules (apply to ALL tasks)

A diff that satisfies any of the following MUST be rejected:

1. The diff modifies only `*_test.go` files for T1, T2, T3, T4, T5,
   T6. Production code is mandatory for those tasks.
2. The diff modifies any file outside `internal/agent/codex*.go` or
   `cmd/contrabass/team.go`. (T6 is the only task allowed to touch
   `team.go`; even there, only the `case "codex":` block.)
3. The diff adds a new Go module dependency. Stdlib only.
4. The diff changes the public signature of `NewCodexRunner` or
   `CodexRunner.Start` / `Stop` / `Close`. (Adding methods is fine;
   changing existing signatures is not.)
5. The diff changes the JSONL protocol semantics in any way other
   than the additive policy fields specified in T1.
6. The diff omits any acceptance bullet from the task it claims to
   implement.

## Task graph

```
T1 ─┐
T2 ── T3 ─┤
T4 ── T5 ──── T8 ─┤
T6 ──────────────┴── T7
```

T1, T2, T4, T6 may proceed in parallel after kickoff. T3 follows T2.
T5 follows T4 (so the terminal-event guard reads through the new
loop). T8 follows T5 (it consumes the same terminal event set). T7
ties everything together; no sub-task of T7 may run before its
dependency's production code lands.
