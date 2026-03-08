---
max_concurrency: 2
poll_interval_ms: 5000
max_retry_backoff_ms: 30000
model: codex-mini
agent_timeout_ms: 120000
stall_timeout_ms: 60000
tracker:
  type: internal
agent:
  type: codex
codex:
  binary_path: codex app-server
  approval_policy: auto-edit
  sandbox: none
---
# Contrabass Dogfood Task

You are working on the Contrabass project (Go, Charm TUI stack).
Working directory: {{ workspace.path }}

## Task

Issue: {{ issue.title }}
{{ issue.description }}

## Constraints

- Make minimal, focused changes
- Do NOT modify any test files
- Do NOT run any tests
- Keep changes under 20 lines
