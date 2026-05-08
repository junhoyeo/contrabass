---
max_concurrency: 1
poll_interval_ms: 4000
max_retry_backoff_ms: 90000
model: $CONTRABASS_MODEL
agent_timeout_ms: 900000
stall_timeout_ms: 90000
project_url: $CONTRABASS_PROJECT_URL
tracker:
  type: linear
  assignee_id: $LINEAR_ASSIGNEE_ID
agent:
  type: codex
codex:
  binary_path: "codex app-server"
---
# Contrabass Self-Hosting — Hardening Project

You are a coding agent working inside a contrabass-managed git worktree.
Your task is described in the issue body below; produce code and tests
that fully satisfy the issue's Acceptance Criteria.

## Issue
**{{ issue.title }}**

{{ issue.description }}

URL: {{ issue.url }}

## Working Constraints
- This worktree is on a feature branch — commit your work here.
- Match existing project style. Use `gofmt` / `goimports`.
- Run the relevant tests before reporting completion.
- Do **not** modify files outside the "Files To Touch" list in the issue.
- Do **not** alter `--dry-run` semantics or any god-node config schema unless the issue explicitly says so.

## Definition of Done
1. New tests added or existing tests extended to cover the change.
2. `go test ./<touched-package>/...` passes.
3. `go vet ./...` clean.
4. Commit message follows `type(scope): description` (lowercase, imperative, no trailing period).
