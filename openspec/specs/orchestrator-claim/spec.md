# orchestrator-claim Specification

## Purpose
TBD - created by archiving change gate-claims-on-blocked-by. Update Purpose after archive.
## Requirements
### Requirement: Orchestrator SHALL skip dispatch when an issue has unresolved blockers

`dispatchUnclaimedIssues` SHALL, before invoking `dispatchIssue` for
each `types.Unclaimed` issue, evaluate the issue's `BlockedBy` slice
against the set of identifiers in the same dispatch batch (the slice
of issues just returned by `Tracker.FetchIssues`). When at least one
blocker identifier appears in that set, dispatch SHALL be skipped for
this cycle and a `dispatch_skipped_blocked_by` log event SHALL be
emitted recording the skipped issue and the unresolved blocker
identifiers.

#### Scenario: Skip when blocker is in the same batch

- GIVEN `FetchIssues` returns
  - `T1` — `Identifier="ZII-49"`, `State=Unclaimed`, `BlockedBy=[]`
  - `T2` — `Identifier="ZII-50"`, `State=Unclaimed`, `BlockedBy=["ZII-49"]`
- WHEN `dispatchUnclaimedIssues` runs against this batch
- THEN `claimIssue` is called for `T1` only;
  `T2` is skipped and a single
  `dispatch_skipped_blocked_by` log event is emitted with
  `blockers="ZII-49"`.

#### Scenario: Dispatch when blocker is absent from the batch

- GIVEN `FetchIssues` returns only
  - `T2` — `BlockedBy=["ZII-49"]`
  (because `ZII-49` has reached a terminal state and was filtered out
  by the tracker)
- WHEN `dispatchUnclaimedIssues` runs
- THEN `claimIssue` is called for `T2` and no
  `dispatch_skipped_blocked_by` event is emitted.

#### Scenario: Multiple blockers — skip if any is unresolved

- GIVEN a candidate has `BlockedBy=["ZII-49","ZII-44"]` and the batch
  contains `ZII-44` (still open) but not `ZII-49`
- WHEN dispatch runs
- THEN the candidate is skipped and the log event records
  `blockers="ZII-44"` (only the unresolved subset, not the entire
  `BlockedBy` list).

#### Scenario: Empty `BlockedBy` is unaffected

- GIVEN a candidate has `BlockedBy=[]` (or nil)
- WHEN dispatch runs
- THEN the gate does not fire, dispatch proceeds, and no
  `dispatch_skipped_blocked_by` event is emitted.

#### Scenario: Backoff (`RetryQueued`) issues bypass the gate

- GIVEN an issue `T2` with `BlockedBy=["ZII-49"]` and `ZII-49` is in
  the batch
- AND `T2` is being processed via the backoff continuation path
  (`processContinuation`), not the unclaimed-dispatch path
- WHEN the backoff cycle runs
- THEN `T2` is dispatched as before; the gate added by this change
  applies only to fresh `Unclaimed` dispatch in
  `dispatchUnclaimedIssues`. Rationale: an in-flight retry must not
  be silently re-blocked by upstream changes mid-attempt.

### Requirement: Orchestrator SHALL skip dispatch when an issue's identifier is found on mainRef

`dispatchUnclaimedIssues` SHALL, after the BlockedBy gate, search the git log
of `cfg.TrackerMainRef()` for commits whose message contains the issue's
`Identifier` as a whole word (using `git log <mainRef> --grep="\b<id>\b" -P`).

When a matching commit is found (`found=true`), dispatch SHALL be skipped for
this cycle. The orchestrator SHALL emit a `ClaimSkippedAlreadyImplemented` event
carrying `IssueIdentifier`, `CommitSHA`, `CommitSubject`, and `MainRef`, and
SHALL log a `dispatch_skipped_already_implemented` event. If
`cfg.TrackerAutoCloseAlreadyImplemented()` returns true, the orchestrator SHALL
call `TransitionToDone` on the tracker (Linear only; other adapters log a skip).

When the mainRef cannot be resolved by git (`unresolvable=true`), the
orchestrator SHALL emit `ClaimMainRefUnresolvable` at most once per dispatch
cycle (warn-once semantics) and SHALL fail open — dispatch proceeds as normal.

When no matching commit is found, dispatch proceeds without emitting any event.

The gate SHALL NOT run when `Identifier` is empty or whitespace-only.

#### Scenario: Skip when identifier is found on mainRef

- GIVEN `FetchIssues` returns `T1` with `Identifier="ABC-1"`, `State=Unclaimed`
- AND `git log main --grep='\bABC-1\b' -P -1` returns a commit SHA + subject
- WHEN `dispatchUnclaimedIssues` runs
- THEN `claimIssue` is NOT called for `T1`
- AND a `ClaimSkippedAlreadyImplemented` event is emitted with the commit details

#### Scenario: Word boundary prevents false positives

- GIVEN commits on main contain "fix: close ABC-12 story"
- WHEN searching for identifier "ABC-1"
- THEN the ABC-12 commit SHALL NOT match (`\b` word-boundary prevents it)

#### Scenario: Fail-open when mainRef is unresolvable

- GIVEN `mainRef` resolves to "no-such-ref" (unknown revision)
- WHEN `dispatchUnclaimedIssues` runs for any unclaimed issue
- THEN `claimIssue` IS called (fail-open)
- AND `ClaimMainRefUnresolvable` event is emitted at most once per cycle

#### Scenario: Auto-close when enabled and gate fires

- GIVEN `auto_close_already_implemented: true`
- AND the gate fires for issue `T1`
- AND the tracker implements `linearAutoCloser`
- THEN `TransitionToDone` SHALL be called with a comment citing the commit SHA

#### Scenario: Auto-close NOT called when disabled (default)

- GIVEN `auto_close_already_implemented: false` (default)
- AND the gate fires for issue `T1`
- THEN `TransitionToDone` SHALL NOT be called

