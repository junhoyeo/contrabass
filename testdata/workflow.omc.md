---
max_concurrency: 4
poll_interval_ms: 8000
model: anthropic/claude-sonnet-4-6
project_url: https://linear.app/example/project/omc
tracker:
  type: internal
agent:
  type: omc
omc:
  binary_path: omc
  team_spec: 2:claude
  poll_interval_ms: 1200
  startup_timeout_ms: 21000
---
# Contrabass OMC Task

Use the OMC team runtime to execute this issue.
