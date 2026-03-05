# Test Plan — Contrabass (Ported from Elixir)

This document ports scenario intent from Elixir ExUnit tests into Go-oriented test planning.
Each scenario records behavior, setup, expected outcome, source mapping, and target package.

Source coverage:
- core_test.exs: 34 scenarios
- app_server_test.exs: 10 scenarios
- workspace_and_config_test.exs: 31 scenarios
- orchestrator_status_test.exs: 41 scenarios
- status_dashboard_snapshot_test.exs: 6 scenarios
- Total scenarios: 122

## Package: internal/config

### Scenario: linear api token resolves from LINEAR_API_KEY env var
- **Behavior**: Validate that linear api token resolves from LINEAR_API_KEY env var.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/config`
- **Source**: `core_test.exs:101`

### Scenario: workflow file path defaults to WORKFLOW.md in the current working directory when app env is unset
- **Behavior**: Validate that workflow file path defaults to WORKFLOW.md in the current working directory when app env is unset.
- **Setup**: Prepare workflow configuration for the scenario.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/config`
- **Source**: `core_test.exs:135`

### Scenario: workflow file path resolves from app env when set
- **Behavior**: Validate that workflow file path resolves from app env when set.
- **Setup**: Prepare workflow configuration for the scenario.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/config`
- **Source**: `core_test.exs:147`

### Scenario: workflow load accepts prompt-only files without front matter
- **Behavior**: Validate that workflow load accepts prompt-only files without front matter.
- **Setup**: Prepare workflow configuration for the scenario.
- **Expected**: Returns success when preconditions are valid.
- **Target Go Package**: `internal/config`
- **Source**: `core_test.exs:159`

### Scenario: workflow load accepts unterminated front matter with an empty prompt
- **Behavior**: Validate that workflow load accepts unterminated front matter with an empty prompt.
- **Setup**: Prepare workflow configuration for the scenario.
- **Expected**: Returns success when preconditions are valid.
- **Target Go Package**: `internal/config`
- **Source**: `core_test.exs:167`

### Scenario: workflow load rejects non-map front matter
- **Behavior**: Validate that workflow load rejects non-map front matter.
- **Setup**: Prepare workflow configuration for the scenario.
- **Expected**: Returns a typed error for invalid or blocked input.
- **Target Go Package**: `internal/config`
- **Source**: `core_test.exs:175`

### Scenario: application configures a rotating file logger handler
- **Behavior**: Validate that application configures a rotating file logger handler.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Returns success when preconditions are valid; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/config`
- **Source**: `orchestrator_status_test.exs:1262`

### Scenario: config reads defaults for optional settings
- **Behavior**: Validate that config reads defaults for optional settings.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/config`
- **Source**: `workspace_and_config_test.exs:640`

### Scenario: config supports per-state max concurrent agent overrides
- **Behavior**: Validate that config supports per-state max concurrent agent overrides.
- **Setup**: Prepare workflow configuration for the scenario.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/config`
- **Source**: `workspace_and_config_test.exs:857`

### Scenario: workflow prompt is used when building base prompt
- **Behavior**: Validate that workflow prompt is used when building base prompt.
- **Setup**: Prepare workflow configuration for the scenario.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/config`
- **Source**: `workspace_and_config_test.exs:879`

## Package: internal/tracker

### Scenario: linear assignee resolves from LINEAR_ASSIGNEE env var
- **Behavior**: Validate that linear assignee resolves from LINEAR_ASSIGNEE env var.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/tracker`
- **Source**: `core_test.exs:119`

### Scenario: linear issue state reconciliation fetch with no running issues is a no-op
- **Behavior**: Validate that linear issue state reconciliation fetch with no running issues is a no-op.
- **Setup**: Use tracker client fixture data or stubbed GraphQL responses.
- **Expected**: Returns success when preconditions are valid.
- **Target Go Package**: `internal/tracker`
- **Source**: `core_test.exs:206`

### Scenario: fetch issues by states with empty state set is a no-op
- **Behavior**: Validate that fetch issues by states with empty state set is a no-op.
- **Setup**: Use tracker client fixture data or stubbed GraphQL responses.
- **Expected**: Returns success when preconditions are valid.
- **Target Go Package**: `internal/tracker`
- **Source**: `core_test.exs:548`

### Scenario: linear issue helpers
- **Behavior**: Validate that linear issue helpers.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Prevents unintended state transitions; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tracker`
- **Source**: `workspace_and_config_test.exs:260`

### Scenario: linear client normalizes blockers from inverse relations
- **Behavior**: Validate that linear client normalizes blockers from inverse relations.
- **Setup**: Use tracker client fixture data or stubbed GraphQL responses.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tracker`
- **Source**: `workspace_and_config_test.exs:272`

### Scenario: linear client marks explicitly unassigned issues as not routed to worker
- **Behavior**: Validate that linear client marks explicitly unassigned issues as not routed to worker.
- **Setup**: Use tracker client fixture data or stubbed GraphQL responses.
- **Expected**: Prevents unintended state transitions.
- **Target Go Package**: `internal/tracker`
- **Source**: `workspace_and_config_test.exs:320`

