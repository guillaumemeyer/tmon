# Plan: model-usage monitoring for tmon (quota + token ledger)

Status: draft for review.
Date: 2026-08-06.
Author: plan prepared from the Omarchy `omarchy.model-usage` analysis.

## 1. Purpose

tmon shows the fleet status. It does not show account quota or token
history. Omarchy has a widget that does both. This plan adds the missing
pieces to tmon.

The missing pieces are:

1. Quota monitoring (rate-limit windows and plan tier).
2. Token ledger (tokens by day, by model, and all-time).
3. Background execution for both (the analysis is too slow for the poll).
4. Optional: cross-machine sync of the ledger.

## 2. Current constraints

These facts drive the design:

- `tmon status` runs as a fresh process on every tmux status refresh.
  The default poll interval is 3000 ms. The status bar runs the command
  about every 3 seconds.
- `cmdStatus` states the contract: complete quickly, never touch the
  network.
- `state.json` is written by every status poll. The dashboard reads it.
- Disk caches live under `<state>/usage/`:
  - `incrementalTokens` parses only the bytes appended since the last
    poll. It has two modes: sum and latest.
  - `runCachedTTL` runs an expensive CLI at most once per TTL window.
  - `usageCacheVersion` (currently 3) invalidates stale counts.
- The `Usage` struct has four fields:
  - `TokensUsed` (context tokens used, per live session).
  - `WindowTokens` (context window size).
  - `QuotaPct` (account quota used percent).
  - `QuotaReset` (next quota reset time).
- The dashboard renders the quota fields already. The test expectation
  is `context: 52.4k/200k ████ 26% · 62% left · reset 14:00`.
- No connector populates `QuotaPct` or `QuotaReset`. The struct comment
  says: "they stay empty until a source exists."
- Live-session context gauges exist for Grok, Claude, Codex, and Hermes.
  The gauge uses "latest" semantics for Claude (each usage block is a
  full context snapshot). The ledger needs "sum" semantics (billing
  style). Both modes already exist in `usagecache.go`.

## 3. Gap analysis

| Capability | Omarchy model-usage | tmon today | Gap |
|---|---|---|---|
| Quota probe, Claude | OAuth usage endpoint | none | missing |
| Quota probe, Codex | `codex app-server` RPC | none | missing |
| Tokens by day (7 days) | full-transcript scan | none | missing |
| Tokens by model (all-time) | full-transcript scan | none | missing |
| Per-session context gauge | none | Grok, Claude, Codex, Hermes | present |
| Dedupe by message id | yes | size-based cursor only | partial |
| Cross-machine sync | snapshot files, date union | none | missing |
| Self-hiding bar icon | yes | n/a (status line) | n/a |

The quota fields and their rendering exist. The data sources do not.
The ledger needs a new data model, new storage, and new dashboard
rendering.

## 4. Why the work cannot run inside the poll

Two job classes are too heavy for a 3-second poll:

1. Quota probes use the network. The status command must never touch
   the network. A Claude probe is an HTTPS GET to
   `api.anthropic.com/api/oauth/usage`. A Codex probe spawns
   `codex app-server` and speaks JSON-RPC over stdio. Both need timeouts
   of 4 to 8 seconds.
2. Full-transcript scans walk hundreds of JSONL files. Omarchy moved
   this work out of the shell process for the same reason. A scan can
   read 100 MB or more of JSONL.

The incremental cache keeps live sessions cheap. It does not help the
first full walk or the daily buckets.

## 5. Architecture options

All options share one job loop. The loop has three jobs:

- Quota probe (network, TTL 15 minutes).
- Ledger scan (incremental per cycle, full walk on first run).
- Sync (read and write snapshots, when enabled).

The loop writes one file: `<state>/usage.json`. It writes atomically
(tmp file + rename). It never writes `state.json`. The status poll
writes `state.json`; the worker writes `usage.json`. The two writers
never contend.

`tmon status` reads `usage.json` when it exists. The read is cheap.
It attaches quota to the live records by agent label. The dashboard
reads the same file for the ledger view.

### Option A: persistent daemon (`tmon daemon`)

A long-running process. The user starts it explicitly.

- Lifecycle: `tmon daemon start`, `tmon daemon stop`, PID file.
- `tmon doctor` reports daemon state.
- Pros: explicit control; single writer; in-memory caches; network
  allowed; clean scheduling.
- Cons: new moving part; the user must start it; it must survive tmux
  reloads and reboots; it breaks the zero-config story.

### Option B: refresh-time with TTL gating (no persistent process)

`tmon status` keeps all work. Heavy jobs run inside it, at most once
per TTL window (`runCachedTTL` already exists).

