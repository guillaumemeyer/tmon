#!/usr/bin/env bash
# tmon dashboard.sh — Interactive agent navigation popup
# ==============================================================================
# Launched via tmux display-popup (prefix a a). Shows a grouped list of
# running AI coding agents organized by session → window, with pane index
# shown on each agent line.
#
# Keybindings:
#   /           — enter search/filter mode
#   In search mode:
#     type      — filter the list in real time (matches agent name, session, window)
#     Backspace — remove last filter character
#     Esc       — exit search mode (filter stays applied)
#   In navigation mode:
#     j/k/↑/↓   — move selection between agent lines
#     ↩/Space/l — focus selected agent's pane (switches to its session)
#     Esc/q     — close popup
#   (auto-refreshes every 1.5s, full reload every 6s)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MONITOR_SCRIPT="$SCRIPT_DIR/monitor.sh"
PANE_MAPPER="$SCRIPT_DIR/pane-map.sh"
STATE_FILE="${HOME}/.cache/tmon/agents.state"
ANIM_FRAME_FILE="${HOME}/.cache/tmon/animation.frame"
REFRESH_INTERVAL=1.5  # seconds between auto-refresh ticks
FULL_REFRESH_TICKS=4  # full data refresh every N ticks (~6s at 1.5s interval)

# ─── ANSI escape codes ────────────────────────────────────────────────────────

ESC=$'\033'
CSI="${ESC}["
CLEAR="${CSI}2J"
HOME="${CSI}H"
HIDE_CURSOR="${CSI}?25l"
SHOW_CURSOR="${CSI}?25h"
BOLD="${CSI}1m"
RESET="${CSI}0m"
FG_DIM="${CSI}38;5;240m"
FG_GREEN="${CSI}38;5;2m"
FG_ORANGE="${CSI}38;5;208m"
FG_BLUE="${CSI}38;5;4m"
FG_CYAN="${CSI}38;5;6m"
FG_WHITE="${CSI}38;5;15m"
BG_HL="${CSI}48;5;236m"
BG_DEFAULT="${CSI}49m"

# ─── Data Structures ──────────────────────────────────────────────────────────

declare -a AGENT_PIDS
declare -a AGENT_LABELS
declare -a AGENT_STATUSES
declare -a AGENT_CMDS
declare -a AGENT_CWDS
declare -a AGENT_PANES
declare -a AGENT_TTYS
declare -a AGENT_CPUS
declare -a AGENT_IOS
declare -a AGENT_SESSION_NAMES
declare -a AGENT_WINDOW_INDEXES
declare -a AGENT_WINDOW_NAMES
declare -a AGENT_PANE_INDEXES
declare -a AGENT_SESSION_IDS

agent_count=0
selected=0

# ─── Display items (grouped by session → window) ──────────────────────────────
# DISPLAY_ITEMS entries: "S|session_name|session_id" or "W|win_idx|win_name" or "A|agent_idx"
declare -a DISPLAY_ITEMS
declare -a SELECTABLE_MAP       # SELECTABLE_MAP[selection_index] = display_item_index
display_count=0
selectable_count=0

# ─── Search state ─────────────────────────────────────────────────────────────

filter_query=""
declare -a FILTERED_INDICES
filtered_count=0

# ─── Agent Detection (inlined to avoid sourcing monitor.sh) ─────────────────

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

sig_label() { echo "${1%%:*}"; }
sig_regex() { echo "${1#*:}"; }

join_by() {
  local d="$1" out=""
  shift
  for item in "$@"; do out="${out}${d}${item}"; done
  echo "${out#$d}"
}

build_detect_regex() {
  local patterns=()
  for sig in "${AGENT_SIGNATURES[@]}"; do
    patterns+=("$(sig_regex "$sig")")
  done
  join_by "|" "${patterns[@]}"
}

# Precomputed agent detection regex (built once at script startup)
AGENT_DETECT_REGEX=""
AGENT_DETECT_REGEX=$(build_detect_regex)

