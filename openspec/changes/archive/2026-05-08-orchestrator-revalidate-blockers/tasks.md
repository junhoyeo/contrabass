## 1. Orchestrator — Core Logic

- [ ] 1.1 Add `releaseBlockedRunning` method to `Orchestrator` in `orchestrator.go`: iterate managed issue IDs, look up each in the fresh `[]types.Issue` slice, call `unresolvedBlockers` against the shared `openIDs` map
- [ ] 1.2 For each managed issue with unresolved blockers: send stop signal via existing run-signal channel, log `running_released_blocked_by` with blocker identifiers
- [ ] 1.3 After agent exits, call `tracker.MoveIssue(ctx, issueID, types.Todo)` to revert state; on failure log error and remove from managed set so the next tick retries the revert

## 2. Orchestrator — Integration into Poll Loop

- [ ] 2.1 Call `releaseBlockedRunning` in `orchestrator_runtime.go` main loop, after `dispatchUnclaimedIssues`, passing the same `issues` slice and `openIDs` map
- [ ] 2.2 Ensure `openIDs` map is built once per tick and shared between dispatch and revalidation (avoid duplicate allocation)

## 3. Tests

- [ ] 3.1 Unit test: managed issue gains a blocker → `releaseBlockedRunning` sends stop signal and moves issue to Todo
- [ ] 3.2 Unit test: managed issue with empty `BlockedBy` → agent not stopped
- [ ] 3.3 Unit test: managed issue whose blocker is already Done → agent not stopped
- [ ] 3.4 Unit test: `MoveIssue` fails → error logged, issue removed from managed set, no panic

## 4. Verification

- [ ] 4.1 `make test-race` passes (race detector clean — `openIDs` and managed-map access must be mutex-guarded)
- [ ] 4.2 Log output contains `running_released_blocked_by` in the race-condition scenario (integration smoke)
