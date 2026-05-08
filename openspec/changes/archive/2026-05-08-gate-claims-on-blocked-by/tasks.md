# Tasks: Gate claims on `BlockedBy`

## T1 — Extend `fetchIssuesQuery` with `inverseRelations`

**Files**: `internal/tracker/linear.go` (production only)

**Contract**: Add the following selection inside the `nodes { … }`
block of `fetchIssuesQuery`:

```graphql
inverseRelations {
  nodes {
    type
    issue { identifier }
  }
}
```

**Acceptance**:
- Query string contains `inverseRelations` field with the exact
  shape above.
- No other production-code change.
- `go build ./...` clean.

**Depends on**: none.
**Blocks**: T2, T3.

---

## T2 — Populate `Issue.BlockedBy` in `normalizeIssue`

**Files**: `internal/tracker/linear.go`

**Contract**:

```go
// extractBlockedBy returns the identifiers of issues whose
// inverseRelations of type "blocks" point at this issue. The result
// is always a non-nil slice; missing or malformed nodes are skipped.
func extractBlockedBy(node map[string]interface{}) []string
```

`normalizeIssue` SHALL call this helper and use its result in place of
the hard-coded `[]string{}` for `BlockedBy`. Order MUST follow the
input order of the `nodes` array. Duplicates SHALL be preserved (no
de-dup) — Linear does not emit them.

**Acceptance**:
- Helper exists with the signature above.
- `normalizeIssue` sets `BlockedBy: extractBlockedBy(node)`.
- Empty `inverseRelations` → returns `[]string{}` (non-nil, len 0).
- Type-`related` and other non-`blocks` entries are excluded.
- `go vet ./...` clean.

**Depends on**: T1.
**Blocks**: T3.

---

## T3 — Tests for Linear `BlockedBy` extraction

**Files**: `internal/tracker/linear_test.go`

**Contract**: Add a single new top-level test
`TestNormalizeIssue_BlockedByFromInverseRelations` that drives
`FetchIssues` against an `httptest.Server` returning a mock GraphQL
payload covering the four scenarios in `specs/tracker-linear/spec.md`:

1. one inverse blocks → single-element `BlockedBy`
2. two inverse blocks plus one `related` → only the blocks are kept,
   in input order
3. empty `inverseRelations.nodes` → `[]string{}`
4. inverse blocks node missing `issue.identifier` → silently skipped

The existing `TestNormalizeIssue_PopulatesExpandedFields` MUST keep
passing unchanged (it omits `inverseRelations` and asserts
`BlockedBy: []string{}`).

**Acceptance**:
- `go test ./internal/tracker/ -run TestNormalizeIssue -count=1 -v`
  passes (both old and new test).

**Depends on**: T1, T2.
**Blocks**: none.

---

## T4 — Gate `dispatchUnclaimedIssues` on `BlockedBy`

**Files**: `internal/orchestrator/orchestrator.go`

**Contract**: Inside `dispatchUnclaimedIssues`, before the
`canDispatch` check for each candidate, compute once at function
entry:

```go
openIDs := make(map[string]struct{}, len(issues))
for _, iss := range issues {
    if iss.Identifier != "" {
        openIDs[iss.Identifier] = struct{}{}
    }
}
```

Then for each candidate, after the existing `State != Unclaimed` skip
but before `canDispatch`, evaluate:

```go
unresolved := make([]string, 0, len(issue.BlockedBy))
for _, b := range issue.BlockedBy {
    if _, blocked := openIDs[b]; blocked {
        unresolved = append(unresolved, b)
    }
}
if len(unresolved) > 0 {
    logging.LogIssueEvent(o.logger, issue.ID,
        "dispatch_skipped_blocked_by",
        "blockers", strings.Join(unresolved, ","))
    continue
}
```

Add `"strings"` to the import block if not already present.

**Acceptance**:
- New gate sits **only** in `dispatchUnclaimedIssues` — `processContinuation`
  / `dispatchIssue` / `claimIssue` are NOT modified.
- `unresolved` log records only the blockers actually present in
  `openIDs`, not the entire `BlockedBy`.
- `go build ./...` clean.

**Depends on**: none (independent of T1-T3 — works with any tracker
that already populates `BlockedBy`, including the local board).
**Blocks**: T5.

---

## T5 — Tests for orchestrator gate

**Files**: `internal/orchestrator/orchestrator_test.go`

**Contract**: Add `TestDispatchUnclaimedIssues_GatesOnBlockedBy` with
table-driven scenarios mirroring `specs/orchestrator-claim/spec.md`:

1. blocker present in batch → candidate skipped
2. blocker absent from batch → candidate dispatched
3. multiple blockers, one in batch → skipped, log records only the
   one in batch
4. empty `BlockedBy` → dispatched, no skip event

Drive via the orchestrator's existing test harness (mock tracker, mock
agent factory). Assert on the `dispatch_skipped_blocked_by` log event
through whatever capture mechanism `orchestrator_test.go` already uses
(e.g. captured `logging.Logger` ring buffer, or a fake event hub) —
inspect existing tests for the right pattern before inventing one.

**Acceptance**:
- `go test ./internal/orchestrator/ -run TestDispatchUnclaimedIssues -count=1 -v`
  passes.
- No pre-existing test in the package is modified.

**Depends on**: T4.
**Blocks**: none.

---

## Rejection rules (apply to ALL tasks)

A diff that satisfies any of the following MUST be rejected:

1. The diff modifies only `*_test.go` files for T1, T2, or T4
   (production code is mandatory for those tasks).
2. The diff modifies any file outside the explicit `Files` list of
   the task it claims to implement.
3. The diff adds a new go module dependency. Use stdlib only.
4. The diff omits any acceptance bullet from the task it claims to
   implement.
5. The diff modifies `processContinuation`, `dispatchIssue`, or
   `claimIssue` (T4 must keep its blast radius inside
   `dispatchUnclaimedIssues`).
6. The diff changes `Issue.BlockedBy` to be `nil` instead of an empty
   slice on the no-blockers path (breaks JSON contract).

## Task graph

```
T1 ── T2 ── T3
T4 ── T5
```

T4 and T5 are independent of T1-T3 and may proceed in parallel.
