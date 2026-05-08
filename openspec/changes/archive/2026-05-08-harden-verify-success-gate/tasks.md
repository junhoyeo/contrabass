# Tasks: Harden verify-success-with-diff gate

> Status (2026-05-07): T1 + T2 + T3 all landed on `feat/dashboard-zh-cn-pr`.
> Commits: T1 — earlier on this branch (success_unverified_workspace_invalid arm),
> T2 — `b4b672a fix(workspace): create per-issue git worktree on Issue.BranchName`,
> T3 — `ce442c8 test(orchestrator,workspace): cover harden-verify-success-gate`.
> Ready for archive into `openspec/specs/` once verified in production.

## T1 — Fail-close on `git_error` in the verify-gate switch

**Files**: `internal/orchestrator/orchestrator_runtime.go`.

**Contract**: Inside the existing
`if finalAttempt.Phase == types.Succeeded` block where
`verifyBranchAdvanced` returns are switched, replace the
`default` arm (which currently fails open on every non-advanced
non-`branch_unchanged` reason) with three explicit arms:

```go
case advanced && reason == "":
    // proceed (unchanged)
case !advanced && reason == "branch_unchanged":
    // emit success_unverified_branch_unchanged + enqueueContinuation (unchanged)
    return
case advanced && reason == "git_error":
    logging.LogIssueEvent(o.logger, issueID,
        "success_unverified_workspace_invalid",
        "attempt", finalAttempt.Attempt,
        "branch", entry.issue.BranchName,
        "head", finalAttempt.ClaimHeadSha,
        "err", err.Error(),
    )
    o.enqueueContinuation(issueID, finalAttempt.Attempt,
        "success_unverified_workspace_invalid")
    return
case advanced && reason == "no_claim_head":
    o.logger.Warn("verifier_skipped",
        "issue_id", issueID, "reason", reason)
    // proceed
default:
    o.logger.Warn("verifier_skipped",
        "issue_id", issueID, "reason", reason, "err", err)
    // proceed (truly unknown future reason)
```

**Acceptance**:
- `git_error` reason takes the new `success_unverified_workspace_invalid`
  branch and returns before the Released path.
- `no_claim_head` reason still fails-open (proceeds).
- An unknown future reason continues to fail-open with an
  explicit WARN log.
- `go vet ./...` clean.

**Depends on**: none.
**Blocks**: T3 (test).

---

## T2 — workspace.Manager.Create registers a real git worktree

**Files**: `internal/workspace/manager.go`.

**Contract**: The body of `(m *Manager) Create(ctx, issue)` SHALL:

1. Compute `workspacePath := filepath.Join(m.root, issue.ID)`.
2. If the path exists AND is NOT a registered worktree, call the
   existing tear-down helper (re-use whatever
   `41b14d3 fix(workspace): tear down stale workspace dirs before
   reuse` exposes).
3. If the path is already a registered worktree at the correct
   branch, return `(workspacePath, nil)` without invoking
   `git worktree add` again.
4. Otherwise invoke `git worktree add -b <issue.BranchName>
   <workspacePath> <baseRef>` (where `baseRef` is the orchestrator's
   current checkout). On exit non-zero, return an error wrapping
   the stderr text.
5. Re-verify via `isRegisteredWorktree(ctx, workspacePath)` and
   return `(workspacePath, nil)` only if it returns true.

**Acceptance**:
- Every `Create` call results in `git worktree list --porcelain`
  containing the new path.
- Existing tests in `internal/workspace/` still pass.
- `git worktree add` failures surface as wrapped errors.

**Depends on**: none.
**Blocks**: T3.

---

## T3 — Tests for both fixes

**Files**: `internal/orchestrator/orchestrator_test.go`,
`internal/workspace/manager_test.go`.

**Contract**:

1. `TestVerifyGate_GitErrorRoutesToWorkspaceInvalid` — mock
   tracker + mock workspace returning a path that's not a real
   worktree; assert `success_unverified_workspace_invalid` event
   fires and `Released` is NOT called.
2. `TestVerifyGate_NoClaimHeadStillProceedsToReleased` — empty
   `ClaimHeadSha` still releases (unchanged contract).
3. `TestWorkspaceManager_CreateRegistersWorktree` — create on a
   real `t.TempDir()` git repo, assert the new path appears in
   `git worktree list --porcelain`.
4. `TestWorkspaceManager_CreateTearsDownStaleDir` — pre-create a
   plain directory at the expected path, then call `Create`,
   assert it ends up registered as a worktree.

**Acceptance**:
- `go test ./internal/orchestrator/ ./internal/workspace/ -count=1
  -race -v` passes the four new tests.

**Depends on**: T1, T2.
**Blocks**: none.

---

## Rejection rules (apply to ALL tasks)

1. The diff modifies only `*_test.go` files for T1 or T2.
2. The diff modifies any file outside
   `internal/orchestrator/**`, `internal/workspace/**`.
3. The diff adds a new Go module dependency.
4. The diff weakens an existing fail-close path back to
   fail-open.
5. The diff changes the `success_unverified_branch_unchanged`
   contract from `verify-success-with-diff`. That existing
   requirement is unmodified by this change.

## Task graph

```
T1 ─┐
T2 ─┴── T3
```

T1 and T2 can land in parallel; T3 follows once both are in.
