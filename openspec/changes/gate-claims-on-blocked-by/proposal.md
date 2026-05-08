# Gate orchestrator claims on `BlockedBy`

## Why

The Linear tracker currently hard-codes `BlockedBy: []string{}` in
`internal/tracker/linear.go:482`, so even if a user has wired
`Issue blocks → Issue` relations in Linear, contrabass cannot see them.
Worse, the orchestrator's main `dispatchUnclaimedIssues` loop never
consults `BlockedBy` — only `internal/team/allocation.go:61` uses it as a
soft scoring penalty inside an already-claimed task pool, and
`team_board.go:355` uses it for local-board parent/child rendering.

The combined effect: contrabass will happily claim a sub-task whose
blocker is still unfinished. In the OpenSpec-driven dispatch experiment
(parent ZII-47 with 5 sub-tasks ZII-49…ZII-53), the only workaround was
to manually park sub-tasks in Linear's `Backlog` state — defeating the
point of automated decomposition.

## What Changes

- **Producer (Linear)**: extend `fetchIssuesQuery` to pull each issue's
  `inverseRelations { type, issue { identifier } }`. Per Linear's
  schema convention an `IssueRelation` of type `"blocks"` reads as
  `issue blocks relatedIssue`, so an issue's *blockers* are the
  `relation.issue.identifier` values found in its **inverseRelations**.
  `normalizeIssue` populates `Issue.BlockedBy` from this list and stops
  hard-coding the empty slice.
- **Consumer (orchestrator)**: in `dispatchUnclaimedIssues`, build a set
  of identifiers for every issue currently in the fetched candidate
  slice (i.e. issues that are visible to the orchestrator and therefore
  not in a Linear-terminal state). For each candidate with a non-empty
  `BlockedBy`, skip dispatch when **any** blocker identifier is still in
  that set. Emit a `dispatch_skipped_blocked_by` log line so the skip is
  observable.

## Impact

- Affected capabilities: `tracker-linear` (new behavior on
  `FetchIssues`), `orchestrator-claim` (new gating step before
  `claimIssue`).
- Affected code: `internal/tracker/linear.go`, `internal/tracker/linear_test.go`,
  `internal/orchestrator/orchestrator.go`,
  `internal/orchestrator/orchestrator_test.go`.
- Out of scope: GitHub Issues tracker (already populates `BlockedBy` via
  `ParseBlockedBy` body parsing — the orchestrator gate added here will
  start working for it automatically and is verified by an existing
  GitHub fixture). Local board (`internal/tracker/local.go`) likewise
  already populates `BlockedBy`; same auto-benefit applies.
- Not addressed: cross-project Linear blockers. If a blocker lives in
  another Linear project that is not in `projectSlug`, it will not appear
  in the candidate set and is therefore treated as completed. Acceptable
  for the current single-project workflow; flagged in `design.md`.