### Scenario: linear client pagination merge helper preserves issue ordering
- **Behavior**: Validate that linear client pagination merge helper preserves issue ordering.
- **Setup**: Use tracker client fixture data or stubbed GraphQL responses.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tracker`
- **Source**: `workspace_and_config_test.exs:336`

### Scenario: linear client logs response bodies for non-200 graphql responses
- **Behavior**: Validate that linear client logs response bodies for non-200 graphql responses.
- **Setup**: Use tracker client fixture data or stubbed GraphQL responses.
- **Expected**: Returns a typed error for invalid or blocked input.
- **Target Go Package**: `internal/tracker`
- **Source**: `workspace_and_config_test.exs:351`

### Scenario: todo issue with non-terminal blocker is not dispatch-eligible
- **Behavior**: Validate that todo issue with non-terminal blocker is not dispatch-eligible.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Prevents unintended state transitions.
- **Target Go Package**: `internal/tracker`
- **Source**: `workspace_and_config_test.exs:418`

### Scenario: issue assigned to another worker is not dispatch-eligible
- **Behavior**: Validate that issue assigned to another worker is not dispatch-eligible.
- **Setup**: Prepare workflow configuration for the scenario.
- **Expected**: Prevents unintended state transitions.
- **Target Go Package**: `internal/tracker`
- **Source**: `workspace_and_config_test.exs:438`

### Scenario: todo issue with terminal blockers remains dispatch-eligible
- **Behavior**: Validate that todo issue with terminal blockers remains dispatch-eligible.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/tracker`
- **Source**: `workspace_and_config_test.exs:460`

### Scenario: dispatch revalidation skips stale todo issue once a non-terminal blocker appears
- **Behavior**: Validate that dispatch revalidation skips stale todo issue once a non-terminal blocker appears.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/tracker`
- **Source**: `workspace_and_config_test.exs:480`

## Package: internal/workspace

### Scenario: non-active issue state stops running agent without cleaning workspace
- **Behavior**: Validate that non-active issue state stops running agent without cleaning workspace.
- **Setup**: Prepare workflow configuration for the scenario; Create temporary workspace and file-system fixtures.
- **Expected**: Prevents unintended state transitions.
- **Target Go Package**: `internal/workspace`
- **Source**: `core_test.exs:210`
- **Portability**: N/A in direct form — Mailbox/process-signal semantics are BEAM-specific; port as Go concurrency/integration test intent only.

### Scenario: terminal issue state stops running agent and cleans workspace
- **Behavior**: Validate that terminal issue state stops running agent and cleans workspace.
- **Setup**: Prepare workflow configuration for the scenario; Create temporary workspace and file-system fixtures.
- **Expected**: Prevents unintended state transitions.
- **Target Go Package**: `internal/workspace`
- **Source**: `core_test.exs:273`
- **Portability**: N/A in direct form — Mailbox/process-signal semantics are BEAM-specific; port as Go concurrency/integration test intent only.

### Scenario: workspace bootstrap can be implemented in after_create hook
- **Behavior**: Validate that workspace bootstrap can be implemented in after_create hook.
- **Setup**: Prepare workflow configuration for the scenario; Create temporary workspace and file-system fixtures.
- **Expected**: Returns success when preconditions are valid; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/workspace`
- **Source**: `workspace_and_config_test.exs:5`

### Scenario: workspace path is deterministic per issue identifier
- **Behavior**: Validate that workspace path is deterministic per issue identifier.
- **Setup**: Prepare workflow configuration for the scenario; Create temporary workspace and file-system fixtures.
- **Expected**: Returns success when preconditions are valid; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/workspace`
- **Source**: `workspace_and_config_test.exs:40`

### Scenario: workspace reuses existing issue directory without deleting local changes
- **Behavior**: Validate that workspace reuses existing issue directory without deleting local changes.
- **Setup**: Prepare workflow configuration for the scenario; Create temporary workspace and file-system fixtures.
- **Expected**: Returns success when preconditions are valid; Prevents unintended state transitions; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/workspace`
- **Source**: `workspace_and_config_test.exs:56`

