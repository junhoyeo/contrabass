---
max_concurrency: 5
poll_interval_ms: 10000
model: anthropic/claude-sonnet
agent_timeout_ms: 600000
stall_timeout_ms: 120000
tracker:
  type: github
  owner: example-org
  repo: example-repo
  labels:
    - bug
    - agent
  assignee: bot-user
  token: $GITHUB_TOKEN
  endpoint: https://api.github.com
agent:
  type: opencode
opencode:
  binary_path: opencode serve
  port: 4096
  password: $OPENCODE_SERVER_PASSWORD
  username: admin
---
# GitHub Workflow

You are implementing tasks for this project.

Issue title: {{ issue.title }}
Issue description: {{ issue.description }}
Issue URL: {{ issue.url }}

Produce code and tests that satisfy the issue requirements.
