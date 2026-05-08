# stop-running-agent Specification

## Purpose
TBD - created by archiving change stop-running-agent. Update Purpose after archive.
## Requirements
### Requirement: Caller can stop a running agent by issue ID
The system SHALL expose `POST /api/v1/running/{issue_id}/stop` that terminates the agent process associated with the given issue and returns HTTP 202.

#### Scenario: Stop a running agent
- **WHEN** a client sends `POST /api/v1/running/{issue_id}/stop` for an issue that is currently running
- **THEN** the server responds with HTTP 202 and an empty body, and the orchestrator sends SIGTERM to the agent PID

#### Scenario: Issue not in running state
- **WHEN** a client sends `POST /api/v1/running/{issue_id}/stop` for an issue that is not currently running
- **THEN** the server responds with HTTP 404 and `{"error": "agent not running"}`

#### Scenario: SIGTERM ignored — SIGKILL fallback
- **WHEN** the agent process does not exit within 5 seconds of SIGTERM
- **THEN** the orchestrator sends SIGKILL to the agent PID

### Requirement: Stop button displayed in running detail view
The dashboard IssueDetailSheet SHALL display a Stop button when the selected entry has `kind = "running"`.

#### Scenario: Button in idle state
- **WHEN** the detail sheet opens for a running entry
- **THEN** a Stop button is visible and clickable

#### Scenario: Button transitions to stopping state
- **WHEN** the user clicks the Stop button
- **THEN** the button becomes disabled and shows a loading indicator, and the frontend sends `POST /api/v1/running/{issue_id}/stop`

#### Scenario: Button disappears after stop completes
- **WHEN** the SSE snapshot no longer includes the stopped issue in the running list
- **THEN** the detail sheet either closes or transitions to the stale/canceled view

