#!/usr/bin/env bash
# tmon dashboard.sh — Interactive agent navigation popup
# ==============================================================================
# Launched via tmux display-popup (prefix a a). Shows a scrollable list of
# running AI coding agents with their status and tmux path.
#
# Keybindings:
#   ↑/k       — move selection up
#   ↓/j       — move selection down
#   →/l/Enter — focus selected agent's pane (switches to its session)
#   /         — start full-text search (filters by agent name, session, window)
#   r         — refresh agent list
#   q/Esc     — quit

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MONITOR_SCRIPT="$SCRIPT_DIR/monitor.sh"
PANE_MAPPER="$SCRIPT_DIR/pane-map.sh"
STATE_FILE="${HOME}/.cache/tmon/agents.state"

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

# ─── Search state ─────────────────────────────────────────────────────────────

search_mode=false
search_query=""
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

detect_agents() {
  local regex
  regex=$(build_detect_regex)

  for pid_dir in /proc/[0-9]*; do
    local pid="${pid_dir##*/}"
    local cmdline=""
    if [[ -r "$pid_dir/cmdline" ]]; then
      cmdline=$(tr '\0' ' ' < "$pid_dir/cmdline" 2>/dev/null || true)
    fi
    [[ -n "$cmdline" ]] || continue

    if ! echo "$cmdline" | grep -qE "$regex" 2>/dev/null; then
      continue
    fi

    local matched_label=""
    for sig in "${AGENT_SIGNATURES[@]}"; do
      local label regex_p
      label=$(sig_label "$sig")
      regex_p=$(sig_regex "$sig")
      if echo "$cmdline" | grep -qE "$regex_p" 2>/dev/null; then
        matched_label="$label"
        break
      fi
    done
    [[ -n "$matched_label" ]] || continue

    local cwd="?"
    if [[ -r "$pid_dir/cwd" ]]; then
      cwd=$(readlink "$pid_dir/cwd" 2>/dev/null || echo "?")
      cwd=$(echo "$cwd" | rev | cut -d'/' -f1-2 | rev)
    fi

    echo "${pid}|${matched_label}|${cmdline:0:80}|${cwd}"
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

  local idx=0
  while IFS='|' read -r pid label cmdline cwd pane tty session_name window_index window_name pane_index session_id; do
    local cpu io
    cpu=$(awk '{print $14+$15+$16+$17}' "/proc/$pid/stat" 2>/dev/null || echo "0")
    io=$(awk '/^rchar|^wchar/{sum+=$2}END{print sum+0}' "/proc/$pid/io" 2>/dev/null || echo "0")

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
  done < <(detect_agents | bash "$PANE_MAPPER" 2>/dev/null | sort -t'|' -k11,11n -k8,8n -k10,10n)

  agent_count=$idx
  rebuild_filter
}

# ─── Search ───────────────────────────────────────────────────────────────────