detect_agents() {
  local regex="${AGENT_DETECT_REGEX}"

  for pid_dir in /proc/[0-9]*; do
    local pid="${pid_dir##*/}"
    local cmdline=""
    if [[ -r "$pid_dir/cmdline" ]]; then
      IFS= read -r -d '' cmdline < "$pid_dir/cmdline" 2>/dev/null || true
      cmdline="${cmdline//$'\0'/ }"
    fi
    [[ -n "$cmdline" ]] || continue

    if [[ ! "$cmdline" =~ $regex ]]; then
      continue
    fi

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

    local cwd="?"
    if [[ -r "$pid_dir/cwd" ]]; then
      cwd=$(readlink "$pid_dir/cwd" 2>/dev/null || echo "?")
      cwd="${cwd#${cwd%/*/*}/}"
    fi

    echo "${pid}|${matched_label}|${cmdline:0:80}|${cwd}"
  done
}

# ─── Pane Mapping (inlined from pane-map.sh) ─────────────────────────────────

# Parse /proc/PID/stat to extract a single field by index (0-based after stripping "pid (comm) ")
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

pid_to_tty() {
  local pid="$1" tty_nr major minor minor_ext
  tty_nr=$(read_stat_field "$pid" 4)
  major=$(( (tty_nr >> 8) & 0xFFF ))
  minor=$(( tty_nr & 0xFF ))
  local minor_ext=$(( (tty_nr >> 12) & 0xFFFFF ))
  if [[ "$minor_ext" -gt 0 ]]; then minor="$minor_ext"; fi
  if [[ "$major" -ge 136 && "$major" -le 143 ]]; then
    local pts_num=$(( ((major - 136) << 20) | minor ))
    echo "/dev/pts/$pts_num"
  elif [[ "$major" -eq 4 ]]; then
    echo "/dev/tty$minor"
  else
    echo "?"
  fi
}

PANE_MAP_CACHE=""  # no longer used
declare -A PANE_BY_TTY
declare -A PANE_BY_PID
build_pane_map() {
  PANE_BY_TTY=()
  PANE_BY_PID=()
  if [[ -z "${TMUX:-}" ]]; then return; fi
  local tty entry pane_pid
  while IFS='|' read -r tty entry pane_pid rest; do
    local full="${entry}|${rest}|${pane_pid}"
    PANE_BY_TTY["$tty"]="$full"
    PANE_BY_PID["${pane_pid}"]="$full"
  done < <(tmux list-panes -a -F '#{pane_tty}|#{session_name}:#{window_index}.#{pane_index}|#{pane_pid}|#{session_id}|#{session_name}|#{window_index}|#{window_name}|#{pane_index}' 2>/dev/null)
}

find_foreground_ancestors() {
  local pid="$1" max_depth=10 depth=0 ppid
  while [[ "$depth" -lt "$max_depth" ]]; do
    ppid=$(read_stat_field "$pid" 1)
    if [[ "$ppid" -le 1 ]]; then break; fi
    pid="$ppid"; depth=$((depth + 1))
    echo "$pid"
  done
}

resolve_pane_for_pid() {
  local pid="$1" tty entry
  entry="${PANE_BY_PID[$pid]:-}"
  if [[ -n "$entry" ]]; then echo "$entry"; return; fi
  tty=$(pid_to_tty "$pid")
  if [[ "$tty" != "?" ]]; then
    entry="${PANE_BY_TTY[$tty]:-}"
    if [[ -n "$entry" ]]; then echo "$entry"; return; fi
  fi
  local anc_pid depth=0
  anc_pid="$pid"
  while [[ "$depth" -lt 10 ]]; do
    ppid=$(read_stat_field "$anc_pid" 1)
    if [[ "$ppid" -le 1 ]]; then break; fi
    anc_pid="$ppid"; depth=$((depth + 1))
    entry="${PANE_BY_PID[$anc_pid]:-}"
    if [[ -n "$entry" ]]; then echo "$entry"; return; fi
  done
  echo "?|?|?|?|?|?"
}

