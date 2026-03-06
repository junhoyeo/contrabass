<div align="center">

# Contrabass

<img alt="Contrabass Logo" src="https://raw.githubusercontent.com/junhoyeo/contrabass/main/.github/assets/contrabass.png" width="300px" />

> **A project-level orchestrator for AI coding agents** <br />
> Go + Charm stack reimplementation of OpenAI's Symphony ([openai/symphony](https://github.com/openai/symphony)) — manage work, not agents

</div>

Contrabass is a terminal-first orchestrator for issue-driven agent runs, with an optional local web dashboard for live visibility.

## Current scope

Today Contrabass ships with:

- A Cobra CLI with TUI, headless, and optional embedded web dashboard modes
- A `WORKFLOW.md` parser with YAML front matter, Liquid prompt rendering, and `$ENV_VAR` interpolation
- Issue tracker adapters for **Linear** and **GitHub Issues**
- Agent runners for **Codex app-server** and **OpenCode**
- Git-worktree-based workspace provisioning under `workspaces/<issue-id>`
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
  - or `opencode serve`
- Tracker credentials for the backend you use:
  - Linear: `LINEAR_API_KEY`
  - GitHub: `GITHUB_TOKEN`

From a fresh clone, run `bun install` once before using the JS/landing build and test commands.

## Quick start

```bash
git clone https://github.com/junhoyeo/contrabass.git
cd contrabass
bun install
make build
```

`make build` first builds `packages/dashboard/dist/` and then embeds it into the Go binary.

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

### Example workflow files

- [`testdata/workflow.demo.md`](testdata/workflow.demo.md) — demo Linear + Codex workflow
- [`testdata/workflow.github.md`](testdata/workflow.github.md) — GitHub + OpenCode workflow
- [`testdata/workflow.md`](testdata/workflow.md) — realistic Linear fixture

## Supported integrations

| Surface | Current support |
|---|---|
| Trackers | Linear, GitHub Issues |
| Agent runners | Codex app-server, OpenCode |
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

### Agent runners

- **Codex**
  - Launches `codex app-server`
  - Performs `initialize` → `initialized` → `thread/start` → `turn/start`
  - Streams newline-delimited JSON notifications and usage updates
- **OpenCode**
  - Starts or reuses an `opencode serve` process
  - Creates sessions over HTTP and streams events over SSE

## Web dashboard and HTTP API

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

## Repository layout

```text
contrabass/
├── cmd/contrabass/          # Cobra entrypoint
├── internal/
│   ├── agent/              # Codex and OpenCode runners
│   ├── config/             # WORKFLOW.md parsing, templating, watcher
│   ├── hub/                # Event fan-out for web subscribers
│   ├── logging/            # Charm log helpers
│   ├── orchestrator/       # Poll/claim/run/retry/shutdown logic
│   ├── tracker/            # Linear and GitHub tracker adapters
│   ├── tui/                # Bubble Tea + Lip Gloss terminal UI
│   ├── types/              # Shared issue/run/event types
│   ├── web/                # HTTP API, SSE, SPA serving
│   └── workspace/          # Git worktree lifecycle
├── packages/
│   ├── dashboard/          # React + Vite operations dashboard
│   └── landing/            # Astro site that renders this README
├── docs/                   # Protocol notes and test planning docs
├── testdata/               # Workflow fixtures and golden snapshots
└── tests/e2e/              # End-to-end smoke coverage
```

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
- [`docs/test-plan.md`](docs/test-plan.md) — ported test-plan notes from the Elixir codebase
- [`testdata/snapshots/`](testdata/snapshots/) — golden snapshots for the TUI renderer

## Charm stack

Contrabass uses the Charm v2 stack with vanity import paths:

- `charm.land/bubbletea/v2`
- `charm.land/bubbles/v2`
- `charm.land/lipgloss/v2`

Plus:

- `github.com/charmbracelet/log` for structured logging
- `github.com/fsnotify/fsnotify` for config watching
- `github.com/osteele/liquid` for prompt templating
- `github.com/stretchr/testify` for Go test assertions

## Notes for contributors

- The dashboard assets must exist before the Go binary is built because the binary embeds `packages/dashboard/dist`.
- `packages/landing` renders `README.md`, so README changes also affect the landing site.
- If workspace package resolution looks broken in `packages/dashboard` or `packages/landing`, rerun `bun install` at the repository root to refresh workspace links.
- TUI snapshots live in `testdata/snapshots/` and are exercised by `internal/tui` tests.
