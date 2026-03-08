---
max_concurrency: 3
poll_interval_ms: 2000
max_retry_backoff_ms: 5000
model: mock
agent_timeout_ms: 30000
stall_timeout_ms: 15000
tracker:
  type: internal
agent:
  type: codex
codex:
  binary_path: /tmp/contrabass-web-team-integration/testdata/mock-agent.sh
  approval_policy: auto-edit
  sandbox: none
team:
  max_workers: 3
  max_fix_loops: 2
---
# Mock Dogfood Task

Working directory: {{ workspace.path }}

## Task

Issue: {{ issue.title }}

{{ issue.description }}

## Instructions

- Complete the task
