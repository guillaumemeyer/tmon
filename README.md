# tmon — Your AI agents, now with a leash

![tmon](https://img.shields.io/badge/tmux-plugin-blue) ![agents](https://img.shields.io/badge/agents-11-green)

You've got Grok Build crunching through a refactor in one pane, Claude Code
negotiating a design doc in another, and Hermes Agent off doing… whatever Hermes
Agent does. Wouldn't it be nice to know who's actually working, who's stuck
waiting for your approval, and who's just daydreaming?

**tmon** sits quietly in your tmux status bar and tells you exactly that.
It sniffs out running AI coding agents from `/proc`, tracks their activity,
and renders it all as a dead-simple count indicator. Need details? Hit
`prefix a a` for an interactive dashboard that shows every agent and lets you
jump straight to their pane.

---

## What you'll see

### Status bar

```
[@] ? 2 - ● 3 - ‖ 1
     ↑      ↑      ↑
  blocked  active  idle
```

- **? orange** — agent is frozen, waiting for you (permission prompt, plan approval, y/n question)
- **● green** — agent is cooking (CPU or IO activity detected, or just booted up)
- **‖ blue** — agent is idle (no activity for a few polls)
- **[@] ? 0 - ● 0 - ‖ 0** — no agents detected (peace and quiet)

Every segment always renders at a fixed width, so your status bar won't
dance around when counts change.

### Navigation (`prefix a a`)

An 80%×80% popup that lists every running agent with its status and exact
tmux location:

```
┌──────────────────────────────────────────────────┐
│  tmon — Agent Navigation              [q] quit   │
├──────────────────────────────────────────────────┤
│                                                  │
│  ▸ ● Grok Build  active                          │
│      [1]:main/[0]:code/[0]                       │
│                                                  │
│  ● Claude Code  active                           │
│      [1]:main/[1]:chat/[0]                       │
│                                                  │
│  ‖ Hermes Agent  idle                            │
│      [0]:main/[2]:tmon/[3]                       │
│                                                  │
├──────────────────────────────────────────────────┤
│  ━━━━━━━━━━━━━━━━━━━━━━━━━━━  ↑↓/jk nav  /...    │
└──────────────────────────────────────────────────┘
```

Each agent gets **two lines**: full name with status indicator and label
on line one, the exact tmux path (`[session]:name/[window]:name/[pane]`)
on line two.

**Status indicator** is always accurate — it reads from the same state file
that drives the status bar, so the navigation and status bar never disagree.

Hit `Enter` or `→` on any agent and you're teleported directly to its pane
(session, window, and pane all switch at once).

**Search**: Press `/` to filter the list by agent name, session name, or
window name. Type your query and the list updates in real time. The first
matching agent is auto-selected. Press `Esc` or `/` again to clear.

| Key | Action |
|-----|--------|
| `↑` `↓` / `j` `k` | Navigate the list |
| `Enter` / `→` | Jump to the selected agent's pane |
| `/` | Start / cancel full-text search |
| `r` | Refresh |
| `q` / `Esc` | Close |

### Who's on the guest list

tmon recognizes **11 agents** out of the box:

| Agent | What it watches for |
|-------|-------------------|
| **Grok Build** | `grok`, `grok-build`, `grok agent` |
| **Claude Code** | `claude`, `claude-code`, `node @anthropic/claude` |
| **Codex CLI** | `codex`, `codex-cli` |
| **Cursor** | `cursor agent`, `cursor-agent` |
| **Cline** | `cline`, `cline agent` |
| **Aider** | `aider`, `python aider` |
| **GitHub Copilot** | `copilot agent` |
| **CodeBuddy** | `codebuddy`, `codebuddy agent` |
| **Windsurf** | `windsurf`, `windsurf agent` |
| **Hermes Agent** | `hermes agent`, `hermes run` |
| **OpenClaw** | `openclaw agent`, `openclaw chat` |

Detection works purely by scanning `/proc/[pid]/cmdline` — no agent APIs,
no daemons, no external dependencies beyond bash and tmux itself.

---

## Installation

### With TPM (recommended)

```tmux
# ~/.tmux.conf
set -g @plugin 'guillaumemeyer/tmon'
```

Then `prefix I` to install.

### Manual

```bash
git clone https://github.com/guillaumemeyer/tmon ~/.tmux/plugins/tmon
```

```tmux
# ~/.tmux.conf
run-shell ~/.tmux/plugins/tmon/tmon.tmux
```

Reload: `tmux source-file ~/.tmux.conf`

---

## Configuration

Set any of these in `~/.tmux.conf` **before** the plugin line. All of them
are optional — the defaults are sensible for most people.

### `@tmon-poll-interval`

> How often tmon scans for agents and samples their activity.

| | |
|---|---|
| **Default** | `3000` |
| **Unit** | milliseconds |
| **Range** | `1000`–`60000` (1s to 60s) |

```tmux
set -g @tmon-poll-interval "5000"   # every 5 seconds
```

Lower values give snappier status updates but burn slightly more CPU.
The default of 3000ms (3 seconds) hits a sweet spot — agents don't change
state faster than that anyway, and `/proc` scans are cheap.

### `@tmon-activity-threshold`

> The CPU floor for calling an agent "active." Agents doing remote API work
> (thinking, streaming) often use very little CPU — tmon treats *any* activity
> above zero as "active" on first detection. This threshold kicks in on
> subsequent polls to distinguish genuine work from background noise.

| | |
|---|---|
| **Default** | `500` |
| **Unit** | CPU milliseconds per second |
| **Range** | `100`–`5000` |

```tmux
set -g @tmon-activity-threshold "200"   # more sensitive
```

### `@tmon-status-position`

> Which side of your status bar gets the tmon indicator.

| | |
|---|---|
| **Default** | `right` |
| **Options** | `right` or `left` |

```tmux
set -g @tmon-status-position "left"
```

### `@tmon-dashboard-key`

> The chord leader key for opening the agent navigation popup.
> Opens with `prefix <key> <key>`.

| | |
|---|---|
| **Default** | `a` |
| **Example binding** | `prefix a a` → navigation |

```tmux
set -g @tmon-dashboard-key "b"   # use prefix b b instead
```

---

## How it works (the juicy details)

### Agent detection

Every poll, tmon walks `/proc/[0-9]*/cmdline` and greps against a combined
regex of all 11 agent signatures. It's fast — even with hundreds of processes,
the scan completes in single-digit milliseconds.

### Activity tracking

tmon reads two counters from `/proc`:

- **CPU ticks** from `/proc/[pid]/stat` (fields 14+15+16+17: user + system + child user + child system)
- **IO bytes** from `/proc/[pid]/io` (rchar + wchar)

On first sight of an agent, it's marked "running" regardless of CPU — agents
often think remotely with near-zero local CPU. On subsequent polls, any CPU or
IO delta > 0 bumps it to "active." After 3 consecutive idle polls (9 seconds at
default interval), it decays to "idle."

### Blocked state detection

If an agent is running inside a tmux pane, tmon uses `tmux capture-pane` to
grab the visible terminal content and looks for telltale signs of a stuck agent:

- **Selectors**: `❯ 1.`, `[y/N]`, `[Y/n]`
- **Permission prompts**: "Do you want to proceed?", "Press any key"
- **Plan approval**: `[approve]`, `[confirm]`, "plan approval required"
- **Chat questions**: "Should I", "Waiting for your input"

Blocked detection overrides everything — if tmon thinks an agent is waiting
for you, it won't call it "active" no matter how much CPU it's burning.

### Pane mapping

Matching an agent PID to a specific tmux pane is a two-step dance:

1. **TTY matching**: Read `/proc/[pid]/stat` field 7 (tty_nr), convert the
   major/minor to `/dev/pts/N`, then match against `tmux list-panes -a -F`.
2. **Process tree fallback**: If TTY matching fails (e.g., the agent is a
   child process), walk up the parent chain looking for a PID that matches
   a tmux `pane_pid`.

This means tmon works even when the agent binary is launched deep inside a
shell pipeline or subshell.

### Architecture

```
tmon.tmux                  ← Plugin entrypoint (sourced by tmux)
├── scripts/monitor.sh     ← Process scanner + activity evaluator + blocked detection
│   └── --once             ← Called by tmux #() interpolation for status bar
├── scripts/pane-map.sh    ← Maps agent PIDs to tmux pane addresses
├── scripts/dashboard.sh   ← Interactive navigation popup
└── scripts/notify.sh      ← Notification dispatcher
```

No npm, no pip, no cargo. Just bash and `/proc`.

---

## Keybindings

| Binding | Action |
|---------|--------|
| `prefix a a` | Open the agent navigation popup |

---

## Adding your own agent

Edit `scripts/monitor.sh` and `scripts/dashboard.sh`. Add an entry to the
`AGENT_SIGNATURES` array in both files:

```bash
"YourAgent:^your-tool( |$)"
"YourAgent:your-tool( |-)(agent|chat|run)"
```

Then add its full display name in `dashboard.sh`'s `agent_full_name()` function.
The regex is matched against the process command line (null-separated args
joined with spaces, no trailing newline).

---

## Troubleshooting

**Status bar is empty** — tmon only renders when agents are detected. Fire up
Grok Build or Claude Code and it should appear. Still nothing? Run manually:

```bash
bash ~/.tmux/plugins/tmon/scripts/monitor.sh --once
```

**Navigation won't open** — Check your keybinding: `tmux list-keys -T a-table`.
If `a` conflicts with another plugin, change `@tmon-dashboard-key`.

**Agent shows as "?" instead of a pane path** — The PID-to-pane mapping
couldn't resolve. This happens with headless agents or processes outside tmux.
Normal and harmless — the status bar still tracks them.

**High CPU from tmon** — Increase `@tmon-poll-interval`. The default 3000ms
is already conservative; bumping to 10000ms (10s) makes the scan cost
essentially invisible.

---

## License

MIT — because the robots haven't taken over yet.
