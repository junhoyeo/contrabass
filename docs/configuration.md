# Workflow configuration

Contrabass reads runtime configuration and the agent prompt from a single
`WORKFLOW.md` file. The YAML front matter configures Contrabass; the Markdown
body is rendered as a Liquid prompt template.

```markdown
---
schema_version: 1
max_concurrency: 4
polling:
  interval_ms: 5000
tracker:
  type: github
  token: $GITHUB_TOKEN
agent:
  type: codex
---
Implement {{ issue.title }}.
```

## Schema and validation

`schema_version` is optional for existing workflows and currently defaults to
`1`. Versions newer than the running binary are rejected.

Configuration parsing is strict:

- unknown top-level and nested YAML fields are rejected;
- explicitly negative durations, limits, and buffer sizes are rejected;
- enum values such as `polling.backoff_strategy`, `team.execution_mode`, and
  `team.worker_mode` are validated;
- cross-field ordering such as testing/review stage thresholds is validated;
- zero numeric values continue to select existing defaults for compatibility.

Validate a workflow without starting the orchestrator:

```bash
contrabass config validate --config WORKFLOW.md
```

## Environment references

A string whose complete value is `$VARIABLE` is replaced with the matching
environment variable. Expansion applies to nested structs, lists, and map
values. The Markdown prompt body is not expanded.

```yaml
tracker:
  token: $GITHUB_TOKEN
oh_my_opencode:
  plugins:
    - $OH_MY_OPENCODE_PLUGIN
  agents:
    reviewer:
      model: $REVIEW_MODEL
```

## Effective configuration

Print the final values selected by defaults and compatibility aliases:

```bash
contrabass config effective --config WORKFLOW.md
contrabass config effective --config WORKFLOW.md --format json
```

The output contains:

- `values`: canonical runtime values;
- `metadata`: value source and reload policy for each field;
- `warnings`: deprecated aliases or conflicting precedence;
- `schema_version`: the effective configuration schema.

Secrets are always redacted in this output. Current sensitive fields include
`tracker.token`, `opencode.password`, and
`oh_my_opencode.provider.api_key`.

## Reload lifecycle

Each effective field is classified as one of:

- `reloadable`: existing runtime code reads the current Watcher snapshot;
- `startup_only`: the associated client, runner, store, cache, or channel is
  constructed once and requires a process restart.

Important startup-only examples include tracker and agent types, executable
paths, workspace and timeline locations, team execution mode, event buffer
sizes, issue cache capacity, and the main polling interval/ticker.

Reloadable examples include maximum concurrency, agent/stall timeouts,
retry/backoff values, shutdown timeouts, and snapshot stage/ETA policy.

The Watcher still parses the entire file on every reload. A successful reload
therefore means the snapshot is valid, not that every changed field has already
reconfigured its owning component. Use `config effective` to inspect the
lifecycle of individual fields.

## Compatibility aliases

Existing top-level fields remain supported. When both forms are present,
current precedence is preserved. For example, `poll_interval_ms` overrides
`polling.interval_ms`, but the nested form is preferred for new workflows.