### Scenario: workspace replaces stale non-directory paths
- **Behavior**: Validate that workspace replaces stale non-directory paths.
- **Setup**: Prepare workflow configuration for the scenario; Create temporary workspace and file-system fixtures.
- **Expected**: Returns success when preconditions are valid; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/workspace`
- **Source**: `workspace_and_config_test.exs:92`

### Scenario: workspace rejects symlink escapes under the configured root
- **Behavior**: Validate that workspace rejects symlink escapes under the configured root.
- **Setup**: Prepare workflow configuration for the scenario; Create temporary workspace and file-system fixtures.
- **Expected**: Returns a typed error for invalid or blocked input.
- **Target Go Package**: `internal/workspace`
- **Source**: `workspace_and_config_test.exs:114`

### Scenario: workspace remove rejects the workspace root itself with a distinct error
- **Behavior**: Validate that workspace remove rejects the workspace root itself with a distinct error.
- **Setup**: Prepare workflow configuration for the scenario; Create temporary workspace and file-system fixtures.
- **Expected**: Returns a typed error for invalid or blocked input.
- **Target Go Package**: `internal/workspace`
- **Source**: `workspace_and_config_test.exs:139`

### Scenario: workspace surfaces after_create hook failures
- **Behavior**: Validate that workspace surfaces after_create hook failures.
- **Setup**: Prepare workflow configuration for the scenario; Create temporary workspace and file-system fixtures.
- **Expected**: Returns a typed error for invalid or blocked input.
- **Target Go Package**: `internal/workspace`
- **Source**: `workspace_and_config_test.exs:157`

### Scenario: workspace surfaces after_create hook timeouts
- **Behavior**: Validate that workspace surfaces after_create hook timeouts.
- **Setup**: Prepare workflow configuration for the scenario; Create temporary workspace and file-system fixtures.
- **Expected**: Returns a typed error for invalid or blocked input.
- **Target Go Package**: `internal/workspace`
- **Source**: `workspace_and_config_test.exs:177`

### Scenario: workspace creates an empty directory when no bootstrap hook is configured
- **Behavior**: Validate that workspace creates an empty directory when no bootstrap hook is configured.
- **Setup**: Prepare workflow configuration for the scenario; Create temporary workspace and file-system fixtures.
- **Expected**: Returns success when preconditions are valid.
- **Target Go Package**: `internal/workspace`
- **Source**: `workspace_and_config_test.exs:198`

### Scenario: workspace removes all workspaces for a closed issue identifier
- **Behavior**: Validate that workspace removes all workspaces for a closed issue identifier.
- **Setup**: Prepare workflow configuration for the scenario; Create temporary workspace and file-system fixtures.
- **Expected**: Prevents unintended state transitions.
- **Target Go Package**: `internal/workspace`
- **Source**: `workspace_and_config_test.exs:218`

### Scenario: workspace cleanup handles missing workspace root
- **Behavior**: Validate that workspace cleanup handles missing workspace root.
- **Setup**: Prepare workflow configuration for the scenario; Create temporary workspace and file-system fixtures.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/workspace`
- **Source**: `workspace_and_config_test.exs:244`

### Scenario: workspace cleanup ignores non-binary identifier
- **Behavior**: Validate that workspace cleanup ignores non-binary identifier.
- **Setup**: Create temporary workspace and file-system fixtures.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/workspace`
- **Source**: `workspace_and_config_test.exs:256`

### Scenario: workspace remove returns error information for missing directory
- **Behavior**: Validate that workspace remove returns error information for missing directory.
- **Setup**: Create temporary workspace and file-system fixtures.
- **Expected**: Returns success when preconditions are valid.
- **Target Go Package**: `internal/workspace`
- **Source**: `workspace_and_config_test.exs:506`

### Scenario: workspace hooks support multiline YAML scripts and run at lifecycle boundaries
- **Behavior**: Validate that workspace hooks support multiline YAML scripts and run at lifecycle boundaries.
- **Setup**: Prepare workflow configuration for the scenario; Create temporary workspace and file-system fixtures.
- **Expected**: Returns success when preconditions are valid; Prevents unintended state transitions; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/workspace`
- **Source**: `workspace_and_config_test.exs:516`

### Scenario: workspace remove continues when before_remove hook fails
- **Behavior**: Validate that workspace remove continues when before_remove hook fails.
- **Setup**: Prepare workflow configuration for the scenario; Create temporary workspace and file-system fixtures.
- **Expected**: Returns success when preconditions are valid; Prevents unintended state transitions.
- **Target Go Package**: `internal/workspace`
- **Source**: `workspace_and_config_test.exs:553`

### Scenario: workspace remove continues when before_remove hook fails with large output
- **Behavior**: Validate that workspace remove continues when before_remove hook fails with large output.
- **Setup**: Prepare workflow configuration for the scenario; Set environment/application overrides for this test; Create temporary workspace and file-system fixtures.
- **Expected**: Returns success when preconditions are valid; Prevents unintended state transitions.
- **Target Go Package**: `internal/workspace`
- **Source**: `workspace_and_config_test.exs:578`

### Scenario: config resolves $VAR references for env-backed secret and path values
- **Behavior**: Validate that config resolves $VAR references for env-backed secret and path values.
- **Setup**: Set environment/application overrides for this test; Create temporary workspace and file-system fixtures.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/workspace`
- **Source**: `workspace_and_config_test.exs:802`

### Scenario: config no longer resolves legacy env: references
- **Behavior**: Validate that config no longer resolves legacy env: references.
- **Setup**: Set environment/application overrides for this test; Create temporary workspace and file-system fixtures.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/workspace`
- **Source**: `workspace_and_config_test.exs:831`

## Package: internal/agent

### Scenario: app server rejects the workspace root and paths outside workspace root
- **Behavior**: Validate that app server rejects the workspace root and paths outside workspace root.
- **Setup**: Prepare workflow configuration for the scenario; Create temporary workspace and file-system fixtures; Run app-server/agent flow with deterministic fake Codex responses.
- **Expected**: Returns a typed error for invalid or blocked input.
- **Target Go Package**: `internal/agent`
- **Source**: `app_server_test.exs:4`

