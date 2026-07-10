# Orchestrator configuration

Contrabass keeps protocol vocabulary and state-machine transitions in code, but
exposes operator-tunable runtime policy through the `WORKFLOW.md` YAML front
matter. Existing workflows remain compatible because every field has the same
default as the previous hard-coded behavior.

```yaml
polling:
  # exponential or linear
  backoff_strategy: exponential

agent:
  # Codex/OpenCode/OhMyOpenCode startup and API timeout.
  startup_timeout_ms: 30000

team:
  # Poll cadence for tmux-backed workers.
  worker_poll_interval_ms: 2000

orchestrator:
  # Applied when the orchestrator is constructed; restart to resize channels
  # or the issue cache.
  event_buffer_size: 256
  run_signal_buffer_size: 256
  issue_cache_size: 1000

  # Read dynamically from the workflow watcher.
  run_shutdown_timeout_ms: 5000
  stop_grace_timeout_ms: 5000
  git_command_timeout_ms: 2000

  shutdown:
    drain_timeout_ms: 30000
    cleanup_timeout_ms: 10000
    poll_interval_ms: 10

  backoff:
    continuation_ms: 1000
    failure_base_ms: 10000
    # Used by exponential strategy; linear grows failure_base_ms by attempt.
    multiplier: 2
    # 0 disables jitter; valid effective range is 0-100.
    jitter_percent: 10

  snapshot:
    diff_timeout_ms: 1000
    stage:
      testing_after_ms: 30000
      reviewing_after_ms: 60000
      reviewing_max_tokens_per_minute: 50000
    eta:
      min_elapsed_ms: 180000
      min_files_per_minute: 0.05
      min_tokens_per_minute: 1000
      medium_confidence_after_ms: 300000
      high_confidence_after_ms: 480000
      high_confidence_min_stage: 3
      estimated_files_multiplier: 1.2
      min_estimated_files: 11
      fallback_remaining_minutes: 5
      uncertainty_multiplier: 1.35
```

## Field semantics

| Field | Purpose |
|---|---|
| `agent.startup_timeout_ms` | Startup/API timeout for Codex, OpenCode, and OhMyOpenCode runners |
| `team.worker_poll_interval_ms` | tmux worker event polling cadence |
| `event_buffer_size` | Public orchestrator event channel capacity |
| `run_signal_buffer_size` | Internal agent-event/completion supervision queue capacity |
| `issue_cache_size` | Maximum cached issue records retained for retries and snapshots |
| `run_shutdown_timeout_ms` | Cleanup budget used by `Orchestrator.Run` after context cancellation |
| `stop_grace_timeout_ms` | Wait after stopping one agent before removing the run entry |
| `git_command_timeout_ms` | Timeout for claim-head capture and success verification |
| `shutdown.*` | Signal-driven drain, cleanup, and drain-polling policy |
| `backoff.*` | Continuation/failure retry delay, growth, and jitter policy |
| `snapshot.diff_timeout_ms` | Timeout for dashboard `git diff --shortstat` collection |
| `snapshot.stage.*` | Dashboard stage-classification thresholds |
| `snapshot.eta.*` | Dashboard ETA confidence and estimation thresholds |

## Compatibility rules

- Missing, zero, or negative numeric values use the documented default, except
  `jitter_percent: 0`, which explicitly disables jitter.
- `jitter_percent` values above 100 are capped at 100; negative values use the
  default.
- Unknown `polling.backoff_strategy` values fall back to `exponential`.
- OMX and OMC startup timeouts continue to use
  `omx.startup_timeout_ms`/`omc.startup_timeout_ms`; the CLI bootstrap no longer
  overrides them with a fixed timeout.

## Intentionally not configurable

The following values are contracts or implementation invariants rather than
operator policy and remain in code:

- issue and run-phase transition graphs;
- orchestrator event types and agent success/failure event names;
- token-usage wire-format compatibility keys;
- Git verification semantics and command arguments (`rev-parse`, `HEAD`);
- deterministic jitter hash constants;
- OMX iteration state path (`.omx/state/run-state.json`);
- timeline node status vocabulary and snapshot JSON field names.
