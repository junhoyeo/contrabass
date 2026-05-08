# Verify success with branch advance

## Why

Contrabass currently treats `agent_event=finished status=Succeeded` as
authoritative. Linear is moved to `Done` on the strength of that event
alone, with no check that the agent actually produced any output. The
result, observed live during the OpenSpec sub-task chain experiment:

- **ZII-49 (T1)** — agent ran for 15:33, omx team reported success,
  contrabass logged `status=Succeeded`, Linear transitioned to `Done`.
  The issue's git branch HEAD never moved from the claim point. Zero
  commits, zero edits in any worker worktree. The reflog has exactly
  one line for the branch: `branch: Created from HEAD`.

- **ZII-50 (T2)** — same harness, same model, same prompt shape; this
  one DID produce a real merge commit (`291ddff Merge commit 'cfe92bb'`)
  and the branch HEAD advanced. Same `status=Succeeded` arrived; same
  Linear transition. Indistinguishable to contrabass.

The 15:30 timer hits `agent_timeout_ms`, contrabass `run_stop_done_closed`
+ re-claims + emits a near-instant second `attempt=1 finished
status=Succeeded`. Both the legitimate ZII-50 path and the hollow ZII-49
path go through the exact same code branch on success. The only
information that distinguishes them is **the workspace branch HEAD**,
which the orchestrator never inspects.

This change adds that inspection. A `Succeeded` event whose branch HEAD
has not advanced from the claim point is reclassified as a failure and
re-queued, never marked Done in Linear.

## What Changes

- At claim time, capture the workspace's `HEAD` SHA on the issue's
  branch and persist it on the per-run attempt state
  (`RunAttempt.ClaimHeadSha`).
- At success transition (the path that handles
  `agent_event=finished status=Succeeded` and would transition the
  issue to `Released` / `Done`), call a new helper
  `verifyBranchAdvanced(workspace, branch, claimHead) (bool, string,
  error)`. The helper resolves the branch's current HEAD via `git
  rev-parse <branch>` in the workspace and reports whether it differs
  from the claimed-at SHA.
- If `verifyBranchAdvanced` returns `(false, "branch_unchanged", nil)`:
  - Treat the run as **failed**, not succeeded.
  - Emit a structured event
    `success_unverified_branch_unchanged` carrying the issue id,
    attempt number, branch name, and the unchanged SHA.
  - Route into the existing backoff path
    (`enqueueContinuation` with a non-nil cause) so a retry is
    scheduled instead of a `Done` transition.
- The helper SHALL fail open: if `git rev-parse` itself errors out
  (corrupt worktree, missing branch), treat verification as
  inconclusive and let the legacy success path proceed. Better to
  miss a real bug than break a legitimate run.

## Impact

- Affected capabilities: `orchestrator-completion` (NEW — covers the
  agent-success transition path; sibling to `orchestrator-claim`
  defined in `gate-claims-on-blocked-by`).
- Affected code:
  - `internal/orchestrator/state.go` (or `types.RunAttempt` —
    the new persistent field),
  - `internal/orchestrator/orchestrator.go` (capture HEAD in
    `claimIssue`, gate in the success transition),
  - `internal/orchestrator/orchestrator_test.go`,
  - one new helper file or a small addition to
    `internal/orchestrator/orchestrator_runtime.go`.
- Out of scope: any change to runners. The verification operates on
  the orchestrator-owned workspace branch, which every runner shares.
  No runner-specific logic.
- Not addressed: detecting a real-but-tiny commit that doesn't
  satisfy the task. That's an LLM-level review concern; this change
  only catches the all-zero case (branch HEAD literally unchanged),
  which is the easy and unambiguous failure mode.
- Future signal expansion: once `improve-dashboard-liveness` lands
  the `diff_added/removed/files` snapshot fields, the verifier can
  also consult those for additional confidence (but is not required
  to).
