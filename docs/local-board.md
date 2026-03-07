# Local board tracker

Contrabass can now run against a filesystem-backed internal tracker with
`tracker.type: internal` (the legacy alias `local` is also accepted).

## Configuration

```yaml
tracker:
  type: internal
  board_dir: .contrabass/board
  issue_prefix: CB
team:
  execution_mode: team
```

## Storage layout

```text
.contrabass/
  board/
    manifest.json
    issues/
      CB-1.json
      CB-2.json
    comments/
      CB-1.jsonl
```

- `manifest.json` stores the issue prefix and next issue number.
- `issues/*.json` stores the board card source of truth.
- `comments/*.jsonl` stores append-only issue comments.

The internal board is intentionally separate from team runtime state under
`.contrabass/state/team/...`:

- `.contrabass/board/...` = long-lived tracker / kanban state
- `.contrabass/state/team/...` = execution-time worker coordination state

## CLI

```bash
contrabass board init --prefix CB
contrabass board create --title "Fix retry loop" --labels bug,orchestrator
contrabass board create --title "Ship team bridge" --parent CB-1 --assignee team-alpha --blocked-by CB-2
contrabass board list
contrabass board assign CB-1 team-alpha
contrabass board move CB-1 in_progress
contrabass board comment CB-1 --body "agent run started"
contrabass board show CB-1
contrabass board dispatch --config WORKFLOW.md --team-name team-alpha
contrabass board dispatch --config WORKFLOW.md --until-empty
contrabass team run --config WORKFLOW.md --issue CB-1
```

Supported board states:

- `todo`
- `in_progress`
- `retry`
- `done`

## Orchestrator mapping

The internal board maps board states to the existing tracker contract:

- `todo` → `Unclaimed`
- `in_progress` → `Claimed` / `Running`
- `retry` → `RetryQueued`
- `done` → `Released`

This keeps the orchestrator logic unchanged while enabling local-first
project tracking.

## Team bridge

`contrabass team run --issue CB-1` hydrates the internal board issue into a
team run automatically.

Current bridge behavior:

- derives a default team name from the board issue when `--name` is omitted
- generates a plan → PRD → exec task chain from the board issue
- when child board issues exist, turns each non-`done` child ticket into its own
  executable team task and maps child `blocked_by` edges into team task
  dependencies
- claims the board issue to the team automatically
- appends team lifecycle activity back to board comments
- mirrors child task lifecycle back into child board ticket state/comments
- moves the board issue to `done` on success or `retry` on run failure

This is the first slice of the autonomous board/team loop where AI agents can
create board issues, assign them to a team run, and let the runtime move the
ticket state automatically.

## Dispatch loop

`contrabass board dispatch --config WORKFLOW.md` closes the next gap in the
autonomous loop by selecting the oldest runnable internal issue and forwarding
it into the existing `team run --issue` path.

Dispatch rules:

- only `todo` and `retry` issues are eligible
- claimed issues are skipped
- child issues are skipped while their parent issue is already running
- blockers listed in `blocked_by` must already be `done`
- `--team-name` limits selection to matching or unassigned issues
- if an issue already has an `assignee`, that becomes the default team name

This lets the harness create and assign tickets on the internal board, then let
Contrabass automatically pick the next ready ticket and execute it with the
team runtime.

## Root command integration

When the workflow resolves to the internal tracker, the default
`contrabass --config WORKFLOW.md` path now uses the board/team runtime instead
of the legacy single-agent orchestrator path. That means the default root app:

- polls the internal board for runnable tickets
- dispatches them into `contrabass team` execution automatically
- updates `.contrabass/board` state as team runs progress
- surfaces board-linked team state in the main TUI

To force the original single-agent root behavior for an internal workflow, set:

```yaml
team:
  execution_mode: single
```

Use `--until-empty` to keep draining the runnable queue until the board has no
ready tickets left. This is useful when the AI planner has already created and
assigned a batch of work and you want Contrabass to keep launching team runs
until every currently-unblocked ticket has been consumed.

Drain mode behavior:

- prints one `dispatched <issue> to <team>` line per launched team run
- exits successfully with `board already drained` when nothing is runnable
- prints `drained board after N dispatches` after consuming the current queue
- stops immediately if any dispatched team run returns an error