- Quota probe runs at most once per 15 minutes inside a poll.
- The ledger stays incremental per poll.
- The first full walk still blocks one poll. The tmux status bar
  freezes for that poll.
- Variant B2: `tmon.tmux` spawns a background shell loop
  (`while true; do tmon scan; sleep 300; done &`). The loop dies with
  the tmux server. Re-running tmon.tmux can duplicate the loop.
- Pros: no new binary; no lifecycle management; zero-config preserved.
- Cons: the network invariant of the status command is broken; the
  first walk blocks the bar; scheduling is fragile; no in-memory state.

### Option C: auto-spawned worker (recommended)

`tmon status` checks the worker heartbeat at start. The heartbeat is a
small file with a timestamp. If the heartbeat is missing or stale, the
status command spawns a detached worker and returns immediately.

- Spawn: `tmon worker` subcommand, same binary. Detach with `setsid`,
  redirect all stdio to a log file. The spawn costs one fork+exec,
  well under the poll budget.
- Single instance: `flock` on a pidfile. One worker per state dir, no
  matter how many tmux servers or polls race.
- Heartbeat: the worker writes the heartbeat every cycle (60 seconds).
  A crashed worker leaves a stale heartbeat. The next poll respawns it.
  The system is self-healing.
- Stop: `tmon worker stop`, or `TMON_WORKER=off` in the environment.
  With the worker off, the status command falls back to Option B
  behavior for quota (TTL-gated, lazy) and shows the ledger as absent.
- Idle behavior: the worker sleeps between cycles. CPU use is near
  zero. It exits after 30 minutes with no live agents and no open
  dashboard, to save battery on laptops.
- Pros: zero-config (auto-starts from the first poll); self-healing;
  single writer; network allowed in the worker; status stays fast; one
  binary, no new dist artifact.
- Cons: a background process appears without explicit user action;
  the spawn logic must be robust in the tmux `#()` context; worker
  lifecycle needs good `doctor` reporting.

### Option D: OS scheduler (systemd user timer / cron)

A timer runs `tmon scan` every N minutes. The status command only
reads.

- Pros: robust lifecycle; no self-spawn hacks; works headless.
- Cons: not zero-config (needs unit files); some tmux-only machines
  have no systemd; timer granularity is coarse.

### Comparison

| Criterion | A daemon | B TTL-lazy | C auto-worker | D scheduler |
|---|---|---|---|---|
| Status-bar latency | low | one slow poll | low | low |
| Network in status | never | yes (gated) | never | never |
| Zero-config | no | yes | yes | no |
| Lifecycle management | manual | none | self-healing | OS |
| Single writer | yes | n/a | yes | yes |
| In-memory state | yes | no | yes | no |
| Dist impact | none | none | none | unit files |
| Failure recovery | manual | n/a | automatic | OS |

### Recommendation

Implement Option C as the primary design. Structure the worker loop as
a shared package (`internal/worker`). Give the same loop three start
modes:

1. `tmon worker` — auto-spawned by the status command (default).
2. `tmon daemon` — the same loop, started manually. Useful for
   headless setups and debugging.
3. TTL-lazy fallback inside the status command — active only when the
   worker is disabled or the heartbeat is too fresh to respawn.

All three modes share one job implementation. The tests cover the
loop once, not three times.

## 6. Data model

New file: `<state>/usage.json`. The worker writes it. Status and
dashboard read it.

Schema version 1:

```json
{
  "schemaVersion": 1,
  "generatedAt": "2026-08-06T12:00:00Z",
  "deviceId": "hostname",
  "quota": {
    "claude": {
      "pct": 38,
      "label": "Session (5-hour)",
      "resetAt": "2026-08-06T14:00:00Z",
      "tier": "Max 20x",
      "statusText": "",
      "authHelpText": ""
    },
    "codex": {
      "pct": 12,
      "label": "Weekly (7-day)",
      "resetAt": "2026-08-09T00:00:00Z",
      "tier": "Pro",
      "statusText": "",
      "authHelpText": ""
    }
  },
  "today": {
    "tokens": 52367,
    "prompts": 14,
    "sessions": 2,
    "tokensByModel": { "claude-sonnet-5": 52367 }
  },
  "recentDays": [
    { "date": "2026-07-31", "tokens": 0 },
    { "date": "2026-08-01", "tokens": 12000 },
    { "date": "2026-08-06", "tokens": 52367 }
  ],
  "modelUsage": {
    "claude-sonnet-5": {
      "inputTokens": 400000,
      "outputTokens": 80000,
      "cacheReadInputTokens": 900000,
      "cacheCreationInputTokens": 30000
    }
  },
  "activeDays": ["2026-08-01", "2026-08-06"]
}
```

Design rules:

- Quota is account-level, not per-agent. The status command attaches
  the quota to the newest live record of that agent label. Multiple
  sessions share one quota.