annotate_agents_with_panes() {
  local pid label cmdline cwd pane_info tty
  while IFS='|' read -r pid label cmdline cwd; do
    pane_info=$(resolve_pane_for_pid "$pid")
    local pane_target="?" session_id="?" session_name="?" window_index="?" window_name="?" pane_index="?"
    if [[ "$pane_info" != "?|?|?|?|?|?" ]]; then
      IFS='|' read -r pane_target session_id session_name window_index window_name pane_index _ <<< "$pane_info"
      session_id="${session_id#\$}"
    fi
    tty=$(pid_to_tty "$pid")
    echo "${pid}|${label}|${cmdline}|${cwd}|${pane_target}|${tty}|${session_name}|${window_index}|${window_name}|${pane_index}|${session_id}"
  done
}

# ─── State file lookup (accurate status from monitor.sh) ──────────────────────

# Load status from the monitor's state file so the dashboard shows the same
# activity level as the status bar (with proper delta-based decay tracking).
load_state_statuses() {
  declare -gA STATE_STATUS
  STATE_STATUS=()
  if [[ -f "$STATE_FILE" ]]; then
    while IFS='|' read -r spid _ _ _ sstatus _ _ _; do
      STATE_STATUS["$spid"]="$sstatus"
    done < "$STATE_FILE"
  fi
}

# ─── Refresh ──────────────────────────────────────────────────────────────────

refresh_data() {
  AGENT_PIDS=()
  AGENT_LABELS=()
  AGENT_STATUSES=()
  AGENT_CMDS=()
  AGENT_CWDS=()
  AGENT_PANES=()
  AGENT_TTYS=()
  AGENT_CPUS=()
  AGENT_IOS=()
  AGENT_SESSION_NAMES=()
  AGENT_WINDOW_INDEXES=()
  AGENT_WINDOW_NAMES=()
  AGENT_PANE_INDEXES=()
  AGENT_SESSION_IDS=()
  agent_count=0

  load_state_statuses

  # Build pane map once per refresh (in-process, no subprocess)
  build_pane_map

  local idx=0
  while IFS='|' read -r pid label cmdline cwd pane tty session_name window_index window_name pane_index session_id; do
    local cpu io stat_line stat_rest sf
    read -r stat_line < "/proc/$pid/stat" 2>/dev/null || stat_line=""
    if [[ -n "$stat_line" ]]; then
      stat_rest="${stat_line##*\) }"
      read -r -a sf <<< "$stat_rest"
      cpu=$(( ${sf[11]:-0} + ${sf[12]:-0} + ${sf[13]:-0} + ${sf[14]:-0} ))
    else
      cpu=0
    fi
    io=0
    if [[ -r "/proc/$pid/io" ]]; then
      local key val
      while IFS=':' read -r key val; do
        [[ "$key" == "rchar" || "$key" == "wchar" ]] && (( io += ${val:-0} ))
      done < "/proc/$pid/io" 2>/dev/null
    fi

    # Use the monitor's state file status if available (accurate delta-based),
    # otherwise default to "running" for first detection.
    local status="${STATE_STATUS[$pid]:-running}"

    # Check for blocked state via pane capture (overrides state file status)
    if [[ "$pane" != "?" ]] && [[ -n "${TMUX:-}" ]]; then
      if tmux capture-pane -t "$pane" -p 2>/dev/null | grep -qiE '(❯ 1\.|❯ Yes|Do you want to proceed|\[y/N\]|Press any key|approval.*required|Waiting for|Should I|plan.*approv)' 2>/dev/null; then
        status="blocked"
      fi
    fi

    AGENT_PIDS+=("$pid")
    AGENT_LABELS+=("$label")
    AGENT_STATUSES+=("$status")
    AGENT_CMDS+=("$cmdline")
    AGENT_CWDS+=("$cwd")
    AGENT_PANES+=("$pane")
    AGENT_TTYS+=("$tty")
    AGENT_CPUS+=("$cpu")
    AGENT_IOS+=("$io")
    AGENT_SESSION_NAMES+=("${session_name:-?}")
    AGENT_WINDOW_INDEXES+=("${window_index:-?}")
    AGENT_WINDOW_NAMES+=("${window_name:-?}")
    AGENT_PANE_INDEXES+=("${pane_index:-?}")
    AGENT_SESSION_IDS+=("${session_id:-?}")
    idx=$((idx + 1))
  done < <(detect_agents | annotate_agents_with_panes | sort -t'|' -k11,11n -k8,8n -k10,10n)

  agent_count=$idx
  rebuild_filter
}

