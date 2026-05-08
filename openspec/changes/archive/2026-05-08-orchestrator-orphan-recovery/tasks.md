## 1. Orchestrator — Core Recovery Logic

- [x] 1.1 Add `recoverOrphanedClaims(issues []types.Issue)` method to `Orchestrator` in `orchestrator.go`: iterate issues, for each with `State == types.Claimed` check if `o.isManagedIssue(issue.ID)` returns false, override `issue.State = types.Unclaimed`
- [x] 1.2 Track a `recoveredThisCycle map[string]struct{}` (reset each tick) to log `orphan_claim_recovered` only on first detection per tick, avoiding log spam

## 2. Orchestrator — Integration into Poll Loop

- [x] 2.1 Call `recoverOrphanedClaims(issues)` in the poll loop (orchestrator_runtime.go) immediately before `dispatchUnclaimedIssues`, passing the same mutable issues slice

## 3. Tests

- [x] 3.1 Unit test: issue is `Claimed` (linear "started") + not in managed set → state overridden to `Unclaimed`, `orphan_claim_recovered` logged
- [x] 3.2 Unit test: issue is `Claimed` + present in managed set → state unchanged, no log
- [x] 3.3 Unit test: issue is `Unclaimed` (linear "unstarted") → state unchanged
- [x] 3.4 Integration scenario: simulate restart (empty managedIssues, In-Progress issue in snapshot) → issue dispatched on first tick

## 4. Verification

- [x] 4.1 `make test-race` passes
- [x] 4.2 Manual smoke: restart orchestrator with In-Progress issues in Linear → confirm agents re-dispatched within one poll interval, `orphan_claim_recovered` appears in log
