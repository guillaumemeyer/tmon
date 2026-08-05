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
#                            emoji (🤖 🚨 💤); "1" switches to ASCII
#                            ([@] B I). Working agents always show the
#                            animated spinner
#   @tmon-bold-counts       "1" (default) — render the per-status counts
#                            (the 2 in 🚨2) in bold; "0" turns it off
#   @tmon-context-warn      85 (default) — context-usage % at which a ⚠️
#                            warning appears in the status bar (and the
#                            dashboard's usage bar turns yellow); "0" disables
#   @tmon-blocked-bell      "off" (default) — ring the terminal bell when an
#                            agent transitions to blocked; "on" enables
#   @tmon-pane-border       "on" (default) — show a status-colored border
#                            strip on agent panes (blocked/working icon+label;
#                            idle clears to the default empty strip); "off"
#                            disables. Owns pane-border-status/format while on.
#   @tmon-pane-border-position "top" (default) or "bottom" — where the strip sits
#   @tmon-color-<slot>      override one theme color slot; slot is one of
#                            bg|app|blocked|working|idle|dim|accent|warn|selbg
#                            (bg is the dashboard popup background) and the
#                            value a tmux color (name, colourNNN, hex)
#   @tmon-icon-<slot>       override one status glyph; slot is one of
#                            app|blocked|idle|warn (working agents use the
#                            animated spinner instead of an icon)
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
CONTEXT_WARN=$(get_tmux_option "@tmon-context-warn" "85")
BLOCKED_BELL=$(get_tmux_option "@tmon-blocked-bell" "off")
PANE_BORDER=$(get_tmux_option "@tmon-pane-border" "on")
PANE_BORDER_POS=$(get_tmux_option "@tmon-pane-border-position" "top")
case "$PANE_BORDER_POS" in
  top|bottom) ;;
  *) PANE_BORDER_POS="top" ;;
esac
# The theme is chosen in the dashboard's selector and saved to state/theme
# (tmux's TMON_THEME is server-state only and vanishes when the server
# restarts), so restore it at load. Trim whitespace so a trailing newline
# from editors never becomes part of the name.
THEME="default"
if [ -s "$STATE_DIR/theme" ]; then
  THEME=$(tr -d '[:space:]' < "$STATE_DIR/theme")
  [ -n "$THEME" ] || THEME="default"
fi
AUTO_HOOKS=$(get_tmux_option "@tmon-auto-hooks" "on")

# ─── Runtime environment ──────────────────────────────────────────────────────

# Push the configuration into tmux's global environment so the #() status
# command and the display-popup actually see it. (The bash plugin only
# exported these in the sourcing shell, which the status bar never inherits —
# so its @tmon-* options were effectively dead for the #() path.)
#
# set_env_everywhere updates both the global environment and every live
# session. tmux copies globals into a session at creation time; later
# set-environment -g alone is shadowed by the session's stale copy, which
# is what broke theme persistence across reloads/restarts.
set_env_everywhere() {
  local name="$1" value="$2" s
  tmux set-environment -g "$name" "$value"
  while IFS= read -r s; do
    [ -n "$s" ] || continue
    tmux set-environment -t "$s" "$name" "$value" 2>/dev/null || true
  done < <(tmux list-sessions -F '#{session_name}' 2>/dev/null || true)
}

set_env_everywhere TMON_STATE_DIR "$STATE_DIR"
set_env_everywhere TMON_BIN_DIR "$BIN_DIR"
set_env_everywhere TMON_POLL_INTERVAL_MS "$POLL_INTERVAL"
set_env_everywhere TMON_ACTIVITY_THRESHOLD_MS "$ACTIVITY_THRESHOLD"
set_env_everywhere TMON_IO_ACTIVITY_THRESHOLD "$IO_THRESHOLD"
set_env_everywhere TMON_CONNECTORS "$CONNECTORS"
set_env_everywhere TMON_CONNECTOR_FRESHNESS "$CONNECTOR_FRESHNESS"
set_env_everywhere TMON_ASCII_ICONS "$ASCII_ICONS"
set_env_everywhere TMON_BOLD_COUNTS "$BOLD_COUNTS"
set_env_everywhere TMON_CONTEXT_WARN "$CONTEXT_WARN"
set_env_everywhere TMON_BLOCKED_BELL "$BLOCKED_BELL"
set_env_everywhere TMON_PANE_BORDER "$PANE_BORDER"
set_env_everywhere TMON_PANE_BORDER_POSITION "$PANE_BORDER_POS"
set_env_everywhere TMON_THEME "$THEME"
set_env_everywhere TMON_HOOK_STATE_DIR "$STATE_DIR/hooks"

# Per-slot theme overrides. Set values are exported to the binary via
# TMON_COLOR_*/TMON_ICON_*; cleared ones are unset so removing the tmux
# option also removes the override.
for slot in bg app blocked working idle dim accent warn selbg; do
  val=$(get_tmux_option "@tmon-color-$slot" "")
  upper=$(echo "$slot" | tr '[:lower:]' '[:upper:]')
  if [ -n "$val" ]; then
    tmux set-environment -g "TMON_COLOR_$upper" "$val"
  else
    tmux set-environment -gu "TMON_COLOR_$upper"
  fi
done
for slot in app blocked working idle warn; do
  val=$(get_tmux_option "@tmon-icon-$slot" "")
  upper=$(echo "$slot" | tr '[:lower:]' '[:upper:]')
  if [ -n "$val" ]; then
    tmux set-environment -g "TMON_ICON_$upper" "$val"
  else
    tmux set-environment -gu "TMON_ICON_$upper"
  fi
done

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

  # Pane border strip: enable chrome when on so the strip is ready before the
  # first poll; clean up when off so a previous session's strips don't linger.
  if [ "$PANE_BORDER" = "on" ]; then
    tmux set -g pane-border-status "$PANE_BORDER_POS"
    tmux set -g pane-border-format '#{E:@tmon_border}'
  elif [ -x "$BINARY" ]; then
    "$BINARY" border off
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
  # The popup opens borderless (-B); the dashboard draws its own rounded
  # border inside, tinted with the current theme.
  tmux bind-key "$DASHBOARD_KEY" switch-client -T a-table
  tmux bind-key -T a-table "$DASHBOARD_KEY" \
    display-popup -B -w 80% -h 80% -E "$BINARY dashboard"

  # Mouse click on the status-bar indicator opens the popup too.
  tmux bind-key -T root MouseDown1Status \
    if -F "#{==:#{mouse_status_range},tmon}" \
    "display-popup -B -w 80% -h 80% -E '$BINARY dashboard'" \
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