### Scenario: app server marks request-for-input events as a hard failure
- **Behavior**: Validate that app server marks request-for-input events as a hard failure.
- **Setup**: Prepare workflow configuration for the scenario; Set environment/application overrides for this test; Create temporary workspace and file-system fixtures; Run app-server/agent flow with deterministic fake Codex responses.
- **Expected**: Returns a typed error for invalid or blocked input; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/agent`
- **Source**: `app_server_test.exs:42`

### Scenario: app server fails when command execution approval is required under safer defaults
- **Behavior**: Validate that app server fails when command execution approval is required under safer defaults.
- **Setup**: Prepare workflow configuration for the scenario; Set environment/application overrides for this test; Create temporary workspace and file-system fixtures; Run app-server/agent flow with deterministic fake Codex responses; Use tracker client fixture data or stubbed GraphQL responses.
- **Expected**: Returns a typed error for invalid or blocked input; Returns success when preconditions are valid; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/agent`
- **Source**: `app_server_test.exs:121`

### Scenario: app server auto-approves MCP tool approval prompts when approval policy is never
- **Behavior**: Validate that app server auto-approves MCP tool approval prompts when approval policy is never.
- **Setup**: Prepare workflow configuration for the scenario; Set environment/application overrides for this test; Create temporary workspace and file-system fixtures; Run app-server/agent flow with deterministic fake Codex responses.
- **Expected**: Returns success when preconditions are valid; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/agent`
- **Source**: `app_server_test.exs:321`

### Scenario: app server sends a generic non-interactive answer for freeform tool input prompts
- **Behavior**: Validate that app server sends a generic non-interactive answer for freeform tool input prompts.
- **Setup**: Prepare workflow configuration for the scenario; Create temporary workspace and file-system fixtures; Run app-server/agent flow with deterministic fake Codex responses.
- **Expected**: Returns success when preconditions are valid; Emits expected asynchronous update or event messages.
- **Target Go Package**: `internal/agent`
- **Source**: `app_server_test.exs:420`
- **Portability**: N/A in direct form — Mailbox/process-signal semantics are BEAM-specific; port as Go concurrency/integration test intent only.

### Scenario: app server sends a generic non-interactive answer for option-based tool input prompts
- **Behavior**: Validate that app server sends a generic non-interactive answer for option-based tool input prompts.
- **Setup**: Prepare workflow configuration for the scenario; Set environment/application overrides for this test; Create temporary workspace and file-system fixtures; Run app-server/agent flow with deterministic fake Codex responses.
- **Expected**: Returns success when preconditions are valid; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/agent`
- **Source**: `app_server_test.exs:496`

### Scenario: app server rejects unsupported dynamic tool calls without stalling
- **Behavior**: Validate that app server rejects unsupported dynamic tool calls without stalling.
- **Setup**: Prepare workflow configuration for the scenario; Set environment/application overrides for this test; Create temporary workspace and file-system fixtures; Run app-server/agent flow with deterministic fake Codex responses.
- **Expected**: Returns success when preconditions are valid; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/agent`
- **Source**: `app_server_test.exs:596`

### Scenario: app server executes supported dynamic tool calls and returns the tool result
- **Behavior**: Validate that app server executes supported dynamic tool calls and returns the tool result.
- **Setup**: Prepare workflow configuration for the scenario; Set environment/application overrides for this test; Create temporary workspace and file-system fixtures; Run app-server/agent flow with deterministic fake Codex responses; Use tracker client fixture data or stubbed GraphQL responses.
- **Expected**: Returns success when preconditions are valid; Emits expected asynchronous update or event messages; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/agent`
- **Source**: `app_server_test.exs:698`
- **Portability**: N/A in direct form — Mailbox/process-signal semantics are BEAM-specific; port as Go concurrency/integration test intent only.

### Scenario: app server emits tool_call_failed for supported tool failures
- **Behavior**: Validate that app server emits tool_call_failed for supported tool failures.
- **Setup**: Prepare workflow configuration for the scenario; Set environment/application overrides for this test; Create temporary workspace and file-system fixtures; Run app-server/agent flow with deterministic fake Codex responses; Use tracker client fixture data or stubbed GraphQL responses.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/agent`
- **Source**: `app_server_test.exs:820`
- **Portability**: N/A in direct form — Mailbox/process-signal semantics are BEAM-specific; port as Go concurrency/integration test intent only.

### Scenario: app server buffers partial JSON lines until newline terminator
- **Behavior**: Validate that app server buffers partial JSON lines until newline terminator.
- **Setup**: Prepare workflow configuration for the scenario; Create temporary workspace and file-system fixtures; Run app-server/agent flow with deterministic fake Codex responses.
- **Expected**: Returns success when preconditions are valid; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/agent`
- **Source**: `app_server_test.exs:926`

