# Codex App-Server Protocol Spike (Exact Framing + Symphony Mapping)

## Scope

This document captures the **actual wire protocol** used between Symphony Elixir and a Codex app-server subprocess, based on source and tests (not assumptions).

Primary references:

- Codex app-server README and Rust transport/protocol code
- Symphony Elixir `lib/symphony_elixir/codex/app_server.ex`
- Symphony Elixir `test/symphony_elixir/app_server_test.exs`
- OpenAI blog post: "Unlocking the Codex harness"

## 1) Exact Framing Mechanism

## Transport and framing

- **Protocol shape:** JSON-RPC-like request/response/notification objects.
- **JSON-RPC variant:** "JSON-RPC lite" (omits `"jsonrpc": "2.0"` on the wire).
- **Default transport:** stdio (`--listen stdio://`).
- **Framing:** **newline-delimited JSON (JSONL / NDJSON-style line framing)**.

Codex side evidence:

- `codex-rs/app-server/src/transport.rs`
  - stdin reader uses `BufReader::lines()` and processes **one line == one JSON message**.
  - stdout writer serializes JSON and appends `"\n"`.

Symphony side evidence:

- `elixir/lib/symphony_elixir/codex/app_server.ex`
  - `send_message/2` writes `Jason.encode!(message) <> "\n"` to port.
  - read loop uses port line mode and handles `{:eol, chunk}` / `{:noeol, chunk}`.
  - partial chunks are buffered until newline before JSON decode.

Test evidence:

- `elixir/test/symphony_elixir/app_server_test.exs`
  - "buffers partial JSON lines until newline terminator" verifies split/large line behavior.

Conclusion:

- **Not Content-Length framing. Not LSP headers.**
- **Actual framing is line-delimited JSON over stdio.**

## 2) Core Envelope Shapes

Wire-level object forms (from `jsonrpc_lite.rs` and runtime behavior):

1. Request (expects response)
2. Response (successful)
3. Error response
4. Notification (no response)

Examples:

```json
{"id":1,"method":"initialize","params":{"clientInfo":{"name":"symphony-orchestrator","title":"Symphony Orchestrator","version":"0.1.0"},"capabilities":{"experimentalApi":true}}}
```

```json
{"id":1,"result":{"userAgent":"..."}}
```

```json
{"id":7,"error":{"code":-32001,"message":"Server overloaded; retry later."}}
```

```json
{"method":"turn/completed"}
```

## 3) Session Handshake and Lifecycle

Symphony startup sequence (`AppServer.start_session/1`, `do_start_session/3`):

1. Spawn app-server subprocess.
2. Send `initialize` request (`id=1`).
3. Wait for response with matching `id=1`.
4. Send `initialized` notification.
5. Send `thread/start` request (`id=2`).
6. Extract `thread.id` from response.
7. On each run, send `turn/start` request (`id=3`) with same `threadId`.
8. Stream notifications until terminal event (`turn/completed`, `turn/failed`, `turn/cancelled`).

### initialize request (Symphony)

```json
{
  "method": "initialize",
  "id": 1,
  "params": {
    "capabilities": { "experimentalApi": true },
    "clientInfo": {
      "name": "symphony-orchestrator",
      "title": "Symphony Orchestrator",
      "version": "0.1.0"
    }
  }
}
```

### initialized notification (Symphony)

```json
{"method":"initialized","params":{}}
```

### thread/start request (Symphony)

```json
{
  "method": "thread/start",
  "id": 2,
  "params": {
    "approvalPolicy": "never | on-request | ...",
    "sandbox": "read-only | workspace-write | danger-full-access | object variant",
    "cwd": "/abs/workspace/path",
    "dynamicTools": [
      {
        "name": "linear_graphql",
        "description": "...",
        "inputSchema": { "type": "object", "required": ["query"] }
      }
    ]
  }
}
```

### thread/start response shape used by Symphony

```json
{"id":2,"result":{"thread":{"id":"thread-123"}}}
```

### turn/start request (Symphony)

```json
{
  "method": "turn/start",
  "id": 3,
  "params": {
    "threadId": "thread-123",
    "input": [{ "type": "text", "text": "<prompt>" }],
    "cwd": "/abs/workspace/path",
    "title": "MT-123: Issue title",
    "approvalPolicy": "...",
    "sandboxPolicy": { "type": "workspaceWrite", "networkAccess": false }
  }
}
```

### turn/start response shape used by Symphony

```json
{"id":3,"result":{"turn":{"id":"turn-456"}}}
```

## 4) Message Types Relevant to Symphony

During turn streaming, Symphony handles these method families:

### Terminal turn events

- `turn/completed` -> success terminal state
- `turn/failed` -> returns `{:error, {:turn_failed, params}}`
- `turn/cancelled` -> returns `{:error, {:turn_cancelled, params}}`

Examples:

```json
{"method":"turn/completed"}
```

```json
{"method":"turn/failed","params":{"message":"..."}}
```

```json
{"method":"turn/cancelled","params":{"reason":"..."}}
```

### Approval and interactive requests (server -> client requests)

- `item/commandExecution/requestApproval`
- `item/fileChange/requestApproval`
- `execCommandApproval`
- `applyPatchApproval`
- `item/tool/requestUserInput`

Symphony reply pattern:

- auto-approve (when policy effectively allows):

```json
{"id":99,"result":{"decision":"acceptForSession"}}
```

