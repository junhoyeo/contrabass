# Tasks: Verify success with branch advance

## T1 — Add `ClaimHeadSha` to `RunAttempt`

**Files**: wherever `RunAttempt` is declared (likely
`internal/types/types.go` — verify with `grep -n "type RunAttempt"
internal/`).

**Contract**: Add a single new field, additive only, no rename:

```go
ClaimHeadSha string `json:"claim_head_sha,omitempty"`
```

The `omitempty` tag keeps existing snapshot consumers from seeing a
new key when the field is empty, preserving JSON shape under the
"workspace had no git" fallback path.

**Acceptance**:
- Field name + type + JSON tag exact.
- `go build ./...` clean.
- No test added in this task.

**Depends on**: none.
**Blocks**: T2, T3.

---

## T2 — Capture HEAD in `claimIssue`

**Files**: `internal/orchestrator/orchestrator.go` (the `claimIssue`
function near line 399 per the current layout — verify by grep).

**Contract**: After the existing successful return path of
`tracker.ClaimIssue` and after the workspace exists, but BEFORE
returning from `claimIssue`, run:

```go
sha, err := workspaceHeadSHA(ctx, issue.Workspace)
if err != nil {
    o.logger.Warn("claim_head_sha_unavailable",
        "issue_id", issue.ID, "err", err)
    sha = ""
}
attempt.ClaimHeadSha = sha
```

Add a new helper somewhere alongside the existing workspace helpers:

```go
// workspaceHeadSHA returns the 40-char HEAD SHA of the workspace's
// current branch. Caller logs/handles the error and falls back to
// an empty SHA, which the verifier will treat as "unknown".
func workspaceHeadSHA(ctx context.Context, workspace string) (string, error)
```

Implementation: `git -C <workspace> rev-parse HEAD`, with a 2-second
context timeout, trimming the trailing newline. No new module
dependency.

**Acceptance**:
- `claimIssue` always populates `ClaimHeadSha` before storing the
  attempt; missing workspace or git error fills it with `""` and
  logs WARN.
- Helper has a 2-second timeout via `exec.CommandContext`.
- `go vet ./...` clean.

**Depends on**: T1.
**Blocks**: T3.

---

## T3 — Add `verifyBranchAdvanced` and gate the Succeeded transition

**Files**: `internal/orchestrator/orchestrator.go` for the gate site;
`internal/orchestrator/orchestrator_runtime.go` (or wherever the
`finished status=Succeeded` event is consumed — verify by grep for
`status=Succeeded` and `Released`).

**Contract**:

```go
// verifyBranchAdvanced compares the workspace branch's current HEAD
// against the claim-time SHA. Returns (advanced, reason, err):
//   - (true,  "",                   nil) → branch moved; let success proceed.
//   - (false, "branch_unchanged",   nil) → hollow run; reject success.
//   - (true,  "git_error",          err) → fail open (caller logs WARN).
//   - (true,  "no_claim_head",      nil) → claimHead was empty; fail open.
func verifyBranchAdvanced(ctx context.Context, workspace, branch, claimHead string) (bool, string, error)
```

At the success transition (immediately before the existing
`UpdateIssueState(Released)` call):

```go
advanced, reason, err := verifyBranchAdvanced(
    ctx, attempt.Workspace, issue.BranchName, attempt.ClaimHeadSha)
switch {
case advanced && reason == "":
    // proceed with existing Released path
case !advanced && reason == "branch_unchanged":
    logging.LogIssueEvent(o.logger, issue.ID,
        "success_unverified_branch_unchanged",
        "attempt", attempt.Attempt,
        "branch", issue.BranchName,
        "head", attempt.ClaimHeadSha,
    )
    o.enqueueContinuation(issue.ID, attempt.Attempt,
        "success_unverified_branch_unchanged")
    return  // skip Released transition
default:
    // (true, "git_error" | "no_claim_head", err) — fail open
    if err != nil {
        o.logger.Warn("verifier_skipped",
            "issue_id", issue.ID, "reason", reason, "err", err)
    } else {
        o.logger.Warn("verifier_skipped",
            "issue_id", issue.ID, "reason", reason)
    }
    // proceed with existing Released path
}
```

**Acceptance**:
- The gate sits BEFORE `UpdateIssueState(Released)` and BEFORE the
  workspace-cleanup step, so a rejected hollow success leaves no
  stale Linear state.
