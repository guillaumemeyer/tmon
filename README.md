# tmon — Your AI agents, now with a leash

You've got Grok Build crunching through a refactor in one pane, Claude Code
negotiating a design doc in another, and Hermes Agent off doing… whatever
Hermes Agent does. Wouldn't it be nice to know who's actually working, who's
stuck waiting for your approval, and who's just daydreaming?

**tmon** sits quietly in your tmux status bar and tells you exactly that.
It finds running AI coding agents, tracks whether they're working, idle, or
waiting on you, and shows a compact count indicator with color-coded status
icons. Need details? Hit `prefix a a` for an interactive dashboard that lists
every agent and lets you jump straight to their pane. Or just **click the
status bar indicator** — that works too.

---

## What you'll see

### Status bar

```
🤖-🛑2-⚡️3-💤1
↑  ↑   ↑   ↑
icon blocked working idle
```

- **🤖 cyan** — your personal fleet of AI agents
- **🛑 orange** — agent is blocked, waiting for you (permission prompt, plan approval, y/n question)
- **⚡️ green** — agent is working
- **💤 blue** — agent is idle: the session is alive but the agent is not actively thinking or writing, and it isn't waiting on you
- **🤖** alone — no agents detected (peace and quiet)

Each status segment (icon + count) only appears when at least one agent is
in that state, so the indicator stays compact. Set `@tmon-ascii-icons "1"`
to swap the emoji for plain ASCII — same colors, same visibility rules:

```
[@]-B2-W3-I1
↑   ↑  ↑  ↑
app B  W  I
   blocked working idle
```

> **Note:** A brand-new agent shows as **💤 idle** until its first activity
> sample.

### Dashboard (`prefix a a`)

An 80%×80% popup that lists every running agent grouped by session and
window, with a live pane preview on the right:

```
┌──────────────────────────────────┬───────────────────┐
│  🤖 tmon  [/] search [esc/q] quit │ Extract Agent Ses…│
│ ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━│───────────────────│
│  main                            │ $ tmon dashboard  │
│    0:code                        │ … pane content …  │
│    > Extract Agent Sessions (Grok…│                   │
│      ~/code                      │                   │
│      ctx 52.4k/200k (26%)        │                   │
│      Claude Code                 │                   │
│      ~/docs  [y/N]               │                   │
│  side                            │                   │
│    0:research                    │                   │
│      Windsurf                    │                   │
│      ~/res                       │                   │
│  v0.4.2        [↑/↓ j/k] navig… │                   │
└──────────────────────────────────┴───────────────────┘
```

Each agent takes two compact lines: a bold **session title and agent name**
(`Title (Name)`, or just the name when the session has not earned a title
yet) and, beneath it, a dimmed **working directory** plus — when the agent
is blocked — the prompt it is waiting on (e.g. `[y/N]`, or `paused` when
the prompt is unknown). When the connector can read token usage, a third
dim **stats line** appears:

```
    > Extract Agent Sessions (Grok Build)
      ~/code
      ctx 52.4k/200k (26%)
```

The stats line shows the **context window** — tokens used over the model's
window size with the used percentage (`ctx 13k/200k (5%)`; the `%` is shown
only when the window size is known) — and, when a connector reports it, the
**account quota** as remaining percentage plus next reset time
(`62% left · reset 14:00`). Both are blank when unknown, and the agent then
stays at two lines. What gets populated today:

| Agent | Stats line |
|-------|------------|
| Grok Build | tokens + window % (from `signals.json`) |
| Claude Code | tokens + window % when the model's window is known (from the transcript) |
| Codex CLI | tokens (from the session history) |
| Hermes Agent | tokens (from `hermes insights`) |
| Others | no stats line — no local usage source |

Account-quota fields are plumbed through but no agent currently exposes
them locally, so the quota segment stays blank until a source exists.
The two-line layout keeps the list readable even when the left column is
narrow. The version is pinned to the bottom-left.
The footer shows `[↑/↓ j/k] navigate` to move the selection,
`[←/→ h/l] resize preview` for the preview split (persisted across opens)
and, when an agent with a pane is selected, `[C-u/C-d] scroll preview` for
the preview.

