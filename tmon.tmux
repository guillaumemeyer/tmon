#!/usr/bin/env bash
# tmon.tmux — Tmux AI Agent Monitor Plugin (Go rewrite)
# ==============================================================================
# Installation (with TPM):
#   set -g @plugin 'guillaumemeyer/tmon'
#
# Manual installation:
#   run-shell ~/.tmux/plugins/tmon/tmon.tmux
#
# Configuration options (set in tmux.conf):
#   @tmon-status-position    "right" (default) or "left" — which side of the
#                            status bar carries the agent indicator
#   @tmon-poll-interval      3000 (default) — ms between agent scans
#   @tmon-activity-threshold 500 (default) — CPU ms/s to consider "working"
#   @tmon-io-threshold       102400 (default) — min IO bytes/poll for "working"
#   @tmon-dashboard-key      "a" (default) — chord leader for the popup
#                            (prefix <key> <key>)
#   @tmon-connectors         "auto" (default) — comma list of connectors, or
#                            "auto" to enable every connector whose agent
#                            state files exist on this machine
#   @tmon-connector-freshness 30 (default) — seconds a connector's status
#                            signal stays authoritative before the /proc
#                            heuristic takes over again
#   @tmon-ascii-icons       "0" (default) — render the status icons as
#                            emoji (🤖 🛑 ⚡️ 💤); "1" switches to ASCII
#                            ([@] B W I)
#   @tmon-bold-counts       "1" (default) — render the per-status counts
#                            (the 2 in 🛑2) in bold; "0" turns it off
#   @tmon-auto-hooks        "on" (default) — auto-install lifecycle hooks at
#                            plugin load for every supported agent found on
#                            this machine (set "off" to disable)
#
# Everything else is internal and self-contained: the binary is downloaded on
# first load into <plugin>/bin (scripts/bootstrap.sh, pinned + checksummed to
# the VERSION file) and all state lives in <plugin>/state. Nothing is written
# to ~/.cache or /tmp.
# ==============================================================================

set -eu

CURRENT_DIR="$(cd "$(dirname "$0")" && pwd)"
BIN_DIR="$CURRENT_DIR/bin"
STATE_DIR="$CURRENT_DIR/state"
BOOTSTRAP="$CURRENT_DIR/scripts/bootstrap.sh"
BINARY="$BIN_DIR/tmon"

# ─── Configuration ────────────────────────────────────────────────────────────

get_tmux_option() {
  local option="$1" default_value="$2" value
  value=$(tmux show-option -gqv "$option" 2>/dev/null || true)
  echo "${value:-$default_value}"
}

STATUS_POSITION=$(get_tmux_option "@tmon-status-position" "right")
POLL_INTERVAL=$(get_tmux_option "@tmon-poll-interval" "3000")
ACTIVITY_THRESHOLD=$(get_tmux_option "@tmon-activity-threshold" "500")
IO_THRESHOLD=$(get_tmux_option "@tmon-io-threshold" "102400")
DASHBOARD_KEY=$(get_tmux_option "@tmon-dashboard-key" "a")
CONNECTORS=$(get_tmux_option "@tmon-connectors" "auto")
CONNECTOR_FRESHNESS=$(get_tmux_option "@tmon-connector-freshness" "30")
ASCII_ICONS=$(get_tmux_option "@tmon-ascii-icons" "0")
BOLD_COUNTS=$(get_tmux_option "@tmon-bold-counts" "1")
AUTO_HOOKS=$(get_tmux_option "@tmon-auto-hooks" "on")

# ─── Runtime environment ──────────────────────────────────────────────────────

# Push the configuration into tmux's global environment so the #() status
# command and the display-popup actually see it. (The bash plugin only
# exported these in the sourcing shell, which the status bar never inherits —
# so its @tmon-* options were effectively dead for the #() path.)
tmux set-environment -g TMON_STATE_DIR "$STATE_DIR"
tmux set-environment -g TMON_BIN_DIR "$BIN_DIR"
tmux set-environment -g TMON_POLL_INTERVAL_MS "$POLL_INTERVAL"
tmux set-environment -g TMON_ACTIVITY_THRESHOLD_MS "$ACTIVITY_THRESHOLD"
tmux set-environment -g TMON_IO_ACTIVITY_THRESHOLD "$IO_THRESHOLD"
tmux set-environment -g TMON_CONNECTORS "$CONNECTORS"
tmux set-environment -g TMON_CONNECTOR_FRESHNESS "$CONNECTOR_FRESHNESS"
tmux set-environment -g TMON_ASCII_ICONS "$ASCII_ICONS"
tmux set-environment -g TMON_BOLD_COUNTS "$BOLD_COUNTS"
tmux set-environment -g TMON_HOOK_STATE_DIR "$STATE_DIR/hooks"

