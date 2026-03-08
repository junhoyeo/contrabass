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
- Git-worktree-based workspace provisioning under `workspaces/<issue-id>`
- Teams: multi-agent coordination with a local task board, phased pipeline (plan → exec → verify), and live TUI team table
- An orchestrator with claim/release, timeout detection, stall detection, deterministic retry backoff, and state snapshots
- A Charm v2 terminal UI built with Bubble Tea, Bubbles, and Lip Gloss
- A React dashboard served from the Go binary, with state snapshots and live SSE updates
- Go unit/integration tests, TUI snapshot tests, and dashboard component/hook tests

## Requirements

- **Go 1.25+**
- **Bun 1.3+** for the dashboard/landing workspace
- **Git** (workspace creation uses `git worktree`)
- A supported agent runtime:
  - `codex app-server`
  - `opencode serve`
  - `oh-my-opencode`
  - `omx` (oh-my-codex team runtime)
  - `omc` (oh-my-claudecode team runtime)
- Tracker credentials for the backend you use:
  - Linear: `LINEAR_API_KEY`
  - GitHub: `GITHUB_TOKEN`

From a fresh clone, run `bun install` once before using the JS/landing build and test commands.

## Installation

### Homebrew (macOS/Linux)

```bash
brew install junhoyeo/contrabass/contrabass
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

## How Contrabass works

1. Poll the configured tracker for candidate issues.
2. Claim an eligible issue.
3. Create or reuse a git worktree in `workspaces/<issue-id>`.
4. Render the prompt body from `WORKFLOW.md` using issue data.
5. Launch the configured agent runner.
6. Stream agent events, track tokens/phases, and publish orchestrator events.
7. Retry failed runs with exponential backoff + deterministic jitter.
8. Mirror state into the TUI and, when enabled, the embedded web dashboard.

### Runtime notes

- `WORKFLOW.md` is watched with `fsnotify`; on parse errors, Contrabass keeps the last known good config.
- The Codex runner speaks newline-delimited JSON (`JSONL`) to `codex app-server` rather than `Content-Length` framed messages. See [`docs/codex-protocol.md`](docs/codex-protocol.md).
- The web dashboard currently has live metrics, running sessions, and retry queue data. The rate-limit panel exists, but there is not yet a live rate-limit feed behind it.
- The workflow parser already accepts more Symphony-shaped fields than the runtime fully consumes today. For example, `workspace`, `hooks`, and some `codex` settings are parsed, but the current runtime mainly uses tracker selection, timeouts, retry settings, binary paths, and prompt/template fields.

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
| Operator surfaces | Charm TUI, embedded web dashboard, headless mode |
| Live config reload | Yes (`WORKFLOW.md` via `fsnotify`) |
| State streaming | JSON snapshot API + SSE |

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
  - Launches `codex app-server`
  - Performs `initialize` → `initialized` → `thread/start` → `turn/start`
  - Streams newline-delimited JSON notifications and usage updates
- **OpenCode**
  - Starts or reuses an `opencode serve` process
  - Creates sessions over HTTP and streams events over SSE
- **oh-my-opencode**
  - Wraps the `oh-my-opencode` agent binary
  - HTTP session creation with SSE event streaming
- **OMX (oh-my-codex)**
  - Launches `omx team ...` with a workspace-scoped task file
  - Polls `omx team api get-summary` and `omx team api list-tasks` for status and results
  - Shuts down the team with `omx team shutdown ... --force` (and `--ralph` when configured)
- **OMC (oh-my-claudecode)**
  - Launches `omc team ...` with a workspace-scoped task file
  - Polls `omc team api get-summary` and `omc team api list-tasks` for status and results
  - Shuts down the team with `omc team shutdown ... --force`

## Web dashboard and HTTP API (WIP)

When `--port` is set, Contrabass serves the embedded dashboard and a small JSON/SSE API.

### Current endpoints

- `GET /api/v1/state` — full orchestrator snapshot
- `GET /api/v1/{identifier}` — cached issue lookup from the latest snapshot
- `GET /api/v1/events` — SSE stream (initial snapshot + live orchestrator events)
- `POST /api/v1/refresh` — currently returns `202 Accepted` as a placeholder hook

The dashboard currently renders:

- connection status
- aggregate runtime/token metrics
- running session table
- retry queue

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
git tag v0.1.0
git push origin v0.1.0
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
