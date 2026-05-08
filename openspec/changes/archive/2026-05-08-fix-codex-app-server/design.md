# Design: Fix codex app-server runner

## Context

Codex's `app-server` is a long-running JSONL-over-stdio subprocess.
contrabass speaks JSON-RPC-lite to it (`{"id":N,"method":...}` ↔
`{"id":N,"result":...}` ↔ `{"method":"turn/event"}` notifications).
The protocol is fully captured in `docs/codex-protocol.md`. The
runner that drives it lives in `internal/agent/codex.go` and was
ported from Symphony Elixir; the wire shape has drifted since then in
small but breaking ways.

Three semantically distinct timing budgets coexist in this runner:

1. **Handshake**: roughly the cost of `initialize → initialized →
   thread/start → turn/start`, plus warming up MCP servers (~5–25 s).
2. **Per-message read** during the streaming loop: how long we'll wait
   between two consecutive event lines before declaring the agent
   stalled.
3. **Per-turn budget**: enforced by the orchestrator outside this
   file; passed to codex as part of `agent_timeout_ms`.

Today (1) and (2) share `r.timeout` and (3) lives in the orchestrator.
Operators have to pick a single number that works for both (1) and
(2), which is impossible: handshake wants generous, stream read wants
tight.

## Goals / Non-Goals

**Goals**

- Make `agent.type: codex` reliably reach a successful `turn/completed`
  in `--no-tui` mode against a stock codex app-server.
- Tolerate transient overload (`-32001`) without losing an attempt.
- Tolerate large agent messages (multi-MB diffs / tool outputs).
- Ensure terminal events (`turn/completed`, `turn/failed`,
  `turn/cancelled`) always reach the orchestrator — never dropped.
- Keep workflow YAML backward-compatible.

**Non-Goals**

- Replacing the JSONL transport with `Content-Length` framing or any
  other LSP-style framing. Codex is line-framed and that is fixed
  upstream; see docs/codex-protocol.md.
- Adding a runtime approval UI. `approvalPolicy="never"` is the only
  policy contrabass supports today and remains the default; an
  interactive surface is a separate, larger change.
- Cross-runner unification. omx, opencode, and oh-my-opencode each
  speak their own protocols — out of scope here.
- Token-usage accounting. codex emits usage on `turn/completed`
  natively; that path already works in TUI mode and only needs hub
  fan-out timing fixes covered by `improve-dashboard-liveness`.

## Decisions

### Decision 1: Default `approvalPolicy` and `sandboxPolicy` baked into the runner, not the config

Workflow YAML *can* set `codex.approval_policy` / `codex.sandbox`, but
when omitted the runner SHALL inject:
- `approvalPolicy: "never"`
- `sandboxPolicy: { "type": "workspaceWrite", "networkAccess": false }`

