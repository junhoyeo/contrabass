## ADDED Requirements

### Requirement: Orchestrator re-validates blockers for managed issues each poll tick
On every poll cycle the orchestrator SHALL check whether any currently-managed (running or claimed) issue has acquired an unresolved blocker since it was dispatched. If so, the orchestrator SHALL stop the agent and revert the issue state to Todo.

#### Scenario: Blocker relation added after dispatch
- **WHEN** issue A is running and a new `blocks` relation is added in the tracker pointing at A while A's blocker B is still open
- **THEN** on the next poll tick the orchestrator stops A's agent process, reverts A to Todo, and logs `running_released_blocked_by`

#### Scenario: No blockers — managed issue untouched
- **WHEN** a managed issue has an empty `BlockedBy` list
- **THEN** the orchestrator does not stop the agent and continues normal execution

#### Scenario: Blocker already resolved — managed issue untouched
- **WHEN** a managed issue has a `BlockedBy` entry but that blocker is no longer in the open issue set (Done or Canceled)
- **THEN** the orchestrator does not stop the agent and continues normal execution

#### Scenario: Tracker revert fails
- **WHEN** the orchestrator stops an agent but the `MoveIssue(Todo)` tracker call fails
- **THEN** the orchestrator logs the error and retries the revert on the next poll tick; the issue is removed from the managed set so it is not re-stopped unnecessarily
