# linear-issue-detail Specification

## Purpose
TBD - created by archiving change sync-linear-workflow-timeline. Update Purpose after archive.
## Requirements
### Requirement: Backend fetches richer Linear issue details
Contrabass SHALL provide a backend-only Linear issue detail read path that returns richer Linear metadata without adding those fields to every orchestration poll.

#### Scenario: Detail fetch succeeds
- **WHEN** the dashboard requests details for a cached Linear issue
- **THEN** the backend fetches and returns richer Linear metadata including assignee, creator, team, project, cycle, estimate, due date, and relation summaries when available

#### Scenario: Candidate polling remains lean
- **WHEN** the orchestrator polls candidate Linear issues
- **THEN** it uses the existing lean issue polling path rather than the richer detail query

### Requirement: Dashboard never receives Linear credentials
Contrabass SHALL keep Linear API credentials server-side and SHALL not expose them to browser code or dashboard payloads.

#### Scenario: Dashboard loads issue details
- **WHEN** the dashboard opens an issue detail view
- **THEN** it requests issue details from the Contrabass backend and does not call Linear directly

#### Scenario: Detail payload excludes secrets
- **WHEN** the backend returns Linear issue details
- **THEN** the response does not include Linear API tokens, Authorization headers, or other credentials

### Requirement: Issue detail endpoint is separate from existing issue route
Contrabass SHALL expose richer issue details through `/api/v1/issues/{issue_id}/details` and SHALL avoid extending the broad `/api/v1/{identifier}` route for this feature.

#### Scenario: Detail endpoint returns payload
- **WHEN** a client requests `GET /api/v1/issues/{issue_id}/details`
- **THEN** the backend returns the normalized issue, Linear detail object when available, and generation timestamp

#### Scenario: Detail provider unavailable
- **WHEN** the tracker does not provide richer issue details
- **THEN** the endpoint returns a non-success status or a payload that clearly indicates the detail provider is unavailable without breaking the base snapshot state

### Requirement: Dashboard renders issue details and timeline together
The dashboard SHALL render Linear issue details and local workflow timeline data in the issue detail sheet with non-blocking loading and error states.

#### Scenario: Detail sheet opens
- **WHEN** a user opens an issue detail sheet
- **THEN** the dashboard loads `/api/v1/issues/{issue_id}/details` and `/api/v1/issues/{issue_id}/timeline` and continues to show base snapshot issue data while those requests are pending

#### Scenario: Detail or timeline fetch fails
- **WHEN** either detail or timeline fetch fails
- **THEN** the dashboard shows an inline non-blocking error and preserves the base snapshot issue display

