# Tasks: gate-claims-on-already-implemented

## Task Group 1 — Config schema

- [x] 1.1 Add `MainRefRaw string` and `AutoCloseAlreadyImplementedRaw bool` to
  `TrackerConfig` in `internal/config/config.go` with yaml tags `main_ref` and
  `auto_close_already_implemented`.
- [x] 1.2 Add `TrackerMainRef() string` accessor (default `"main"`) and
  `TrackerAutoCloseAlreadyImplemented() bool` accessor (default `false`) to
  `WorkflowConfig`.
- [x] 1.3 Cover defaults and override in `internal/config/config_test.go`.

## Task Group 2 — Git grep helper

- [x] 2.1 Add `grepMainForIdentifier(ctx, mainRef, identifier)` to
  `internal/orchestrator/orchestrator.go`. Shells
  `git log <mainRef> --grep="\b<id>\b" -P -1 --no-color --format=%H%n%s`.
  Treat "unknown revision" stderr as `unresolvable=true, found=false, err=nil`.
  Also add `grepMainForIdentifierIn` variant accepting explicit `dir` for tests.
- [x] 2.2 Table-driven tests in `orchestrator_test.go`: hit, miss,
  prefix-overlap (ABC-1 vs ABC-12), unresolvable mainRef, multiple matches
  (returns first), empty/whitespace identifier returns false without git call.

## Task Group 3 — Event types

- [x] 3.1 Add `EventClaimSkippedAlreadyImplemented` and
  `EventClaimMainRefUnresolvable` `EventType` constants to
  `internal/orchestrator/events.go` with `String()` cases.
- [x] 3.2 Add `ClaimSkippedAlreadyImplemented` and `ClaimMainRefUnresolvable`
  payload structs implementing `EventPayload`.

## Task Group 4 — Wire gate into dispatch

- [x] 4.1 In `dispatchUnclaimedIssues`, after the BlockedBy gate, call
  `o.grepFn(ctx, mainRef, issue.Identifier)`. On hit: emit
  `ClaimSkippedAlreadyImplemented`, skip dispatch, optionally auto-close. On
  unresolvable: emit `ClaimMainRefUnresolvable` once per cycle, fail open. On
  miss: proceed.
- [x] 4.2 Add injectable `grepFn` field (no-op default) to `Orchestrator` and
  `EnableMainRefGate()` method. Call `EnableMainRefGate()` from
  `cmd/contrabass/main.go` after `NewOrchestrator`.
- [x] 4.3 Tests: gate fires on hit, dispatch skipped, event shape correct;
  fail-open on unresolvable; warn-once semantics.

## Task Group 5 — Auto-close (Linear adapter)

- [x] 5.1 Add `TransitionToDone(ctx, issueID, commentBody) error` to
  `internal/tracker/linear.go`. Queries `states(filter: {type: {eq: "completed"}})`,
  picks first, calls `issueUpdate(stateId)` + `commentCreate`.
- [x] 5.2 Tests in `linear_test.go`: success (3-request sequence), no Done
  state available, network error.
- [x] 5.3 Define `linearAutoCloser` interface in `orchestrator.go`; gate calls
  `autoCloseAlreadyImplemented` when enabled. Tests for auto-close enabled and
  disabled paths.

## Task Group 6 — Documentation

- [x] 6.1 Add `tracker.main_ref` and `tracker.auto_close_already_implemented`
  rows to the Tunables table in `README.md`.