Filter by status with `b` (blocked), `w` (working), `i` (idle); press the
key again to clear. `1`–`9` jumps to the Nth agent. Hit `Enter` or **click
an agent line** to jump to that agent's pane.

**Fuzzy search** — press `/`, then type a Telescope/fzy-style query. Matches
are subsequences (not substrings) over the session title, agent name,
working directory, and the full pane capture (including content not
currently visible in the preview). Space-separated terms are ANDed. Results
are ranked by match quality. `Esc` leaves search mode (the filter stays);
`Esc` again closes the popup.

| Key / Mouse | Action |
|-----|--------|
| `↑` `↓` / `j` `k` | Navigate the list |
| `←` / `→` / `h` / `l` | Grow / shrink the preview pane (persisted) |
| `Ctrl-u` / `Ctrl-d` | Scroll the preview up / down |
| `1`–`9` | Jump to the Nth agent in the list |
| `b` / `w` / `i` | Filter by status: blocked / working / idle (press again to clear) |
| `Enter` / `Space` | Jump to the selected agent's pane |
| Click on an agent line | Select and jump to that agent's pane |
| `/` | Start fuzzy search (session title, name, directory, pane content) |
| Type | Filter the list |
| `Backspace` | Remove last character from filter |
| `Esc` | Leave search mode; if not searching, close popup |
| `q` / `Ctrl-c` | Close popup |

### Supported agents

tmon recognizes **11 agents** out of the box: Grok Build, Claude Code, Codex
CLI, Cursor, Cline, Aider, GitHub Copilot, CodeBuddy, Windsurf, Hermes Agent,
and OpenClaw.

How closely tmon can track each one depends on the agent's own state
surface. Agents that publish live state files (Grok, Hermes, Cline,
CodeBuddy, Aider) or accept lifecycle hooks (Claude Code, Codex, Cursor,
Copilot, Windsurf) give tmon authoritative working / blocked / idle signals;
everyone else falls back to the CPU/IO and pane-content heuristics. The
matrix below shows which features each agent's connector provides:

| Agent | Connector | Status | Blocked | Detail | Title | Tokens |
|-------|-----------|--------|:---:|--------|:---:|:---:|
| Grok Build | native (`~/.grok`) | exact | ✓ | phase · tool · permission · model | ✓ | ✓ + window % |
| Claude Code | hooks | exact | ✓ | tool · permission | ✓ | ✓ + window % |
| Codex CLI | hooks (+ `/hooks` trust) | exact | ✓ | tool · permission | — | ✓ |
| Hermes Agent | native (`~/.hermes`) | gateway | — | gateway · N active agents | — | ✓ |
| GitHub Copilot | hooks, else native fallback | exact | ✓ | tool · permission | — | — |
| Cursor | hooks, else native fallback | exact | — | tool | — | — |
| Windsurf | hooks | exact | — | tool | — | — |
| Cline | native (`~/.cline`) | working | — | session id | — | — |
| CodeBuddy | native (`~/.codebuddy`) | idle | — | session id | — | — |
| Aider | native (`.aider.chat.history.md`) | working | — | editing | — | — |
| OpenClaw | reserved | heuristic | — | — | — | — |

**Status** is how precisely tmon knows the working / blocked / idle state:
`exact` from the agent's own signals, a partial signal (`working`, `idle`,
`gateway`), or `heuristic` (CPU/IO inference). **Blocked** marks connectors
that detect a permission wait themselves; a — falls back to the pane-pattern
heuristic (`[y/N]`, permission prompts, …). **Detail** is what the dashboard
shows under the agent's name, **Title** the session name ("Title (Agent)"),
and **Tokens** the stats line (tokens + context-window % when known).

Hooks install automatically at plugin load unless `@tmon-auto-hooks` is
`off` — or by hand with `tmon hooks install <agent>`. Codex additionally
requires the hooks to be trusted in-session via `/hooks`. Without hooks, the
Cursor/Copilot native fallback only reports the agent as idle.

---

## Requirements

- **Linux or macOS** (amd64 or arm64)
- **tmux ≥ 3.2** (tested on 3.4)
- A downloader and checksum tool used once on first load:
  - download: **curl** or **wget**
  - verify: **sha256sum** (Linux) or **shasum** (macOS)
- **Network on first load** — the binary is downloaded from GitHub Releases;
  afterwards it lives in the plugin directory

