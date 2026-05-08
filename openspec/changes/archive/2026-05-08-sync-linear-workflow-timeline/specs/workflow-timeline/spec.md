## ADDED Requirements

### Requirement: Durable workflow timeline store
Contrabass SHALL persist single-agent workflow run summaries, workflow-node summaries, and sync state in a durable local timeline store.

#### Scenario: Persist node summary
- **WHEN** a single-agent workflow node reaches a durable completion, failure, retry, or needs-review state
- **THEN** the system persists a workflow-node summary with issue ID, run ID, node ID, attempt, status, timestamps, content hash, and available runtime metrics

#### Scenario: Snapshot survives restart
- **WHEN** Contrabass restarts after timeline records were written
- **THEN** reducing the issue timeline file returns the same runs, nodes, run sync states, and node sync states that were present before restart

### Requirement: Timeline writes are idempotent
The workflow timeline store SHALL make node writes idempotent by `(issue_id, run_id, node_id, attempt)` and content hash.

#### Scenario: Duplicate node write with same hash
- **WHEN** the same workflow-node summary is written twice with the same content hash
- **THEN** the timeline snapshot contains only one effective node summary for that key

#### Scenario: Replacement node write with changed hash
- **WHEN** a workflow-node summary is written again for the same key with a different content hash
- **THEN** the timeline snapshot uses the newest content and content hash for that key

### Requirement: Timeline writes are file locked
The workflow timeline store SHALL acquire a file lock for every append or sync-state upsert against a per-issue JSONL file.

#### Scenario: Concurrent timeline writes
- **WHEN** multiple goroutines append run, node, run-sync, and node-sync records for the same issue concurrently
- **THEN** the resulting JSONL file contains valid non-interleaved JSON records and the reduced snapshot includes all successful writes

### Requirement: Pre-agent failures are timeline nodes
Contrabass SHALL create syncable failed workflow-node summaries for failures that occur before the agent process is running.

#### Scenario: Claim failure creates node
- **WHEN** claiming an issue fails before workspace creation
- **THEN** the timeline contains `run:attempt-<n>:claim-failed` with status `failed` and the error summary

#### Scenario: Workspace creation failure creates node
- **WHEN** workspace creation fails after claim
- **THEN** the timeline contains `run:attempt-<n>:workspace-failed` with status `failed` and the error summary

#### Scenario: Prompt render failure creates node
- **WHEN** prompt rendering fails before agent start
- **THEN** the timeline contains `run:attempt-<n>:prompt-failed` with status `failed` and the error summary

#### Scenario: Agent start failure creates node
- **WHEN** starting the agent process fails
- **THEN** the timeline contains `run:attempt-<n>:agent-start-failed` with status `failed` and the error summary

### Requirement: Timeline is exposed to dashboard through backend
Contrabass SHALL expose an issue workflow timeline through a backend endpoint without requiring dashboard access to local timeline files.

#### Scenario: Dashboard requests issue timeline
- **WHEN** the dashboard requests `GET /api/v1/issues/{issue_id}/timeline`
- **THEN** the backend returns a workflow timeline snapshot containing runs, nodes, run sync states, node sync states, and generation time
