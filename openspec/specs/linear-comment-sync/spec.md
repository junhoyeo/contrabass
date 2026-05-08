# linear-comment-sync Specification

## Purpose
TBD - created by archiving change sync-linear-workflow-timeline. Update Purpose after archive.
## Requirements
### Requirement: Linear comment sync is opt-in
Contrabass SHALL write workflow-node summaries to Linear comments only when the tracker type is Linear and `linear.sync_comments.enabled` is true.

#### Scenario: Sync disabled preserves legacy comments
- **WHEN** `linear.sync_comments.enabled` is false
- **THEN** existing direct completion and unverified-success tracker comments remain unchanged

#### Scenario: Sync enabled suppresses legacy Linear comments
- **WHEN** `linear.sync_comments.enabled` is true and the tracker type is Linear
- **THEN** direct legacy Linear completion and unverified-success comments are suppressed and equivalent timeline nodes are queued for Linear sync

#### Scenario: Non-Linear tracker preserves legacy behavior
- **WHEN** the tracker type is not Linear
- **THEN** legacy `Tracker.PostComment` behavior remains unchanged regardless of Linear sync configuration

### Requirement: Linear sync creates one root comment per run
The Linear syncer SHALL maintain a run-level sync state keyed by `(issue_id, run_id, target)` and SHALL create at most one root comment for that run.

#### Scenario: Multiple nodes share one root
- **WHEN** two pending workflow nodes for the same issue and run are synced to Linear
- **THEN** the syncer creates exactly one root comment and associates both node sync records with that run-level root

#### Scenario: Restart after root creation
- **WHEN** Contrabass restarts after creating a root comment but before creating a node reply
- **THEN** the syncer reuses the persisted root comment ID and creates only the missing node reply or top-level fallback comment

### Requirement: Linear sync creates idempotent node comments
The Linear syncer SHALL create at most one effective Linear reply or top-level fallback comment per `(issue_id, run_id, node_id, target, content_hash)`.

#### Scenario: Restart after node sync
- **WHEN** Contrabass restarts after successfully syncing a node comment
- **THEN** the syncer does not create a duplicate comment for the same node key and content hash

#### Scenario: Node content hash changes
- **WHEN** a node summary changes content hash after a previous sync
- **THEN** the syncer updates the existing comment when supported or appends a superseding comment according to the effective sync mode

### Requirement: Linear sync is asynchronous and non-blocking
Linear comment writes SHALL be performed by an asynchronous syncer that does not block issue completion, release, retry enqueueing, or dashboard snapshot rendering.

#### Scenario: Linear API failure during completion
- **WHEN** Linear comment sync fails while an issue completes
- **THEN** the issue completion path proceeds and the node sync state records `failed` with the last error

#### Scenario: Web dashboard disabled
- **WHEN** `linear.sync_comments.enabled` is true and the web dashboard port is disabled
- **THEN** the Linear syncer still starts and processes pending timeline nodes

### Requirement: Linear sync handles rate limits and mode fallback
The Linear syncer SHALL persist retry state for rate limits and SHALL use deterministic behavior for unsupported comment modes.

#### Scenario: Rate limited response
- **WHEN** Linear returns a rate-limit response with Retry-After
- **THEN** the syncer persists retry-after state and does not retry that node before the retry time

#### Scenario: Default reply mode unsupported
- **WHEN** the default `reply_thread` mode is unsupported by the verified Linear schema
- **THEN** the syncer logs the fallback once and uses effective mode `top_level`

#### Scenario: Explicit unsupported mode
- **WHEN** the operator explicitly configures an unsupported Linear comment mode
- **THEN** configuration validation fails before runtime sync starts

