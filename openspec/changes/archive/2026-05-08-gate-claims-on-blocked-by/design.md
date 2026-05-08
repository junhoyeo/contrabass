# Design: Gate claims on `BlockedBy`

## Context

Three code paths intersect:

1. `internal/tracker/linear.go` — `FetchIssues` paginates Linear's
   `issues` query, runs each node through `normalizeIssue`, and returns
   the slice. `BlockedBy` is hard-coded `[]string{}`.
2. `internal/types/types.go` — `Issue` already declares
   `BlockedBy []string` with JSON tag `blocked_by`. No struct change
   needed.
3. `internal/orchestrator/orchestrator.go` —
   `dispatchUnclaimedIssues(ctx, …, issues, …)` iterates and, for each
   `types.Unclaimed` issue, calls `dispatchIssue` → `claimIssue`. There
   is no pre-claim gate beyond `canDispatch`/`isManagedIssue`.

The Linear schema for `IssueRelation` (verified against Linear's public
GraphQL schema) is `{ issue, relatedIssue, type }` where for
`type == "blocks"` the `issue` is the source of the verb — i.e. it is
the blocker. So:

- `issue.relations.nodes[?type=blocks].relatedIssue.identifier` =
  identifiers this issue is blocking (downstream).
- `issue.inverseRelations.nodes[?type=blocks].issue.identifier` =
  identifiers blocking this issue (the *BlockedBy* set).

We read **only** `inverseRelations` to populate `BlockedBy`; reading
`relations` is unnecessary and would inflate the field with downstream
identifiers.

## Goals / Non-Goals

**Goals**

- Producer side reads Linear's `inverseRelations` and populates
  `Issue.BlockedBy` with stable identifiers (e.g. `ZII-49`).
- Orchestrator skips dispatching any unclaimed issue whose `BlockedBy`
  intersects the set of currently-visible candidates.
- Skip is observable via a structured log event.
- Behavior is tracker-agnostic: GitHub and local-board trackers already
  populate `BlockedBy` and benefit automatically.

**Non-Goals**

- Multi-tracker BlockedBy (e.g. a Linear issue blocked by a GitHub
  issue). Out of scope; would require cross-tracker resolution.
- Cross-project Linear blockers. Out of scope per proposal.
- A separate `Tracker.LookupIssueStates` method. Treating "absent from
  candidate set ⇒ completed" is sufficient for single-project workflows
  and avoids a new tracker API surface.
- Hardening Linear-relation pagination beyond a single page. Linear's
  default page size of 50 inverse relations is well above any realistic
  blocker count.

## Decisions

### Decision 1: Use `inverseRelations`, not `relations`, for `BlockedBy`

`relations` returns the issue's *outgoing* edges, which for
`type=blocks` mean "what this issue blocks" — the wrong direction.
`inverseRelations` returns *incoming* edges. For `type=blocks` the
incoming edge is "X blocks me", i.e. exactly the `BlockedBy` set.

This is the canonical Linear convention. Workflows that wired their
relations using the inverse convention (passing `issueId=blocked,
relatedIssueId=blocker, type=blocks`) will see `BlockedBy=[]` and need
to fix their tooling — not contrabass.

### Decision 2: Gate inside `dispatchUnclaimedIssues`, not inside `FetchIssues`

`FetchIssues` already strips Linear-terminal states; adding the
`BlockedBy` gate there would couple two distinct concerns and would
break for non-Linear trackers. Putting the gate in
`dispatchUnclaimedIssues` lets every tracker contribute `BlockedBy`
through whatever native mechanism it has (Linear inverseRelations,
GitHub body parsing, local-board JSON), and keeps the gate logic in one
place.

### Decision 3: "Blocker is open" ≡ "blocker identifier appears in fetched candidate set"

The orchestrator builds `openIDs := { issue.Identifier for issue in
issues }` once per dispatch cycle. For each candidate `c`, dispatch is
skipped when `∃ b ∈ c.BlockedBy : b ∈ openIDs`. Properties:

- A blocker in a Linear-terminal state is filtered by `FetchIssues`,
  not in `openIDs`, treated as completed → does not block. ✓
- A blocker in `Backlog` is also filtered (current `terminalLinearStateTypes`
  includes `backlog`) — treated as completed. Acceptable: users no
  longer need Backlog as a manual gate now that the real gate exists,
  and Backlog issues are by definition not yet committed work that the
  orchestrator should be reasoning about.
- A blocker in `Todo` / `In Progress` is in `openIDs` → blocks. ✓
- A blocker outside the fetched project is not in `openIDs` → treated
  as completed. Documented limitation.

### Decision 4: Log skips as structured events, not silent

`logging.LogIssueEvent(o.logger, issue.ID, "dispatch_skipped_blocked_by",
"blockers", strings.Join(unresolved, ","))` so the SSE stream surfaces
the reason. Tests assert the log line.

### Decision 5: Tracker-Linear pre-existing test fixtures keep using `BlockedBy: []string{}`

`linear_test.go:744` and `:758` assert empty `BlockedBy` against
fixtures that omit `inverseRelations` from the mocked GraphQL response.
That stays valid: if the response lacks the field, the parser produces
an empty slice. We add a *new* test that supplies `inverseRelations`
and asserts the populated identifiers; we do not modify the existing
assertions.

## Risks / Trade-offs

- **Risk**: Linear may rename `inverseRelations` in a major API
  revision. Mitigation: helper extraction (`extractBlockedBy(node)`)
  with focused tests so the GraphQL coupling is testable.
- **Risk**: A blocker present in Linear but not yet polled (e.g. just
  created, before the next FetchIssues tick) is missing from the
  candidate set on the racing tick → dependent dispatches early.
  Mitigation: subsequent ticks correct it; not a hard-correctness
  problem because `claimIssue` is idempotent and the dependent
  re-enters its run from scratch on retry.
- **Trade-off**: Single-project assumption. Documented; revisit if and
  when contrabass grows multi-project support.

## Migration Plan

No schema migration. Deploy as a normal contrabass binary upgrade. On
the next dispatch cycle, the orchestrator starts honoring `BlockedBy`
for every tracker that already populates it (GitHub, local board, and
— after this change — Linear).

If existing Linear projects had their relations wired with the
non-canonical convention (issueId/relatedIssueId reversed), users will
notice that their gates do not fire and need to recreate the relations
on the correct side. This is documented in the proposal "Impact".
