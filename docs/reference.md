# Reference

File formats and the non-interactive outputs: the run file schema,
reports, the one-line status, screen previews and watching several
machines.

See also: [Delegating](delegate.md) · [Configuration](configuration.md) ·
[Architecture](ARCHITECTURE.md) · back to the [README](../README.md).

## Run files

`panal delegate` writes one JSON file per attempt to `~/.panal/runs`
(`PANAL_RUNS`), named `<id>-<agent>.json`. It is written with status
`running` before the agent starts and rewritten (atomically, through a
`.tmp` file) when it ends. The format is defined in one place,
`internal/runs/runs.go`; everything else in Panal reads runs through it.

```json
{
  "version": 2,
  "id": "20261002-101500",
  "agent": "codex",
  "model": "gpt-6-luna",
  "effort": "low",
  "task": "Fix the off-by-one in the pager; add a test",
  "dir": "/home/you/src/wt-fix",
  "read_only": false,
  "pid": 12345,
  "log": "/home/you/.panal/logs/20261002-101500-codex-gpt-6-luna.txt",
  "start": "2026-10-02T10:15:00-06:00",
  "status": "done",
  "end": "2026-10-02T10:21:40-06:00",
  "rc": 0,
  "task_type": "fix",
  "tier": "simple",
  "auto": true,
  "route": "auto: codex:gpt-6-luna:low — fix · simple · 4/4 ok in the last 30 days (exploring 1 in 11)",
  "choice": 1
}
```

| field | type | meaning |
|---|---|---|
| `version` | int | format version, currently `2` |
| `id` | string | the delegation's start time, `YYYYMMDD-HHMMSS` (`-2`, `-3`… if taken); every attempt of one delegation shares it |
| `agent` | string | `codex`, `agy` or `opencode` |
| `model`, `effort` | string | the chain link's model and effort; empty = the CLI's default (`effort` omitted when empty) |
| `task` | string | the task's first line |
| `task_file` | string | path of the full task (`<id>.task.md`) when it has more than one line; omitted otherwise |
| `dir` | string | absolute directory the agent worked in |
| `read_only` | bool | `-r` |
| `pid` | int | the `panal delegate` process |
| `log` | string | the attempt's log file |
| `start`, `end` | string | RFC 3339; `end` omitted while running |
| `status` | string | `running`, `done`, `failed`, `out_of_quota`, `no_permission`, `timeout`, `interrupted`, `skipped` |
| `rc` | int | exit code ([table](delegate.md#exit-codes-and-statuses)); omitted while running and for skipped links |
| `task_type` | string | `review`, `fix`, `tests`, `refactor`, `docs`, `feature`, `other` |
| `tier` | string | `simple`, `medium`, `complex` |
| `auto` | bool | the chain came from the router (`-c auto`) |
| `route` | string | the router's one-line reason |
| `choice` | int | auto only: this link's place in the chain, 1 = first choice |
| `explored` | bool | auto only: the first choice was exploration, not the favorite |

The router fields are omitted when empty. Run files written by the older
delegar.sh helper use Spanish keys and status values; they are read from
`~/.ct-delegar/estado` and translated on read in
`internal/runs/legacy.go`, the only place that knows that format.

## Reports

| command | output |
|---|---|
| `panal -report 7` | per agent, last 7 days: runs, % finished well, failures, out of quota, broke the task, median time, credits, tokens; plus the router's section |
| `panal -report 30 -format md > month.md` | the same as markdown, with the agent × task type matrix and the delegated savings |
| `panal -report 7 -format csv` | one row per agent: `agent,runs,finished,failed,out_of_quota,violations,median_s,total_s,credits,tokens` |
| `panal -report 7 -format json` | everything above as JSON |
| `panal -summary` | today as a markdown table, ready to paste |
| `panal -history` | the last 100 runs as plain text |
| `panal -once` | the current status, one line per agent, no colors |

`-format` takes `text` (the default), `md`, `csv` or `json`. "Broke the
task" runs git (read-only) on each run's directory. A report reuses the
router's cached checks without writing them.

## One-line status

`panal -statusline` prints each agent with its status glyph and highest
quota, for example `claude ● · agy ✔ 15% · codex ✔ 100% · opencode ◐`.
Agents that are off and idle are left out.

- **Claude Code**: [`scripts/statusline.sh`](../scripts/statusline.sh)
  saves the status line JSON for the dashboard and prints this line (see
  [Getting started](getting-started.md#claude-code-the-orchestrator)).
- **tmux**: `set -g status-right '#(panal -statusline)'`.

## Screen previews

`panal -preview WIDTH [-screen NAME]` draws one screen once, at that
width and 40 lines, and exits. `NAME` is one of `table`, `detail`,
`history`, `history-detail`, `report`, `timeline` or `help`; without it,
the cards. `panal -mascots` draws the mascots with their current status.
Neither writes anything.

## Multiple machines

One dashboard can watch the agents of other machines.

1. **On the other machine**, serve its status: `panal -serve :8765`
   (headless), or `serve = :8765` in its config so its dashboard also
   serves. It needs `serve_token = a-long-secret` in its config (or
   `PANAL_TOKEN`) and refuses to start without one.
2. **On yours**, one line per machine in `panal.conf`:

   ```ini
   machine = pc2 http://10.0.0.5:8765 a-long-secret
   ```

   (`http://` is assumed when the URL has no scheme.)

Below the cards you get one line per machine, refreshed every 10 s: `⇄ pc2`
with each remote agent's status and current activity and how long ago it
was read, or a note that the machine isn't answering or rejected the token.

The server only answers `GET /status`, and only with
`Authorization: Bearer <token>`:

```json
{
  "machine": "pc2",
  "time": "2026-10-02T10:30:00Z",
  "agents": [
    {"agent": "codex", "status": "working", "glyph": "●", "model": "gpt-6-luna",
     "task": "Fix the off-by-one in the pager", "activity": "$ go test ./...",
     "since": "2026-10-02T10:15:00Z",
     "bars": [{"name": "5h", "used": 42, "resets_at": "2026-10-02T13:00:00Z"}]}
  ]
}
```

It is plain HTTP and the status includes tasks and commands: use it on
your local network or over a VPN (Tailscale, WireGuard), never exposed to
the internet. Both machines should run the same Panal version.
