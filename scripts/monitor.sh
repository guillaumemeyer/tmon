#!/usr/bin/env bash
# tmon — Tmux AI Agent Monitor
# Scans /proc for running AI coding agents and tracks their activity level.
# Outputs a compact status line for tmux's status bar.
#
# Usage: monitor.sh [--notify] [--once]
#   --notify   Also emit display-message notifications on state transitions
#   --once     Run once and exit (for #() interpolation in tmux status)

set -euo pipefail

STATE_DIR="${TMON_STATE_DIR:-$HOME/.cache/tmon}"
STATE_FILE="$STATE_DIR/agents.state"
ANIM_FRAME_FILE="$STATE_DIR/animation.frame"
POLL_INTERVAL_MS="${TMON_POLL_INTERVAL_MS:-3000}"  # milliseconds between full scans
POLL_INTERVAL_SEC=$(( POLL_INTERVAL_MS / 1000 ))
[[ "$POLL_INTERVAL_SEC" -lt 1 ]] && POLL_INTERVAL_SEC=1
ACTIVITY_THRESHOLD_MS="${TMON_ACTIVITY_THRESHOLD_MS:-500}"  # CPU ms/s to consider "active"
IO_ACTIVITY_THRESHOLD="${TMON_IO_ACTIVITY_THRESHOLD:-102400}"  # min IO bytes/poll to consider "active"
mkdir -p "$STATE_DIR"

# ─── Agent Detection Signatures ───────────────────────────────────────────────
# Format: "label:cmdline_regex:working_dir_hint"
#   label            — short display name (used in status bar)
#   cmdline_regex    — grep -E pattern matched against /proc/PID/cmdline
#   working_dir_hint — optional, if set, also requires cwd to contain this
AGENT_SIGNATURES=(
  "Grok:^grok( |$)"
  "Grok:/grok[-_]build"
  "Grok:grok[-_](build|agent|chat|run)"
  "Claude:^claude( |$)"
  "Claude:claude( |-)(code|agent|chat|run)"
  "Claude:claude-code"
  "Claude:/claude-code/"
  "Claude:node.*@anthropic.*claude"
  "Codex:^codex( |$)"
  "Codex:codex( |-)(chat|agent|run)"
  "Codex:/codex-cli/"
  "Cursor:cursor( |-)agent"
  "Cursor:/cursor[-_]agent/"
  "Cline:^cline( |$)"
  "Cline:cline( |-)(agent|chat|run)"
  "Cline:/cline"
  "Aider:^aider( |$)"
  "Aider:aider( |-)(agent|chat|run)"
  "Aider:python.*aider"
  "Copilot:copilot( |-)agent"
  "CodeBuddy:^codebuddy( |$)"
  "CodeBuddy:codebuddy( |-)(agent|chat|run)"
  "CodeBuddy:/codebuddy/"
  "Windsurf:^windsurf( |$)"
  "Windsurf:windsurf( |-)(agent|chat|run)"
  "Windsurf:/windsurf/"
  "Hermes:^hermes( |$)"
  "Hermes:/hermes( |$)"
  "Hermes:hermes (agent|chat|run)"
  "OpenClaw:^openclaw( |$)"
  "OpenClaw:openclaw (agent|chat|run)"
)

# ─── Helpers ──────────────────────────────────────────────────────────────────

# Read the agent label from a signature entry
sig_label() { echo "${1%%:*}"; }
sig_regex() { echo "${1#*:}"; }

# Join array elements with a delimiter
join_by() {
  local d="$1" out=""
  shift
  for item in "$@"; do
    out="${out}${d}${item}"
  done
  echo "${out#$d}"
}

# Build the combined regex for all signatures
build_detect_regex() {
  local patterns=()
  for sig in "${AGENT_SIGNATURES[@]}"; do
    patterns+=("$(sig_regex "$sig")")
  done
  join_by "|" "${patterns[@]}"
}

