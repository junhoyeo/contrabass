## 1. Go Backend — Orchestrator

- [ ] 1.1 Add `StopAgent(issueID string) error` method to orchestrator: look up PID in running map (under mutex), call `os.FindProcess`, send SIGTERM, wait up to 5 s, send SIGKILL if still alive
- [ ] 1.2 After kill, remove entry from running map and emit a canceled/stopped state update so SSE subscribers receive the updated snapshot
- [ ] 1.3 Add `StopAgent` to the `SnapshotProvider` interface (or introduce a separate `AgentStopper` interface) in `internal/web/`

## 2. Go Backend — HTTP Endpoint

- [ ] 2.1 Add `POST /api/v1/running/{issue_id}/stop` route in `server.go` `newMux()`
- [ ] 2.2 Implement `handleStopAgent`: validate `issue_id`, call `orchestrator.StopAgent`, return 202 on success, 404 if not running
- [ ] 2.3 Update `withCORS` allow-methods to include the new verb if needed

## 3. Go Backend — Tests

- [ ] 3.1 Unit test `StopAgent`: mock process, verify SIGTERM sent, verify entry removed from snapshot
- [ ] 3.2 HTTP handler test in `server_test.go`: 202 for running entry, 404 for unknown issue_id

## 4. Frontend — Stop Button

- [ ] 4.1 Add `stopping` boolean state to `IssueDetailSheet` component
- [ ] 4.2 Render Stop button at the bottom of the running-kind section (above the debug block)
- [ ] 4.3 On click: set `stopping = true`, `POST /api/v1/running/{issue_id}/stop`, reset `stopping` on error
- [ ] 4.4 Add `zhCN.detail.stop*` i18n keys (`stopAgent`, `stopping`) to `messages.ts`
- [ ] 4.5 Auto-clear `stopping` state when `data` transitions away from running kind (SSE update received)

## 5. Verification

- [ ] 5.1 `make test` passes (Go unit + race)
- [ ] 5.2 `make test-dashboard` passes (bun test)
- [ ] 5.3 Manual smoke: click Stop on a running agent, confirm entry leaves running list within ~6 s