### Scenario: config defaults and validation checks
- **Behavior**: Validate that config defaults and validation checks.
- **Setup**: Prepare workflow configuration for the scenario; Create temporary workspace and file-system fixtures.
- **Expected**: Returns a typed error for invalid or blocked input; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/agent`
- **Source**: `core_test.exs:4`

### Scenario: prompt builder renders issue and attempt values from workflow template
- **Behavior**: Validate that prompt builder renders issue and attempt values from workflow template.
- **Setup**: Prepare workflow configuration for the scenario.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/agent`
- **Source**: `core_test.exs:552`

### Scenario: prompt builder renders issue datetime fields without crashing
- **Behavior**: Validate that prompt builder renders issue datetime fields without crashing.
- **Setup**: Prepare workflow configuration for the scenario.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/agent`
- **Source**: `core_test.exs:574`

### Scenario: prompt builder normalizes nested date-like values, maps, and structs in issue fields
- **Behavior**: Validate that prompt builder normalizes nested date-like values, maps, and structs in issue fields.
- **Setup**: Prepare workflow configuration for the scenario.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/agent`
- **Source**: `core_test.exs:600`

### Scenario: prompt builder uses strict variable rendering
- **Behavior**: Validate that prompt builder uses strict variable rendering.
- **Setup**: Prepare workflow configuration for the scenario.
- **Expected**: Raises an explicit error for malformed templates or missing prerequisites.
- **Target Go Package**: `internal/agent`
- **Source**: `core_test.exs:621`

### Scenario: prompt builder surfaces invalid template content with prompt context
- **Behavior**: Validate that prompt builder surfaces invalid template content with prompt context.
- **Setup**: Prepare workflow configuration for the scenario.
- **Expected**: Raises an explicit error for malformed templates or missing prerequisites.
- **Target Go Package**: `internal/agent`
- **Source**: `core_test.exs:640`

### Scenario: prompt builder uses a sensible default template when workflow prompt is blank
- **Behavior**: Validate that prompt builder uses a sensible default template when workflow prompt is blank.
- **Setup**: Prepare workflow configuration for the scenario.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/agent`
- **Source**: `core_test.exs:657`

### Scenario: prompt builder default template handles missing issue body
- **Behavior**: Validate that prompt builder default template handles missing issue body.
- **Setup**: Prepare workflow configuration for the scenario.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/agent`
- **Source**: `core_test.exs:681`

### Scenario: prompt builder reports workflow load failures separately from template parse errors
- **Behavior**: Validate that prompt builder reports workflow load failures separately from template parse errors.
- **Setup**: Prepare workflow configuration for the scenario.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/agent`
- **Source**: `core_test.exs:700`
- **Portability**: N/A in direct form — Mailbox/process-signal semantics are BEAM-specific; port as Go concurrency/integration test intent only.

### Scenario: prompt builder adds continuation guidance for retries
- **Behavior**: Validate that prompt builder adds continuation guidance for retries.
- **Setup**: Prepare workflow configuration for the scenario.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/agent`
- **Source**: `core_test.exs:762`

### Scenario: agent runner keeps workspace after successful codex run
- **Behavior**: Validate that agent runner keeps workspace after successful codex run.
- **Setup**: Prepare workflow configuration for the scenario; Set environment/application overrides for this test; Create temporary workspace and file-system fixtures; Run app-server/agent flow with deterministic fake Codex responses.
- **Expected**: Emits expected asynchronous update or event messages; Prevents unintended state transitions; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/agent`
- **Source**: `core_test.exs:780`
- **Portability**: N/A in direct form — Mailbox/process-signal semantics are BEAM-specific; port as Go concurrency/integration test intent only.

### Scenario: agent runner stops continuing once agent.max_turns is reached
- **Behavior**: Validate that agent runner stops continuing once agent.max_turns is reached.
- **Setup**: Prepare workflow configuration for the scenario; Set environment/application overrides for this test; Create temporary workspace and file-system fixtures; Run app-server/agent flow with deterministic fake Codex responses.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/agent`
- **Source**: `core_test.exs:1083`

### Scenario: app server starts with workspace cwd and expected startup command
- **Behavior**: Validate that app server starts with workspace cwd and expected startup command.
- **Setup**: Prepare workflow configuration for the scenario; Set environment/application overrides for this test; Create temporary workspace and file-system fixtures; Run app-server/agent flow with deterministic fake Codex responses.
- **Expected**: Returns success when preconditions are valid; Prevents unintended state transitions.
- **Target Go Package**: `internal/agent`
- **Source**: `core_test.exs:1180`

### Scenario: app server startup command supports codex args override from workflow config
- **Behavior**: Validate that app server startup command supports codex args override from workflow config.
- **Setup**: Prepare workflow configuration for the scenario; Set environment/application overrides for this test; Create temporary workspace and file-system fixtures; Run app-server/agent flow with deterministic fake Codex responses.
- **Expected**: Returns success when preconditions are valid; Prevents unintended state transitions.
- **Target Go Package**: `internal/agent`
- **Source**: `core_test.exs:1325`