# Return true if process $1 has any child processes
has_children() {
  local pid="$1"
  local children
  children=$(awk -F' ' -v p="$pid" '$4 == p { print $1 }' /proc/*/stat 2>/dev/null | head -1)
  [[ -n "$children" ]]
}

# ─── Process Detection ────────────────────────────────────────────────────────

# Scan all running processes for AI agent matches.
# Returns lines: "PID|LABEL|CMDLINE_SNIPPET|CWD"
detect_agents() {
  local regex="${AGENT_DETECT_REGEX}"

  for pid_dir in /proc/[0-9]*; do
    local pid="${pid_dir##*/}"
    local cmdline=""
    # Read cmdline, replacing null bytes with spaces (pure bash, no tr fork)
    if [[ -r "$pid_dir/cmdline" ]]; then
      IFS= read -r -d '' cmdline < "$pid_dir/cmdline" 2>/dev/null || true
      cmdline="${cmdline//$'\0'/ }"
    fi
    [[ -n "$cmdline" ]] || continue

    # Quick match against the combined regex (pure bash [[ =~ ]], no grep fork)
    if [[ ! "$cmdline" =~ $regex ]]; then
      continue
    fi

    # Now match against individual signatures to get the label
    local matched_label=""
    for sig in "${AGENT_SIGNATURES[@]}"; do
      local label regex_p
      label=$(sig_label "$sig")
      regex_p=$(sig_regex "$sig")
      if [[ "$cmdline" =~ $regex_p ]]; then
        matched_label="$label"
        break
      fi
    done

    [[ -n "$matched_label" ]] || continue

    # Get working directory (pure bash truncation, no readlink+rev+cut forks)
    local cwd="?"
    if [[ -r "$pid_dir/cwd" ]]; then
      cwd=$(readlink "$pid_dir/cwd" 2>/dev/null || echo "?")
      # Truncate to last 2 path components: /a/b/c/d → c/d
      cwd="${cwd#${cwd%/*/*}/}"
    fi

    echo "${pid}|${matched_label}|${cmdline:0:80}|${cwd}"
  done
}

# ─── Activity Sampling ────────────────────────────────────────────────────────

# Read CPU ticks (user + system) for a PID from /proc/PID/stat
# Uses pure bash instead of awk to avoid a fork per agent.
read_cpu_ticks() {
  local pid="$1" stat_line stat_rest sf
  local stat_file="/proc/$pid/stat"
  if [[ -r "$stat_file" ]]; then
    read -r stat_line < "$stat_file" 2>/dev/null || { echo "0"; return; }
    # /proc/PID/stat format: pid (comm) state ppid ... utime(14) stime(15) cutime(16) cstime(17)
    # Strip "pid (comm) " prefix to get clean space-delimited fields
    stat_rest="${stat_line##*\) }"
    read -r -a sf <<< "$stat_rest"
    # sf[0]=state, sf[1]=ppid, ..., sf[11]=utime, sf[12]=stime, sf[13]=cutime, sf[14]=cstime
    echo $(( ${sf[11]:-0} + ${sf[12]:-0} + ${sf[13]:-0} + ${sf[14]:-0} ))
  else
    echo "0"
  fi
}

# Read IO read+write bytes for a PID from /proc/PID/io
# Uses pure bash while-read instead of awk to avoid a fork per agent.
read_io_bytes() {
  local pid="$1" io_file="/proc/$pid/io"
  if [[ -r "$io_file" ]]; then
    local key val total=0
    while IFS=':' read -r key val; do
      [[ "$key" == "rchar" || "$key" == "wchar" ]] && (( total += ${val:-0} ))
    done < "$io_file" 2>/dev/null
    echo "$total"
  else
    echo "0"
  fi
}

# ─── State Management ─────────────────────────────────────────────────────────

# Load previous state: "PID|LABEL|CPU_TICKS|IO_BYTES|STATUS|CWD|PANE"
declare -A PREV_CPU   # PREV_CPU[PID]=ticks
declare -A PREV_IO    # PREV_IO[PID]=bytes
declare -A PREV_STATUS # PREV_STATUS[PID]=running|active|idle|blocked
declare -A PREV_PANE   # PREV_PANE[PID]=session:window.pane
declare -A PREV_IDLE_STREAK # PREV_IDLE_STREAK[PID]=consecutive idle polls

# Number of consecutive idle polls before an agent decays from "running" to "idle"
IDLE_DECAY_POLLS="${TMON_IDLE_DECAY_POLLS:-3}"

load_state() {
  PREV_CPU=()
  PREV_IO=()
  PREV_STATUS=()
  PREV_PANE=()
  PREV_IDLE_STREAK=()
  if [[ -f "$STATE_FILE" ]]; then
    while IFS='|' read -r pid label cpu io status cwd pane streak; do
      PREV_CPU["$pid"]="$cpu"
      PREV_IO["$pid"]="$io"
      PREV_STATUS["$pid"]="$status"
      PREV_PANE["$pid"]="${pane:-?}"
      PREV_IDLE_STREAK["$pid"]="${streak:-0}"
    done < "$STATE_FILE"
  fi
}