# ─── Search ───────────────────────────────────────────────────────────────────

rebuild_filter() {
  FILTERED_INDICES=()
  if [[ -z "$filter_query" ]]; then
    # No filter active: all agents
    for ((i = 0; i < agent_count; i++)); do
      FILTERED_INDICES+=("$i")
    done
  else
    local q="${filter_query,,}"
    for ((i = 0; i < agent_count; i++)); do
      local haystack
      haystack="$(agent_full_name "${AGENT_LABELS[$i]}")"
      haystack+=" ${AGENT_SESSION_NAMES[$i]}"
      haystack+=" ${AGENT_WINDOW_NAMES[$i]}"
      haystack="${haystack,,}"
      if [[ "$haystack" == *"$q"* ]]; then
        FILTERED_INDICES+=("$i")
      fi
    done
  fi
  filtered_count=${#FILTERED_INDICES[@]}

  build_display_items
}

# ─── Display item grouping ────────────────────────────────────────────────────

# Build DISPLAY_ITEMS and SELECTABLE_MAP from FILTERED_INDICES.
# Groups agents by session (non-selectable header), then window (non-selectable
# sub-header). Only agent lines are selectable.
build_display_items() {
  DISPLAY_ITEMS=()
  SELECTABLE_MAP=()
  display_count=0
  selectable_count=0

  local last_sid="__none__"
  local last_widx="__none__"

  for ((fi = 0; fi < filtered_count; fi++)); do
    local i="${FILTERED_INDICES[$fi]}"
    local sid="${AGENT_SESSION_IDS[$i]}"
    local sname="${AGENT_SESSION_NAMES[$i]}"
    local widx="${AGENT_WINDOW_INDEXES[$i]}"
    local wname="${AGENT_WINDOW_NAMES[$i]}"

    # New session → emit session header
    if [[ "$sid" != "$last_sid" ]]; then
      DISPLAY_ITEMS+=("S|${sname}|${sid}")
      display_count=$((display_count + 1))
      last_sid="$sid"
      last_widx="__none__"
    fi

    # New window → emit window sub-header
    if [[ "$widx" != "$last_widx" ]]; then
      DISPLAY_ITEMS+=("W|${widx}|${wname}")
      display_count=$((display_count + 1))
      last_widx="$widx"
    fi

    # Emit agent line (selectable)
    DISPLAY_ITEMS+=("A|${i}")
    SELECTABLE_MAP+=("$display_count")
    selectable_count=$((selectable_count + 1))
    display_count=$((display_count + 1))
  done

  # Clamp selection to valid range
  if [[ "$selectable_count" -eq 0 ]]; then
    selected=0
  elif [[ "$selected" -ge "$selectable_count" ]]; then
    selected=$((selectable_count - 1))
  fi
}

# Return true (0) if the display item at index $1 is the currently selected one.
is_selected() {
  local di="$1"
  if [[ "$selectable_count" -gt 0 ]]; then
    local sel_di="${SELECTABLE_MAP[$selected]}"
    [[ "$sel_di" -eq "$di" ]]
  else
    return 1
  fi
}

# ─── Agent names ──────────────────────────────────────────────────────────────

agent_full_name() {
  case "$1" in
    Grok)      echo "Grok Build" ;;
    Claude)    echo "Claude Code" ;;
    Codex)     echo "Codex CLI" ;;
    Cursor)    echo "Cursor" ;;
    Cline)     echo "Cline" ;;
    Aider)     echo "Aider" ;;
    Copilot)   echo "Copilot" ;;
    CodeBuddy) echo "CodeBuddy" ;;
    Windsurf)  echo "Windsurf" ;;
    Hermes)    echo "Hermes Agent" ;;
    OpenClaw)  echo "OpenClaw" ;;
    *)         echo "$1" ;;
  esac
}

