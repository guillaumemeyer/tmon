#!/usr/bin/env bash
# tmon.tmux — Tmux AI Agent Monitor Plugin
# ==============================================================================
# Installation (with TPM):
#   set -g @plugin 'guillaumemeyer/tmon'
#
# Manual installation:
#   run-shell ~/.tmux/plugins/tmon/tmon.tmux
#
# Configuration options (set in tmux.conf):
#   @tmon-status-position    "right" (default) or "left" — which side of status bar
#   @tmon-poll-interval      3000 (default) — milliseconds between agent scans
#   @tmon-activity-threshold 500 (default) — CPU ms/s to consider "active"
#   @tmon-io-threshold       1024 (default) — min IO bytes/poll to consider "active"
#   @tmon-dashboard-key      "a" (default) — chord leader for popup (prefix a a)
# ==============================================================================

set -euo pipefail

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MONITOR_SCRIPT="$CURRENT_DIR/scripts/monitor.sh"

# ─── Configuration ────────────────────────────────────────────────────────────

get_tmux_option() {
  local option="$1"
  local default_value="$2"
  local value
  value=$(tmux show-option -gqv "$option" 2>/dev/null)
  echo "${value:-$default_value}"
}

STATUS_POSITION=$(get_tmux_option "@tmon-status-position" "right")
POLL_INTERVAL=$(get_tmux_option "@tmon-poll-interval" "3000")
ACTIVITY_THRESHOLD=$(get_tmux_option "@tmon-activity-threshold" "500")
IO_THRESHOLD=$(get_tmux_option "@tmon-io-threshold" "1024")
DASHBOARD_KEY=$(get_tmux_option "@tmon-dashboard-key" "a")

export TMON_POLL_INTERVAL_MS="$POLL_INTERVAL"
export TMON_ACTIVITY_THRESHOLD_MS="$ACTIVITY_THRESHOLD"
export TMON_IO_ACTIVITY_THRESHOLD="$IO_THRESHOLD"
export TMON_STATE_DIR="${HOME}/.cache/tmon"

# ─── Plugin Entrypoint ────────────────────────────────────────────────────────

main() {

  # Ensure scripts are executable
  chmod +x "$MONITOR_SCRIPT" 2>/dev/null || true
  chmod +x "$CURRENT_DIR/scripts/notify.sh" 2>/dev/null || true

  # Build the monitor interpolation string, wrapped in a clickable range
  local monitor_widget
  monitor_widget="#[range=user|tmon]#(bash '$MONITOR_SCRIPT' --once 2>/dev/null)#[norange]"

  # Set the status bar interpolation
  if [[ "$STATUS_POSITION" == "left" ]]; then
    local existing_left
    existing_left=$(tmux show-option -gqv status-left 2>/dev/null || echo "")
    if [[ -n "$existing_left" ]]; then
      tmux set -g status-left "$monitor_widget $existing_left"
    else
      tmux set -g status-left "$monitor_widget"
    fi
  else
    # Append to status-right
    local existing_right
    existing_right=$(tmux show-option -gqv status-right 2>/dev/null || echo "")
    if [[ -n "$existing_right" ]]; then
      tmux set -g status-right "$existing_right $monitor_widget"
    else
      tmux set -g status-right "$monitor_widget"
    fi
  fi

  # Setup chord table for agent navigation popup
  tmux bind-key "$DASHBOARD_KEY" switch-client -T a-table
  tmux bind-key -T a-table a display-popup -w 80% -h 80% -E "bash '$CURRENT_DIR/scripts/dashboard.sh'"

  # Mouse click on the status bar agent indicator opens the dashboard
  tmux bind-key -T root MouseDown1Status \
    "if -F '#{==:#{mouse_status_range},tmon}' 'display-popup -w 80% -h 80% -E \"bash \\'$CURRENT_DIR/scripts/dashboard.sh\\'\"' ''"
}

main "$@"