save_state() {
  > "$STATE_FILE"
  for pid in "${!PREV_CPU[@]}"; do
    echo "${pid}|x|${PREV_CPU[$pid]}|${PREV_IO[$pid]}|${PREV_STATUS[$pid]}|x|${PREV_PANE[$pid]:-?}|${PREV_IDLE_STREAK[$pid]:-0}" >> "$STATE_FILE"
  done
}

# Remove entries for PIDs that are no longer detected (process died)
prune_stale_state() {
  local current_pids_str="$1"  # space-separated list of current PIDs
  for pid in "${!PREV_CPU[@]}"; do
    if [[ ! " $current_pids_str " =~ " $pid " ]]; then
      unset "PREV_CPU[$pid]"
      unset "PREV_IO[$pid]"
      unset "PREV_STATUS[$pid]"
      unset "PREV_PANE[$pid]"
      unset "PREV_IDLE_STREAK[$pid]"
    fi
  done
}

# ─── Evaluation ───────────────────────────────────────────────────────────────

# Determine the current activity level for a detected agent.
# Sets global EVAL_STATUS to: "active", "idle", "blocked", or "running"
# Also updates PREV_CPU, PREV_IO, PREV_STATUS, PREV_PANE globals.
evaluate_activity() {
  local pid="$1" label="$2" pane="${3:-?}"
  local hz="${CLK_TCK}" ticks_per_sec
  ticks_per_sec="$hz"

  local cpu_now io_now
  cpu_now=$(read_cpu_ticks "$pid")
  io_now=$(read_io_bytes "$pid")

  local cpu_prev="${PREV_CPU[$pid]:-0}"
  local io_prev="${PREV_IO[$pid]:-0}"
  local old_status="${PREV_STATUS[$pid]:-running}"

  # Update tracked values (in the caller's shell, not a subshell)
  PREV_CPU["$pid"]="$cpu_now"
  PREV_IO["$pid"]="$io_now"
  PREV_PANE["$pid"]="$pane"

  # Check for blocked state first (pane capture, takes priority)
  if [[ "$pane" != "?" ]] && detect_blocked "$pane"; then
    PREV_STATUS["$pid"]="blocked"
    PREV_IDLE_STREAK["$pid"]=0
    EVAL_STATUS="blocked"
    return
  fi

  # First time seeing this PID — always show as "running" (agent might be
  # thinking / waiting on remote API with minimal local CPU)
  if [[ "$cpu_prev" == "0" ]]; then
    PREV_STATUS["$pid"]="running"
    PREV_IDLE_STREAK["$pid"]=0
    EVAL_STATUS="running"
    return
  fi

  local cpu_delta=$((cpu_now - cpu_prev))
  local io_delta=$((io_now - io_prev))

  # Convert CPU threshold (ms/s) to ticks per poll interval
  # e.g. 500 ms/s * 3s * 100 ticks/s / 1000 ms/s = 150 ticks minimum
  local cpu_threshold_ticks=$(( ACTIVITY_THRESHOLD_MS * POLL_INTERVAL_SEC * hz / 1000 ))
  [[ "$cpu_threshold_ticks" -lt 1 ]] && cpu_threshold_ticks=1

  # Significant CPU or IO activity means the agent is doing real work.
  # Minor CPU (scheduler noise, cursor updates) and IO below threshold
  # are ignored to avoid false "active" classifications on paused agents.
  if [[ "$cpu_delta" -ge "$cpu_threshold_ticks" ]] || [[ "$io_delta" -ge "$IO_ACTIVITY_THRESHOLD" ]]; then
    PREV_STATUS["$pid"]="active"
    PREV_IDLE_STREAK["$pid"]=0
    EVAL_STATUS="active"
    return
  fi

  # No activity this poll — apply decay grace period before calling it "idle"
  local streak=$(( ${PREV_IDLE_STREAK[$pid]:-0} + 1 ))
  PREV_IDLE_STREAK["$pid"]="$streak"

  if [[ "$streak" -lt "$IDLE_DECAY_POLLS" ]]; then
    # Still within grace period: keep previous status instead of dropping to idle
    EVAL_STATUS="${PREV_STATUS[$pid]:-running}"
    return
  fi

  PREV_STATUS["$pid"]="idle"
  EVAL_STATUS="idle"
}

# ─── Blocked Detection ────────────────────────────────────────────────────────
# Scans the visible terminal output of an agent's pane for patterns indicating
# the agent is waiting for user input (permission prompts, questions, approval).

