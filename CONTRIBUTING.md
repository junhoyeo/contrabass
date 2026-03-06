# Contributing to Contrabass

Thank you for your interest in contributing! This document covers everything you need to set up the project locally, run the test suite, and submit a pull request.

---

## Table of contents

- [Prerequisites](#prerequisites)
- [Setting up the dev environment](#setting-up-the-dev-environment)
- [Project layout](#project-layout)
- [Running tests](#running-tests)
- [Linting](#linting)
- [Building the binary](#building-the-binary)
- [Running from source](#running-from-source)
- [Dashboard development](#dashboard-development)
- [Code guidelines](#code-guidelines)
- [Submitting a pull request](#submitting-a-pull-request)
- [Commit message convention](#commit-message-convention)

---

## Prerequisites

| Tool | Version | Notes |
|------|---------|-------|
| **Go** | 1.25+ | <https://go.dev/dl/> |
| **Bun** | 1.3+ | <https://bun.sh> — required for dashboard & landing builds |
| **Git** | any recent | workspace creation uses `git worktree` |

Optional (needed only to run Contrabass end-to-end, not to build or test it):

- `codex app-server` or `opencode serve` — agent runtime
- `LINEAR_API_KEY` or `GITHUB_TOKEN` — tracker credentials

---

## Setting up the dev environment

```bash
# 1. Fork and clone
git clone https://github.com/<your-fork>/contrabass.git
cd contrabass

# 2. Install JavaScript dependencies (dashboard + landing)
bun install

# 3. Fetch Go module dependencies
go mod download
```

That's it. No special environment variables are required to build or test the project.

> **Dashboard embed note**: `make build` first compiles the React dashboard SPA into
> `packages/dashboard/dist/` and then embeds that directory into the Go binary via
> `embed.FS`. If `packages/dashboard/dist/` is missing or empty, `go build` will still
> succeed but `--port` will serve an empty page. Run `make build-dashboard` if you need
> the full embedded dashboard.

---

## Project layout

```
contrabass/
├── cmd/contrabass/          # CLI entry point (Cobra)
├── internal/
│   ├── agent/               # Agent runners (Codex, OpenCode)
│   ├── config/              # WORKFLOW.md parser (YAML + Liquid)
│   ├── logging/             # Structured logging helpers
│   ├── orchestrator/        # Claim/retry/state-machine logic
│   ├── tracker/             # Issue tracker adapters (Linear, GitHub)
│   ├── tui/                 # Bubble Tea TUI components
│   ├── types/               # Shared type definitions
│   └── workspace/           # Git-worktree provisioning
├── packages/
│   ├── dashboard/           # React dashboard (Vite)
│   └── landing/             # Astro landing site
├── docs/                    # Architecture and protocol notes
├── testdata/                # Workflow fixtures and TUI snapshots
└── Makefile                 # All build, test, and CI targets
```

---

## Running tests

Use `make test-quick` for the recommended local validation path before pushing:

```bash
make test-quick   # go test ./... + dashboard tests + landing checks
```

Individual targets:

```bash
make test             # go test ./... -count=1
make test-dashboard   # bun test in packages/dashboard
make test-landing     # astro check in packages/landing
make test-all         # alias for test-quick
```

For a CI-equivalent pass (lint + test + build):

```bash
make ci
```

### Go tests in detail

- Tests live alongside the code they cover (`internal/<package>/<file>_test.go`).
- Use **table-driven tests** and [testify](https://github.com/stretchr/testify) assertions.
- TUI snapshot golden files are stored in `testdata/snapshots/` and updated automatically when the renderer output changes intentionally.

To run a single package or test:

```bash
go test ./internal/config/...
go test ./internal/tui/... -run TestView
```

---

## Linting

```bash
make lint   # runs go vet ./...
```

Fix any `go vet` warnings before opening a PR. The CI gate rejects builds with vet errors.

---

## Building the binary

```bash
make build          # build dashboard SPA, then compile ./contrabass
make build-dashboard  # build packages/dashboard/dist/ only
```

The binary embeds the dashboard with `embed.FS`, so the JS assets must be compiled first — `make build` handles this automatically.

---

## Running from source

```bash
go run ./cmd/contrabass --config testdata/workflow.demo.md
go run ./cmd/contrabass --config testdata/workflow.demo.md --port 8080  # with dashboard
go run ./cmd/contrabass --config testdata/workflow.demo.md --no-tui     # headless
```

---

## Dashboard development

Hot-reload dev server for the React dashboard:

```bash
make dev-dashboard    # starts Vite on localhost:5173
```

Hot-reload for the Astro landing site:

```bash
make dev-landing
```

> The Astro site renders `README.md` as its main content, so any README edit is
> also a landing-site edit.

---

## Code guidelines

### Go

- **Error handling**: return errors explicitly; do not panic in library code.
- **Context**: accept and propagate `context.Context` through all I/O and network calls.
- **Concurrency**: use `errgroup.Group` + `context.WithCancel` for goroutine supervision.
- **Tests**: table-driven, testify assertions, no test deletions to force a pass.
- **Type safety**: no `as any`, `//nolint`, `// nolint`, or `//go:build ignore` to silence real errors.

### Charm / TUI

- Import Charm v2 via the vanity paths — **never** `github.com/charmbracelet/...`:
  - `charm.land/bubbletea/v2`
  - `charm.land/bubbles/v2`
  - `charm.land/lipgloss/v2`
- `View()` returns `string` (Lip Gloss v2 breaking change from v1).
- Use Lip Gloss v2 `table` for static table rendering; do **not** use Bubbles' interactive table.
- Follow the Elm architecture: `Model` → `Update` → `View`.

### JSON-RPC / Codex protocol

- Use JSONL framing (one JSON object per line). No `Content-Length` headers.
- Handle error code `-32001` (server overload) gracefully with retry logic.

---

## Submitting a pull request

1. **Fork** the repository and create a feature branch from `main`:

   ```bash
   git checkout -b feat/my-feature
   ```

2. **Make your changes.** Keep each commit atomic and scoped to one logical change.

3. **Validate locally** before pushing:

   ```bash
   make ci
   ```

4. **Push** your branch and open a PR against `main` on GitHub.

5. **PR description**: briefly explain *what* changed and *why*. Link any related issues.

6. CI runs automatically (lint → test → build). All checks must pass.

7. A maintainer will review and either merge or leave feedback.

### Conflict resolution

When your branch diverges from `main`, resolve conflicts with a **merge commit** — do not rebase or squash:

```bash
git checkout feat/my-feature
git merge main
# resolve conflicts, then:
git add -A && git commit
git push origin feat/my-feature
```

This preserves the full commit history of your branch.

---

## Commit message convention

Follow the format used throughout this repository:

```
<type>(<scope>): <description>
```

**Types**: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`

**Scopes** (Go package name or area):

| Scope | Area |
|-------|------|
| `config` | `internal/config` |
| `tracker` | `internal/tracker` |
| `workspace` | `internal/workspace` |
| `agent` | `internal/agent` |
| `orchestrator` | `internal/orchestrator` |
| `tui` | `internal/tui` |
| `logging` | `internal/logging` |
| `types` | `internal/types` |
| `cli` | `cmd/contrabass` |
| `project` | repo-wide (go.mod, CI, etc.) |
| `docs` | documentation files |

**Rules**: lowercase description, imperative mood, no trailing period, ≤ 72 characters.

Examples:

```
feat(config): add WORKFLOW.md parser with YAML front matter support
fix(agent): handle JSON-RPC error code -32001 for server overload
docs(project): add CONTRIBUTING.md with contributor guidelines
test(orchestrator): port state machine transition tests
```

---

If you have questions, feel free to open a [GitHub Discussion](https://github.com/junhoyeo/contrabass/discussions) or leave a comment on the relevant issue.
