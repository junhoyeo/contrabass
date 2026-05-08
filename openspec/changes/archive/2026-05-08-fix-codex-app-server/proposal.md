# Fix codex app-server runner

## Why

`internal/agent/codex.go` is the runner that lets contrabass speak directly
to a `codex app-server` subprocess (vs. omx, which wraps codex behind its
own team CLI). It is the path that `agent.type: codex` plus
`worker_mode: goroutine` (or `--no-tui` mode in general) hits, and it has
been broken in production since the codex protocol tightened around
approvals / sandbox semantics. Concretely six defects compound to make
the runner unreliable; any one of them can produce a silent failure that
the orchestrator misclassifies as either `start_failed` or, worse,
`success_unverified_branch_unchanged` (the failure mode the
`verify-success-with-diff` change was written to detect — but a healthy
codex runner is the better place to keep that bug from happening in the
first place).

The six defects live in:

1. `Start()` builds `thread/start` and `turn/start` params without
   `approvalPolicy` and `sandboxPolicy`. Recent codex app-server builds
   reject the streams (closing stdout after a single line) when those
   are missing because the server cannot pre-decide approval flow. This
   is the direct cause of "`codex EOF in --no-tui` mode" reported in
   the prior session.

2. `awaitResponse()` treats every JSON-RPC error symmetrically, including
   the well-known `-32001 server overloaded`. Codex returns this code
   when its ingress queue saturates and asks the client to retry. Today
   contrabass kills the subprocess and goes through the
   `BackoffEnqueued → re-claim` loop, which is ~10× more expensive than
   a one-second resend.

3. `streamEventsAndWait()` uses `bufio.Scanner` with a hard cap that —
   even if increased to the current `maxJSONLineSize` — does not
   accommodate the multi-megabyte `item/agentMessage` lines that codex
   emits when an agent outputs a large diff or a long tool result. A
   single overlong line aborts the scanner; the runner reports EOF as
   if codex exited, and the rest of the turn is dropped.

4. The events channel is buffered at 128 with `select { case events <-
   event: default: }` semantics. Terminal events
   (`turn/completed`, `turn/failed`, `turn/cancelled`) can be silently
   dropped under burst load, leaving the orchestrator believing the run
   is still in flight until the timeout fires.

5. `sendMessage()` uses `bufio.Writer` but never explicitly `Flush()`es
   after writing a message. On a busy host the kernel can hold the
   write in the pipe buffer for tens of seconds before codex sees it,
   skewing handshake timing and surfacing as `handshake timeout` even
   though the message did eventually arrive.

6. `Start()` and `awaitResponse()` share a single `r.timeout` value used
   for both the handshake (where 30s is normal because codex is warming
   up MCP servers) and the per-line read inside the streaming loop
   (where it should match the orchestrator's `stall_timeout_ms`). The
   conflation forces operators to either tolerate slow handshakes
   (bumping `r.timeout`) or quick stalls (lowering it) — never both.

7. **codex exec mode does not exit by itself after `turn/completed`** —
   it idles waiting for further input (codex was designed for an
   interactive driver). contrabass treats this idle as a stall; after
   `stall_timeout_ms` (default 60s) it synthesizes a
   `finished status=Failed err=""` and routes the run to backoff. The
   net effect: every successful single-turn run looks like a 60-second
   wait followed by a synthetic failure. Live evidence from the
   verify-success-with-diff demo: ZII-44 and ZII-47 each emitted
   `turn/completed`, then *exactly* 60 seconds later contrabass wrote
   `finished status=Failed err=""` and enqueued a retry.

## What Changes

- **Wire-format completeness** (defect 1): `thread/start` and
  `turn/start` send the policy fields verbatim from
  `cfg.Codex.ApprovalPolicy` and `cfg.Codex.Sandbox`. The defaults when
  workflow YAML omits them: `approvalPolicy="never"` and
  `sandboxPolicy={"type":"workspaceWrite","networkAccess":false}`.

- **Overload retry** (defect 2): introduce a sentinel
  `errCodexOverloaded`. `awaitResponse()` recognizes JSON-RPC `code ==
  -32001` and returns it. Callers (`Start()`'s three handshake calls)
  retry the same request up to `r.overloadRetries` (default 5) with
  exponential backoff capped at `r.overloadRetryCap` (default 4s). The
  retry budget is per-request, not per-process — a lasting overload
  still surfaces as a real error.

- **Line-length tolerance** (defect 3): replace the `bufio.Scanner`
  call site with a `bufio.Reader.ReadBytes('\n')` loop that imposes a
  high but finite per-line limit (`maxStreamLineSize = 32 MB`) and a
  clean error path when the limit is exceeded (drop the line, log,
  continue — never abort the whole turn).

- **Terminal-event delivery** (defect 4): teach the streaming loop to
  block on `events <- ev` (instead of drop-on-full) for events whose
  type is in `terminalEventTypes`. Non-terminal item events keep the
  drop-on-full semantics so a stuck consumer doesn't deadlock the
  whole turn. Bump default channel buffer from 128 → 512.

- **Explicit flush** (defect 5): every `sendMessage()` call ends with
  `writer.Flush()`; surfaces the underlying error.

- **Split timeouts** (defect 6): introduce two new options on
  `CodexRunner`: `handshakeTimeout` and `streamReadTimeout`. The
  existing constructor argument `timeout` becomes
  `handshakeTimeout` (back-compat default 30s). Add an opt-in
  `WithStreamReadTimeout` setter for the orchestrator to wire from
  `WorkflowConfig.StallTimeoutMs`.

- **Finalize codex exec on terminal turn events** (defect 7): inside
  `streamEventsAndWait`, after parsing an event whose type is in
  `terminalCodexEventTypes` (`turn/completed`, `turn/failed`,
  `turn/cancelled`), the runner SHALL close its stdin pipe to signal
  end-of-input to codex. codex reacts by exiting cleanly; the
  existing `process.cmd.Wait()` then returns nil, the orchestrator
  routes through its normal success / failure handler, and verify
  gates (`verify-success-with-diff`) remain authoritative. No more
  60-second stall window.

## Impact

- **Affected capability**: `codex-runner-protocol` (NEW — the
  contrabass ↔ codex app-server JSONL transport).
- **Affected code**:
  - `internal/agent/codex.go` (all six fixes),
  - `internal/agent/codex_test.go` (unit + integration coverage),
  - `internal/config/config.go` (the optional handshake/stream
    timeout fields if they don't already exist; verify in T6).
- **Out of scope**:
  - Any change to the orchestrator's success-verification path.
    `verify-success-with-diff` already covers that; this change
    addresses the runner protocol so the success path actually fires
    correctly.
  - omx / opencode / oh-my-opencode runners.
  - Reworking `cmd/contrabass/team.go createRunner` flow.
- **Migration**: workflow YAML stays compatible. Workflows that omit
  `codex.approval_policy` / `codex.sandbox` get the new defaults
  (`never` / `workspaceWrite,no-network`) which match what every
  current production workflow set anyway.