agent_icon() {
  case "$1" in
    Grok)      echo "🧠" ;;   # deep understanding ("grok")
    Claude)    echo "🏛️" ;;   # classical / Anthropic aesthetic
    Codex)     echo "📖" ;;   # codex = ancient manuscript / book
    Cursor)    echo "🖱️" ;;   # the editor is named after the cursor
    Cline)     echo "🔧" ;;   # a tool / VS Code extension
    Aider)     echo "🤝" ;;   # "aider" = "to help" in French
    Copilot)   echo "👨‍✈️" ;;  # pilot / copilot
    CodeBuddy) echo "🧑‍💻" ;;  # coding buddy / developer
    Windsurf)  echo "🏄" ;;   # windsurfing
    Hermes)    echo "🪶" ;;   # Hermes' winged sandals / messenger
    OpenClaw)  echo "🦞" ;;   # claw
    *)         echo "[@]" ;;   # generic AI fallback
  esac
}

# ─── Animation ────────────────────────────────────────────────────────────────

# Read the animation frame counter (written by monitor.sh each poll).
# Returns 0 if the file doesn't exist yet.
read_animation_frame() {
  local frame=0
  if [[ -f "$ANIM_FRAME_FILE" ]]; then
    frame=$(cat "$ANIM_FRAME_FILE" 2>/dev/null || echo "0")
  fi
  echo "$frame"
}

# Return a colored, animated status character for an individual agent.
# Toggles on alternating frames (matches monitor.sh behavior):
#   blocked: ? ↔ ! (orange)
#   active:  ● ↔ ! (green)
#   idle:    ‖     (blue, static)
animated_status_char() {
  local status="$1" frame="$2"

  case "$status" in
    blocked)
      if [[ $((frame % 2)) -eq 0 ]]; then
        echo "${FG_ORANGE}?${RESET}"
      else
        echo "${FG_ORANGE}!${RESET}"
      fi
      ;;
    active|running)
      if [[ $((frame % 2)) -eq 0 ]]; then
        echo "${FG_GREEN}●${RESET}"
      else
        echo "${FG_GREEN}!${RESET}"
      fi
      ;;
    idle)
      echo "${FG_BLUE}‖${RESET}"
      ;;
    *)
      echo "${FG_DIM}·${RESET}"
      ;;
  esac
}

# ─── Status display ───────────────────────────────────────────────────────────

status_dot() {
  case "$1" in
    active)  echo "${FG_GREEN}●${RESET}" ;;
    running) echo "${FG_GREEN}●${RESET}" ;;
    blocked) echo "${FG_ORANGE}?${RESET}" ;;
    idle)    echo "${FG_BLUE}‖${RESET}" ;;
    *)       echo "${FG_DIM}·${RESET}" ;;
  esac
}

status_label() {
  case "$1" in
    active)  echo "active" ;;
    running) echo "running" ;;
    blocked) echo "blocked" ;;
    idle)    echo "idle" ;;
    *)       echo "unknown" ;;
  esac
}

# ─── Rendering ────────────────────────────────────────────────────────────────

