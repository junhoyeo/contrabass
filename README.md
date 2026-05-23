<div align="center">

# Contrabass

<img alt="Contrabass Logo" src="https://raw.githubusercontent.com/junhoyeo/contrabass/main/.github/assets/contrabass.png" width="300px" />

> **A project-level orchestrator for AI coding agents** <br />
> Go + Charm stack reimplementation of OpenAI's Symphony ([openai/symphony](https://github.com/openai/symphony)) — manage work, not agents

![Contrabass Demo (TUI in Action)](https://raw.githubusercontent.com/junhoyeo/contrabass/main/.github/assets/demo.png)

</div>

Contrabass is a terminal-first orchestrator for issue-driven agent runs, with an optional local web dashboard for live visibility.

## Current scope

Today Contrabass ships with:

- A Cobra CLI with TUI, headless, and optional embedded web dashboard modes
- A `WORKFLOW.md` parser with YAML front matter, Liquid prompt rendering, and `$ENV_VAR` interpolation
- Issue tracker adapters for **Linear**, **GitHub Issues**, and a built-in **Internal Board** (local filesystem, no external service required)
- Agent runners for **Codex app-server**, **OpenCode**, **oh-my-opencode**, **OMX (oh-my-codex)**, and **OMC (oh-my-claudecode)**
- Git-worktree-based workspace provisioning under `workspaces/<issue-id>` with non-git fallback for repositories without git
- Teams: multi-agent coordination with a local task board, phased pipeline (plan → exec → verify), live TUI team table, and dual worker modes (tmux-based multi-process or goroutine-based in-process)
- An orchestrator with claim/release, BlockedBy gating, orphan claim recovery, branch advance verification, stall detection, deterministic retry backoff, liveness snapshots with agent stage classification and ETA estimation
- A Charm v2 terminal UI built with Bubble Tea, Bubbles, and Lip Gloss
- **Ziikoo** — a React dashboard (neo-brutalism theme, shadcn + Tailwind v4) with a three-pane IDE-style layout, live SSE streaming, queue navigation, stage progression pills, completion ETAs, issue detail sheets with Linear metadata and workflow timelines, team/worker tables, agent logs, and zh-CN localization
- Go unit/integration tests, TUI snapshot tests, and dashboard component/hook tests
- A tmux-based multi-process worker mode (default) alongside the in-process goroutine mode, with JSONL event logging, file-based heartbeats, dispatch queue, governance policies, and crash recovery

## Requirements

- **Go 1.25+**
- **Bun 1.3+** for the dashboard/landing workspace
- **Git** (workspace creation uses `git worktree`)
- **tmux** (required for the default tmux worker mode in team runs; not needed for goroutine mode)
- A supported agent runtime:
  - `codex app-server`
  - `opencode serve`
  - [`oh-my-opencode`](https://github.com/code-yeongyu/oh-my-openagent)
  - `omx` ([oh-my-codex](https://github.com/Yeachan-Heo/oh-my-codex) team runtime)
  - `omc` ([oh-my-claudecode](https://github.com/Yeachan-Heo/oh-my-claudecode) team runtime)
- Tracker credentials for the backend you use:
  - Linear: `LINEAR_API_KEY`
  - GitHub: `GITHUB_TOKEN`

From a fresh clone, run `bun install` once before using the JS/landing build and test commands.

## Installation

### Homebrew (macOS/Linux)

```bash
brew install junhoyeo/contrabass/contrabass
```

### Nix

```bash
# Run without installing
nix --extra-experimental-features 'nix-command flakes' run github:junhoyeo/contrabass -- --help

# Install to profile
nix profile install github:junhoyeo/contrabass

# Enter development shell (provides Go, Bun, etc.)
nix develop github:junhoyeo/contrabass
```

For NixOS system-wide installation, add to your configuration:

```nix
{
  inputs.contrabass.url = "github:junhoyeo/contrabass";
  # ...
  environment.systemPackages = [ inputs.contrabass.packages.${system}.default ];
}
```

### Download from GitHub Releases

Pre-built binaries for macOS and Linux (amd64/arm64) are available on the
[Releases](https://github.com/junhoyeo/contrabass/releases) page.

### Build from source

```bash
git clone https://github.com/junhoyeo/contrabass.git
cd contrabass
bun install
make build
```

`make build` first builds `packages/dashboard/dist/` and then embeds it into the Go binary.

> **Note:** `go install github.com/junhoyeo/contrabass/cmd/contrabass@latest` works for the
> CLI and TUI, but the embedded web dashboard (`--port`) will be empty because `go install`
> does not run the JS build step.

## Quick start

### Run with the demo workflow

```bash
LINEAR_API_KEY=your-linear-token \
./contrabass --config testdata/workflow.demo.md
```

### Run with the embedded web dashboard

```bash
LINEAR_API_KEY=your-linear-token \
./contrabass --config testdata/workflow.demo.md --port 8080
```

Then open `http://localhost:8080`.

### Run headless

```bash
LINEAR_API_KEY=your-linear-token \
./contrabass --config testdata/workflow.demo.md --no-tui
```

### CLI flags

```text
--config string      path to WORKFLOW.md file (required)
--dry-run            exit after first poll cycle
--log-file string    log output path (default "contrabass.log")
--log-level string   log level (debug/info/warn/error) (default "info")
--no-tui             headless mode — skip TUI, log events to stdout
--port int           web dashboard port (0 = disabled)
```

#### Team subcommand flags

```text
contrabass team run --config workflow.md [flags]

--worker-mode string   override worker mode (goroutine|tmux, default from config)
```

## How Contrabass works

1. Poll the configured tracker for candidate issues.
2. Skip issues with unresolved `BlockedBy` dependencies (BlockedBy gating).
3. Claim an eligible issue, recording the workspace HEAD SHA at claim time.
4. Create or reuse a git worktree in `workspaces/<issue-id>` (falls back to plain directory when git is unavailable).
5. Render the prompt body from `WORKFLOW.md` using issue data.
6. Launch the configured agent runner.
7. Stream agent events, classify agent stage (Exploration → Editing → Testing → Reviewing → Wrapping), track token consumption, and estimate completion ETAs.
8. On completion, verify the workspace branch advanced beyond the claim HEAD before marking success.
9. On failure, retry with deterministic exponential backoff + FNV-hash jitter.
10. Recover orphaned claims on restart — issues marked Claimed but not actively running are reset to Unclaimed.
11. Mirror state into the TUI, the Ziikoo dashboard (via SSE), and the JSON snapshot API.

### Orchestrator features

| Feature | Description |
|---------|-------------|
| **BlockedBy gating** | Issues with unresolved blockers are deferred from dispatch |
| **Orphan claim recovery** | Claimed-but-not-running issues are reclaimed on restart |
| **Branch advance verification** | Verifies agents made commits before marking success |
| **Agent stage classification** | Monotonic 5-stage progression based on diff velocity and token patterns |
| **Completion ETA** | Confidence-banded estimates (requires 3+ min elapsed, stage ≥ 3 for high confidence) |
| **Liveness snapshots** | Per-agent heartbeat age, activity timestamps, diff stats, iteration progress |
| **Stall detection** | Flags runs lacking recent events beyond `stall_timeout_ms` |
| **Deterministic backoff** | Exponential growth with FNV-hash jitter (reproducible across restarts) |
| **Graceful shutdown** | Drains running agents before process exit |

### Runtime notes

- `WORKFLOW.md` is watched with `fsnotify`; on parse errors, Contrabass keeps the last known good config.
- The Codex runner speaks newline-delimited JSON (`JSONL`) to `codex app-server` rather than `Content-Length` framed messages. See [`docs/codex-protocol.md`](docs/codex-protocol.md).
- The Codex runner handles `-32001` server overload errors with exponential backoff retry (up to 5 attempts) and detects stalled streams via configurable read timeouts.
- The workflow parser already accepts more Symphony-shaped fields than the runtime fully consumes today. For example, `workspace`, `hooks`, and some `codex` settings are parsed, but the current runtime mainly uses tracker selection, timeouts, retry settings, binary paths, and prompt/template fields.

### Team worker modes

Teams support two worker modes, configured via `team.worker_mode` in the workflow file or the `--worker-mode` CLI flag:

| Mode | Description | Default |
|------|-------------|---------|
| `tmux` | Each worker runs in a separate tmux pane with process isolation, cross-process IPC via JSONL events, and file-based heartbeats | Yes |
| `goroutine` | Workers run as goroutines within the contrabass process — lighter weight, no tmux dependency | |

**tmux mode** (default) provides:

- Process isolation — each agent CLI runs in its own tmux pane
- JSONL event log for cross-process event streaming
- File-based heartbeat monitoring with stale detection
- Dispatch queue with ack tracking and timeout redelivery
- Governance policies with role routing heuristics
- Crash recovery with state diagnosis and automatic cleanup
- Advisory file locking via `flock(2)` for safe concurrent access

**goroutine mode** runs all workers in-process using Go's `errgroup` and `sync.Mutex`. It requires no external dependencies but shares the process address space.

Team state is persisted as JSON files under `.contrabass/state/team/{teamName}/`.

## Workflow file format

Contrabass reads a Markdown workflow file with YAML front matter followed by the prompt template body.

```md
---
max_concurrency: 3
poll_interval_ms: 2000
max_retry_backoff_ms: 240000
model: openai/gpt-5-codex
project_url: https://linear.app/acme/project/example
agent_timeout_ms: 900000
stall_timeout_ms: 60000
tracker:
  type: linear
linear:
  issue_details:
    enabled: true
  sync_comments:
    enabled: false
    mode: reply_thread
agent:
  type: codex
codex:
  binary_path: codex app-server
---
# Workflow Prompt

Issue title: {{ issue.title }}
Issue description: {{ issue.description }}
Issue URL: {{ issue.url }}

Produce code and tests that satisfy the issue requirements.
```

### Linear detail and timeline sync settings

When `tracker.type: linear` is used, the dashboard can load richer issue
metadata through the Contrabass backend without exposing Linear credentials to
browser code.

```yaml
linear:
  issue_details:
    enabled: true
  sync_comments:
    enabled: false
    mode: reply_thread # reply_thread by default; top_level is the fallback-safe mode
```

- `linear.issue_details.enabled` controls backend issue detail reads used by
  the issue detail sheet. Candidate polling remains lean.
- `linear.sync_comments.enabled` is opt-in and defaults to `false`; when
  enabled, durable workflow timeline nodes are projected to Linear comments.
- Comment sync is best-effort and asynchronous. It records retry/sync status in
  local timeline state and does not block issue completion, retry queueing, or
  dashboard rendering.
- Disable `linear.sync_comments.enabled` to preserve legacy direct completion
  comments and avoid any Linear comment projection.

### Template bindings

The current prompt renderer exposes:

- `issue.title`
- `issue.description`
- `issue.url`

### Environment-variable interpolation

String values in YAML front matter can reference environment variables using `$NAME` syntax.

Examples:

- `tracker.token: $GITHUB_TOKEN`
- `opencode.password: $OPENCODE_SERVER_PASSWORD`
- `omx.binary_path: $OMX_BINARY`
- `omc.binary_path: $OMC_BINARY`

### Linear issue details and workflow timeline

For Linear trackers, Contrabass can load richer issue metadata for the dashboard and maintain a local workflow timeline that is projected back to Linear comments only when explicitly enabled.

```yaml
tracker:
  type: linear
linear:
  issue_details:
    enabled: true
  sync_comments:
    enabled: false
    mode: reply_thread # or top_level
```

- `linear.issue_details.enabled` defaults to enabled for Linear trackers and is ignored for non-Linear trackers.
- `linear.sync_comments.enabled` defaults to `false`; comment sync is best-effort and opt-in.
- `linear.sync_comments.mode` defaults to `reply_thread`; use `top_level` when threaded replies are unsupported or undesired.
- Workflow timeline files are local Contrabass state and remain the source of truth even when Linear sync is disabled or temporarily fails.

### OMC / OMX workflow sections

For team-runtime-backed runners, set `agent.type` to `omx` or `omc` and configure the corresponding section.

```yaml
agent:
  type: omx
omx:
  binary_path: omx
  team_spec: 2:executor
  poll_interval_ms: 1500
  startup_timeout_ms: 22000
  ralph: true
```

```yaml
agent:
  type: omc
omc:
  binary_path: omc
  team_spec: 2:claude
  poll_interval_ms: 1200
  startup_timeout_ms: 21000
```

Notes:

- `binary_path` can point to the installed CLI wrapper, for example `omx` or `omc`.
- `team_spec` is passed directly to the team runtime, such as `1:executor`, `2:executor`, or `2:claude`.
- Contrabass writes the rendered task prompt into `.contrabass/runner/<runner>/...` inside the workspace and instructs the team runtime to execute from that file.
- OMC/OMX team runners generally require the underlying toolchain prerequisites those CLIs expect, especially tmux-based team support.

### Team configuration

The `team` section configures multi-agent coordination:

```yaml
team:
  max_workers: 5
  max_fix_loops: 3
  claim_lease_seconds: 300
  state_dir: .contrabass/state/team
  execution_mode: team    # team | single | auto
  worker_mode: tmux       # tmux (default) | goroutine
```

- `worker_mode`: Controls how agent workers are spawned. `tmux` (default) uses separate tmux panes with process isolation. `goroutine` runs workers in-process.
- `execution_mode`: Controls coordination strategy. `team` uses the full phased pipeline, `single` runs one agent at a time, `auto` selects based on task count.

### Example workflow files

- [`testdata/workflow.demo.md`](testdata/workflow.demo.md) — demo Linear + Codex workflow
- [`testdata/workflow.github.md`](testdata/workflow.github.md) — GitHub + OpenCode workflow
- [`testdata/workflow.ohmyopencode.md`](testdata/workflow.ohmyopencode.md) — oh-my-opencode workflow
- [`testdata/workflow.omx.md`](testdata/workflow.omx.md) — OMX workflow
- [`testdata/workflow.omc.md`](testdata/workflow.omc.md) — OMC workflow
- [`testdata/workflow.md`](testdata/workflow.md) — realistic Linear fixture

## Supported integrations

| Surface | Current support |
|---|---|
| Trackers | Linear, GitHub Issues, Internal Board |
| Agent runners | Codex app-server, OpenCode, oh-my-opencode, OMX, OMC |
| Operator surfaces | Charm TUI, Ziikoo web dashboard, headless mode |
| Live config reload | Yes (`WORKFLOW.md` via `fsnotify`) |
| State streaming | JSON snapshot API + SSE (orchestrator, team, board, agent log events) |

### Trackers

- **Linear**
  - GraphQL-based issue fetch, claim, release, state update, and comment posting
  - Can auto-resolve the assignee from the API token when `tracker.assignee_id` is omitted
- **GitHub Issues**
  - REST-based issue fetch, assign/unassign, comment, and close-on-release behavior
  - Pull requests are skipped when fetching issues
- **Internal Board**
  - File-based local issue tracking under `.contrabass/board/` — no external service required
  - Supports team-scoped boards for multi-agent coordination
  - See [`docs/local-board.md`](docs/local-board.md) for format details

### Agent runners

- **Codex**
  - Launches `codex app-server` with JSONL protocol (newline-delimited JSON, not Content-Length framed)
  - Performs `initialize` → `initialized` → `thread/start` → `turn/start`
  - Streams notifications and token usage updates in real time
  - Handles `-32001` server overload with exponential backoff retry (up to 5 attempts)
  - Detects stalled streams via configurable read timeout (`WithStreamReadTimeout`)
  - Closes stdin on terminal events (`turn/completed`, `turn/failed`, `turn/cancelled`) for clean exit
  - Supports Codex 0.128+ `thread/tokenUsage` shape
  - Forwards workflow-level `codex` config as `-c key=value` overrides (model, approval policy, sandbox)
- **OpenCode**
  - Starts or reuses an `opencode serve` process
  - Creates sessions over HTTP and streams events over SSE
- **oh-my-opencode**
  - Wraps the `oh-my-opencode` agent binary
  - HTTP session creation with SSE event streaming
- **OMX (oh-my-codex)**
  - Launches `omx team ...` with a workspace-scoped task file
  - Polls `omx team api get-summary` and `omx team api list-tasks` for status and results
  - Tracks per-session token usage (input/output/total) and rate limit proximity (5-hour, weekly)
  - Monitors worker liveness via file-based heartbeats with stale detection
  - Shuts down the team with `omx team shutdown ... --force` (and `--ralph` when configured)
  - Supports OMX v0.16+ native worker supervisor protocol
- **OMC (oh-my-claudecode)**
  - Launches `omc team ...` with a workspace-scoped task file
  - Polls `omc team api get-summary` and `omc team api list-tasks` for status and results
  - Same token/heartbeat monitoring as OMX
  - Shuts down the team with `omc team shutdown ... --force`

## Web dashboard (Ziikoo) and HTTP API

When `--port` is set, Contrabass serves **Ziikoo** — a React dashboard embedded in the Go binary — alongside a JSON/SSE API for programmatic access.

### Dashboard features

Ziikoo uses a three-pane IDE-style layout:

1. **Left sidebar** — queue navigation (running, backoff, todo, backlog, recently done, canceled) with live counts
2. **Main content** — responsive data tables with aggregate metric cards
3. **Right detail sheet** — slide-out panel with issue metadata, workflow timeline, and agent controls

Key capabilities:

- **5-step agent stage pill** showing progression: Exploration → Editing → Testing → Reviewing → Wrapping
- **Completion ETA** with confidence bands (low/medium/high)
- **Activity indicators** with freshness coloring (fresh/warm/stale based on heartbeat age)
- **Live metrics** — running load, queued count, archived count, token consumption (in/out)
- **Issue detail sheets** — Linear metadata (assignee, creator, team, project, cycle, estimate, due date, relations), workflow timeline with sync status badges, debug info (PID, session ID, workspace path)
- **Blocked queue panel** — issues deferred by BlockedBy with blocker identifiers
- **Retry queue** — backoff entries with live countdown timers
- **Team table** — team phase, worker counts, task counts, fix loop progress
- **Worker table** — per-worker status (busy/idle/stopped), current task, PID
- **Agent logs** — streaming stdout/stderr with worker filter dropdown
- **Board view** — CRUD interface for the internal board tracker (create/edit issues, change state)
- **Stop agent button** — terminate running agents directly from the detail sheet
- **zh-CN localization** — full Simplified Chinese interface

### HTTP API

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/state` | Full orchestrator snapshot (stats, running entries, backoff queue, issues, build info) |
| `GET` | `/api/v1/issues/{issue_id}/details` | Issue with Linear metadata when available |
| `GET` | `/api/v1/issues/{issue_id}/timeline` | Workflow timeline snapshot |
| `GET` | `/api/v1/{identifier}` | Single issue lookup from snapshot |
| `GET` | `/api/v1/board/issues` | List all internal board issues |
| `GET` | `/api/v1/board/issues/{identifier}` | Get single board issue |
| `POST` | `/api/v1/board/issues` | Create board issue |
| `PATCH` | `/api/v1/board/issues/{identifier}` | Update board issue (title, description, state, assignee) |
| `POST` | `/api/v1/running/{issue_id}/stop` | Terminate running agent and release issue |
| `POST` | `/api/v1/refresh` | Trigger refresh (202 Accepted) |
| `GET` | `/api/v1/events` | SSE event stream |

### SSE event stream

Connect to `/api/v1/events` for real-time updates. The initial event is a full `snapshot`, followed by incremental events:

| Kind | Events |
|------|--------|
| `orchestrator` | `StatusUpdate`, `AgentStarted`, `AgentFinished`, `BackoffEnqueued`, `IssueReleased` |
| `team` | `tool_call`, `team/stalled`, `team/all_idle`, `team/missing`, `team/event` |
| `board` | `board_issue_created`, `board_issue_updated`, `board_issue_moved` |
| `agent_log` | Streaming worker stdout/stderr |
| `queue` | Dispatch blocked by unresolved dependencies |

Heartbeat events are filtered server-side and never reach clients. Keep-alive comments are sent every 15 seconds.

## Development

### Build and test

```bash
make build            # build dashboard, then build ./contrabass
make build-dashboard  # build packages/dashboard/dist only
make build-landing    # build packages/landing/dist only
make test             # go test ./... -count=1
make test-dashboard   # bun test in packages/dashboard
make test-landing     # astro check in packages/landing
make test-quick       # recommended local validation path
make test-all         # Go + dashboard tests + landing checks
make ci               # lint + test-quick + binary/dashboard build + landing build
make lint             # go vet ./...
make clean            # remove built artifacts
make release-dry      # dry-run GoReleaser locally (skips publish)
```

For day-to-day local validation, use `make test-quick`.
For a fuller pre-push or CI-style pass, use `make ci`.

### Dashboard development

```bash
make dev-dashboard
make dev-landing
```

The repository is a root Bun workspace with `packages/dashboard` and `packages/landing`.
The Astro landing site renders `README.md`, so this file is both repo documentation and site content.

### Running from source

```bash
go run ./cmd/contrabass --config testdata/workflow.demo.md --port 8080
```

## Docs and fixtures

- [`docs/codex-protocol.md`](docs/codex-protocol.md) — notes on the Codex app-server framing and lifecycle used here
- [`docs/local-board.md`](docs/local-board.md) — internal board tracker file format and schema
- [`docs/test-plan.md`](docs/test-plan.md) — ported test-plan notes from the Elixir codebase
- [`testdata/snapshots/`](testdata/snapshots/) — golden snapshots for the TUI renderer

## Charm stack

Direct dependencies from the [Charm](https://charm.sh) v2 ecosystem:

| Logo | Library | Import Path | Purpose |
|------|---------|-------------|---------|
| &nbsp;&nbsp; <img height="64px" src="https://raw.githubusercontent.com/junhoyeo/contrabass/main/.github/assets/charm/charm-bubbletea.webp" alt="Bubble Tea" /> | [**Bubble Tea**](https://github.com/charmbracelet/bubbletea) | `charm.land/bubbletea/v2` | TUI framework (Elm architecture) |
| <img height="64px" src="https://raw.githubusercontent.com/junhoyeo/contrabass/main/.github/assets/charm/charm-lipgloss.webp" alt="Lip Gloss" /> | [**Lip Gloss**](https://github.com/charmbracelet/lipgloss) | `charm.land/lipgloss/v2` | Styling & layout |
| <img height="64px" src="https://raw.githubusercontent.com/junhoyeo/contrabass/main/.github/assets/charm/charm-bubbles.webp" alt="Bubbles" /> | [**Bubbles**](https://github.com/charmbracelet/bubbles) | `charm.land/bubbles/v2` | Reusable TUI components |
| <img height="64px" src="https://raw.githubusercontent.com/junhoyeo/contrabass/main/.github/assets/charm/charm-log.webp" alt="Log" /> | [**Log**](https://github.com/charmbracelet/log) | `github.com/charmbracelet/log` | Structured logging |
| <img height="64px" src="https://user-images.githubusercontent.com/25087/236529273-6f8c841f-f11b-4ec8-b01d-7e3d9b17c85f.png" alt="X" /> | [**x**](https://github.com/charmbracelet/x) | `github.com/charmbracelet/x` | `x/mosaic` for terminal image rendering |

Plus:

- `github.com/charmbracelet/log` for structured logging
- `github.com/fsnotify/fsnotify` for config watching
- `github.com/osteele/liquid` for prompt templating
- `github.com/stretchr/testify` for Go test assertions

## Releasing

CI and release workflows run automatically via GitHub Actions:

- **CI** (`.github/workflows/ci.yml`) — runs on every push and PR: lint, test, build
- **Release** (`.github/workflows/release.yml`) — triggered by pushing a version tag

To ship a new release:

```bash
git tag v0.4.1
git push origin v0.4.1
```

This builds cross-platform binaries (macOS/Linux, amd64/arm64) via [GoReleaser](https://goreleaser.com),
publishes a GitHub Release with grouped changelogs, and updates the
[Homebrew tap](https://github.com/junhoyeo/homebrew-contrabass).

After GoReleaser publishes the release, [`scripts/generate-release-notes.ts`](scripts/generate-release-notes.ts)
appends contributor attribution — each change is tagged with the author's `@username` and linked PR,
and first-time contributors get a dedicated shout-out section.

## Notes for contributors

For detailed contribution guidelines, see [CONTRIBUTING.md](CONTRIBUTING.md).

- The dashboard assets must exist before the Go binary is built because the binary embeds `packages/dashboard/dist`.
- `packages/landing` renders `README.md`, so README changes also affect the landing site.
- If workspace package resolution looks broken in `packages/dashboard` or `packages/landing`, rerun `bun install` at the repository root to refresh workspace links.
- TUI snapshots live in `testdata/snapshots/` and are exercised by `internal/tui` tests.
