# Gate orchestrator claims on already-implemented issues

## Why

The orchestrator currently dispatches every `Unclaimed` issue it sees in the
tracker. If an issue's feature has already been implemented and merged into the
main branch, the orchestrator wastes an agent run re-implementing work that is
done. There is no mechanism to detect "this was already shipped" at claim time.

This is especially common in two scenarios:

1. **Stale issues**: a developer fixed the issue in a branch that was merged to
   main, but the issue was never transitioned to Done in the tracker.
2. **Re-opened issues**: a tracker migration or manual re-open puts an
   already-resolved issue back into the Unclaimed pool.

## What Changes

- **Config (TrackerConfig)**: add `main_ref` (default `"main"`) — the git ref
  to search for already-implemented commits — and
  `auto_close_already_implemented` (default `false`) — opt-in flag that
  transitions gated issues to Done automatically.

- **Helper (orchestrator)**: add `grepMainForIdentifier(ctx, mainRef, identifier)`
  that shells out to `git log <mainRef> --grep="\b<id>\b" -P -1` and returns
  the first matching commit SHA + subject when found. Word-boundary matching
  (`-P` / Perl regex) ensures `ABC-1` does not match `ABC-12`.

- **Gate (orchestrator)**: in `dispatchUnclaimedIssues`, after the existing
  `BlockedBy` gate, call `grepMainForIdentifier`. On hit: emit
  `ClaimSkippedAlreadyImplemented` and skip dispatch. On unresolvable mainRef:
  emit `ClaimMainRefUnresolvable` (warn once per cycle) and fail open. On miss:
  proceed to existing dispatch.

- **Auto-close (Linear adapter)**: add `TransitionToDone(ctx, issueID, comment)`
  — resolves the first `type=completed` workflow state and applies it, then
  posts a comment citing the commit. Called from the gate when
  `auto_close_already_implemented` is true.

## Impact

- Affected capabilities: `orchestrator-claim`, `tracker-linear`.
- Affected code: `internal/config/config.go`,
  `internal/orchestrator/orchestrator.go`,
  `internal/orchestrator/events.go`,
  `internal/tracker/linear.go`,
  `README.md`.
- Out of scope: GitHub Issues auto-close, cross-repo commit search, fuzzy
  matching, non-Linear auto-close adapters.
