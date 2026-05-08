# Surface omx token usage to contrabass dashboard

## Why

The contrabass dashboard always shows `0 输入 / 0 输出` tokens when the agent
type is `omx`. The downstream consumer (`parseUsageTokens` in
`internal/orchestrator/orchestrator_runtime.go`) is wired correctly, the SSE
hub fans events out, and the React panel reads `Stats.TotalTokensIn/Out`.
The break is in the **producer**: events emitted by `teamCLIRunner` for omx
runs (`turn/started`, `session.status`, `item/*`, `task/*`) carry
`team_name`, `phase`, `summary`, `task` — never a `usage` field. omx tracks
token counts per turn in `<workspace>/.omx/metrics.json`, but contrabass
never reads that file.

A previous attempt at this change (Linear ZII-47, codex commit 6baf416)
modified only `*_test.go` and dashboard `*.test.ts(x)` files and shipped
zero producer code, leaving the dashboard still at `0/0`. That failure is
the motivation for the spec-driven decomposition: the work splits cleanly
into 5 atomic units that an agent cannot accidentally stub out.

## What Changes

- Read `<workspace>/.omx/metrics.json` from inside `teamCLIRunner.monitorProcess`
  on every poll cycle.
- Emit a new `session.usage` agent event with `Data["usage"]` shaped
  `{input_tokens, output_tokens, total_tokens}` — the exact shape that
  `parseUsageTokens` already understands as cumulative totals.
- Throttle: only emit when token totals actually change since the last
  emit (avoid event flooding).
- No changes to the orchestrator, the SSE hub, or any frontend code. The
  existing pipeline accepts the new event with no code changes.

## Impact

- Affected specs: `team-runtime` (new behavior on the polling loop).
- Affected code: `internal/agent/teamcli.go` (producer + helper),
  `internal/agent/teamcli_test.go` (unit coverage).
- Out of scope: 5-hour and weekly Linear/OpenAI quota percentages
  (omx exposes them in the same file but they don't fit the existing
  `usage` schema; deferred to a follow-up change).
- Backward compatibility: yes — runners other than omx skip the read
  (file simply doesn't exist).
- Dashboard refactor: no — the existing token panel re-renders on the
  new stats values automatically via SSE.