### Windows

Native Windows is not supported, and the Windows build has not been tested.
Use **WSL2** with tmux and install tmon
*inside* the Linux environment (plugin under the Linux home, e.g.
`~/.tmux/plugins/tmon` — avoid `/mnt/c/...` when you can). Bootstrap fetches
the Linux binary; agents must also run as Linux processes in WSL for
detection to see them.

---

## Installation

### 1. Agent install (recommended)

Paste this into your coding agent (Grok Build, Claude Code, Codex, …) and let
it wire up your tmux config:

```
Install the tmux plugin guillaumemeyer/tmon for me.

1. Check whether TPM (Tmux Plugin Manager) is installed:
   - Look for ~/.tmux/plugins/tpm (or whatever path run '~/.tmux/plugins/tpm/tpm'
     would use), and whether ~/.tmux.conf already loads TPM via
     `run '.../tpm/tpm'`.
2. If TPM is installed (or you can install it cleanly):
   - Add this line to ~/.tmux.conf if it is not already present:
       set -g @plugin 'guillaumemeyer/tmon'
   - Ensure the TPM initializer is the **last** line of ~/.tmux.conf:
       run '~/.tmux/plugins/tpm/tpm'
   - If TPM itself is missing, clone it first:
       git clone https://github.com/tmux-plugins/tpm ~/.tmux/plugins/tpm
   - Then install plugins (TPM: prefix + I, or run the TPM install scripts).
3. If TPM is not available and should not be installed, do a manual install:
   - git clone https://github.com/guillaumemeyer/tmon ~/.tmux/plugins/tmon
   - Add to ~/.tmux.conf if missing:
       run-shell ~/.tmux/plugins/tmon/tmon.tmux
4. Reload: tmux source-file ~/.tmux.conf
5. Confirm success: the plugin dir exists and, on first load, bootstrap
   downloads the binary into ~/.tmux/plugins/tmon/bin/tmon (status message
   like "tmon: installed v…"). Report what you changed.
```

On first load, tmon downloads its binary into `<plugin>/bin/` — you'll see a
one-line status-bar message such as `tmon: installed v0.3.0 (linux/amd64)`.

### 2. Manual install

#### With TPM

```tmux
# ~/.tmux.conf
set -g @plugin 'guillaumemeyer/tmon'
```

Then `prefix I` to install.

If you're setting up TPM for the first time, clone it and keep the initializer
at the **very bottom** of `~/.tmux.conf`:

```bash
git clone https://github.com/tmux-plugins/tpm ~/.tmux/plugins/tpm
```

```tmux
# Initialize TMUX plugin manager (keep this line at the very bottom of tmux.conf)
run '~/.tmux/plugins/tpm/tpm'
```

Reload: `tmux source-file ~/.tmux.conf`, then `prefix I` if plugins are not
installed yet.

#### Without TPM

```bash
git clone https://github.com/guillaumemeyer/tmon ~/.tmux/plugins/tmon
```

```tmux
# ~/.tmux.conf
run-shell ~/.tmux/plugins/tmon/tmon.tmux
```

Reload: `tmux source-file ~/.tmux.conf`. On first load, tmon downloads its
binary into `<plugin>/bin/` — you'll see a one-line
`tmon: installed v0.3.0 (linux/amd64)` (or `darwin/arm64`, etc.) status-bar
message.

### Updating

TPM: hit `prefix U` (uppercase). tmon installs a git `post-merge` hook in the
plugin repo, so the pull itself re-downloads the binary matching the new
`VERSION` and re-applies the tmux wiring — no reload needed. If the hook
cannot run (e.g. the plugin is not a git clone), fall back to
`prefix U`, then `tmux source-file ~/.tmux.conf`.

Manual installs: `git pull origin main` (the same hook applies).

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

### `@tmon-activity-threshold`

> CPU sensitivity for calling an agent "working." Lower values are more
> sensitive to light activity.

| | |
|---|---|
| **Default** | `500` |
| **Unit** | CPU milliseconds per second |
| **Range** | `100`–`5000` |

```tmux
set -g @tmon-activity-threshold "200"   # more sensitive
```

### `@tmon-io-threshold`

> IO sensitivity for calling an agent "working." Lower values pick up lighter
> file and stream activity.

