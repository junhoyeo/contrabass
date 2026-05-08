---
max_concurrency: 4
poll_interval_ms: 8000
model: $CONTRABASS_MODEL
project_url: $CONTRABASS_PROJECT_URL
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
