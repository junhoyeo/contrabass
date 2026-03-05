# AGENTS.md — Contrabass Project Guidelines

## Project Overview

**Project**: Contrabass — Go reimplementation of OpenAI's Symphony using Charm TUI stack

**Module**: `github.com/junhoyeo/contrabass`

**Go Version**: 1.25.0

**Repository**: https://github.com/junhoyeo/contrabass

## Architecture

```
contrabass/
├── cmd/contrabass/          # CLI entry point
├── internal/
│   ├── config/                  # Configuration parsing
│   ├── tracker/                 # State tracking
│   ├── workspace/               # Workspace management
│   ├── agent/                   # Agent orchestration
│   ├── orchestrator/            # Orchestration logic
│   ├── tui/                     # Terminal UI components
│   ├── logging/                 # Logging utilities
│   └── types/                   # Shared type definitions
├── docs/                        # Documentation
└── testdata/                    # Test fixtures
```

## Key Libraries & Dependencies

### Charm v2 (Stable — Feb 24, 2026)
- **bubbletea** v2 — TUI framework
- **bubbles** v2 — Reusable components
- **lipgloss** v2 — Styling and layout

**CRITICAL**: Use vanity import paths:
- ✅ `charm.land/bubbletea/v2`
- ✅ `charm.land/bubbles/v2`
- ✅ `charm.land/lipgloss/v2`
- ❌ `github.com/charmbracelet/...` (WRONG)

### Other Dependencies
- **Cobra** — CLI framework
- **Fang** v0.4.4 — Experimental (cosmetics only, not stable)
- **fsnotify** — File system watching
- **osteele/liquid** — Template rendering
- **testify** — Testing assertions

## Commit Message Convention

### Format

```
<type>(<scope>): <description>
```

### Types

| Type | Purpose |
|------|---------|
| `feat` | New feature or capability |
| `fix` | Bug fix |
| `refactor` | Code restructuring without behavior change |
| `docs` | Documentation only |
| `test` | Adding or updating tests |
| `chore` | Build, tooling, dependency updates |
| `perf` | Performance improvement |

### Scopes

Use the Go package name as the scope:

| Scope | Package |
|-------|---------|
| `config` | `internal/config` |
| `tracker` | `internal/tracker` |
| `workspace` | `internal/workspace` |
| `agent` | `internal/agent` |
| `orchestrator` | `internal/orchestrator` |
| `tui` | `internal/tui` |
| `logging` | `internal/logging` |
| `types` | `internal/types` |
| `cli` | `cmd/contrabass` |
| `project` | Project-wide (go.mod, .gitignore, CI, etc.) |
| `docs` | Documentation files |

### Rules

1. **Atomic commits** — One logical change per commit
2. **Description** — Lowercase, imperative mood, no period at end
3. **Length** — Keep description under 72 characters
4. **No metadata** — No internal review labels or metadata in commit messages
5. **No trailing punctuation** — Description ends without period

### Examples

```
feat(config): add WORKFLOW.md parser with YAML front matter support
test(orchestrator): port state machine transition tests from Elixir
fix(agent): handle JSON-RPC error code -32001 for server overload
refactor(tui): split model into header and table sub-components
docs(project): add AGENTS.md with commit convention
chore(project): initialize go.mod with Charm v2 dependencies
perf(tracker): cache workspace state to reduce file I/O
```

## Git History & PR Branch Rules

### NEVER Destroy Commit History

**CRITICAL**: When resolving conflicts or updating PR branches, you MUST preserve the original commit history. Every commit represents a logical unit of work and its message documents WHY the change was made.

### Conflict Resolution Strategy

When a PR branch has diverged from main and needs to incorporate main's changes:

1. **Use `git merge main` INTO the PR branch** — this creates a merge commit that preserves both histories
2. **NEVER squash PR commits** — squashing destroys the granular history of the PR's development
3. **NEVER create a new branch and cherry-pick/squash** — this rewrites history and loses the original commit SHAs
4. **NEVER use `git rebase`** on shared/pushed PR branches without explicit owner permission — rebase rewrites commit SHAs

### What a Correct PR Update Looks Like

```
git checkout <pr-branch>
git merge main --no-edit
# resolve conflicts
git add -A
git commit  # merge commit is created automatically
git push origin <pr-branch> --force  # only if branch was previously force-pushed
```

