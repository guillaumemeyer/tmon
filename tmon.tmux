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
#   @tmon-activity-threshold 500 (default) — CPU ms/s to consider "active"
#   @tmon-io-threshold       102400 (default) — min IO bytes/poll for "active"
#   @tmon-dashboard-key      "a" (default) — chord leader for the popup
#                            (prefix <key> <key>)
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

# The bootstrap subprocess reads TMON_BIN_DIR from the environment; the
# set-environment above only affects tmux's own environment.
export TMON_BIN_DIR="$BIN_DIR"

# ─── Plugin Entrypoint ────────────────────────────────────────────────────────

main() {
  # Download/verify the binary on first load or update (a no-op when the
  # installed version matches VERSION). A failure only shows a message and
  # leaves the plugin loadable; the next reload retries. The #() status path
  # below never runs bootstrap logic — `tmon status` stays instant.
  if [ -f "$BOOTSTRAP" ]; then
    "$BOOTSTRAP" || true
  fi

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

main "$@"
