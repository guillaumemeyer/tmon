#!/usr/bin/env bash
# tmon pane-map.sh — Map agent PIDs to tmux panes
# ==============================================================================
# Reads agent lines from stdin: PID|LABEL|CMDLINE|CWD
# Outputs annotated lines:      PID|LABEL|CMDLINE|CWD|SESSION:WINDOW.PANE|TTY
#
# Usage:
#   detect_agents | pane-map.sh
#   pane-map.sh 12345            (look up a single PID)

set -euo pipefail

# ─── Cache: build a map of tmux pane TTYs → pane addresses ────────────────────

build_pane_map() {
  # Output format: TTY|PANE_TARGET|SESSION_ID|SESSION_NAME|WINDOW_INDEX|WINDOW_NAME|PANE_INDEX|PANE_PID
  # Example:      /dev/pts/3|main:0.0|1|main|0|bash|0|12345
  if [[ -z "${TMUX:-}" ]]; then
    return 0
  fi
  tmux list-panes -a -F '#{pane_tty}|#{session_name}:#{window_index}.#{pane_index}|#{session_id}|#{session_name}|#{window_index}|#{window_name}|#{pane_index}|#{pane_pid}' 2>/dev/null
}

# ─── TTY lookup for a PID ─────────────────────────────────────────────────────

# Read /proc/PID/stat field 7 (tty_nr) and convert to /dev/pts/N
pid_to_tty() {
  local pid="$1"
  local stat_file="/proc/$pid/stat"
  if [[ ! -r "$stat_file" ]]; then
    echo "?"
    return
  fi

  # Field 7 is tty_nr: (major << 8) | minor for PTY, or (major << 20) | minor for TTY
  # PTY major is 136; Linux uses major 136 for /dev/pts/*
  local tty_nr
  tty_nr=$(awk '{print $7}' "$stat_file" 2>/dev/null || echo "0")
  local major=$(( (tty_nr >> 8) & 0xFFF ))
  local minor=$(( tty_nr & 0xFF ))

  # Also check the high bits for devpts (major 136-143 use extended minor)
  local minor_ext=$(( (tty_nr >> 12) & 0xFFFFF ))
  if [[ "$minor_ext" -gt 0 ]]; then
    minor="$minor_ext"
  fi

  if [[ "$major" -ge 136 && "$major" -le 143 ]]; then
    local pts_num=$(( ((major - 136) << 20) | minor ))
    echo "/dev/pts/$pts_num"
  elif [[ "$major" -eq 4 ]]; then
    # Legacy: /dev/ttyN
    echo "/dev/tty$minor"
  else
    echo "?"
  fi
}

# ─── Process tree walk (fallback) ─────────────────────────────────────────────

# Walk up the process tree from $pid to find the foreground process
# whose PID matches a tmux pane_pid
find_foreground_ancestor() {
  local pid="$1"
  local max_depth=10
  local depth=0

  while [[ "$depth" -lt "$max_depth" ]]; do
    local ppid
    ppid=$(awk '{print $4}' "/proc/$pid/stat" 2>/dev/null || echo "0")
    if [[ "$ppid" -le 1 ]]; then
      break
    fi
    pid="$ppid"
    depth=$((depth + 1))
    echo "$pid"
  done
}

# ─── Resolve a single PID to pane info ────────────────────────────────────────

# Output: PANE_TARGET|SESSION_ID|SESSION_NAME|WINDOW_INDEX|WINDOW_NAME|PANE_INDEX
# or "?|?|?|?|?|?" if not in a tmux pane.
# Fields: TTY|PANE_TARGET|SESSION_ID|SESSION_NAME|WINDOW_INDEX|WINDOW_NAME|PANE_INDEX|PANE_PID
resolve_pid() {
  local pid="$1"
  shift
  local pane_map="$*"

  local tty
  tty=$(pid_to_tty "$pid")

  # Method 1: Match TTY against pane TTYs
  if [[ "$tty" != "?" ]]; then
    local match
    match=$(echo "$pane_map" | grep -F "$tty" | head -1)
    if [[ -n "$match" ]]; then
      # Extract fields 2-7 (skip TTY at field 1 and PANE_PID at field 8)
      echo "$match" | cut -d'|' -f2-7
      return
    fi
  fi

  # Method 2: Walk up process tree and match against pane_pid
  local ancestors
  ancestors=$(find_foreground_ancestor "$pid")
  for anc_pid in $ancestors; do
    local match
    match=$(echo "$pane_map" | grep "|$anc_pid$" | head -1)
    if [[ -n "$match" ]]; then
      # Extract fields 2-7 (skip TTY at field 1 and PANE_PID at field 8)
      echo "$match" | cut -d'|' -f2-7
      return
    fi
  done

  echo "?|?|?|?|?|?"
}

# ─── Main ─────────────────────────────────────────────────────────────────────

main() {
  # Build the pane map once
  local pane_map
  pane_map=$(build_pane_map)

  # Single PID mode
  if [[ $# -ge 1 ]] && [[ "$1" =~ ^[0-9]+$ ]]; then
    local pane_info
    pane_info=$(resolve_pid "$1" "$pane_map")
    # Output just the pane target (field 1) for backward compat
    echo "${pane_info%%|*}"
    return
  fi

  # Stdin mode: annotate detect_agents output
  # Input:  PID|LABEL|CMDLINE|CWD
  # Output: PID|LABEL|CMDLINE|CWD|PANE_TARGET|TTY|SESSION_NAME|WINDOW_INDEX|WINDOW_NAME|PANE_INDEX|SESSION_ID
  #         (fields 1-6 unchanged from original format for backward compat)
  while IFS='|' read -r pid label cmdline cwd; do
    local pane_info tty pane_target session_id session_name window_index window_name pane_index
    pane_info=$(resolve_pid "$pid" "$pane_map")
    # pane_info format: PANE_TARGET|SESSION_ID|SESSION_NAME|WINDOW_INDEX|WINDOW_NAME|PANE_INDEX
    pane_target=$(echo "$pane_info" | cut -d'|' -f1)
    session_id=$(echo "$pane_info"  | cut -d'|' -f2 | sed 's/^\$//')
    session_name=$(echo "$pane_info" | cut -d'|' -f3)
    window_index=$(echo "$pane_info"  | cut -d'|' -f4)
    window_name=$(echo "$pane_info"  | cut -d'|' -f5)
    pane_index=$(echo "$pane_info"   | cut -d'|' -f6)
    tty=$(pid_to_tty "$pid")
    echo "${pid}|${label}|${cmdline}|${cwd}|${pane_target}|${tty}|${session_name}|${window_index}|${window_name}|${pane_index}|${session_id}"
  done
}

main "$@"
