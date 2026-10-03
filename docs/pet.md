# Panal as a pet

`panal pet` is one Panal mascot to keep next to an agent CLI (Codex,
Claude Code, …) in a small split pane: it works while your agents work,
celebrates when a run finishes, gets scared when one fails and falls asleep
when nothing has happened for hours. Under it, one line for its agent and
one for the others.

```
       ▄▄▄▄▄▄
      █▀████▀█
     ▄████████▄
     ▀████████▀█
      ▀ ▀  ▀ ▀

claude ● orchestrat…
  agy ✔ codex ◐ …
```

It reads the same files as `panal -once` every 2 seconds: it never asks
the CLIs for their live quota, never starts an agent process and writes
nothing.

## Open it in a split

Windows Terminal, a pane a quarter of the window wide on the right:

```sh
wt -w 0 sp -V -s 0.25 panal pet
```

tmux, a pane 24 columns wide:

```sh
tmux split-window -h -l 24 panal pet
```

`q`, `esc` or `ctrl+c` quits. It resizes live and fits down to about 20
columns × 8 rows: first the blank line goes, then the mascot (below 12
columns or 7 rows), and the lines shorten (`claude ● orchestrating · 12 min`
→ without the time → the word cut with `…`; the others without the dots,
then the last ones give way to `…`).

## Inside Claude Code: the `panal-pet` plugin

The repo is also a Claude Code plugin marketplace with one plugin,
[`plugins/panal-pet`](../plugins/panal-pet). It keeps one
`panal pet -stream -all` running and draws your agents inside Claude Code.

```text
/plugin marketplace add AlbertoVasquezR/panal
/plugin install panal-pet@panal
```

**Above the prompt (default).** A small honeycomb at the right edge, one cell
per agent in its status color, so it takes one line and stays out of the way:

```text
                                                              ⬢⬢⬢ Panal
❯ _
```

Hover the honeycomb and every agent's mascot appears above it, each with its
name and status glyph; click **Panal** to keep them open (click again to hide
them). Only the mascots' pixels are painted: the background stays your
terminal's.

```text
     ▄▄▄▄▄▄           ▄▄▄▄                ▀
    █▀████▀█         ██████  ▄       █▀▀▀▀▀▀█ ▄
   ▄████████▄       ▄▀▀▀▀▀▀▄         ████████
   ▀████████▀█      ▀▀█▀▀█▀▀        ▄████████▄
    ▀ ▀  ▀ ▀         ▀▀  ▀▀          ▀▀    ▀▀
    claude ●          agy ✔            codex ✔
                                                              ⬢⬢⬢ Panal
```

**In a pane.** `/panal pane` shows one card per agent: the mascot, its name,
status and time, model, and its last action while it works (its task
otherwise). The border takes the status color; the pet (the agent
orchestrating, or the one working) is marked with ★.

```text
◆ Panal  1 orchestrating · 2 done
╭──────────────────────────────────────────────────╮
│    ▄▄▄▄▄▄     claude ★                           │
│   █▀████▀█    ● orchestrating · 12 min           │
│  ▄████████▄   Opus 5.5 (1M context) · medium     │
│  ▀████████▀█  Running go test ./...              │
│   ▀ ▀  ▀ ▀                                       │
╰──────────────────────────────────────────────────╯
╭──────────────────────────────────────────────────╮
│    ▄▄▄▄       agy                                │
│   ██████  ▄   ✔ done · 3 min ago                 │
│  ▄▀▀▀▀▀▀▄     gemini-3.8-flash-medium            │
│  ▀▀█▀▀█▀▀     Add a footer to the report view    │
│   ▀▀  ▀▀                                         │
╰──────────────────────────────────────────────────╯
```

| command | where the pets show |
|---|---|
| `/panal band` (default; also `top`, `on`, `show`) | the honeycomb above the prompt |
| `/panal pane` (also `panel`, `side`) | the cards in a pane |
| `/panal off` (also `hide`, `none`) | nowhere |
| `/panal` | toggles between off and the band |

Where it shows and whether the pets are pinned open are remembered across
sessions. `panal` must be on your `PATH`; if it is missing or too old, the
plugin says so once and keeps retrying, waiting a little longer each time. On
surfaces that cannot draw raster cells (the desktop app) it shows text lines
only.

## Which mascot

1. `-agent NAME` (claude, agy, codex or opencode), if given;
2. else claude while it orchestrates;
3. else the agent working (or stuck) that started last;
4. else the one whose last run ended last;
5. else claude.

Agents turned off in `panal.conf` are left out while they are at rest,
like in `panal -statusline`.

## What it does

| mood | when | mascot |
|---|---|---|
| `work` | its agent is working or orchestrating | its work cycle |
| `celebrate` | a run just finished (its own or another agent's) | hops with confetti |
| `scared` | a run just failed, an agent got stuck, ran out of quota or hit a permission; or its own agent is stuck | shakes, red `!` |
| `greet` | its agent started working, or the pet changed to another agent | `!` and a hop |
| `sleep` | idle or done for over 2 hours (a nap, in color); or failed / out of quota (gray) | eyes closed, rising z |
| `idle` | anything else | blinks and looks around |

Reactions last as long as on the dashboard (1–3 s). A run that ended less
than 5 seconds ago also counts as "just finished", so a single
`panal pet -json` call can celebrate it.

It animates at about 4 frames per second. `-no-animation` (or
`PANAL_NO_ANIMATION=1`, or `animation = no` in `panal.conf`) keeps the
mascot still and skips the reactions. With `NO_COLOR` the mascot is not
drawn; both lines stay, with glyph and word. `-theme` (or `PANAL_THEME`,
or `theme` in `panal.conf`) picks the colors as on the dashboard.

## For other programs: `-json` and `-stream`

`panal pet -json` prints one JSON frame and exits; `panal pet -stream`
prints one frame per line (NDJSON) whenever something changes, at most
about 4 per second, and at least one every 2 seconds as a heartbeat. It
exits when its stdout is closed or on `ctrl+c`. A Claude Code plugin, a
status bar or an editor extension can draw the pet from these frames
without a terminal of its own. The fields are in the
[reference](reference.md#pet-frames).

| flag | default | what |
|---|---|---|
| `-agent NAME` | picked | always show this agent |
| `-every 2s` | `2s` (or `every` in `panal.conf`) | how often to re-read the agents |
| `-json` | | one frame and exit |
| `-stream` | | one frame per line until the pipe closes |
| `-all` | | with `-json`/`-stream`: also every shown agent's own mascot, in `pets` |
| `-cols N` · `-rows N` | the sprite's 12 × 5 | with `-json`/`-stream`: the largest raster to send (it is cropped: centered across, from the top down) |
| `-no-animation` | | still mascot, no reactions |
| `-theme NAME` | `auto` | `auto`, `dark`, `light` or `contrast` |

One `-json` call takes about 0.3 s on Windows (process start and reading
the agents' files); to follow the pet live, keep one `-stream` running
rather than polling `-json`.