rebuild_filter() {
  FILTERED_INDICES=()
  if ! $search_mode || [[ -z "$search_query" ]]; then
    # No filter active: all agents
    for ((i = 0; i < agent_count; i++)); do
      FILTERED_INDICES+=("$i")
    done
  else
    local q="${search_query,,}"
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

  # Clamp selection
  if [[ "$filtered_count" -eq 0 ]]; then
    selected=0
  elif [[ "$selected" -ge "$filtered_count" ]]; then
    selected=$((filtered_count - 1))
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

# ─── Formatting ───────────────────────────────────────────────────────────────

tmux_path_display() {
  local i="$1"
  local sid="${AGENT_SESSION_IDS[$i]:-?}"
  local sn="${AGENT_SESSION_NAMES[$i]:-?}"
  local wi="${AGENT_WINDOW_INDEXES[$i]:-?}"
  local wn="${AGENT_WINDOW_NAMES[$i]:-?}"
  local pi="${AGENT_PANE_INDEXES[$i]:-?}"
  echo "[${sid}]:${sn} / [${wi}]:${wn} / [${pi}]"
}

# ─── Rendering ────────────────────────────────────────────────────────────────

render() {
  local terms_cols terms_lines
  terms_cols=$(tput cols 2>/dev/null || echo "80")
  terms_lines=$(tput lines 2>/dev/null || echo "24")

  printf '%s%s' "$HOME" "$CLEAR"

  # --- Header (line 1) ---
  printf "${CSI}1;1H%s%s [@] TMON%s" "$BOLD" "$FG_CYAN" "$RESET"

  # --- Divider (line 2) ---
  printf "${CSI}2;1H%s" "$FG_DIM"
  printf '━%.0s' $(seq 1 "$terms_cols")
  printf '%s' "$RESET"

  # --- Body (line 3+) ---
  local row=3
  local max_body_row=$((terms_lines - 1))  # last line is footer

  if [[ "$filtered_count" -eq 0 ]]; then
    printf "${CSI}${row};1H"
    if $search_mode && [[ -n "$search_query" ]]; then
      printf '    No agents match "%s"' "$search_query"
    else
      printf '    No agents detected.'
    fi
  else
    for ((fi = 0; fi < filtered_count; fi++)); do
      [[ "$row" -ge "$max_body_row" ]] && break

      local i="${FILTERED_INDICES[$fi]}"
      local label name path
      label="${AGENT_LABELS[$i]}"
      name=$(agent_full_name "$label")
      path=$(tmux_path_display "$i")

      local line
      printf -v line ' %s: %s' "$name" "$path"

      if [[ "$fi" -eq "$selected" ]]; then
        printf "${CSI}${row};1H%s%s%s" "$BG_HL" "$line" "$BG_DEFAULT"
      else
        printf "${CSI}${row};1H%s" "$line"
      fi

      row=$((row + 1))
    done
  fi

  # --- Footer (last line) ---
  if $search_mode; then
    printf "${CSI}${terms_lines};1H%s / %s" "$FG_WHITE" "$search_query"
    printf "${FG_DIM} ▌${RESET}"
    local match_str
    printf -v match_str "  %d/%d matches" "$filtered_count" "$agent_count"
    printf "${CSI}${terms_lines};$((terms_cols - ${#match_str}))H%s" "$match_str"
    printf '%s' "$RESET"
  else
    local hints="  ↑↓/jk navigate  enter/→/l focus  / search  r refresh  q quit"
    printf "${CSI}${terms_lines};1H%s" "$FG_DIM"
    printf '━%.0s' $(seq 1 $((terms_cols - ${#hints})))
    printf '%s%s' "$hints" "$RESET"
  fi
}

# ─── Actions ──────────────────────────────────────────────────────────────────

focus_agent() {
  if [[ "$filtered_count" -eq 0 ]]; then
    return
  fi
  local i="${FILTERED_INDICES[$selected]}"
  local pane_target="${AGENT_PANES[$i]}"
  if [[ "$pane_target" != "?" ]] && [[ -n "${TMUX:-}" ]]; then
    tmux switch-client -t "$pane_target" 2>/dev/null || true
  fi
}

# ─── Main Loop ────────────────────────────────────────────────────────────────

main() {
  printf '%s' "$HIDE_CURSOR"
  trap 'printf "%s" "$SHOW_CURSOR"' EXIT

  refresh_data
  render

  while true; do
    local key=""
    IFS= read -r -s -n 1 key 2>/dev/null || { key="q"; }

    # Handle escape sequences
    if [[ "$key" == $'\033' ]]; then
      local seq
      IFS= read -r -s -n 1 -t 0.01 seq 2>/dev/null || true
      if [[ "$seq" == "[" ]]; then
        IFS= read -r -s -n 1 -t 0.01 key 2>/dev/null || true
        case "$key" in
          A) key="UP" ;;
          B) key="DOWN" ;;
          C) key="RIGHT" ;;
          D) key="LEFT" ;;
        esac
      else
        # Standalone Esc
        key="ESC"
      fi
    fi

    # ── Search mode input ──
    if $search_mode; then
      case "$key" in
        ESC|/)
          # Exit search mode
          search_mode=false
          search_query=""
          rebuild_filter
          render
          ;;
        $'\177'|$'\010')  # Backspace (DEL or BS)
          if [[ -n "$search_query" ]]; then
            search_query="${search_query:0:${#search_query}-1}"
            rebuild_filter
            render
          fi
          ;;
        UP|DOWN|LEFT|RIGHT|"")
          # Ignore navigation keys in search mode (except Enter/Right to focus)
          if [[ "$key" == "RIGHT" ]] || [[ "$key" == "" ]]; then
            focus_agent
            printf '%s' "$SHOW_CURSOR"
            exit 0
          fi
          ;;
        [[:print:]])
          # Printable character: append to query
          search_query+="$key"
          rebuild_filter
          render
          ;;
        *)
          # Unknown key in search mode: ignore
          ;;
      esac
      continue
    fi

    # ── Normal mode input ──
    case "$key" in
      UP|k)
        if [[ "$filtered_count" -gt 0 ]]; then
          selected=$(( (selected - 1 + filtered_count) % filtered_count ))
          render
        fi
        ;;
      DOWN|j)
        if [[ "$filtered_count" -gt 0 ]]; then
          selected=$(( (selected + 1) % filtered_count ))
          render
        fi
        ;;
      RIGHT|l|"")  # Enter
        focus_agent
        printf '%s' "$SHOW_CURSOR"
        exit 0
        ;;
      "/")
        search_mode=true
        search_query=""
        rebuild_filter
        render
        ;;
      r)
        refresh_data
        render
        ;;
      q|ESC)
        printf '%s' "$SHOW_CURSOR"
        exit 0
        ;;
    esac
  done
}

main