### Scenario: app server startup payload uses configurable approval and sandbox settings from workflow config
- **Behavior**: Validate that app server startup payload uses configurable approval and sandbox settings from workflow config.
- **Setup**: Prepare workflow configuration for the scenario; Set environment/application overrides for this test; Create temporary workspace and file-system fixtures; Run app-server/agent flow with deterministic fake Codex responses.
- **Expected**: Returns success when preconditions are valid; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/agent`
- **Source**: `core_test.exs:1410`

## Package: internal/orchestrator

### Scenario: current WORKFLOW.md file is valid and complete
- **Behavior**: Validate that current WORKFLOW.md file is valid and complete.
- **Setup**: Prepare workflow configuration for the scenario.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `core_test.exs:74`

### Scenario: SymphonyElixir.start_link delegates to the orchestrator
- **Behavior**: Validate that SymphonyElixir.start_link delegates to the orchestrator.
- **Setup**: Prepare workflow configuration for the scenario; Set environment/application overrides for this test.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `core_test.exs:182`
- **Portability**: N/A in direct form — Mailbox/process-signal semantics are BEAM-specific; port as Go concurrency/integration test intent only.

### Scenario: reconcile updates running issue state for active issues
- **Behavior**: Validate that reconcile updates running issue state for active issues.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `core_test.exs:336`

### Scenario: reconcile stops running issue when it is reassigned away from this worker
- **Behavior**: Validate that reconcile stops running issue when it is reassigned away from this worker.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `core_test.exs:375`
- **Portability**: N/A in direct form — Mailbox/process-signal semantics are BEAM-specific; port as Go concurrency/integration test intent only.

### Scenario: normal worker exit schedules active-state continuation retry
- **Behavior**: Validate that normal worker exit schedules active-state continuation retry.
- **Setup**: Start orchestrator with controlled in-memory state.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `core_test.exs:422`

### Scenario: abnormal worker exit increments retry attempt progressively
- **Behavior**: Validate that abnormal worker exit increments retry attempt progressively.
- **Setup**: Start orchestrator with controlled in-memory state.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `core_test.exs:462`

### Scenario: first abnormal worker exit waits before retrying
- **Behavior**: Validate that first abnormal worker exit waits before retrying.
- **Setup**: Start orchestrator with controlled in-memory state.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `core_test.exs:502`

### Scenario: in-repo WORKFLOW.md renders correctly
- **Behavior**: Validate that in-repo WORKFLOW.md renders correctly.
- **Setup**: Prepare workflow configuration for the scenario.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `core_test.exs:730`

### Scenario: snapshot returns :timeout when snapshot server is unresponsive
- **Behavior**: Validate that snapshot returns :timeout when snapshot server is unresponsive.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `orchestrator_status_test.exs:4`
- **Portability**: N/A in direct form — Mailbox/process-signal semantics are BEAM-specific; port as Go concurrency/integration test intent only.

### Scenario: orchestrator snapshot reflects last codex update and session id
- **Behavior**: Validate that orchestrator snapshot reflects last codex update and session id.
- **Setup**: Start orchestrator with controlled in-memory state.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `orchestrator_status_test.exs:24`

### Scenario: orchestrator snapshot tracks codex thread totals and app-server pid
- **Behavior**: Validate that orchestrator snapshot tracks codex thread totals and app-server pid.
- **Setup**: Start orchestrator with controlled in-memory state.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `orchestrator_status_test.exs:104`

### Scenario: orchestrator snapshot tracks turn completed usage when present
- **Behavior**: Validate that orchestrator snapshot tracks turn completed usage when present.
- **Setup**: Start orchestrator with controlled in-memory state.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `orchestrator_status_test.exs:202`

### Scenario: orchestrator snapshot tracks codex token-count cumulative usage payloads
- **Behavior**: Validate that orchestrator snapshot tracks codex token-count cumulative usage payloads.
- **Setup**: Start orchestrator with controlled in-memory state.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `orchestrator_status_test.exs:277`

### Scenario: orchestrator snapshot tracks codex rate-limit payloads
- **Behavior**: Validate that orchestrator snapshot tracks codex rate-limit payloads.
- **Setup**: Start orchestrator with controlled in-memory state.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `orchestrator_status_test.exs:390`

### Scenario: orchestrator token accounting prefers total_token_usage over last_token_usage in token_count payloads
- **Behavior**: Validate that orchestrator token accounting prefers total_token_usage over last_token_usage in token_count payloads.
- **Setup**: Start orchestrator with controlled in-memory state.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `orchestrator_status_test.exs:471`

### Scenario: orchestrator token accounting accumulates monotonic thread token usage totals
- **Behavior**: Validate that orchestrator token accounting accumulates monotonic thread token usage totals.
- **Setup**: Start orchestrator with controlled in-memory state.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `orchestrator_status_test.exs:559`

### Scenario: orchestrator token accounting ignores last_token_usage without cumulative totals
- **Behavior**: Validate that orchestrator token accounting ignores last_token_usage without cumulative totals.
- **Setup**: Start orchestrator with controlled in-memory state.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `orchestrator_status_test.exs:633`

### Scenario: orchestrator snapshot includes retry backoff entries
- **Behavior**: Validate that orchestrator snapshot includes retry backoff entries.
- **Setup**: Start orchestrator with controlled in-memory state.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `orchestrator_status_test.exs:716`

### Scenario: orchestrator snapshot includes poll countdown and checking status
- **Behavior**: Validate that orchestrator snapshot includes poll countdown and checking status.
- **Setup**: Start orchestrator with controlled in-memory state.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `orchestrator_status_test.exs:754`

### Scenario: orchestrator triggers an immediate poll cycle shortly after startup
- **Behavior**: Validate that orchestrator triggers an immediate poll cycle shortly after startup.
- **Setup**: Prepare workflow configuration for the scenario; Start orchestrator with controlled in-memory state.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `orchestrator_status_test.exs:797`

### Scenario: orchestrator poll cycle resets next refresh countdown after a check
- **Behavior**: Validate that orchestrator poll cycle resets next refresh countdown after a check.
- **Setup**: Prepare workflow configuration for the scenario; Start orchestrator with controlled in-memory state.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `orchestrator_status_test.exs:849`

### Scenario: orchestrator restarts stalled workers with retry backoff
- **Behavior**: Validate that orchestrator restarts stalled workers with retry backoff.
- **Setup**: Prepare workflow configuration for the scenario; Start orchestrator with controlled in-memory state.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `orchestrator_status_test.exs:898`

### Scenario: application stop renders offline status
- **Behavior**: Validate that application stop renders offline status.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `orchestrator_status_test.exs:1543`

### Scenario: orchestrator sorts dispatch by priority then oldest created_at
- **Behavior**: Validate that orchestrator sorts dispatch by priority then oldest created_at.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/orchestrator`
- **Source**: `workspace_and_config_test.exs:380`

