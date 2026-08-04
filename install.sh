#!/bin/sh
# tmon — one-line installer
#
#   curl -fsSL https://raw.githubusercontent.com/guillaumemeyer/tmon/main/install.sh | sh
#
# Pure POSIX sh. Detects which install mode applies:
#
#   TPM mode    — ~/.tmux.conf already has a `guillaumemeyer/tmon` plugin
#                 line and TPM is installed: delegate to TPM's installer
#                 (TPM stays the owner of the plugin).
#   Manual mode — otherwise: clone the plugin into ~/.tmux/plugins/tmon,
#                 wire a run-shell line into ~/.tmux.conf, reload tmux.
#
# Safe to re-run: an existing checkout is updated with git pull; the config
# line is added only once. Overrides for testing: TMUX_CONF, TMON_DIR,
# TMON_REPO_URL.

set -eu

TMUX_CONF="${TMUX_CONF:-$HOME/.tmux.conf}"
TMON_DIR="${TMON_DIR:-$HOME/.tmux/plugins/tmon}"
TPM_DIR="${TPM_DIR:-$HOME/.tmux/plugins/tpm}"
REPO_URL="${TMON_REPO_URL:-https://github.com/guillaumemeyer/tmon.git}"

fail() {
  echo "tmon: $1" >&2
  exit 1
}

have() {
  command -v "$1" >/dev/null 2>&1
}

main() {
  have git || fail "git is required to install tmon"
  have tmux || fail "tmux is required (install tmux >= 3.2, or use WSL2 on Windows)"

  # TPM mode: the plugin line is already in tmux.conf and TPM is installed —
  # let TPM own the install (and future updates via `prefix I`).
  if grep -qs "guillaumemeyer/tmon" "$TMUX_CONF" 2>/dev/null && [ -d "$TPM_DIR" ]; then
    echo "tmon: TPM detected — installing through TPM"
    "$TPM_DIR/bin/install_plugins" || fail "TPM install failed"
    echo "tmon: done. Press prefix + I inside tmux to finish (or run: tmux source-file $TMUX_CONF)"
    return 0
  fi

  # Manual mode.
  if [ -d "$TMON_DIR/.git" ]; then
    echo "tmon: already installed — updating"
    (cd "$TMON_DIR" && git pull --ff-only) || fail "update failed (stash local changes and re-run)"
  else
    if [ -e "$TMON_DIR" ]; then
      fail "$TMON_DIR exists but is not a git checkout — move it aside and re-run"
    fi
    mkdir -p "$(dirname "$TMON_DIR")"
    echo "tmon: cloning into $TMON_DIR"
    git clone --depth 1 "$REPO_URL" "$TMON_DIR" || fail "clone failed — check network access to GitHub"
  fi

  # Wire the plugin into tmux.conf (idempotent).
  if [ ! -f "$TMUX_CONF" ]; then
    : > "$TMUX_CONF"
    echo "tmon: created $TMUX_CONF"
  fi
  if ! grep -qs "plugins/tmon/tmon.tmux" "$TMUX_CONF"; then
    printf '\n# tmon — AI agent monitor\nrun-shell "%s/tmon.tmux"\n' "$TMON_DIR" >> "$TMUX_CONF"
    echo "tmon: added run-shell line to $TMUX_CONF"
  fi

  if [ -n "${TMUX:-}" ]; then
    if tmux source-file "$TMUX_CONF"; then
      echo "tmon: done — tmux config reloaded"
    else
      echo "tmon: done — installed, but reloading tmux config failed (reload manually)" >&2
    fi
  else
    echo "tmon: done. Start tmux (or run: tmux source-file $TMUX_CONF)"
  fi
}

main "$@"
