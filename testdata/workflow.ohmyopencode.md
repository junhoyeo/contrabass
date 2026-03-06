---
max_concurrency: 5
poll_interval_ms: 10000
model: anthropic/claude-sonnet-4-6
agent_timeout_ms: 600000
stall_timeout_ms: 120000
tracker:
  type: github
  owner: example-org
  repo: example-repo
  labels:
    - agent
  assignee: bot-user
  endpoint: https://api.github.com
agent:
  type: oh-my-opencode
opencode:
  binary_path: opencode serve
  port: 8787
  username: admin
oh_my_opencode:
  plugin_version: oh-my-opencode@3.10.0
  plugins:
    - opencode-antigravity-auth@1.2.7-beta.3
  agents:
    sisyphus:
      model: anthropic/claude-sonnet-4-6
  categories:
    visual-engineering:
      model: anthropic/claude-sonnet-4-6
    quick:
      model: anthropic/claude-haiku-4-5
    writing:
      model: anthropic/claude-sonnet-4-6
  provider:
    name: anthropic
    base_url: https://proxy.example.com/v1
    api_key: sk-test-key
---
# Oh-My-OpenCode Workflow

You are an AI agent running with oh-my-opencode orchestration.

Issue title: {{ issue.title }}
Issue description: {{ issue.description }}
Issue URL: {{ issue.url }}

Produce code and tests that satisfy the issue requirements.
