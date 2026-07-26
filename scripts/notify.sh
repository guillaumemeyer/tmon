#!/usr/bin/env bash
# tmon — Notification handler
# Reads from stdin (or piped from monitor.sh) and dispatches tmux notifications.
# Typically called by the monitor daemon when --notify is active.
#
# Usage: notify.sh <message>
#        or piped: some_command | notify.sh

set -euo pipefail

# If an argument is provided, display it directly
if [[ $# -gt 0 ]]; then
  msg="$*"
  if [[ -n "${TMUX:-}" ]]; then
    tmux display-message "$msg" 2>/dev/null || true
  fi
  echo "[tmon] $msg"
  exit 0
fi

# Otherwise, read lines from stdin
while IFS= read -r line; do
  if [[ -n "${TMUX:-}" ]]; then
    tmux display-message "$line" 2>/dev/null || true
  fi
  echo "[tmon] $line"
done
