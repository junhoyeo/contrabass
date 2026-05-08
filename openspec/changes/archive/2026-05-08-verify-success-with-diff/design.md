# Design: Verify success with branch advance

## Context

The orchestrator's run lifecycle is roughly:

1. `dispatchUnclaimedIssues` picks an unclaimed issue.
2. `dispatchIssue` calls `claimIssue` → tracker `ClaimIssue` →
   workspace creation → agent runner `Start`.
3. The runner emits agent events. The orchestrator consumes them via
   the hub. On `agent_event=finished status=Succeeded` (the field we
   target), the runtime transitions the run to a "Released" state and
   calls tracker `UpdateIssueState(Released)` → which the Linear
   adapter maps to the `Done` workflow state.
4. Workspace cleanup runs.

`claimIssue` already creates the issue branch (`workspaces/<id>` is a
git worktree at the issue's branch). At step 2 the branch HEAD equals
whatever main was at the moment of `git worktree add`. At step 3,
when the runner reports success, the branch HEAD should normally be
either the same SHA (if the work was a no-op) or a new commit
authored by the agent's worker.

omx-style runners use detached worktrees inside `.omx/team/.../worktrees/`
to do their actual codex work, then merge back into the issue branch.
A successful run produces at least one merge commit on the issue
branch — see `291ddff` in the ZII-50 trace.

A hollow run produces no commits at all. The reflog for the
branch shows only `branch: Created from HEAD`.

The verification opportunity is exactly here: at the transition from
`finished status=Succeeded` to `Released`, compare the branch's
current HEAD against the SHA captured at claim time. If they match,
the agent produced no commits and the success is hollow.

## Goals / Non-Goals

**Goals**

- Catch the unambiguous "agent produced zero output" failure mode
  observed on ZII-49 by inspecting branch HEAD at the success
  transition.
- Reroute a hollow success into the existing backoff/retry path so
  the issue is not marked Done in the tracker.
- Emit a distinct, observable log event so the failure mode is
  visible in dashboards / SSE / logs without log-grepping.
- Tracker-agnostic: works whether the issue lives in Linear, GitHub,
  or the local board. Branch comparison is purely git-local.

**Non-Goals**

- Anomaly detection on small but legitimate commits. The verifier
  catches *only* "branch HEAD unchanged". Anything that produces at
  least one commit passes — that's intentional simplicity.
- Replacing the `task_completed` ack protocol with a richer schema.
- Runner-side verification. Runners stay dumb event emitters; the
  policy lives in the orchestrator.
- Cross-attempt comparison. We compare HEAD at *this attempt's*
  claim, not at the issue's first ever claim, so a retry that
  legitimately makes progress on the second attempt still passes.

## Decisions

### Decision 1: Capture HEAD at claim time, not at run-start

`claimIssue` runs after `tracker.ClaimIssue` succeeds and after the
workspace is provisioned. By the time we read HEAD here, the branch
is at its baseline; any subsequent commit is unambiguously this
attempt's work.

If the workspace provisioning step itself committed
(it doesn't today, but might in some refactor), the verifier would
incorrectly count that as agent work. Document this as a caveat in
the helper godoc and bind the timing tightly to "right after
`tracker.ClaimIssue` and immediately before `runner.Start`".

### Decision 2: Use `git rev-parse <branch>` in the workspace, not in the main repo

The workspace at `workspaces/<id>` is a worktree pointing at the
issue branch. `git -C <workspace> rev-parse HEAD` is cheap, safe
under concurrent worker writes, and doesn't require taking a lock
on the main repo's index. The main repo's view of the branch could
be stale relative to what omx merged in the worker worktree's
checkout; the workspace's view is authoritative.

### Decision 3: Verifier fails open on git errors

If `git rev-parse` errors (worktree corrupted, branch deleted,
binary missing), the verifier returns `(advanced=true,
reason="git_error", err=err)` so the success path proceeds. We log
the underlying error at WARN. The justification: a real bug we
discover later is recoverable; aborting a legitimate run because
git was momentarily unhappy is destructive (forces the user to
manually re-Done the issue and re-merge).

### Decision 4: Hollow-success path reuses the existing backoff queue

`enqueueContinuation(issueID, attempt, cause)` already exists and
handles the retry-with-jitter behavior. Routing a hollow success
through it gives us the right semantics for free: the issue gets
re-claimed on a future poll cycle, attempt counter advances, and
backoff bounds the rate.

The `cause` string SHALL be
`success_unverified_branch_unchanged` so the backoff log line
distinguishes this failure from a normal agent error.

### Decision 5: Persist the claim HEAD on `RunAttempt`

The state struct that already carries `IssueID`, `Attempt`, `Phase`,
`StartTime` etc. gains one new field:

```go
ClaimHeadSha string
```

Populated by `claimIssue`, read by the success transition. JSON tag
`claim_head_sha,omitempty` so existing snapshot consumers ignore it
gracefully.

### Decision 6: Dashboard surfacing is a follow-up

Dashboards that already render the agent event stream will see the
new `success_unverified_branch_unchanged` log line. A purpose-built
banner ("⚠ run rejected: branch unchanged") can be added under
`improve-dashboard-liveness` once both changes archive. Out of scope
here.

## Risks / Trade-offs

- **Risk**: a legitimate task is documentation-only and its
  acceptance is a comment on the Linear issue, not code. Such a
  task today succeeds via this code path; under the new check it
  would be re-queued forever. Mitigation: this exact pattern hasn't
  been observed in the project yet; if it appears, we extend the
  check to also accept "PostComment was issued for this run" as a
  positive signal. The cost of the false positive is a retry, not
  data loss.

- **Risk**: an agent commits to a *worker* worktree but never
  merges into the issue branch (omx supervisor crash mid-merge).
  The verifier correctly reports "branch unchanged" and re-queues —
  this is the *correct* behavior, even though some real work
  exists in the orphaned worker worktree. The retry will re-run the
  task; the orphan commit becomes garbage on the next worktree
  prune. Acceptable.

- **Trade-off**: one extra `git rev-parse` per claim and per success
  transition. Sub-millisecond per call; negligible.

## Migration Plan

No persisted state migration. `ClaimHeadSha` is populated on every
new claim; existing in-flight runs (none, for a clean restart) get
zero-valued field which the verifier treats as "fail open" via
Decision 3.