# Subprocesses spawned from this sourcing shell (bootstrap, hooks auto) read
# the TMON_* variables from the environment; set-environment above only
# affects tmux's own environment.
export TMON_BIN_DIR="$BIN_DIR"
export TMON_STATE_DIR="$STATE_DIR"
export TMON_HOOK_STATE_DIR="$STATE_DIR/hooks"

# ─── Plugin Entrypoint ────────────────────────────────────────────────────────

main() {
  # Download/verify the binary on first load or update (a no-op when the
  # installed version matches VERSION). A failure only shows a message and
  # leaves the plugin loadable; the next reload retries. The #() status path
  # below never runs bootstrap logic — `tmon status` stays instant.
  if [ -f "$BOOTSTRAP" ]; then
    "$BOOTSTRAP" || true
  fi

  # Auto-install lifecycle hooks for supported agents found on this machine
  # (opt out with @tmon-auto-hooks off). Idempotent: a no-op once configured,
  # so reloads stay silent.
  if [ "$AUTO_HOOKS" = "on" ] && [ -x "$BINARY" ]; then
    "$BINARY" hooks auto
  fi

  # Make `git pull` a complete update. TPM's `prefix U` runs exactly `git pull`
  # in the plugin dir and then does nothing else — so without help the new
  # VERSION file lands on disk but bootstrap never runs and the binary stays
  # stale. Installing a post-merge hook makes the pull itself re-run bootstrap
  # (downloading the binary that matches the new VERSION) and re-source this
  # file, so `prefix U` alone updates the app. Idempotent; skipped when the
  # plugin is not a git clone or the hook dir is not writable.
  install_post_merge_hook

  # Status-bar widget. The binary path is inlined because #() commands run
  # with tmux's environment, not with this shell's variables.
  widget="#[range=user|tmon]#($BINARY status 2>/dev/null)#[norange]"

  # Wire the widget into the requested status position, skipping if this
  # plugin's binary path is already present (idempotent reloads).
  if [ "$STATUS_POSITION" = "left" ]; then
    existing=$(tmux show-option -gqv status-left 2>/dev/null || echo "")
    case "$existing" in
      *"$BINARY"*) ;;
      *)
        existing="${existing%"${existing##*[![:space:]]}"}" # drop trailing spaces
        if [ -n "$existing" ]; then
          tmux set -g status-left "$widget $existing"
        else
          tmux set -g status-left "$widget"
        fi
        ;;
    esac
  else
    existing=$(tmux show-option -gqv status-right 2>/dev/null || echo "")
    case "$existing" in
      *"$BINARY"*) ;;
      *)
        existing="${existing%"${existing##*[![:space:]]}"}" # drop trailing spaces
        if [ -n "$existing" ]; then
          tmux set -g status-right "$existing $widget"
        else
          tmux set -g status-right "$widget"
        fi
        ;;
    esac
  fi

  # Chord table for the agent navigation popup: prefix <key> <key>.
  tmux bind-key "$DASHBOARD_KEY" switch-client -T a-table
  tmux bind-key -T a-table "$DASHBOARD_KEY" \
    display-popup -w 80% -h 80% -E "$BINARY dashboard"

  # Mouse click on the status-bar indicator opens the popup too.
  tmux bind-key -T root MouseDown1Status \
    if -F "#{==:#{mouse_status_range},tmon}" \
    "display-popup -w 80% -h 80% -E '$BINARY dashboard'" \
    "select-window -t ="
}

# ─── Self-update hook ─────────────────────────────────────────────────────────

# install_post_merge_hook writes <plugin>/.git/hooks/post-merge. git runs it
# after every successful `git pull`/merge in the plugin repo, which is what
# TPM's `prefix U` does. The hook re-runs bootstrap (a no-op unless VERSION
# moved, in which case the matching release binary is downloaded) and then
# re-sources tmon.tmux into the running tmux server so the new binary and
# keybindings take effect immediately.
install_post_merge_hook() {
  [ -d "$CURRENT_DIR/.git" ] || return 0
  HOOK_DIR="$CURRENT_DIR/.git/hooks"
  mkdir -p "$HOOK_DIR" 2>/dev/null || return 0
  HOOK="$HOOK_DIR/post-merge"
  cat > "$HOOK" <<EOF || return 0
#!/bin/sh
# Generated by tmon.tmux — do not edit. Runs after 'git pull' so the tmon
# binary and tmux wiring stay in sync with the repo.
PLUGIN_DIR='$CURRENT_DIR'
"\$PLUGIN_DIR/scripts/bootstrap.sh" || true
if command -v tmux >/dev/null 2>&1 && tmux ls >/dev/null 2>&1; then
  tmux source-file "\$PLUGIN_DIR/tmon.tmux" >/dev/null 2>&1 || true
fi
EOF
  chmod +x "$HOOK" 2>/dev/null || true
}

main "$@"