BLOCKED_PATTERNS=(
  # -- Formal selectors / clarifications --
  "❯ 1\."
  "❯ Yes"
  "❯ No"
  "❯ Approve"
  "❯ Confirm"
  "\[y/N\]"
  "\[Y/n\]"
  "\[yes/no\]"
  # -- Permission / approval prompts --
  "Do you want to proceed"
  "Do you approve"
  "Proceed anyway"
  "Continue anyway"
  "Continue?"
  "Would you like to"
  "Press any key"
  "Press Enter"
  "Press.*to continue"
  # -- Plan approval mode --
  "approval.*required"
  "requires approval"
  "waiting for approval"
  "plan.*approv"
  "\[approve\]"
  "\[confirm\]"
  "\[reject\]"
  # -- Chat questions (agent asked a question and is waiting) --
  "Waiting for input"
  "Waiting for your"
  "What would you like"
  "How would you like"
  "Can I proceed"
  "Should I"
  "Do you want me to"
)

# Precomputed combined blocked regex (built once at script startup)
BLOCKED_COMBINED_REGEX=""
build_blocked_regex() {
  local IFS='|'
  BLOCKED_COMBINED_REGEX="${BLOCKED_PATTERNS[*]}"
}
build_blocked_regex

# Precomputed agent detection regex (built once at script startup)
AGENT_DETECT_REGEX=""
AGENT_DETECT_REGEX=$(build_detect_regex)

# Cached clock tick rate (getconf CLK_TCK is constant, no need to call per agent)
CLK_TCK=$(getconf CLK_TCK 2>/dev/null || echo "100")

# Returns 0 (true) if the pane content matches any blocked-state pattern
detect_blocked() {
  local pane="$1"
  if [[ -z "${TMUX:-}" ]] || [[ "$pane" == "?" ]]; then
    return 1
  fi

  local content
  content=$(tmux capture-pane -t "$pane" -p 2>/dev/null || true)
  if [[ -z "$content" ]]; then
    return 1
  fi

  # Single grep with precomputed combined regex (31 patterns → 1 call)
  if echo "$content" | grep -qEi "$BLOCKED_COMBINED_REGEX" 2>/dev/null; then
    return 0
  fi

  return 1
}

# ─── Pane Mapping (inlined from pane-map.sh) ─────────────────────────────────

# Parse /proc/PID/stat to extract a single field by index (0-based after stripping "pid (comm) ")
# Field mapping after "pid (comm) ": 0=state, 1=ppid, 2=pgrp, 3=session, 4=tty_nr, ...
read_stat_field() {
  local pid="$1" field_idx="$2" stat_file="/proc/$pid/stat" stat_line stat_rest sf
  if [[ -r "$stat_file" ]]; then
    read -r stat_line < "$stat_file" 2>/dev/null || { echo "0"; return; }
    stat_rest="${stat_line##*\) }"
    read -r -a sf <<< "$stat_rest"
    echo "${sf[$field_idx]:-0}"
  else
    echo "0"
  fi
}

# Read /proc/PID/stat field 7 (tty_nr) and convert to /dev/pts/N
pid_to_tty() {
  local pid="$1" tty_nr major minor minor_ext
  tty_nr=$(read_stat_field "$pid" 4)  # field 4 = tty_nr (0-based after stripping pid+comm)
  major=$(( (tty_nr >> 8) & 0xFFF ))
  minor=$(( tty_nr & 0xFF ))

  # Check extended minor for devpts (major 136-143)
  local minor_ext=$(( (tty_nr >> 12) & 0xFFFFF ))
  if [[ "$minor_ext" -gt 0 ]]; then
    minor="$minor_ext"
  fi

  if [[ "$major" -ge 136 && "$major" -le 143 ]]; then
    local pts_num=$(( ((major - 136) << 20) | minor ))
    echo "/dev/pts/$pts_num"
  elif [[ "$major" -eq 4 ]]; then
    echo "/dev/tty$minor"
  else
    echo "?"
  fi
}

