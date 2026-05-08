## Why

Operators have no way to stop a runaway or misguided agent short of shelling into the host and `kill`-ing the process manually. This is the single most requested operational action on the running page and a blocker for safe unsupervised use.

## What Changes

- New `POST /api/v1/running/{issue_id}/stop` HTTP endpoint that signals the orchestrator to terminate the agent for the given issue.
- Orchestrator gains a `StopAgent(issueID string) error` method that sends SIGTERM to the tracked PID and updates run state to `canceled`.
- `IssueDetailSheet` running view gains a **Stop** button with three visual states: idle → stopping (spinner) → done.
- CORS allow-list updated to include `DELETE` / new `POST` verb pattern.

## Capabilities

### New Capabilities

- `stop-running-agent`: HTTP + orchestrator wiring that lets a caller terminate an in-progress agent session by issue ID, returning 202 on success and 404/409 on not-found/already-stopped.

### Modified Capabilities

<!-- none — no existing spec-level contracts change -->

## Impact

- **Backend**: `internal/web/server.go` (new route), `internal/orchestrator/` (new `StopAgent` method, PID lifecycle).
- **Frontend**: `packages/dashboard/src/components/IssueDetailSheet.tsx` (Stop button + fetch), `packages/dashboard/src/hooks/useSSE.ts` (no change needed — state updates propagate via existing SSE).
- **Tests**: `internal/web/server_test.go`, `internal/orchestrator/orchestrator_test.go`.
- **No breaking changes** to existing endpoints or wire format.
