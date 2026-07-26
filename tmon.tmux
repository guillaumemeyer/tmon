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

set -eu

CURRENT_DIR="$(cd "$(dirname "$0")" && pwd)"
MONITOR_SCRIPT="$CURRENT_DIR/scripts/monitor.sh"

# ─── Configuration ────────────────────────────────────────────────────────────

get_tmux_option() {
  _tmo_option="$1"
  _tmo_default_value="$2"
  _tmo_value=$(tmux show-option -gqv "$_tmo_option" 2>/dev/null)
  echo "${_tmo_value:-$_tmo_default_value}"
}

STATUS_POSITION=$(get_tmux_option "@tmon-status-position" "right")
POLL_INTERVAL=$(get_tmux_option "@tmon-poll-interval" "3000")
ACTIVITY_THRESHOLD=$(get_tmux_option "@tmon-activity-threshold" "500")
IO_THRESHOLD=$(get_tmux_option "@tmon-io-threshold" "102400")
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
  _tmon_widget="#[range=user|tmon]#(bash '$MONITOR_SCRIPT' --once 2>/dev/null)#[norange]"

  # Set the status bar interpolation (skip if already present — guards against double-load)
  if [ "$STATUS_POSITION" = "left" ]; then
    _tmon_existing=$(tmux show-option -gqv status-left 2>/dev/null || echo "")
    case "$_tmon_existing" in
      *"$MONITOR_SCRIPT"*) ;;  # already present, skip
      *)
        if [ -n "$_tmon_existing" ]; then
          tmux set -g status-left "$_tmon_widget $_tmon_existing"
        else
          tmux set -g status-left "$_tmon_widget"
        fi
        ;;
    esac
  else
    # Append to status-right
    _tmon_existing=$(tmux show-option -gqv status-right 2>/dev/null || echo "")
    case "$_tmon_existing" in
      *"$MONITOR_SCRIPT"*) ;;  # already present, skip
      *)
        if [ -n "$_tmon_existing" ]; then
          tmux set -g status-right "$_tmon_existing $_tmon_widget"
        else
          tmux set -g status-right "$_tmon_widget"
        fi
        ;;
    esac
  fi

  # Setup chord table for agent navigation popup
  tmux bind-key "$DASHBOARD_KEY" switch-client -T a-table
  tmux bind-key -T a-table a display-popup -w 80% -h 80% -E "bash '$CURRENT_DIR/scripts/dashboard.sh'"

  # Mouse click on the status bar agent indicator opens the dashboard
  tmux bind-key -T root MouseDown1Status \
    if -F "#{==:#{mouse_status_range},tmon}" \
    "display-popup -w 80% -h 80% -E 'bash $CURRENT_DIR/scripts/dashboard.sh'" \
    "select-window -t ="
}

main "$@"
