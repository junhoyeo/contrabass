# Orchestrator completion — verify branch advance

## ADDED Requirements

### Requirement: Orchestrator SHALL capture the workspace branch HEAD at claim time

`claimIssue` SHALL, immediately after `tracker.ClaimIssue` returns
without error and the workspace exists, resolve the workspace's
branch HEAD via `git -C <workspace> rev-parse HEAD` and store the
40-character SHA on the per-attempt state struct as
`RunAttempt.ClaimHeadSha`. Errors from `git rev-parse` SHALL be
logged at `WARN` and SHALL NOT abort the claim; instead
`ClaimHeadSha` is left empty (the verifier interprets empty as
"unknown" and fails open).

#### Scenario: Successful claim records HEAD

- GIVEN a fresh worktree at the issue branch with HEAD `abc1234…`
- WHEN `claimIssue` runs to completion
- THEN the persisted `RunAttempt` carries `ClaimHeadSha = "abc1234…"`
  (full 40-char SHA).

#### Scenario: `git rev-parse` failure does not block the claim

- GIVEN `git rev-parse HEAD` fails (e.g. broken worktree)
- WHEN `claimIssue` runs
- THEN the claim still succeeds, `ClaimHeadSha = ""` is recorded,
  and exactly one WARN-level log line names the issue and the git
  error.

### Requirement: Orchestrator SHALL verify branch advance before transitioning a Succeeded run to Released

When the runtime processes an `agent_event=finished status=Succeeded`
event for a run, it SHALL — before transitioning the issue to
`Released` and before calling `tracker.UpdateIssueState(Released)` —
invoke `verifyBranchAdvanced(workspace, branch, claimHead)` and act
on its result:

| verifier returns | orchestrator action |
|---|---|
| `(true, "", nil)` (advanced) | proceed with the existing Released path |
| `(false, "branch_unchanged", nil)` | reroute to the backoff path with cause `success_unverified_branch_unchanged`; do NOT transition the issue to Released; do NOT call `tracker.UpdateIssueState(Released)` |
| `(true, "git_error", err)` | log WARN with err, proceed with the existing Released path (fail open) |

`verifyBranchAdvanced` SHALL resolve the *current* branch HEAD via
`git -C <workspace> rev-parse <branch>` and compare against
`claimHead`. An empty `claimHead` (per the previous requirement)
SHALL be treated identically to `git_error`.

#### Scenario: Branch HEAD advanced — proceed normally

- GIVEN `claimHead = "abc1234…"`, current `git rev-parse <branch>` =
  `def5678…`
- WHEN the runner emits `finished status=Succeeded`
- THEN the run transitions to Released, the tracker receives
  `UpdateIssueState(Released)`, and **no**
  `success_unverified_branch_unchanged` event is emitted.

#### Scenario: Branch HEAD unchanged — reroute as backoff failure

- GIVEN `claimHead = "abc1234…"`, current `git rev-parse <branch>` =
  `abc1234…` (identical)
- WHEN the runner emits `finished status=Succeeded`
- THEN the orchestrator emits exactly one
  `success_unverified_branch_unchanged` event whose payload includes
  the issue id, attempt number, branch name, and the unchanged SHA;
  AND the issue is enqueued for backoff with cause
  `success_unverified_branch_unchanged`;
  AND `tracker.UpdateIssueState(Released)` is NOT called for this
  run.

#### Scenario: Empty `ClaimHeadSha` fails open

- GIVEN `RunAttempt.ClaimHeadSha == ""` (claim-time `git rev-parse`
  had errored)
- WHEN the runner emits `finished status=Succeeded`
- THEN the run transitions to Released as if the verifier had
  succeeded. Exactly one WARN-level log line names the issue and
  the reason `verifier_skipped_no_claim_head`.

#### Scenario: `git rev-parse` failure at success time fails open

- GIVEN `claimHead = "abc1234…"` and current `git rev-parse <branch>`
  errors
- WHEN the runner emits `finished status=Succeeded`
- THEN the run transitions to Released, exactly one WARN-level log
  line names the issue and the rev-parse error, and no
  `success_unverified_branch_unchanged` event is emitted.

#### Scenario: Failed runs bypass the verifier

- GIVEN the runner emits `agent_event=finished status=Failed`
- WHEN the runtime processes that event
- THEN the existing failure path runs unchanged; the verifier is NOT
  invoked.

### Requirement: The `success_unverified_branch_unchanged` event SHALL feed the existing backoff queue

When the verifier rejects a hollow success, the orchestrator SHALL
call its existing backoff entry point with cause
`success_unverified_branch_unchanged`. The retry SHALL be subject to
the same `MaxRetryBackoffMs` jitter and concurrency rules as any
agent-error retry, and SHALL NOT bypass them.

#### Scenario: Hollow success retries via the standard backoff queue

- GIVEN the verifier rejected a Succeeded event for issue I, attempt
  1
- WHEN the next poll cycle runs
- THEN the same backoff path that handles agent errors is observed
  to schedule a retry for issue I at attempt 2, AND the
  `BackoffEnqueued` event carries the `success_unverified_branch_unchanged`
  cause.