## Package: internal/tui

### Scenario: status dashboard renders offline marker to terminal
- **Behavior**: Validate that status dashboard renders offline marker to terminal.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:962`

### Scenario: status dashboard renders linear project link in header
- **Behavior**: Validate that status dashboard renders linear project link in header.
- **Setup**: Build snapshot payload representative of runtime/dashboard state.
- **Expected**: Prevents unintended state transitions; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:972`

### Scenario: status dashboard renders dashboard url on its own line when server port is configured
- **Behavior**: Validate that status dashboard renders dashboard url on its own line when server port is configured.
- **Setup**: Set environment/application overrides for this test.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:988`

### Scenario: status dashboard prefers the bound server port and normalizes wildcard hosts
- **Behavior**: Validate that status dashboard prefers the bound server port and normalizes wildcard hosts.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1018`

### Scenario: status dashboard renders next refresh countdown and checking marker
- **Behavior**: Validate that status dashboard renders next refresh countdown and checking marker.
- **Setup**: Build snapshot payload representative of runtime/dashboard state.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1026`

### Scenario: status dashboard adds a spacer line before backoff queue when no agents are active
- **Behavior**: Validate that status dashboard adds a spacer line before backoff queue when no agents are active.
- **Setup**: Build snapshot payload representative of runtime/dashboard state.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1055`

### Scenario: status dashboard adds a spacer line before backoff queue when agents are active
- **Behavior**: Validate that status dashboard adds a spacer line before backoff queue when agents are active.
- **Setup**: Build snapshot payload representative of runtime/dashboard state.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1071`

### Scenario: status dashboard renders an unstyled closing corner when the retry queue is empty
- **Behavior**: Validate that status dashboard renders an unstyled closing corner when the retry queue is empty.
- **Setup**: Build snapshot payload representative of runtime/dashboard state.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1110`

### Scenario: status dashboard coalesces rapid updates to one render per interval
- **Behavior**: Validate that status dashboard coalesces rapid updates to one render per interval.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1125`
- **Portability**: N/A in direct form — Mailbox/process-signal semantics are BEAM-specific; port as Go concurrency/integration test intent only.

### Scenario: status dashboard computes rolling 5-second token throughput
- **Behavior**: Validate that status dashboard computes rolling 5-second token throughput.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1175`

### Scenario: status dashboard throttles tps updates to once per second
- **Behavior**: Validate that status dashboard throttles tps updates to once per second.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Prevents unintended state transitions; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1193`

### Scenario: status dashboard formats timestamps at second precision
- **Behavior**: Validate that status dashboard formats timestamps at second precision.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1210`

### Scenario: status dashboard renders 10-minute TPS graph snapshot for steady throughput
- **Behavior**: Validate that status dashboard renders 10-minute TPS graph snapshot for steady throughput.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1215`

### Scenario: status dashboard renders 10-minute TPS graph snapshot for ramping throughput
- **Behavior**: Validate that status dashboard renders 10-minute TPS graph snapshot for ramping throughput.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1228`

### Scenario: status dashboard keeps historical TPS bars stable within the active bucket
- **Behavior**: Validate that status dashboard keeps historical TPS bars stable within the active bucket.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1241`

### Scenario: status dashboard renders last codex message in EVENT column
- **Behavior**: Validate that status dashboard renders last codex message in EVENT column.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Prevents unintended state transitions; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1273`

### Scenario: status dashboard strips ANSI and control bytes from last codex message
- **Behavior**: Validate that status dashboard strips ANSI and control bytes from last codex message.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Prevents unintended state transitions; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1299`

### Scenario: status dashboard expands running row to requested terminal width
- **Behavior**: Validate that status dashboard expands running row to requested terminal width.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1328`

