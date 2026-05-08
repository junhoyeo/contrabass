# Orchestrator claim gating on `BlockedBy`

## ADDED Requirements

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