# Build pane maps from tmux for O(1) lookups (no grep/cut forks).
# Populates two associative arrays:
#   PANE_BY_TTY[/dev/pts/N] = "PANE_TARGET|SID|SNAME|WIDX|WNAME|PIDX|PPID"
#   PANE_BY_PID[pid]        = same value
declare -A PANE_BY_TTY
declare -A PANE_BY_PID
build_pane_map() {
  PANE_BY_TTY=()
  PANE_BY_PID=()
  if [[ -z "${TMUX:-}" ]]; then return; fi
  local line tty entry pane_pid
  while IFS='|' read -r tty entry pane_pid rest; do
    # entry = session_name:window_index.pane_index, rest = session_id|...|pane_index
    # Full value: PANE_TARGET|SID|SNAME|WIDX|WNAME|PIDX|PPID
    # input format: TTY|SNAME:WIDX.PIDX|SID|SNAME|WIDX|WNAME|PIDX|PPID
    local full="${entry}|${rest}|${pane_pid}"
    PANE_BY_TTY["$tty"]="$full"
    PANE_BY_PID["${pane_pid}"]="$full"
  done < <(tmux list-panes -a -F '#{pane_tty}|#{session_name}:#{window_index}.#{pane_index}|#{pane_pid}|#{session_id}|#{session_name}|#{window_index}|#{window_name}|#{pane_index}' 2>/dev/null)
}

# Resolve a PID to pane info: output "PANE_TARGET|SESSION_ID|SESSION_NAME|WINDOW_INDEX|WINDOW_NAME|PANE_INDEX"
# or "?|?|?|?|?|?" if not in a tmux pane. Uses pre-built associative arrays for O(1) lookup.
resolve_pane_for_pid() {
  local pid="$1" tty entry

  # Method 0: Direct PID lookup
  entry="${PANE_BY_PID[$pid]:-}"
  if [[ -n "$entry" ]]; then
    echo "$entry"
    return
  fi

  # Method 1: TTY lookup
  tty=$(pid_to_tty "$pid")
  if [[ "$tty" != "?" ]]; then
    entry="${PANE_BY_TTY[$tty]:-}"
    if [[ -n "$entry" ]]; then
      echo "$entry"
      return
    fi
  fi

  # Method 2: Walk up process tree for parent PID match
  local anc_pid ppid depth=0 max_depth=10
  anc_pid="$pid"
  while [[ "$depth" -lt "$max_depth" ]]; do
    ppid=$(read_stat_field "$anc_pid" 1)  # field 1 = ppid
    if [[ "$ppid" -le 1 ]]; then break; fi
    anc_pid="$ppid"
    depth=$((depth + 1))
    entry="${PANE_BY_PID[$anc_pid]:-}"
    if [[ -n "$entry" ]]; then
      echo "$entry"
      return
    fi
  done

  echo "?|?|?|?|?|?"
}

# Annotate detect_agents output with pane info (in-process, no subprocess)
# Input:  PID|LABEL|CMDLINE|CWD
# Output: PID|LABEL|CMDLINE|CWD|PANE_TARGET|TTY|session_name|window_index|window_name|pane_index|session_id
annotate_agents_with_panes() {
  local pid label cmdline cwd pane_info tty IFS_save="$IFS"
  while IFS='|' read -r pid label cmdline cwd; do
    pane_info=$(resolve_pane_for_pid "$pid")
    # Parse pane_info with pure bash (no cut fork): "PANE_TARGET|SID|SNAME|WIDX|WNAME|PIDX|PPID"
    local pane_target="?" session_id="?" session_name="?" window_index="?" window_name="?" pane_index="?"
    if [[ "$pane_info" != "?|?|?|?|?|?" ]]; then
      IFS='|' read -r pane_target session_id session_name window_index window_name pane_index _ <<< "$pane_info"
      session_id="${session_id#\$}"
    fi
    tty=$(pid_to_tty "$pid")
    echo "${pid}|${label}|${cmdline}|${cwd}|${pane_target}|${tty}|${session_name}|${window_index}|${window_name}|${pane_index}|${session_id}"
  done
}

# ─── Rendering ────────────────────────────────────────────────────────────────

# Tmux format strings for styling
TMUX_FG_GREEN='#[fg=green]'
TMUX_FG_ORANGE='#[fg=colour208]'
TMUX_FG_BLUE='#[fg=blue]'
TMUX_FG_CYAN='#[fg=cyan]'
TMUX_FG_DIM='#[fg=colour240]'
TMUX_RESET='#[default]'

# ─── Icon Animation ───────────────────────────────────────────────────────────

# Advance the animation frame counter and return the current frame (0-indexed).
# Wraps at 100 to avoid unbounded growth.
tick_animation_frame() {
  local frame=0
  if [[ -f "$ANIM_FRAME_FILE" ]]; then
    frame=$(cat "$ANIM_FRAME_FILE" 2>/dev/null || echo "0")
  fi
  frame=$(( (frame + 1) % 100 ))
  echo "$frame" > "$ANIM_FRAME_FILE"
  echo "$frame"
}