- `enqueueContinuation` is reached with the exact cause string
  `"success_unverified_branch_unchanged"` (so the existing
  backoff log line is greppable).
- The Failed-status event path is NOT touched.
- `go build ./...` and `go vet ./...` clean.

**Depends on**: T1, T2.
**Blocks**: T4, T5.

---

## T4 — Unit tests for the helper

**Files**: a new test in `internal/orchestrator/orchestrator_test.go`
(or extract to a new `verify_test.go` if preferred — same package).

**Contract**: Drive `verifyBranchAdvanced` and `workspaceHeadSHA`
against a real on-disk git repo created with `t.TempDir()`. Tests:

1. `TestWorkspaceHeadSHA_FreshRepo` — initialize a temp repo,
   commit one file, assert the helper returns the commit SHA
   trimmed.
2. `TestWorkspaceHeadSHA_BadPath` — non-existent path returns
   non-nil error.
3. `TestVerifyBranchAdvanced_HeadMatches` — claim-head equals
   current HEAD → `(false, "branch_unchanged", nil)`.
4. `TestVerifyBranchAdvanced_HeadDiffers` — claim-head differs
   from current HEAD → `(true, "", nil)`.
5. `TestVerifyBranchAdvanced_EmptyClaimHead` —
   `claimHead == ""` → `(true, "no_claim_head", nil)`.
6. `TestVerifyBranchAdvanced_GitError` — point at a non-repo
   directory → `(true, "git_error", non-nil)`.

**Acceptance**:
- All listed cases pass: `go test ./internal/orchestrator/ -run
  "TestWorkspaceHeadSHA|TestVerifyBranchAdvanced" -count=1 -v`.
- No external git server, no network. Each subtest in its own
  `t.TempDir()`.

**Depends on**: T2, T3.
**Blocks**: none.

---

## T5 — Integration test for the gate

**Files**: `internal/orchestrator/orchestrator_test.go` — add a
table-driven `TestSuccessGate_HollowRunReroutesToBackoff` using
the existing `observingTracker` + `MockRunner` harness.

**Contract**: Two scenarios:

1. **hollow success rejected** — workspace branch stays at the
   claim SHA, runner emits `finished status=Succeeded`. Assert:
   - `mt.UpdateIssueStateCount(<issue>, types.Released)` remained
     `0` (Released transition never happened).
   - the run is enqueued for backoff (the existing
     `BackoffEnqueued` event arrives with cause field including
     `success_unverified_branch_unchanged`).
   - exactly one `success_unverified_branch_unchanged` orchestrator
     event was observed in the `eventCollector`.

2. **real success proceeds** — workspace receives a synthetic
   commit (`git commit --allow-empty`) before the runner emits
   `Succeeded`. Assert:
   - `mt.UpdateIssueStateCount(<issue>, types.Released) >= 1`.
   - no `success_unverified_branch_unchanged` event.

The harness already provides workspace creation via
`workspace.NewMockManager`. The test SHALL drop a `.git` dir into
the mock workspace (or call `git init` via `exec.Command`) so that
`git rev-parse HEAD` succeeds. This is the most fiddly part of the
task; budget the time for it accordingly.

**Acceptance**:
- `go test ./internal/orchestrator/ -run TestSuccessGate -count=1
  -race -v` passes.
- No pre-existing test in the package is modified.
- The test does not depend on real network or external repositories.

**Depends on**: T3, T4.
**Blocks**: none.

---

## Rejection rules (apply to ALL tasks)

A diff that satisfies any of the following MUST be rejected:

1. The diff modifies only `*_test.go` files for T1, T2, or T3
   (production code is mandatory).
2. The diff modifies any file outside `internal/orchestrator/**`,
   `internal/types/**`, or the explicit task `Files` list.
3. The diff adds a new go module dependency. Stdlib only
   (`os/exec` + `context` are sufficient).
4. The diff modifies the Failed-status path in any way (T3 must keep
   its blast radius on the Succeeded path).
5. The diff changes the existing semantics of `enqueueContinuation`
   or its log line — the new code MUST reuse the existing entry
   point with a new cause string only.
6. The diff omits any acceptance bullet from the task it claims to
   implement.

## Task graph

```
T1 ── T2 ── T3 ──┬── T4
                 └── T5
```

T4 and T5 may proceed in parallel after T3 lands. T2 cannot start
without T1 (the field must exist). T3 needs both T1 and T2.