render() {
  local terms_cols terms_lines
  terms_cols=$(tput cols 2>/dev/null || echo "80")
  terms_lines=$(tput lines 2>/dev/null || echo "24")

  printf '%s%s' "$HOME" "$CLEAR"

  # --- Header (line 1) ---
  local esc_hint="[/] search  [esc/q] quit"
  printf "${CSI}1;1H%s%s [@] TMON%s" "$BOLD" "$FG_CYAN" "$RESET"
  printf "${CSI}1;$((terms_cols - ${#esc_hint}))H%s%s%s" "$FG_DIM" "$esc_hint" "$RESET"

  # --- Divider (line 2) ---
  printf "${CSI}2;1H%s" "$FG_DIM"
  printf '━%.0s' $(seq 1 "$terms_cols")
  printf '%s' "$RESET"

  # Read animation frame for status character toggling
  local frame
  frame=$(read_animation_frame)

  # --- Body (line 3+) ---
  local row=3
  local max_body_row=$((terms_lines - 1))  # last line is footer

  if [[ "$filtered_count" -eq 0 ]]; then
    printf "${CSI}${row};1H"
    if [[ -n "$filter_query" ]]; then
      printf '    No agents match "%s"' "$filter_query"
    else
      printf '    No agents detected.'
    fi
  else
    for ((di = 0; di < display_count; di++)); do
      [[ "$row" -ge "$max_body_row" ]] && break

      local item="${DISPLAY_ITEMS[$di]}"
      local type="${item%%|*}"
      local rest="${item#*|}"

      case "$type" in
        S)
          # Session header (non-selectable)
          local sname="${rest%%|*}"
          printf "${CSI}${row};1H${FG_CYAN}${BOLD}  ${sname}${RESET}"
          printf "${CSI}K"  # clear rest of line
          ;;
        W)
          # Window header (non-selectable)
          local widx="${rest%%|*}"
          local wname="${rest#*|}"
          if [[ -n "$wname" && "$wname" != "?" ]]; then
            printf "${CSI}${row};1H${FG_DIM}    ${widx}:${wname}${RESET}"
          else
            printf "${CSI}${row};1H${FG_DIM}    ${widx}${RESET}"
          fi
          printf "${CSI}K"
          ;;
        A)
          # Agent line (selectable)
          local i="$rest"
          local label name sc pi cwd
          label="${AGENT_LABELS[$i]}"
          name=$(agent_full_name "$label")
          sc=$(animated_status_char "${AGENT_STATUSES[$i]}" "$frame")
          pi="${AGENT_PANE_INDEXES[$i]:-?}"
          cwd="${AGENT_CWDS[$i]:-?}"

          local line
          printf -v line '      [%s] %s %s  %s' "$pi" "$sc" "$name" "$cwd"

          if is_selected "$di"; then
            printf "${CSI}${row};1H%s%s" "$BG_HL" "$line"
            printf "${CSI}K"  # fill rest of line with highlight
            printf "${RESET}"
          else
            printf "${CSI}${row};1H%s" "$line"
            printf "${CSI}K"
          fi
          ;;
      esac

      row=$((row + 1))
    done
  fi

  # --- Footer (last line) ---
  if [[ -n "$search_mode" ]]; then
    # Search mode: live filter input with cursor
    printf "${CSI}${terms_lines};1H%s ▌ %s" "$FG_WHITE" "$filter_query"
    printf "${FG_DIM}▌${RESET}"
    printf -v match_str "  %d/%d" "$filtered_count" "$agent_count"
    printf "${CSI}${terms_lines};$((terms_cols - ${#match_str}))H%s" "$match_str"
    printf '%s' "$RESET"
  elif [[ -n "$filter_query" ]]; then
    # Active filter, not in search mode — show filter dimmed with count
    printf "${CSI}${terms_lines};1H%s ▌ %s" "$FG_DIM" "$filter_query"
    printf -v match_str "%d/%d" "$filtered_count" "$agent_count"
    printf "${CSI}${terms_lines};$((terms_cols - ${#match_str}))H%s" "$match_str"
    printf '%s' "$RESET"
  else
    # No filter, navigation mode
    printf "${CSI}${terms_lines};1H%s ▌ / to search" "$FG_DIM"
    local hint_right="  j/k/↑↓ nav  ↩/spc/l focus  esc/q quit"
    printf "${CSI}${terms_lines};$((terms_cols - ${#hint_right}))H%s%s" "$FG_DIM" "$hint_right"
    printf '%s' "$RESET"
  fi
}

