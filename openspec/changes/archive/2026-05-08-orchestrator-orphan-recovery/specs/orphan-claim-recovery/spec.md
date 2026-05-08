## ADDED Requirements

### Requirement: Orchestrator recovers orphaned claimed issues on every poll tick
Before each dispatch cycle the orchestrator SHALL cross-reference all issues whose internal state is `Claimed` against the live managed-issues set. Any `Claimed` issue not present in the managed set SHALL have its state overridden to `Unclaimed` so it is eligible for dispatch.

#### Scenario: Orphaned issue re-dispatched after restart
- **WHEN** the orchestrator restarts with an empty managed-issues map and Linear contains an issue with state type "started"
- **THEN** on the first poll tick after restart the orchestrator overrides that issue's state to Unclaimed, dispatches an agent for it, and logs `orphan_claim_recovered`

#### Scenario: Genuinely managed issue not affected
- **WHEN** an issue is both "started" in Linear AND present in the managed-issues map
- **THEN** the orchestrator does not override its state and does not re-dispatch it

#### Scenario: Recovery fires every tick until dispatch succeeds
- **WHEN** an orphaned issue is detected but dispatch is skipped (e.g. max concurrency reached)
- **THEN** the override is applied again on the next tick until the issue is successfully dispatched

#### Scenario: Unstarted and terminal issues are not affected
- **WHEN** an issue has Linear state type "unstarted", "completed", or "canceled"
- **THEN** `recoverOrphanedClaims` does not modify its state