| | |
|---|---|
| **Default** | `102400` |
| **Unit** | bytes per poll interval |
| **Range** | `1024`–`10485760` |

```tmux
set -g @tmon-io-threshold "51200"  # more sensitive to IO
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

### `@tmon-connectors`

> Which agent connectors to enable. Connectors use each agent's own status
> signals (when available) for more accurate working / blocked / idle state.

| | |
|---|---|
| **Default** | `auto` |
| **Options** | `auto` (every connector whose agent is installed) or a comma list, e.g. `grok,claude` |

```tmux
set -g @tmon-connectors "grok,hermes"   # only these two connectors
```

### `@tmon-connector-freshness`

> How long a connector's status signal stays valid before tmon falls back to
> activity-based detection.

| | |
|---|---|
| **Default** | `30` |
| **Unit** | seconds |

```tmux
set -g @tmon-connector-freshness "60"   # keep connector state longer
```

### `@tmon-auto-hooks`

> Auto-install lifecycle hooks at plugin load for agents that need them
> (Claude Code, Codex, Cursor, Copilot, Windsurf). Install is idempotent and
> backs up each config once (`.tmon.bak`) before the first change. Set `off`
> to manage hooks by hand.

| | |
|---|---|
| **Default** | `on` |
| **Options** | `on` or `off` |

```tmux
set -g @tmon-auto-hooks off   # manage hooks manually
```

Manual hook install (if auto-hooks are off):

```bash
~/.tmux/plugins/tmon/bin/tmon hooks install claude
~/.tmux/plugins/tmon/bin/tmon hooks install codex     # also run /hooks once inside Codex to trust them
~/.tmux/plugins/tmon/bin/tmon hooks install cursor
~/.tmux/plugins/tmon/bin/tmon hooks install copilot
~/.tmux/plugins/tmon/bin/tmon hooks install windsurf
```

`tmon hooks remove <agent>` undoes an install; `tmon hooks status` lists
what's installed. Hooks are optional — without them, agents still appear via
activity detection.

### `@tmon-ascii-icons`

> Render status icons as plain ASCII instead of emoji.

| | |
|---|---|
| **Default** | `0` |
| **Options** | `0` (emoji) or `1` (ASCII) |

```tmux
set -g @tmon-ascii-icons "1"   # [@]-B2-W3-I1 instead of 🤖-🛑2-⚡️3-💤1
```

### `@tmon-bold-counts`

> Render the per-status counts (the `2` in `🛑2`) in bold.

| | |
|---|---|
| **Default** | `1` |
| **Options** | `0` (normal weight) or `1` (bold) |

```tmux
set -g @tmon-bold-counts "0"
```

---

## Keybindings

| Binding | Action |
|---------|--------|
| `prefix a a` | Open the agent navigation popup |
| Click status bar indicator | Open the agent navigation popup |

---

## Notifications

tmon can send tmux popups when an agent becomes blocked, working, or idle.
Run the daemon in notify mode:

```bash
~/.tmux/plugins/tmon/bin/tmon daemon --notify
```

Notifications are opt-in and off by default. To start them with tmux, add
to `~/.tmux.conf`:

```tmux
run-shell -b "~/.tmux/plugins/tmon/bin/tmon daemon --notify"
```

---

## Troubleshooting

**Status bar is empty** — tmon only renders when agents are detected. Fire up
an agent and it should appear. Still nothing? Run:

```bash
~/.tmux/plugins/tmon/bin/tmon status
```

**Dashboard won't open** — Check your keybinding: `tmux list-keys -T a-table`.
If `a` conflicts with another plugin, change `@tmon-dashboard-key`.

**Agent shows as "?" instead of a pane path** — The agent couldn't be mapped
to a tmux pane (e.g. headless or outside tmux). Harmless — the status bar
still tracks it.

**High CPU from tmon** — Increase `@tmon-poll-interval` (e.g. to `10000`).

**Stale agent counts after crash** — delete the state file and let it rebuild:

```bash
rm ~/.tmux/plugins/tmon/state/state.json
```

**Binary won't download** — needs network access to GitHub Releases. Check
connectivity, then from the plugin directory run `scripts/bootstrap.sh`
manually — it prints the failure reason.

---

## License

MIT — because the robots haven't taken over yet.
