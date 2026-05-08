# Design: Harden verify-success-with-diff gate

## Context

`verify-success-with-diff`'s Decision 3 traded false-positive risk
for false-negative risk: prefer "let a legitimate run land" over
"block a possibly-bad run." That trade-off assumed `git rev-parse`
errors would be rare and transient. The ZII-65 demo proves the
assumption wrong — every contrabass run produces a persistent
`exit status 128` because workspaces are not real git worktrees.
The gate fails open on every run, marks every run Done, and the
hollow-success class of bug returns in full force.

The fix is two-pronged. The orchestrator side (B) flips the
fail-open branch for the specific `git_error` reason so the gate
stops rubber-stamping. The workspace side (A) eliminates the root
cause by making `Create` produce real worktrees, so the happy path
fires correctly and the gate has accurate signal.

## Decisions

### Decision 1: Fail-close on `git_error`, not fail-open

`verifyBranchAdvanced` already discriminates between
`branch_unchanged`, `git_error`, and `no_claim_head`. We treat each
reason explicitly:

| reason | new behavior | rationale |
|---|---|---|
| `branch_unchanged` | enqueue continuation (unchanged) | hollow run |
| `git_error` | **enqueue continuation** with cause `success_unverified_workspace_invalid` | persistent workspace-creation bugs would otherwise rubber-stamp every run |
| `no_claim_head` | proceed to Released (unchanged) | claim-time SHA capture genuinely failed; blocking penalizes legitimate retries |

The new event name `success_unverified_workspace_invalid` matches
the new failure mode and stays distinct from the
`success_unverified_branch_unchanged` already established by
`verify-success-with-diff`.

### Decision 2: Workspace.Create must register a real worktree

`internal/workspace/manager.Create` SHALL call `git worktree add
-b <branch> <path>` (or the existing branch variant if the branch
already exists) and SHALL verify success via the existing
`isRegisteredWorktree` helper before returning. If `git worktree
add` fails, `Create` returns an error; the caller (claim path)
handles the failure normally.

Stale plain directories from older orchestrator versions are
already torn down by `41b14d3 fix(workspace): tear down stale
workspace dirs before reuse`. That tear-down path stays.

### Decision 3: Per-issue branch naming stays

`Issue.BranchName` is already populated upstream
(`symphony/<lower-id>` pattern). `Create` uses it as the
`-b <branch>` argument. No naming change.

### Decision 4: ClaimHeadSha now refers to the worktree's HEAD

When `Create` returns, the worktree's HEAD is at whatever main
branch it was forked from (the orchestrator's checkout). The
existing `claimIssue` code captures HEAD via `git -C <ws>
rev-parse HEAD` — that call now succeeds (worktree exists), and
the SHA reflects the issue branch's tip at claim time. The
verify-gate comparison becomes meaningful: any agent commit on the
issue branch advances HEAD past `ClaimHeadSha` and the gate
returns `(true, "", nil)`.

## Risks / Trade-offs

- **Risk**: legitimate transient git errors (disk full, NFS hiccup)
  now block runs. Mitigation: such errors should be rare; backoff
  retry attempts will succeed once the underlying condition
  clears. The user can always force a state transition manually
  via Linear if a real run is wrongly blocked.
- **Risk**: `git worktree add` fails for some issue branches that
  somehow already exist in a corrupted state. Mitigation: the
  tear-down-stale-dirs path already handles plain-dir collisions;
  this change extends it to also `git worktree remove` registered
  worktrees if the path is dirty.
- **Trade-off**: workspace creation cost goes from "mkdir + clone
  files" to "git worktree add" — 5 ms vs 50–200 ms depending on
  the project. Negligible at the orchestrator's scale (≤8
  concurrent claims).

## Migration Plan

- Decision 1 ships as a one-line switch arm change with no wire
  format implications.
- Decision 2 ships as a `Create` body rewrite. Existing callers
  see the same `(workspace string, error)` signature.
- Existing stale workspaces under `workspaces/<uuid>/` get cleaned
  up by the existing tear-down path on the first new claim.
