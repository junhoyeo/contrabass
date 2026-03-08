---
max_concurrency: 4
poll_interval_ms: 8000
model: openai/gpt-5-codex
project_url: https://linear.app/example/project/omx
tracker:
  type: internal
agent:
  type: omx
omx:
  binary_path: omx
  team_spec: 2:executor
  poll_interval_ms: 1500
  startup_timeout_ms: 22000
  ralph: true
---
# Contrabass OMX Task

Use the OMX team runtime to execute this issue.
