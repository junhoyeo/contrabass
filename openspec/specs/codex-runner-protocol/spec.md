# Codex runner protocol — handshake, streaming, error handling

## ADDED Requirements

### Requirement: Codex runner SHALL include `approvalPolicy` and `sandboxPolicy` in `thread/start` and `turn/start`

`CodexRunner.Start` SHALL build the params for both `thread/start` and
`turn/start` such that they include both `approvalPolicy` and
`sandboxPolicy` keys. Values come from
`CodexRunnerOptions.ApprovalPolicy` and
`CodexRunnerOptions.Sandbox` if set; otherwise from the runner's
defaults: `approvalPolicy="never"` and
`sandboxPolicy={"type":"workspaceWrite","networkAccess":false}`.

#### Scenario: Defaults applied when config is empty

- GIVEN a `CodexRunner` constructed with empty `ApprovalPolicy` and
  empty `Sandbox`
- WHEN `Start` runs
- THEN the wire-encoded `thread/start.params` contains
  `"approvalPolicy":"never"` and
  `"sandboxPolicy":{"type":"workspaceWrite","networkAccess":false}`,
  AND the same fields appear in `turn/start.params`.

#### Scenario: Workflow override is forwarded verbatim

- GIVEN `ApprovalPolicy="on-request"` and `Sandbox={"type":"readOnly"}`
- WHEN `Start` runs
- THEN `thread/start.params.approvalPolicy == "on-request"` and
  `thread/start.params.sandboxPolicy == {"type":"readOnly"}`. Same for
  `turn/start.params`.

### Requirement: Codex runner SHALL retry on `-32001 server overloaded`

`awaitResponse` SHALL recognize JSON-RPC error responses whose code is
`-32001` and return a sentinel `errCodexOverloaded` (in addition to or
in place of the generic error string). Each handshake call site
(`initialize`, `thread/start`, `turn/start`) SHALL retry the same
request (preserving the same id) on `errCodexOverloaded` up to
`r.overloadRetries` times (default 5) with exponential backoff capped
at `r.overloadRetryCap` (default 4 s, starting at 100 ms, doubling
each step). After the retry budget is exhausted the runner SHALL
return the error and trigger `cleanupOnStartFailure` as before.

#### Scenario: Single overload retried successfully

- GIVEN the codex stub responds to `id=2` (`thread/start`) with
  `{"id":2,"error":{"code":-32001,"message":"Server overloaded"}}` on
  the first read and with `{"id":2,"result":{"thread":{"id":"t-1"}}}`
  on the second read
- WHEN `Start` runs
- THEN `Start` returns successfully and the runner's stderr buffer
  records exactly one overload retry log line.

#### Scenario: Sustained overload exhausts retry budget

- GIVEN the codex stub returns `-32001` for every read of `id=2`
- AND `r.overloadRetries == 2` for the test
- WHEN `Start` runs
- THEN `Start` returns an error wrapping `errCodexOverloaded`, the
  runner cleans up the subprocess, and `cleanupOnStartFailure` was
  called exactly once.

#### Scenario: Other JSON-RPC errors are not retried

- GIVEN the codex stub returns
  `{"id":2,"error":{"code":-32600,"message":"Invalid request"}}`
- WHEN `Start` runs
- THEN `Start` returns an error and zero overload retries are
  attempted.

### Requirement: Codex runner SHALL tolerate per-line sizes up to 32 MB without aborting the stream

`streamEventsAndWait` SHALL parse JSONL output using a reader that
admits per-line lengths up to `maxStreamLineSize` (default 32 MB).
Lines exceeding the cap SHALL be logged at WARN, dropped from the
event stream, and the loop SHALL continue reading subsequent lines.

#### Scenario: 5 MB agent message is delivered

- GIVEN the codex stub emits a single 5 MB JSON line whose `method` is
  `item/agentMessage`
- WHEN the streaming loop reads it
- THEN exactly one `item/agentMessage` event reaches the events
  channel and the loop continues reading subsequent lines.

#### Scenario: Oversized line is skipped, stream survives

- GIVEN the codex stub emits a 64 MB malformed JSON line followed by a
  normal `turn/completed` line
- WHEN the streaming loop reads them
- THEN the oversized line produces zero events and one WARN log entry
  (`maxStreamLineSize_exceeded` or equivalent), AND the
  `turn/completed` event still reaches the events channel.

### Requirement: Terminal events SHALL never be dropped

For agent events whose type is in `terminalCodexEventTypes` =
`{"turn/completed", "turn/failed", "turn/cancelled"}`, the runner
SHALL deliver the event to the events channel using a blocking send
(in tandem with context cancellation). Other events MAY use
non-blocking send semantics (the existing `select { case ...:
default: }`) so a slow consumer cannot deadlock the runner.

#### Scenario: Slow consumer receives `turn/completed` after backpressure

- GIVEN the events channel has buffer 4 and the consumer is paused
- AND the runner emits 100 `item/*` events followed by one
  `turn/completed`
