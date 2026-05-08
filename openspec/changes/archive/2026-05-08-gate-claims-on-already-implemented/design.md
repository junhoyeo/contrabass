# Design: Gate claims on already-implemented issues

## Context

Three code paths intersect:

1. `internal/config/config.go` — `TrackerConfig` holds tracker-specific tunables.
   The new `main_ref` and `auto_close_already_implemented` fields land here.
2. `internal/orchestrator/orchestrator.go` — `dispatchUnclaimedIssues` is the
   single place where unclaimed issues enter the claim pipeline. The BlockedBy
   gate already lives here; the already-implemented gate follows the same pattern.
3. `internal/tracker/linear.go` — optional `TransitionToDone` method for
   auto-close. This is an extension beyond the Tracker interface, gated by a
   type assertion to a local `linearAutoCloser` interface.

## Key Design Decisions

### Helper placement
`grepMainForIdentifier` lives in `internal/orchestrator/orchestrator.go` — not
a new package. It is a small git-shelling helper used only by the dispatch loop.

### Word-boundary matching
`git log --grep="\b<id>\b" -P` uses Perl regex (`-P`) for `\b` word-boundary
support. `-E` (extended regex) does NOT support `\b` on macOS (POSIX ERE
limitation). `-P` works on both macOS and Linux.

### Fail-open on unresolvable mainRef
If `git log <mainRef>` exits with "unknown revision or ambiguous argument",
`grepMainForIdentifier` returns `unresolvable=true, found=false, err=nil`. The
caller emits `ClaimMainRefUnresolvable` (once per dispatch cycle, not once per
issue) and falls through to normal dispatch. This prevents a misconfigured or
unavailable mainRef from blocking all claims.

### Gate is opt-in via `EnableMainRefGate()`
`NewOrchestrator` installs a no-op `grepFn` by default. The production CLI calls
`orch.EnableMainRefGate()` after construction to activate the real git-based
implementation. Tests inject their own `grepFn` stub. This prevents the real git
subprocess from running in tests that don't set `grepFn`, avoiding false-positive
"already implemented" hits from the test repo's own commit history.

### Auto-close defaults OFF
`tracker.auto_close_already_implemented` defaults to `false`. When `true`, the
orchestrator calls `TransitionToDone(ctx, issueID, commentBody)` on the tracker.
The `linearAutoCloser` interface is defined locally in `orchestrator.go`; only
`LinearClient` satisfies it. Other tracker types skip auto-close with a log event.

### Event shape matches sibling gate
`ClaimSkippedAlreadyImplemented` and `ClaimMainRefUnresolvable` are defined in
`internal/orchestrator/events.go` and implement the `EventPayload` marker
interface, exactly matching the shape of existing event types (`AgentStarted`,
`BackoffEnqueued`, etc.).

## Goals / Non-Goals

**Goals**

- Git word-boundary search on a configurable mainRef (default `"main"`).
- Fail-open semantics when mainRef is unresolvable.
- Observable via structured events and log lines.
- Optional auto-close for Linear issues (off by default).

**Non-Goals**

- GitHub Issues auto-close via TransitionToDone.
- Cross-repo or cross-project commit search.
- Fuzzy matching or semantic similarity.
- Retroactive re-check of already-running agents.