`workspaceWrite` matches how every contrabass workflow has been used
in practice (the issue branch is the agent's sandbox). `networkAccess:
false` is the safer default; workflows that legitimately need network
(e.g. running tests that hit a registry) override it explicitly.

### Decision 2: Sentinel error + per-request retry budget for `-32001`

Returning `errCodexOverloaded` from `awaitResponse()` instead of a
generic `rpc error ...` lets the three handshake call sites in
`Start()` decide whether to retry without parsing strings. Each call
site retries the *same* JSON request (id stays the same) so duplicate
delivery is naturally idempotent on codex's side: if codex eventually
processed the original, our resend's response is the matching
`{"id":N,"result":...}` we were already waiting for; if it didn't,
codex sees the new request as fresh.

Retry budget defaults: 5 attempts, 100 ms → 200 → 400 → 800 → 4000 ms
backoff. After exhaustion the runner falls back to the existing
`cleanupOnStartFailure` path; nothing about the orchestrator's
`BackoffEnqueued` semantics changes.

### Decision 3: Replace `bufio.Scanner` with a `Reader` loop

`bufio.Scanner` has a hard maximum (`MaxScanTokenSize`) and a
quasi-hidden `bufio.ErrTooLong` failure mode that aborts the scan.
For codex agent-message frames that legitimately span multiple
megabytes, switching to:

```go
reader := bufio.NewReader(stdout)
for {
    line, err := reader.ReadBytes('\n')
    ...
}
```

with our own per-line size guard (`maxStreamLineSize = 32 MB`) is the
right primitive. Lines that cross the guard are logged and skipped
(not dropped silently); the loop continues with the next line. The
guard exists only to prevent a runaway codex from OOMing contrabass.

### Decision 4: Block on terminal events; drop only item-level events

Terminal events (`turn/completed`, `turn/failed`, `turn/cancelled`)
are the only events the orchestrator's success/failure determination
depends on. Dropping any of them is a correctness hole. The runner
SHALL `events <- ev` (blocking) for events whose type matches a small
fixed set:

```go
var terminalCodexEventTypes = map[string]struct{}{
    "turn/completed": {},
    "turn/failed":    {},
    "turn/cancelled": {},
}
```

For all other events (`item/*`, `session.status`, `protocol/error`)
the existing `select { case events <- ev: default: }` semantics
remains, so a slow consumer cannot deadlock the runner. The default
buffer goes from 128 to 512 to lower drop probability under healthy
loads.

### Decision 5: `Flush()` after every `sendMessage`

`bufio.Writer` defaults to a 4 KB buffer. A single ~1 KB JSON message
will fit but won't be flushed until the next message exceeds the
buffer. The fix is one line; the cost is negligible.

### Decision 6: Two-axis timeouts on the runner; orchestrator opts in

```go
type CodexRunner struct {
    ...
    handshakeTimeout  time.Duration  // existing r.timeout, renamed
    streamReadTimeout time.Duration  // NEW; default 0 == disabled
}
```

`handshakeTimeout` covers everything `Start()` does up to the moment
`streamEventsAndWait()` is launched. `streamReadTimeout` is enforced
inside the streaming loop's `readLineWithTimeout`-style call; if 0,
the runner falls back to "no per-line deadline, only context cancel".

Workflow plumbing: `cmd/contrabass/team.go::createRunner` checks for
`cfg.StallTimeoutMs > 0` and, if so, calls
`runner.WithStreamReadTimeout(...)`. Existing workflows that don't
set `stall_timeout_ms` see no behavior change.

### Decision 7: Close stdin after the first terminal turn event to finalize codex exec

`codex exec` semantics are "drive a single conversation through stdin
until input ends". After `turn/completed` (or `turn/failed` /
`turn/cancelled`), codex's app-server is done with the current turn
but does not voluntarily emit a separate `finished` event nor exit —
it sits in an idle read on stdin awaiting follow-up. Symphony Elixir
drove codex interactively (Elixir port could keep stdin open across
turns), so this idle was natural. contrabass runs codex one-shot per
issue and then expects clean termination, so the same idle reads as
a stall and contrabass synthesizes
`finished status=Failed err=""` after `stall_timeout_ms`.

The fix is a one-liner with a clean trigger: when
`streamEventsAndWait` parses an event whose type is in
`terminalCodexEventTypes` (already defined in T5 for terminal-event
delivery), call `process.stdin.Close()` exactly once. codex receives
EOF, finishes its bookkeeping (e.g. final `notify-hook`), and exits
cleanly. The existing `process.cmd.Wait()` path observes
`exitErr == nil` and the orchestrator transitions through the normal
"finished" handling.

This deliberately does NOT add a new event type, NOT add a new
config knob, and NOT change the orchestrator-side semantics. The
stall-detection path is preserved as a backstop for cases where
codex genuinely hangs after a non-terminal event (e.g. a stuck MCP
tool call).

A `sync.Once` field on `codexProcess` guards the close — multiple
terminal events in the same stream (rare but legal) MUST not cause a
double-close panic. The existing `cleanupOnStartFailure` and
`Stop()` paths already close stdin; both gain idempotency under the
new `Once`.

## Risks / Trade-offs

- **Risk**: codex changes its overload code from `-32001` to something
  else in a future release. Mitigation: helper
  `isOverloadError(rpcErr)` documented to be expanded; a single test
  asserts on the constant, so the breakage is loud.
- **Risk**: blocking send on terminal events could hang the runner if
  the orchestrator's hub is wedged. Mitigation: existing context
  cancellation in `streamEventsAndWait()` propagates; the blocking
  send respects `select { case events <- ev: case <-ctx.Done(): }` —
  no unconditional block.
- **Risk**: 32 MB per-line cap is still finite. Mitigation: the cap
  is configurable via `WithMaxStreamLineSize` for users with weirder
  agents; the default protects against runaway memory.
- **Trade-off**: explicit flush adds one syscall per message. Six
  messages per turn × tens of turns per run × ~1 µs/syscall = single
  microseconds, immeasurable.

## Migration Plan

- All changes are additive on the JSON wire format (extra params) or
  internal to the runner.
- Workflows that already set `codex.approval_policy` / `codex.sandbox`
  see no behavior change.
- Workflows that omit them gain the documented defaults; in practice
  that matches what every current contrabass deployment uses.
- The renamed `r.timeout → r.handshakeTimeout` keeps the public
  constructor signature; the field rename is internal only.
- `WithStreamReadTimeout` is opt-in; nothing fails if the orchestrator
  doesn't wire it.
