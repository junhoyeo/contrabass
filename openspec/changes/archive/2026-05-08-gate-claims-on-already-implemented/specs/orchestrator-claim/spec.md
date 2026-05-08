# orchestrator-claim delta — gate-claims-on-already-implemented

## ADDED Requirements

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