# Render the full status line — count-based aggregate format
render_status() {
  local idle_count=0
  local active_count=0
  local blocked_count=0
  local total=0

  # Build pane map once per poll (in-process, no subprocess)
  build_pane_map

  # Collect all currently detected agents, annotate with pane info
  local current_pids=""
  while IFS='|' read -r pid label cmdline cwd pane tty; do
    current_pids="$current_pids $pid"
    local status
    evaluate_activity "$pid" "$label" "$pane"
    status="$EVAL_STATUS"
    total=$((total + 1))

    case "$status" in
      blocked) blocked_count=$((blocked_count + 1)) ;;
      active|running) active_count=$((active_count + 1)) ;;
      idle) idle_count=$((idle_count + 1)) ;;
    esac
  done < <(detect_agents | annotate_agents_with_panes)

  # Remove stale PIDs from state (processes that died)
  prune_stale_state "$current_pids"

  # Tick animation frame for status character toggling
  local frame
  frame=$(tick_animation_frame)

  # If nothing detected, show all zeros with constant width
  if [[ "$total" -eq 0 ]]; then
    printf -v z " 0"
    echo -n "${TMUX_FG_CYAN}[@]${TMUX_RESET} "
    echo -n "${TMUX_FG_ORANGE}?${z}${TMUX_RESET}"
    echo -n " - "
    echo -n "${TMUX_FG_GREEN}●${z}${TMUX_RESET}"
    echo -n " - "
    echo -n "${TMUX_FG_BLUE}‖${z}${TMUX_RESET}"
    return
  fi

  # Render count segments: always show all three, padded to 2 chars
  printf -v b_pad "%2d" "$blocked_count"
  printf -v a_pad "%2d" "$active_count"
  printf -v i_pad "%2d" "$idle_count"

  # Animated status characters: toggle only when agents exist in that state
  local b_char a_char
  if [[ "$blocked_count" -gt 0 ]] && [[ $((frame % 2)) -ne 0 ]]; then
    b_char="!"
  else
    b_char="?"
  fi
  if [[ "$active_count" -gt 0 ]] && [[ $((frame % 2)) -ne 0 ]]; then
    a_char="!"
  else
    a_char="●"
  fi

  echo -n "${TMUX_FG_CYAN}[@]${TMUX_RESET} "
  echo -n "${TMUX_FG_ORANGE}${b_char}${b_pad}${TMUX_RESET}"
  echo -n " - "
  echo -n "${TMUX_FG_GREEN}${a_char}${a_pad}${TMUX_RESET}"
  echo -n " - "
  echo -n "${TMUX_FG_BLUE}‖${i_pad}${TMUX_RESET}"
}

# ─── Notifications ────────────────────────────────────────────────────────────

notify_state_change() {
  local label="$1" old_status="$2" new_status="$3" cwd="$4"
  local msg

  case "$new_status" in
    active)  msg="$label is now active ${cwd:+in $cwd}" ;;
    running) msg="$label started ${cwd:+in $cwd}" ;;
    *)       return ;;  # Don't notify on "idle" transitions
  esac

  # Send tmux display-message for popup notification
  if [[ -n "${TMUX:-}" ]]; then
    tmux display-message "$msg" 2>/dev/null || true
  fi
}

# ─── Main ─────────────────────────────────────────────────────────────────────

main() {
  load_state

  if [[ "${1:-}" == "--once" ]]; then
    render_status
    save_state
    return
  fi

  local do_notify=false
  if [[ "${1:-}" == "--notify" ]]; then
    do_notify=true
  fi

  # Continuous monitoring loop (for background daemon mode)
  while true; do
    render_status

    if $do_notify; then
      # Check for state transitions
      declare -A current_statuses
      build_pane_map
      while IFS='|' read -r pid label cmdline cwd pane tty; do
        local status old_status
        evaluate_activity "$pid" "$label" "$pane"
        status="$EVAL_STATUS"
        old_status="${PREV_STATUS[$pid]:-}"
        current_statuses["$pid"]="$status"

        if [[ "$status" != "$old_status" && -n "$old_status" ]]; then
          notify_state_change "$label" "$old_status" "$status" "$cwd"
        fi
      done < <(detect_agents | annotate_agents_with_panes)
    fi

    save_state
    sleep "$POLL_INTERVAL_SEC"
  done
}

main "$@"
