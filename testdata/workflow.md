---
max_concurrency: 8
poll_interval_ms: 15000
max_retry_backoff_ms: 240000
model: openai/gpt-5-codex
project_url: https://linear.app/contrabass/project/contrabass-symphony-demo-4bded72b77d3
agent_timeout_ms: 900000
stall_timeout_ms: 60000
---
# Symphony Workflow

You are implementing tasks for this project.

Issue title: {{ issue.title }}
Issue description: {{ issue.description }}
Issue URL: {{ issue.url }}

Produce code and tests that satisfy the issue requirements.