# ─── Actions ──────────────────────────────────────────────────────────────────

focus_agent() {
  if [[ "$selectable_count" -eq 0 ]]; then
    return
  fi
  local di="${SELECTABLE_MAP[$selected]}"
  local item="${DISPLAY_ITEMS[$di]}"
  local type="${item%%|*}"
  if [[ "$type" != "A" ]]; then
    return  # safety: only agent lines are selectable
  fi
  local i="${item#*|}"
  local pane_target="${AGENT_PANES[$i]}"
  if [[ "$pane_target" != "?" ]] && [[ -n "${TMUX:-}" ]]; then
    tmux switch-client -t "$pane_target" 2>/dev/null || true
  fi
}

# ─── Main Loop ────────────────────────────────────────────────────────────────

main() {
  printf '%s' "$HIDE_CURSOR"
  trap 'printf "%s" "$SHOW_CURSOR"' EXIT

  local search_mode=0  # 0 = navigation mode, 1 = search mode
  local auto_ticks=0

  refresh_data
  render

  while true; do
    local key=""
    local read_rc=0
    IFS= read -r -s -n 1 -t "$REFRESH_INTERVAL" key 2>/dev/null || read_rc=$?

    # Auto-refresh on timeout (no key pressed within REFRESH_INTERVAL)
    if [[ $read_rc -ne 0 ]]; then
      auto_ticks=$((auto_ticks + 1))
      if [[ $((auto_ticks % FULL_REFRESH_TICKS)) -eq 0 ]]; then
        refresh_data
      fi
      render
      continue
    fi

    # Handle escape sequences (arrow keys)
    if [[ "$key" == $'\033' ]]; then
      local seq
      IFS= read -r -s -n 1 -t 0.01 seq 2>/dev/null || true
      if [[ "$seq" == "[" ]]; then
        IFS= read -r -s -n 1 -t 0.01 key 2>/dev/null || true
        case "$key" in
          A) key="UP" ;;
          B) key="DOWN" ;;
        esac
      else
        # Standalone Esc
        key="ESC"
      fi
    fi

    # ── Search mode input handling ──
    if [[ "$search_mode" -eq 1 ]]; then
      case "$key" in
        ESC)
          search_mode=0
          render
          ;;
        $'\177'|$'\010')  # Backspace (DEL or BS)
          if [[ -n "$filter_query" ]]; then
            filter_query="${filter_query:0:${#filter_query}-1}"
            rebuild_filter
            render
          fi
          ;;
        [[:print:]])
          filter_query+="$key"
          rebuild_filter
          render
          ;;
        *)
          ;;  # ignore non-printable keys in search mode
      esac
      continue
    fi

    # ── Navigation mode input handling ──
    case "$key" in
      "/")
        search_mode=1
        render
        ;;
      ESC|"q")
        printf '%s' "$SHOW_CURSOR"
        exit 0
        ;;
      UP|"k")
        if [[ "$selectable_count" -gt 0 ]]; then
          selected=$(( (selected - 1 + selectable_count) % selectable_count ))
          render
        fi
        ;;
      DOWN|"j")
        if [[ "$selectable_count" -gt 0 ]]; then
          selected=$(( (selected + 1) % selectable_count ))
          render
        fi
        ;;
      ""|$'\n'|$'\r'|" "|"l")  # Enter, Space, l
        focus_agent
        printf '%s' "$SHOW_CURSOR"
        exit 0
        ;;
      *)
        ;;  # ignore other keys in navigation mode
    esac
  done
}

main