The result should be:
- All original PR commits preserved with their original SHAs
- A single merge commit on top that brings in main's changes
- The merge commit resolves any conflicts

### What is FORBIDDEN

- `git rebase main` on a PR branch (rewrites all commit SHAs)
- Creating a new branch from main and squash-merging PR changes into it (destroys history)
- `git reset --hard` on a PR branch to a different base (destroys commits)
- Any operation that reduces N commits into 1 commit without explicit permission

### Force Push Rules

- **NEVER force-push to `main`** — this is always destructive
- **Force-push to PR branches** is acceptable ONLY when:
  1. Restoring previously destroyed history (fixing a mistake)
  2. The PR owner explicitly requests it
  3. The branch has already been force-pushed before (not the first push)

## Code Guidelines for AI Agents

### Go Standards

- **Testing**: Table-driven tests with testify assertions
- **Error handling**: Explicit error returns, no panic in libraries
- **Context**: Use `context.Context` for cancellation and timeouts
- **Concurrency**: goroutines + `errgroup` + `context.WithCancel` for supervision

### TUI Development

#### Lip Gloss v2 (Static Rendering)
- Use Lip Gloss v2 for styling and layout
- **IMPORTANT**: `View()` returns `string` (not `io.Writer`)
- **REMOVED**: `AdaptiveColor` — use explicit colors or `lipgloss.Color()`
- Use Lip Gloss v2 table for static rendering

#### Bubbles v2 (Reusable Components)
- Use Bubbles v2 components where available
- **NOT** Bubbles Table (interactive) — use Lip Gloss table instead

#### Bubbletea v2 (Framework)
- Follow Elm architecture: Model, Update, View
- Use `tea.Cmd` for side effects
- Proper cleanup in `Quit` command

### JSON-RPC Protocol

- **Framing**: JSONL (JSON Lines) — one JSON object per line
- **NOT** Content-Length headers
- Error codes: Handle `-32001` (server overload) gracefully

### Dependencies & Imports

- **Fang v0.4.4**: Experimental — use for cosmetics only, not core logic
- **fsnotify**: For file system watching
- **osteele/liquid**: For template rendering
- **testify**: For assertions in tests

## Testing

- **Strategy**: Table-driven tests
- **Assertions**: Use testify (`assert`, `require`)
- **Command**: `go test ./...`
- **Fixtures**: Place test data in `testdata/`

## Documentation

- Keep `docs/` directory up-to-date
- Use Markdown for all documentation
- Link to relevant code sections
- Document architectural decisions in `AGENTS.md` or `docs/`

## Known Patterns & Wisdom

### Charm v2 Stability
- Charm v2 released stable on Feb 24, 2026
- Vanity import paths are the official way to import
- All v2 APIs are stable and recommended

### Fang Limitations
- Fang v0.4.4 is experimental
- Use only for cosmetics (styling, formatting)
- Do not rely on Fang for core functionality

### Lip Gloss v2 Changes
- `View()` returns `string` (breaking change from v1)
- `AdaptiveColor` removed — use explicit colors
- Table component is static (not interactive)

### Codex Protocol
- Uses JSONL framing (one JSON object per line)
- No Content-Length headers
- Handle error code `-32001` (server overload)

### Concurrency Patterns
- Use `errgroup.Group` for managing goroutines
- Use `context.WithCancel` for graceful shutdown
- Always propagate context through function calls

## Questions for AI Agents

When working on this codebase:

1. **Is this a Charm v2 import?** Use vanity paths (`charm.land/...`)
2. **Is this TUI rendering?** Use Lip Gloss v2 (static), not Bubbles Table
3. **Is this a test?** Use table-driven tests with testify
4. **Is this concurrent?** Use `errgroup` + `context.WithCancel`
5. **Is this a commit message?** Follow the convention above

## References

- [Charm v2 Documentation](https://charm.sh)
- [Bubbletea v2 Guide](https://github.com/charmbracelet/bubbletea)
- [Lip Gloss v2 Docs](https://github.com/charmbracelet/lipgloss)
- [Cobra CLI Framework](https://cobra.dev)
- [Go Testing Best Practices](https://golang.org/doc/effective_go#testing)