- The ledger keeps the four-way token split (input, output, cache read,
  cache creation). The split survives from the Omarchy scanner design.
- `recentDays` holds the last 7 days. `activeDays` holds all-time
  active dates, for the "N days" summary.
- The ledger uses sum semantics (billing style). The context gauge uses
  latest semantics. Keep the two separate. `usagecache.go` already
  encodes this duality.
- Merge rules for sync: union active days by date. Never sum rate
  limits across devices. Rate limits are per account.

## 7. Implementation phases

### Phase 1: quota + worker skeleton (small)

- Add `internal/worker` with the loop, heartbeat, and pidfile.
- Add `tmon worker`, `tmon daemon`, `tmon worker stop` subcommands.
- Add the Claude quota probe:
  - Read `~/.claude/.credentials.json` for the OAuth access token.
  - GET `https://api.anthropic.com/api/oauth/usage`.
  - Parse the 5-hour session and 7-day weekly windows.
  - No credentials: set `statusText`, fall back to local stats only.
- Add the Codex quota probe:
  - Spawn `codex -s read-only -a untrusted app-server`.
  - JSON-RPC: `initialize`, `account/read`, `account/rateLimits/read`.
  - Timeouts: 8 s for initialize, 4 s for the reads. Terminate the
    process after the probe.
- Write `usage.json` v1 with the quota block only.
- Enrich records in the poll: attach quota by agent label.
- Add `doctor` checks: worker running, heartbeat fresh, usage.json
  valid, quota reachable.
- Update the README feature matrix (Tokens column gains quota).

### Phase 2: token ledger (medium)

- Extend the worker scan jobs:
  - Claude: walk `~/.claude/projects/**/*.jsonl`. Pre-filter lines on
    `"usage":`. Dedupe assistant messages by message id. Bucket by day
    and by model. Keep the four-way split.
  - Codex: walk `~/.codex/sessions` and `~/.codex/archived_sessions`.
    Read `token_count` events only. Use `last_token_usage`, not the
    cumulative `total_token_usage`. Subtract cached tokens from input.
    Restrict to files touched in the last 30 days.
- Persist per-transcript cursors in the worker state (extend the
  `usageEntry` pattern with day buckets). Each cycle is O(delta).
- Add a dashboard usage view: plan tier, quota meters, tokens by day,
  tokens by model, hover for the four-way split.
- Keep the per-agent stats line unchanged.

### Phase 3: cross-machine sync (optional)

- Settings: `syncMode`, `syncDir`, `syncFileName`, `syncDeviceId`.
- The worker writes `<syncDir>/<hostname>.json` and merges all
  snapshots. Union active days by date. Never merge rate limits.
- The dashboard shows a device marker on synced data.

### Phase 4: extended coverage (optional)

- Grok: check `~/.grok` for quota or usage sources.
- Hermes: `hermes insights` is already TTL-cached. Check whether it
  exposes quota or per-day usage.

## 8. Risks and edge cases

- Spawn context: the worker must detach fully. Use `setsid`, redirect
  stdin, stdout, and stderr to a log file. A child that inherits the
  tmux pipe can block the status bar or die with it.
- Spawn races: two polls can spawn two workers. `flock` on the pidfile
  makes the second spawn exit immediately.
- First poll after install: the spawn costs one fork+exec only. The
  full ledger walk happens in the worker, never in a poll.
- Network timeouts: the probes must have hard deadlines. A hung probe
  must not stall the worker loop.
- Codex session files rotate and archive. A truncated file restarts
  its cursor from zero (existing behavior). The 30-day mtime window
  bounds the archive walk.
- Claude usage blocks are full snapshots. The gauge uses the latest
  block. The ledger sums blocks. Do not mix the two semantics.
- `usageCacheVersion` must bump when parser logic changes. The worker
  cursors follow the same rule.
- Battery: the worker sleeps between cycles and exits when idle.
  `TMON_WORKER=off` disables it entirely.
- Write contention: `usage.json` and `state.json` are separate files.
  The worker never opens `state.json`.
- Tests: inject fake probes and fake transcripts into the worker loop.
  Use fixture `usage.json` files for the status enrichment and the
  dashboard view. Reuse the existing cache fixtures.

## 9. Open decisions

1. Scope: Phase 1 only, or Phase 1 + 2, or all phases?
2. Worker start mode: auto-spawn (Option C), manual daemon (Option A),
   or systemd (Option D)? The plan recommends C with A as a manual
   alias.
3. First agents: Claude and Codex only, or also Grok and Hermes quota?
4. Storage: separate `usage.json` (recommended) or extend `state.json`?
5. Status bar: show quota in the status line (for example a warning
   icon above 90 %), or only in the dashboard?
