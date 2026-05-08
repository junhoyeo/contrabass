---
max_concurrency: 3
poll_interval_ms: 2000
max_retry_backoff_ms: 240000
model: $CONTRABASS_MODEL
project_url: $CONTRABASS_PROJECT_URL
agent_timeout_ms: 900000
stall_timeout_ms: 60000
tracker:
  type: linear
  assignee_id: $LINEAR_ASSIGNEE_ID
codex:
  binary_path: codex app-server
---
# Contrabass Demo Workflow

You are implementing tasks for this project.

Issue title: {{ issue.title }}
Issue description: {{ issue.description }}
Issue URL: {{ issue.url }}

Produce code and tests that satisfy the issue requirements.
