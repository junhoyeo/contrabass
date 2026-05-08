# Orchestrator completion — fail-close on git_error

## ADDED Requirements

### Requirement: Verify gate SHALL fail-close on `git_error` from verifyBranchAdvanced

When `verifyBranchAdvanced` returns `(true, "git_error", err)` —
indicating the branch HEAD comparison could not be performed
because git operations themselves failed — the runtime SHALL
treat this as an unverified success and route the run to backoff
exactly like a confirmed `branch_unchanged` rejection. Only the
event name differs:

| return | event emitted | issue routed to |
|---|---|---|
| `(false, "branch_unchanged", nil)` | `success_unverified_branch_unchanged` | `enqueueContinuation` |
| `(true, "git_error", err)` | **`success_unverified_workspace_invalid`** | `enqueueContinuation` |
| `(true, "no_claim_head", nil)` | `verifier_skipped` (WARN log only) | proceed to Released |
| `(true, "", nil)` (advanced) | nothing | proceed to Released |

#### Scenario: git_error blocks Released and emits workspace_invalid event

- GIVEN a Succeeded run whose `verifyBranchAdvanced` returns
  `(true, "git_error", err)` (e.g. exit 128 from `git rev-parse`)
- WHEN the runtime processes the success transition
- THEN exactly one
  `issue event ... event=success_unverified_workspace_invalid`
  log line is emitted with `issue_id`, `attempt`, `branch`,
  `head`, and the `err` text
- AND `enqueueContinuation` is called with cause
  `"success_unverified_workspace_invalid"`
- AND `tracker.UpdateIssueState(Released)` is NOT invoked.

#### Scenario: no_claim_head still fail-opens to preserve retry recovery

- GIVEN `ClaimHeadSha` is empty and `verifyBranchAdvanced` returns
  `(true, "no_claim_head", nil)`
- WHEN the runtime processes the success transition
- THEN one WARN log entry with `verifier_skipped reason=no_claim_head`
  is emitted
- AND the run proceeds to Released (`tracker.UpdateIssueState(Released)`
  is called).

#### Scenario: verify-gate logs `err` text on workspace_invalid path

- GIVEN `verifyBranchAdvanced` returns `(true, "git_error", err)` where
  `err.Error() = "exit status 128"`
- WHEN the runtime emits the workspace_invalid event
- THEN the event payload contains the literal `err` text so the
  dashboard / SSE consumer can display the underlying git error.