### Scenario: status dashboard humanizes full codex app-server event set
- **Behavior**: Validate that status dashboard humanizes full codex app-server event set.
- **Setup**: Use tracker client fixture data or stubbed GraphQL responses.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1358`

### Scenario: status dashboard humanizes dynamic tool wrapper events
- **Behavior**: Validate that status dashboard humanizes dynamic tool wrapper events.
- **Setup**: Use tracker client fixture data or stubbed GraphQL responses.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1404`

### Scenario: status dashboard unwraps nested codex payload envelopes
- **Behavior**: Validate that status dashboard unwraps nested codex payload envelopes.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1436`

### Scenario: status dashboard uses shell command line as exec command status text
- **Behavior**: Validate that status dashboard uses shell command line as exec command status text.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1455`

### Scenario: status dashboard formats auto-approval updates from codex
- **Behavior**: Validate that status dashboard formats auto-approval updates from codex.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1467`

### Scenario: status dashboard formats auto-answered tool input updates from codex
- **Behavior**: Validate that status dashboard formats auto-answered tool input updates from codex.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1484`

### Scenario: status dashboard enriches wrapper reasoning and message streaming events with payload context
- **Behavior**: Validate that status dashboard enriches wrapper reasoning and message streaming events with payload context.
- **Setup**: Construct minimal inputs and invoke the target module.
- **Expected**: Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `orchestrator_status_test.exs:1501`

### Scenario: snapshot fixture: idle dashboard
- **Behavior**: Validate that snapshot fixture: idle dashboard.
- **Setup**: Build snapshot payload representative of runtime/dashboard state.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/tui`
- **Source**: `status_dashboard_snapshot_test.exs:8`

### Scenario: snapshot fixture: idle dashboard with observability url
- **Behavior**: Validate that snapshot fixture: idle dashboard with observability url.
- **Setup**: Set environment/application overrides for this test.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/tui`
- **Source**: `status_dashboard_snapshot_test.exs:21`

### Scenario: snapshot fixture: super busy dashboard
- **Behavior**: Validate that snapshot fixture: super busy dashboard.
- **Setup**: Build snapshot payload representative of runtime/dashboard state.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/tui`
- **Source**: `status_dashboard_snapshot_test.exs:46`

### Scenario: snapshot fixture: backoff queue pressure
- **Behavior**: Validate that snapshot fixture: backoff queue pressure.
- **Setup**: Build snapshot payload representative of runtime/dashboard state.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/tui`
- **Source**: `status_dashboard_snapshot_test.exs:88`

### Scenario: backoff queue row escapes escaped newline sequences
- **Behavior**: Validate that backoff queue row escapes escaped newline sequences.
- **Setup**: Build snapshot payload representative of runtime/dashboard state.
- **Expected**: Prevents unintended state transitions; Computed values, ordering, or rendered output match expected content.
- **Target Go Package**: `internal/tui`
- **Source**: `status_dashboard_snapshot_test.exs:141`

### Scenario: snapshot fixture: unlimited credits variant
- **Behavior**: Validate that snapshot fixture: unlimited credits variant.
- **Setup**: Build snapshot payload representative of runtime/dashboard state.
- **Expected**: Behavior matches the scenario assertions.
- **Target Go Package**: `internal/tui`
- **Source**: `status_dashboard_snapshot_test.exs:169`

## Flagged as N/A (Elixir-Specific)

- `app server sends a generic non-interactive answer for freeform tool input prompts` (app_server_test.exs:420): Mailbox/process-signal semantics are BEAM-specific.
- `app server executes supported dynamic tool calls and returns the tool result` (app_server_test.exs:698): Mailbox/process-signal semantics are BEAM-specific.
- `app server emits tool_call_failed for supported tool failures` (app_server_test.exs:820): Mailbox/process-signal semantics are BEAM-specific.
- `SymphonyElixir.start_link delegates to the orchestrator` (core_test.exs:182): Mailbox/process-signal semantics are BEAM-specific.
- `non-active issue state stops running agent without cleaning workspace` (core_test.exs:210): Mailbox/process-signal semantics are BEAM-specific.
- `terminal issue state stops running agent and cleans workspace` (core_test.exs:273): Mailbox/process-signal semantics are BEAM-specific.
- `reconcile stops running issue when it is reassigned away from this worker` (core_test.exs:375): Mailbox/process-signal semantics are BEAM-specific.
- `prompt builder reports workflow load failures separately from template parse errors` (core_test.exs:700): Mailbox/process-signal semantics are BEAM-specific.
- `agent runner keeps workspace after successful codex run` (core_test.exs:780): Mailbox/process-signal semantics are BEAM-specific.
- `snapshot returns :timeout when snapshot server is unresponsive` (orchestrator_status_test.exs:4): Mailbox/process-signal semantics are BEAM-specific.
- `status dashboard coalesces rapid updates to one render per interval` (orchestrator_status_test.exs:1125): Mailbox/process-signal semantics are BEAM-specific.