or

```json
{"id":99,"result":{"decision":"approved_for_session"}}
```

- for tool user-input approvals:

```json
{"id":110,"result":{"answers":{"mcp_tool_call_approval_call-717":{"answers":["Approve this Session"]}}}}
```

- non-interactive fallback answer:

```json
{"id":111,"result":{"answers":{"freeform-718":{"answers":["This is a non-interactive session. Operator input is unavailable."]}}}}
```

### Dynamic tool call request

- `item/tool/call` -> Symphony executes local tool executor and returns result in response to same `id`.

Success example from tests:

```json
{"id":102,"result":{"success":true,"contentItems":[{"type":"inputText","text":"{\"data\":{\"viewer\":{\"id\":\"usr_123\"}}}"}]}}
```

Failure/unsupported pattern:

```json
{"id":101,"result":{"success":false,"contentItems":[{"type":"inputText","text":"Unsupported dynamic tool ..."}]}}
```

### Other notifications

- Any unrecognized `method` is emitted as generic notification and processing continues.
- Non-JSON lines are logged and classified as malformed stream lines; loop continues.

## 5) Multi-Turn and Thread Continuation Semantics

- Symphony creates one thread via `thread/start` and stores `thread_id` in session.
- Each new prompt in same session uses **new `turn/start`** with the **same `threadId`**.
- Session id in Symphony observability is composed as `<thread_id>-<turn_id>`.

So continuation is:

- **same thread, multiple turns** (not a new thread per turn).

## 6) Error Handling (Exact Behaviors)

## Handshake/response wait errors

- Timeout waiting for a specific response id -> `:response_timeout`.
- Port exits while waiting -> `{:port_exit, status}`.
- Matching-id response with `error` -> `{:response_error, error}`.

## Turn-stream errors

- No event before turn timeout -> `:turn_timeout`.
- `turn/failed` -> `{:turn_failed, params}`.
- `turn/cancelled` -> `{:turn_cancelled, params}`.
- input-required variants -> `{:turn_input_required, payload}`.
- approval required with safer policy -> `{:approval_required, payload}`.

## Malformed and noisy output

- Non-JSON lines are tolerated; logged; loop continues.
- stderr merged into stdout (`:stderr_to_stdout`) and treated as stream lines.

## Codex-side overload behavior

- If ingress queue saturates, app-server can return JSON-RPC error:
  - code `-32001`
  - message `"Server overloaded; retry later."`

## 7) Sequence Diagrams

### Normal flow

```text
Symphony Client                           Codex app-server
    |                                           |
    | -- initialize(id=1, clientInfo, caps) --> |
    | <-- result(id=1, userAgent/...) --------- |
    | -- initialized(notification) -----------> |
    | -- thread/start(id=2, cwd, policy, ...) ->|
    | <-- result(id=2, thread.id) ------------- |
    | -- turn/start(id=3, threadId, input, ..)->|
    | <-- result(id=3, turn.id) --------------- |
    | <-- item/... notifications (stream) ----- |
    | <-- turn/completed ---------------------- |
    |                                           |
```

### Error/approval flow

```text
Symphony Client                           Codex app-server
    |                                           |
    | -- turn/start(id=3, ...) --------------> |
    | <-- result(id=3, turn.id) -------------- |
    | <-- item/commandExecution/requestApproval(id=99)
    | -- result(id=99, decision=acceptForSession) --> (if auto-approve)
    |                       OR
    | (if not auto-approve) return approval_required error and stop
    |                                           |
    | <-- turn/failed or turn/cancelled ------- |
    | -> return structured error                |
```

## 8) Symphony Handling Matrix

- `initialize` request/response: required first; then `initialized` notification.
- `thread/start` response: extracts `thread.id`, errors if payload shape invalid.
- `turn/start` response: extracts `turn.id`.
- `turn/completed`: success return.
- `turn/failed`: error return.
- `turn/cancelled`: error return.
- `item/tool/call`: execute `DynamicTool`, respond with `result` payload.
- approval requests: auto-approve only when configured (`approvalPolicy == "never"` in current policy derivation), else fail fast requiring approval.
- malformed line: log + continue.

## 9) Practical Integration Notes

- You must treat the stream as **line framed**; buffering until newline is mandatory.
- Request ids can be integer or string; preserve id type in replies.
- Do not require `"jsonrpc":"2.0"`; Codex intentionally omits it.
- For robust clients: handle out-of-order notifications while waiting for a specific response id.

## 10) Sources

- OpenAI blog: https://openai.com/index/unlocking-the-codex-harness/
- Codex app-server README: https://raw.githubusercontent.com/openai/codex/main/codex-rs/app-server/README.md
- Codex transport implementation: https://raw.githubusercontent.com/openai/codex/main/codex-rs/app-server/src/transport.rs
- Codex JSON-RPC lite types: https://raw.githubusercontent.com/openai/codex/main/codex-rs/app-server-protocol/src/jsonrpc_lite.rs
- Codex message processor init guard: https://raw.githubusercontent.com/openai/codex/main/codex-rs/app-server/src/message_processor.rs
- Symphony Elixir app-server client: https://raw.githubusercontent.com/openai/symphony/main/elixir/lib/symphony_elixir/codex/app_server.ex
- Symphony Elixir app-server tests: https://raw.githubusercontent.com/openai/symphony/main/elixir/test/symphony_elixir/app_server_test.exs
