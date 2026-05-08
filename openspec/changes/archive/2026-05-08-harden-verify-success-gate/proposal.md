# Harden verify-success-with-diff gate against fail-open exploits

## Why

The `verify-success-with-diff` change shipped with a deliberate fail-open
on git errors (Decision 3 of its design.md): when `git rev-parse` returns
non-zero, the gate logs WARN `verifier_skipped reason=git_error` and lets
the run proceed to `Released`. The intent was "a real bug we discover
later is recoverable; aborting a legitimate run because git was momentarily
unhappy is destructive."

Live evidence from a ZII-65 demo invalidates that trade-off:

```
12:24:33  ZII-65 claimed
12:24:35  agent_event=started
12:26:33  turn/completed
12:26:33  WARN verifier_skipped reason=git_error err="exit status 128"
12:26:35  agent_event=finished status=Succeeded
12:26:35  Linear ZII-65 → Done
```

The `exit status 128` is **not a transient git hiccup**. It's the
permanent state of every workspace this orchestrator creates today
because `internal/workspace/manager.Create` never actually calls
`git worktree add` — `workspaces/<uuid>/` is just a plain directory,
not a registered worktree. The fail-open path therefore fires on
**every codex run**, every time, defeating the entire point of the
verify gate.

Two compounding problems:

1. **Verify-gate is too permissive on `git_error`.** A persistent
   error condition should not be treated as transient. The gate must
   distinguish "git was momentarily unhappy" from "this workspace
   was never a worktree to begin with."

2. **Workspace creation never registers as a worktree.** The
   contrabass orchestrator hands codex a sandbox directory, but
   the directory is not the per-issue git worktree the gate
   expects. As a result, branch HEAD never advances visibly, and
   the gate has nothing solid to compare against.

## What Changes

- **Verify-gate fail-close on `git_error`** (immediate): the default
  switch branch in `orchestrator_runtime.handleAgentDone` no longer
  proceeds to the existing Released path when
  `verifyBranchAdvanced` returns `(true, "git_error", err)`. Instead
  it emits a new `success_unverified_workspace_invalid` event,
  routes the run to `enqueueContinuation` (backoff + retry), and
  declines the Released transition.
- **`no_claim_head` keeps fail-open**: an empty `ClaimHeadSha`
  (Decision 3 of the original spec) is genuinely "we never captured
  it" — that path stays as-is, since blocking it would penalize
  legitimate retries after a contrabass restart.
- **Workspace creation registers a real worktree** (medium-term):
  `internal/workspace/manager.Create` SHALL call `git worktree add`
  with a per-issue branch and verify the worktree appears in
  `git worktree list` before returning success. Existing
  `isRegisteredWorktree` helper already exists; this change makes it
  the fast-path check rather than the cleanup-on-reuse check.

## Impact

- Affected capability: `orchestrator-completion` (extends the
  `verify-success-with-diff` requirements with the new
  `workspace_invalid` rejection branch).
- New capability: `workspace-isolation` (covers the worktree
  creation contract).
- Affected code:
  - `internal/orchestrator/orchestrator_runtime.go` — switch arm
    for `git_error` flips to fail-close.
  - `internal/orchestrator/orchestrator.go` — verifyBranchAdvanced
    returns a richer error so `workspace_invalid` can be
    distinguished from a transient one (optional but cleaner).
  - `internal/workspace/manager.go` — `Create` now invokes
    `git worktree add` + verifies registration.
  - Tests in `internal/orchestrator/` and `internal/workspace/`.
- Out of scope:
  - Reworking the orchestrator's claim → workspace → runner
    sequencing.
  - Changing how runners (codex, omx, opencode) behave inside
    workspaces — they keep treating their `cwd` argument as the
    sandbox.
- Migration: stale workspaces left over from before this change
  remain plain directories. The new `Create` will tear them down
  and recreate as proper worktrees on first claim (re-using the
  existing `tear-down stale workspace` path from
  `41b14d3 fix(workspace): tear down stale workspace dirs before
  reuse`).