- WHEN the consumer eventually drains the channel
- THEN the final event read is `turn/completed` (it survived the
  backpressure period).
- AND some `item/*` events MAY have been dropped (acceptable).

#### Scenario: Context cancellation supersedes blocking send

- GIVEN the consumer never reads the channel
- AND the parent context is cancelled while the runner is trying to
  send `turn/completed`
- WHEN the runner reacts to the cancellation
- THEN the blocking send returns within the cancellation window
  (~tens of ms) and the streaming loop exits cleanly without
  goroutine leak.

### Requirement: Codex runner SHALL flush stdin after every JSON-RPC message

Every code path that builds a JSON-RPC message via `sendMessage`
SHALL invoke `bufio.Writer.Flush()` (or equivalent) before returning,
and SHALL surface any flush error to the caller as the function's
return value.

#### Scenario: Flush error is surfaced

- GIVEN the codex stub closes stdin before any message can be flushed
- WHEN `sendMessage` runs
- THEN it returns a non-nil error (`flush stdin: ...`) and does not
  silently swallow the failure.

### Requirement: Handshake timeout and per-line stream-read timeout SHALL be independently configurable

The runner SHALL expose two distinct durations:

- `handshakeTimeout` — applied to each `awaitResponse` call inside
  `Start` (default 30 s).
- `streamReadTimeout` — applied to each per-line read inside
  `streamEventsAndWait` (default 0 == disabled).

The constructor `NewCodexRunner(binaryPath, timeout)` retains its
existing two-argument signature with `timeout` interpreted as
`handshakeTimeout`. A new opt-in setter
`(*CodexRunner).WithStreamReadTimeout(d time.Duration)` SHALL
configure the per-line read budget and SHALL be invoked by
`createRunner` in `cmd/contrabass/team.go` when
`WorkflowConfig.StallTimeoutMs > 0`.

#### Scenario: Handshake timeout enforced independently

- GIVEN `handshakeTimeout = 1 s` and `streamReadTimeout = 0`
- AND the codex stub never responds to `id=1`
- WHEN `Start` runs
- THEN it returns a handshake-timeout error within 1 s plus a small
  margin.

#### Scenario: Stream read timeout enforced independently

- GIVEN `handshakeTimeout = 30 s` and `streamReadTimeout = 500 ms`
- AND the codex stub completes the handshake but emits no further
  lines
- WHEN the streaming loop runs
- THEN within ~500 ms the runner observes a stream stall and
  `streamEventsAndWait` finishes with the corresponding error
  (without hanging up the whole 30 s handshake budget).

#### Scenario: Default behavior is unchanged for callers that don't opt in

- GIVEN `streamReadTimeout` is left at its default 0
- WHEN the streaming loop runs against a healthy codex stub
- THEN behavior is identical to today's runner: the loop reads until
  the underlying reader returns EOF, with no per-line deadline.

### Requirement: Codex runner SHALL finalize the subprocess after a terminal turn event

After `streamEventsAndWait` parses an event whose `Type` is in
`terminalCodexEventTypes` (`turn/completed`, `turn/failed`,
`turn/cancelled`), the runner SHALL close codex's stdin pipe exactly
once via a `sync.Once` guard on `codexProcess`. The streaming loop
SHALL continue to drain remaining stdout lines until natural EOF,
and the existing `process.cmd.Wait()` path remains the single source
of truth for the runner's exit status.

#### Scenario: turn/completed triggers stdin close and clean exit

- GIVEN a stub app-server that completes the handshake and emits
  `turn/completed`, then sits idle on stdin and exits 0 only when
  it observes EOF
- WHEN the runner streams the events
- THEN the runner closes stdin within ~50 ms of receiving
  `turn/completed`, the stub then exits 0, and
  `process.cmd.Wait()` returns nil; the parent contrabass code path
  observes a clean run termination (no synthetic `Failed` from
  `stall_timeout_ms`).

#### Scenario: turn/failed triggers stdin close

- GIVEN a stub that emits `turn/failed` instead of `turn/completed`
- WHEN the runner streams the event
- THEN the runner still closes stdin and `process.cmd.Wait()`
  returns the underlying exit status; the runner does NOT synthesize
  an extra failure on top of codex's own failure signal.

#### Scenario: Multiple terminal events in a single run do not double-close

- GIVEN a (degenerate) stub that emits `turn/completed` followed by
  another `turn/cancelled`
- WHEN both events arrive at `streamEventsAndWait`
- THEN stdin is closed exactly once (no panic, no error from
  duplicate close), and the runner exits the loop cleanly.

#### Scenario: `Stop()` after a terminal event is idempotent

- GIVEN `Stop()` is called after `turn/completed` has already
  triggered stdin close
- WHEN `Stop()` runs
- THEN it does not panic and does not log an error about
  already-closed stdin; codex's exit is allowed to complete
  naturally per the existing graceful-stop budget.
