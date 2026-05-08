## Context

The orchestrator tracks each running agent as a `RunningEntry` (pid, issue_id, workspace). The web server exposes read-only state via SSE + `/api/v1/state`. There is currently no mutation endpoint for in-flight agents. The frontend detail sheet is the natural stop point for a Stop action since it already shows per-agent state.

## Goals / Non-Goals

**Goals:**
- Provide a `POST /api/v1/running/{issue_id}/stop` endpoint returning 202 on success.
- Orchestrator kills the agent process (SIGTERM → SIGKILL after grace period) and marks the entry canceled.
- Frontend Stop button in `IssueDetailSheet` cycles through idle → stopping → done states.

**Non-Goals:**
- Force-kill without grace period on first request.
- Stopping backoff/queued agents (only `running` entries).
- Persisting stop history or emitting a new SSE event type (existing state update suffices).

## Decisions

**1. SIGTERM + 5 s grace → SIGKILL**
Agents may hold open files or git worktrees. A brief grace period lets them flush state. Alternative (instant SIGKILL) risks corrupted worktrees and was rejected.

**2. `POST .../stop` over `DELETE .../running/{id}`**
REST purists prefer DELETE, but the resource isn't being deleted — the _session_ is being terminated. `POST /stop` is an accepted RPC-style action verb and avoids confusion with future DELETE-issue endpoints.

**3. PID-based kill in orchestrator, not web handler**
The web handler must not import `os/signal` or call `syscall.Kill` directly. Orchestrator owns the process lifecycle; the handler calls `orchestrator.StopAgent(issueID)` and returns 202. This preserves the existing separation of concerns.

**4. Frontend optimistic state (`stopping` flag in component state)**
The SSE feed will eventually deliver the updated snapshot (entry removed from running). The button immediately enters `stopping` state on click without waiting for SSE confirmation, then resets to idle if the entry disappears from the snapshot.

## Risks / Trade-offs

- **PID reuse** — between snapshot read and kill, the PID could be reused by another process. Mitigation: orchestrator validates the pid still appears in the running map before calling `os.FindProcess`.
- **Concurrent stop requests** — two simultaneous calls could try to kill the same PID. Mitigation: orchestrator uses its existing mutex; second call returns 404 (entry already gone) or is a no-op.
- **Agent ignores SIGTERM** — SIGKILL fallback after 5 s covers this.

## Migration Plan

No schema changes. Deploy is a binary swap — new endpoint is additive, existing clients are unaffected.

## Open Questions

- Should the stop action move the issue back to `Todo` or to `Canceled`? (Current plan: `Canceled`, consistent with existing cancel flow.)
