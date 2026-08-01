# tmon — Your AI agents, now with a leash

![tmux-plugin](https://img.shields.io/badge/tmux-plugin-blue) ![agents](https://img.shields.io/badge/agents-11-green) ![language](https://img.shields.io/badge/language-Go-blue)

You've got Grok Build crunching through a refactor in one pane, Claude Code
negotiating a design doc in another, and Hermes Agent off doing… whatever
Hermes Agent does. Wouldn't it be nice to know who's actually working, who's
stuck waiting for your approval, and who's just daydreaming?

**tmon** sits quietly in your tmux status bar and tells you exactly that.
It sniffs out running AI coding agents from `/proc`, tracks their activity
via CPU ticks and IO bytes, and renders it all as a dead-simple count
indicator with animated status characters. Need details? Hit `prefix a a`
for an interactive dashboard that shows every agent and lets you jump
straight to their pane. Or just **click the status bar indicator** — that
works too.

The engine is a single **Go binary**: typed, unit-tested, and fast. The tmux
side is a thin loader that downloads the binary on first run (or update) and
wires up the status bar, keybindings, and popup.

---

## What you'll see

### Status bar

```
[@] ? 2 - ● 3 - ‖ 1
 ↑    ↑      ↑      ↑
icon blocked  active  idle
```

- **● cyan `[@]` prefix** — your personal fleet of AI agents
- **? orange** — agent is blocked, waiting for you (permission prompt, plan approval, y/n question). Toggles to **!** every other poll as a visual nudge.
- **● green** — agent is cooking (CPU or IO activity detected, or just booted up). Toggles to **!** every other poll as a visual pulse.
- **‖ blue** — agent is idle (no meaningful activity for several polls)
- **[@] ? 0 - ● 0 - ‖ 0** — no agents detected (peace and quiet)

Every segment always renders at a fixed width, so your status bar won't
dance around when counts change.

> **Note:** The green ● segment includes both "active" agents (actively using
> CPU/IO above threshold) and "running" agents (just detected, haven't yet
> shown activity). Both are doing real work. The distinction matters mainly
> for the dashboard where you can see the exact status label.

### Dashboard (`prefix a a`)

An 80%×80% popup that lists every running agent grouped by session and
window, with its emoji icon, status, and exact tmux location:

```
┌──────────────────────────────────────────────────────┐
│  [@] TMON                             [ESC] to quit  │
│ ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ │
│                                                      │
│  main                                                │
│    0:code                                            │
│      [0] ● 🧠 Grok Build        ~/code/tmon          │
│      [1] ● 🏛️ Claude Code       ~/docs/design        │
│  side                                                │
│    0:research                                        │
│      [2] ‖ 🏄 Windsurf           ~/research           │
│                                                      │
│ ▌ / to search                    j/k nav  ↩ focus   │
└──────────────────────────────────────────────────────┘
```

Each agent gets one line with its emoji icon, display name, animated status
character, pane index, and working directory. The selected row is highlighted
with a dim background.

**Status is always accurate** — the dashboard reads the same state file that
drives the status bar, so the two never disagree. "Blocked" detection runs
live against each pane's visible content on every refresh.

Hit `Enter` or `→` on any agent and you're teleported directly to its pane
(session, window, and pane all switch at once).

**Type to filter** — press `/`, then type to narrow the list by agent name,
session, or window name. The list updates in real time and a match count
appears in the footer. `Esc` clears the filter; `Esc` again closes the popup.

**Auto-refresh**: the dashboard refreshes every 1.5 seconds, with a full
data reload every ~6 seconds.

| Key | Action |
|-----|--------|
| `↑` `↓` / `j` `k` | Navigate the list |
| `Enter` / `→` / `l` / `Space` | Jump to the selected agent's pane |
| `/` | Start filtering (agent name, session, window) |
| Type | Filter the list |
| `Backspace` | Remove last character from filter |
| `Esc` | Clear filter; if already clear, close popup |
| `q` / `Ctrl-c` | Close popup |

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

Detection works purely by scanning `/proc/[pid]/cmdline` — no agent APIs, no
per-agent daemons, no dependencies beyond the tmon binary and tmux itself.

---

## Requirements

- **Linux** (amd64 or arm64) — tmon reads `/proc`
- **tmux ≥ 3.2** (tested on 3.4) — the dashboard popup needs `display-popup`
- **curl and sha256sum** — used once by the installer to fetch and verify the binary
- **Network on first load** — the binary is downloaded from GitHub Releases
  (see below); afterwards it lives in the plugin directory

A Go toolchain is **not** required for users — only for developing tmon itself.

---

## Installation

### With TPM (recommended)

```tmux
# ~/.tmux.conf
set -g @plugin 'guillaumemeyer/tmon'
```

Then `prefix I` to install.

> **Note:** If you're setting up TPM for the first time, make sure the
> TPM initializer runs at the **very bottom** of your `~/.tmux.conf`:
>
> ```tmux
> # Initialize TMUX plugin manager (keep this line at the very bottom of tmux.conf)
> run '~/.tmux/plugins/tpm/tpm'
> ```

### Manual

```bash
git clone https://github.com/guillaumemeyer/tmon ~/.tmux/plugins/tmon
```

```tmux
# ~/.tmux.conf
run-shell ~/.tmux/plugins/tmon/tmon.tmux
```

Reload: `tmux source-file ~/.tmux.conf`. On first load, tmon downloads its
binary into `<plugin>/bin/` — you'll see a one-line "tmon: installed v0.3.0
(amd64)" status-bar message.

---

## How the binary is distributed

tmon is **two parts**: a thin tmux loader (plain bash, committed to the repo)
and a Go binary (**never** committed — downloaded as a release artifact).

- **First run** — `tmon.tmux` runs `scripts/bootstrap.sh`, which downloads
  the pinned asset `tmon_<VERSION>_linux_<arch>` plus `checksums.txt` from
  GitHub Releases, verifies the SHA-256 checksum, and installs it to
  `<plugin>/bin/tmon`. If the installed binary already matches the repo's
  `VERSION` file, bootstrap is a no-op (instant).
- **Updates** — every push to `main` triggers a GitHub Action that reads
  `VERSION`, tags `v<VERSION>` (skipping if that release already exists), and
  ships a goreleaser build for linux/amd64 + linux/arm64. Users update the
  repo (TPM `prefix U` or `git pull`) and reload; bootstrap sees the new
  `VERSION` and re-downloads the matching artifact. `tmux source-file` is
  enough — no tmux restart needed.
- **Offline / failure** — bootstrap fails gracefully: it shows a status-bar
  message, leaves the plugin loadable, and retries on the next reload. Run
  `scripts/bootstrap.sh` manually to retry.
- **Developer builds** — `make build` produces a binary stamped `dev`, which
  bootstrap never overwrites.

**Nothing is written outside the plugin directory**: the binary, the state
file, the download lock, and temp download files all live in `<plugin>/bin`
and `<plugin>/state`. No `~/.cache`, no `/tmp`, no system temp dirs.

### Updating

TPM: hit `prefix U` (uppercase), then `tmux source-file ~/.tmux.conf`.
Manual installs: `git pull origin master`, then `tmux source-file ~/.tmux.conf`.

> **Releasing a new version:** `make bump-patch` (bumps `VERSION`), commit,
> push to `main`. CI auto-releases `v<VERSION>` if it doesn't already exist —
> no manual tagging.

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

### `@tmon-io-threshold`

> The minimum IO throughput for calling an agent "active." Agents streaming
> responses or writing files generate IO — this threshold distinguishes real
> work from background filesystem noise (logs, terminal echo).

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

### Advanced: environment variables

These are exported by `tmon.tmux` from the tmux options above (and pushed
into tmux's global environment, so the status-bar `#()` command and the popup
actually see them). They can also be overridden directly for custom setups:

| Variable | From option | Description |
|----------|-------------|-------------|
| `TMON_POLL_INTERVAL_MS` | `@tmon-poll-interval` | Poll interval in ms |
| `TMON_ACTIVITY_THRESHOLD_MS` | `@tmon-activity-threshold` | CPU threshold in ms/s |
| `TMON_IO_ACTIVITY_THRESHOLD` | `@tmon-io-threshold` | IO threshold in bytes |
| `TMON_IDLE_DECAY_POLLS` | *(no tmux option)* | Consecutive idle polls before "idle" (default: 3) |
| `TMON_STATE_DIR` | *(set by tmon.tmux)* | State dir, default `<plugin>/state` |
| `TMON_BIN_DIR` | *(set by tmon.tmux)* | Binary dir, default `<plugin>/bin` |

---

## How it works (the juicy details)

### Agent detection

Every poll, tmon walks `/proc/[0-9]*/cmdline` and matches it against a
compiled regex of all 11 agent signatures. It's fast — even with hundreds of
processes, a scan completes in single-digit milliseconds.

### Activity tracking

tmon reads two counters from `/proc`:

- **CPU ticks** from `/proc/[pid]/stat` (fields 14+15+16+17: user + system + child user + child system)
- **IO bytes** from `/proc/[pid]/io` (rchar + wchar)

Agents transition through a **4-state machine**:

1. **running** — First sight of an agent; shown immediately regardless of activity
   (agents often think remotely with near-zero local CPU).
2. **active** — CPU delta ≥ `@tmon-activity-threshold` or IO delta ≥ `@tmon-io-threshold`
   since the last poll. This filters out scheduler noise and terminal cursor updates.
3. **blocked** — Overrides everything. If the pane content matches any blocked-state
   pattern (permission prompts, y/n questions, approval wait), the agent is stuck
   waiting for you.
4. **idle** — No meaningful CPU or IO activity for 3 consecutive polls (9 seconds
   at default 3s interval). The grace period prevents flickering for agents
   between API calls.

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

Matching an agent PID to a specific tmux pane uses three strategies, in order:

1. **Direct PID match** — Check if the PID itself is a tmux `pane_pid`
   (O(1) via a pre-built map).
2. **TTY matching** — Read `/proc/[pid]/stat` field 7 (tty_nr), convert the
   major/minor to `/dev/pts/N`, then match against `tmux list-panes -a -F`
   (also O(1)).
3. **Process tree fallback** — If both above fail (e.g., the agent is a child
   process), walk up the parent chain looking for a PID that matches a tmux
   `pane_pid`.

This means tmon works even when the agent binary is launched deep inside a
shell pipeline or subshell.

### Architecture

```
tmon.tmux                  ← Plugin entrypoint (sourced by tmux)
├── scripts/bootstrap.sh   ← Downloads the pinned release binary + verifies sha256
├── bin/tmon               ← Go binary: status / daemon / dashboard / version
├── state/state.json       ← Shared state (written by `status`, read by the dashboard)
└── cmd/tmon, internal/    ← Go source (only needed when developing)
```

No npm, no pip, no cargo. Just a small Go binary and `/proc`.

---

## Keybindings

| Binding | Action |
|---------|--------|
| `prefix a a` | Open the agent navigation popup |
| Click status bar indicator | Open the agent navigation popup |

> **Tip:** The status bar indicator is a clickable `#[range=user|tmon]` range.
> Clicking anywhere on it opens the dashboard without needing a keyboard
> shortcut.

---

## Notifications

tmon can send tmux `display-message` popups on state transitions (agent
started, agent became active). Run the daemon in notify mode:

```bash
~/.tmux/plugins/tmon/bin/tmon daemon --notify
```

This runs continuously in the background, emitting transient popups like
"Grok Build started in code/tmon" or "Claude Code is now active in src/api"
whenever an agent changes state. Notifications are opt-in and off by default
to avoid noise. (To have tmux start it on every server, add
`run-shell -b "~/.tmux/plugins/tmon/bin/tmon daemon --notify"` to your
`~/.tmux.conf` — the `-b` flag detaches it.)

## Adding your own agent

Three small edits, then `make build`:

1. **Detection** — add a signature (a label + regex matched against the
   process command line) to the table in `internal/detect/signatures.go`.
2. **Display name + icon** — add them to `internal/dashboard/names.go`.
3. **Tests** — add rows to the signature test matrix in
   `internal/detect/signatures_test.go` and rebuild.

The blocked-state patterns live in `internal/blocked/blocked.go`. The regex
is matched against the process command line (null-separated args joined with
spaces, no trailing newline).

---

## Troubleshooting

**Status bar is empty** — tmon only renders when agents are detected. Fire up
Grok Build or Claude Code and it should appear. Still nothing? Run manually:

```bash
~/.tmux/plugins/tmon/bin/tmon status
```

**Dashboard won't open** — Check your keybinding: `tmux list-keys -T a-table`.
If `a` conflicts with another plugin, change `@tmon-dashboard-key`.

**Agent shows as "?" instead of a pane path** — The PID-to-pane mapping
couldn't resolve. This happens with headless agents or processes outside tmux.
Normal and harmless — the status bar still tracks them.

**High CPU from tmon** — Increase `@tmon-poll-interval`. The default 3000ms
is already conservative; bumping to 10000ms (10s) makes the scan cost
essentially invisible.

**Stale agent counts after crash** — tmon persists agent state to
`<plugin>/state/state.json`. If counts seem wrong after an abnormal tmux
exit, delete this file and let it rebuild:

```bash
rm ~/.tmux/plugins/tmon/state/state.json
```

**Binary won't download** — bootstrap needs network access to GitHub
Releases. Check connectivity, then run `scripts/bootstrap.sh` manually — it
prints the reason for the failure.

---

## License

MIT — because the robots haven't taken over yet.
