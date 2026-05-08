# Workspace isolation — guaranteed per-issue git worktree

## ADDED Requirements

### Requirement: workspace.Manager.Create SHALL register a real git worktree

`internal/workspace/manager.Manager.Create` SHALL invoke
`git worktree add -b <branch> <path> <baseRef>` (or the existing
`-B` variant when the branch already exists) for every claimed
issue and SHALL verify success by re-checking
`isRegisteredWorktree(ctx, path)` before returning the workspace
path to the caller. When the underlying `git worktree add`
command fails for any reason — including pre-existing dirty
directory at `path`, branch-already-checked-out elsewhere, or
shallow-clone limitations — `Create` SHALL return a non-nil error
and SHALL NOT return a workspace path.

#### Scenario: First claim registers a fresh worktree

- GIVEN a clean orchestrator state with no `workspaces/<id>/`
  directory
- WHEN `Manager.Create(ctx, issue)` is called
- THEN `git worktree list --porcelain` includes a line for the
  new path
- AND the working directory under that path contains a `.git`
  pointer file (worktree-style)
- AND the function returns `(workspacePath, nil)`.

#### Scenario: Stale plain directory is torn down then recreated as a worktree

- GIVEN `workspaces/<id>/` already exists as a plain directory
  (not a registered worktree, e.g. left over from a prior
  contrabass version)
- WHEN `Manager.Create(ctx, issue)` is called for the same issue
- THEN the plain directory is removed via the existing
  tear-down path
- AND `git worktree add` is invoked
- AND `isRegisteredWorktree(ctx, path)` returns true at the end
- AND the function returns `(workspacePath, nil)`.

#### Scenario: git worktree add failure surfaces as Create error

- GIVEN the issue branch is already checked out in another
  worktree (rare; an external tool created it)
- WHEN `Manager.Create(ctx, issue)` is called
- THEN `Create` returns a non-nil error wrapping the underlying
  `git worktree add` exit status
- AND no half-initialized directory is left behind at the target
  path.

### Requirement: ClaimHeadSha SHALL refer to the new worktree's HEAD

After `Manager.Create` returns successfully, `claimIssue`'s
`workspaceHeadSHA` call SHALL execute against the registered
worktree (not the parent repo) and SHALL return the issue
branch's tip SHA. The orchestrator SHALL store this on
`RunAttempt.ClaimHeadSha` for the verify-gate to compare against.

#### Scenario: ClaimHeadSha matches the registered worktree HEAD

- GIVEN a freshly created worktree at HEAD `abc1234…`
- WHEN `claimIssue` runs
- THEN `RunAttempt.ClaimHeadSha == "abc1234…"`
- AND `git rev-parse HEAD` inside the workspace returns the same
  SHA.
