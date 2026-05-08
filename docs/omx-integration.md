# OMX integration notes

Contrabass drives [`oh-my-codex`](https://github.com/madebyoll/oh-my-codex)
(`omx`) v0.16+ as one of its team-CLI agents. omx itself is a "fire-and-forget"
launcher: `omx team N:agent "task"` spawns a detached tmux session, prints
`Team started: <team-name>` on stdout, and exits 0. Subsequent state queries
go through `omx team status|await|api`.

This doc captures two things contrabass users sometimes ask about:

1. What contrabass does — and does not — guarantee about isolation between an
   omx team it launched and an unrelated `omx team` the user opened in some
   other project on the same machine.
2. Why team panes from different projects all show up in `tmux list-sessions`
   and `omx team status` output, and what to do if you really need them
   visually separated.

## What contrabass guarantees

- **Per-issue git worktree.** Every claimed issue gets its own
  `workspaces/<issue-id>/` worktree on a fresh branch. omx workers create
  *their* own worktrees as nested subdirectories under
  `<workspace>/.omx/team/<team-name>/worktrees/worker-N/`. No two issues
  share a working tree.
- **Unique team names.** contrabass parses the actual `Team started:
  <name>` line from `omx team` stdout and uses the printed name (which
  includes a hash suffix, e.g. `cb-3-add-godoc-commen-82cddf69`) for every
  follow-up `omx team api` / `omx team shutdown` call. Two teams launched
  from any source — contrabass-driven, user-typed, or older retries — are
  guaranteed to be addressable independently.
- **Self-healing handoff to omx.** For omx specifically, contrabass does
  *not* run its own `RestartDeadWorkers` / `releaseExpiredClaims` /
  worker-quarantine loop on top of omx's supervisor. omx is allowed to
  reach its own conclusions about worker health and recover (or fail)
  without contrabass tearing down healthy codex turns mid-task. Other team
  CLIs (`opencode`, `omc`) keep the older active-supervisor behaviour.

This means: as long as omx itself respects team-name addressing, an `omx
team` you started by hand cannot be claimed, mutated, or shut down by a
contrabass-driven team — and vice versa.

## What contrabass does not isolate (and why)

omx's tmux runtime is **user-global**: it uses the default tmux server
(usually under `/tmp/tmux-<uid>/default`), names its sessions
`omx-<repo>-<branch>-<id>`, and writes coordination state under
`~/.omx/state/`. Any process running as your user can see all omx-managed
sessions on that server.

So if you have, say:

- `omx team` running for the writer-agent project (session
  `omx-writer-agent-main-...`), and
- contrabass driving an issue in the contrabass repo (session
  `omx-contrabass-cb-3-...`),

both sessions appear together in `tmux list-sessions`, and `omx team
status` (without a team name) lists all of them. This is **expected**, not
a contrabass bug. The work is logically isolated by team name; the *view*
is shared because that is how omx's tmux integration is designed.

Contrabass does not sandbox the tmux server for two reasons:

1. omx team requires being launched from inside a tmux pane (it checks
   `$TMUX` / "current leader pane"). Spawning omx with a stripped or
   redirected `TMUX_TMPDIR` would force contrabass to also pre-create a
   host session inside that isolated socket, attach into it, and run omx
   from the pane — a fragile contract pinned to internal omx CLI
   behaviour that has shifted between releases.
2. The contamination users actually observe is purely cosmetic (HUD
   listing, `tmux list-sessions`). No claim, no commit, no codex turn
   crosses between teams.

## If you really want visually-separated tmux servers

The right place to fix this is upstream in `omx` (e.g. an `omx
--tmux-socket=<path>` flag or honouring `TMUX_TMPDIR` end-to-end). Until
that lands, the workaround is to launch contrabass itself inside its own
tmux server:

```bash
TMUX_TMPDIR=/tmp/contrabass-tmux \
  tmux new-session -s contrabass \
  'contrabass --config workflow.omx.md'
```

Anything contrabass spawns inherits that `TMUX_TMPDIR`, so omx team will
land on the dedicated socket and stay out of your default tmux server.
Your other `omx team` invocations (using `/tmp/tmux-<uid>/default`)
remain visually separate. This is purely a presentation choice — the
correctness guarantees above hold either way.
