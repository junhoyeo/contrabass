<div align="center">

# Contrabass

<img alt="Contrabass Logo" src="https://raw.githubusercontent.com/junhoyeo/contrabass/main/.github/assets/contrabass.png" width="300px" />

> Go & Charm stack implementation of OpenAI's Symphony ([openai/symphony](https://github.com/openai/symphony)) — manage work, not agents

</div>

## What it does

Contrabass orchestrates coding-agent runs against Linear issues and provides:

- A native terminal UI built with Charm v2
- A local web dashboard (embedded into the Go binary)
- Real-time event streaming over SSE
- Retry/backoff orchestration and state snapshots

## Project layout

```text
contrabass/
├── cmd/contrabass/          # CLI entrypoint
├── internal/                # Orchestrator, tracker, TUI, web server, hub, etc.
├── packages/
│   ├── dashboard/           # React + Vite dashboard (embedded at build time)
│   └── landing/             # Astro landing site
└── testdata/                # Fixtures (including demo workflow)
```

## Requirements

- Go 1.25+
- Bun 1.3+

## Quick start

```bash
git clone https://github.com/junhoyeo/contrabass.git
cd contrabass

# Install JS workspace dependencies (root workspace)
bun install

# Build dashboard + Go binary
make build
```

Run TUI only:

```bash
./contrabass --config WORKFLOW.md
```

Run TUI + local web dashboard:

```bash
./contrabass --config WORKFLOW.md --port 8080
```

Then open `http://localhost:8080`.

## Bun workspace

This repo uses a root Bun workspace:

- Root `package.json` with `workspaces: ["packages/*"]`
- Single root `bun.lock`
- Package-level lockfiles are not used

## Web surfaces

- `packages/dashboard`: local operational dashboard (embedded into the binary)
- `packages/landing`: static OSS-facing landing page

`packages/dashboard/dist/` is gitignored except for `.gitkeep` so generated assets do not pollute git history.

## Charm stack

Direct dependencies from the [Charm](https://charm.sh) v2 ecosystem:

| Logo | Library | Import Path | Purpose |
|------|---------|-------------|---------|
| &nbsp;&nbsp; <img height="64px" src="https://raw.githubusercontent.com/junhoyeo/contrabass/main/.github/assets/charm/charm-bubbletea.webp" alt="Bubble Tea" /> | [**Bubble Tea**](https://github.com/charmbracelet/bubbletea) | `charm.land/bubbletea/v2` | TUI framework (Elm architecture) |
| <img height="64px" src="https://raw.githubusercontent.com/junhoyeo/contrabass/main/.github/assets/charm/charm-lipgloss.webp" alt="Lip Gloss" /> | [**Lip Gloss**](https://github.com/charmbracelet/lipgloss) | `charm.land/lipgloss/v2` | Styling and layout |
| <img height="64px" src="https://raw.githubusercontent.com/junhoyeo/contrabass/main/.github/assets/charm/charm-bubbles.webp" alt="Bubbles" /> | [**Bubbles**](https://github.com/charmbracelet/bubbles) | `charm.land/bubbles/v2` | Reusable TUI components |
| <img height="64px" src="https://raw.githubusercontent.com/junhoyeo/contrabass/main/.github/assets/charm/charm-log.webp" alt="Log" /> | [**Log**](https://github.com/charmbracelet/log) | `github.com/charmbracelet/log` | Structured logging |
