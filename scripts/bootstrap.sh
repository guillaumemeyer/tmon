#!/bin/sh
# tmon bootstrap — download the pinned tmon binary into the plugin's bin dir.
#
# Pure POSIX sh. External tools: a downloader (curl or wget) and a SHA-256
# checker (sha256sum or shasum). The binary, the temp file and the lock all
# live inside the plugin directory (default <plugin>/bin) — nothing is written
# to ~/.cache or /tmp. Safe to run repeatedly: exits 0 immediately when the
# installed version matches the repo VERSION file. Called by tmon.tmux at load
# time, never from the #() status path.
#
# Environment:
#   TMON_BIN_DIR        informational; never trusted for the install/version
#                       check path (see below — it can leak from another tmux
#                       server or checkout)
#   TMON_DOWNLOAD_BASE  release base URL override (for testing)

set -eu

# CDPATH= (empty) prevents `cd` from printing the resolved path when CDPATH
# is set in the caller's environment — the construct is intentional.
# shellcheck disable=SC1007
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck disable=SC1007
PLUGIN_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
# The binary always lives in <plugin>/bin, derived from this script's own
# location. TMON_BIN_DIR can leak into the environment from another tmux
# server or checkout (tmon.tmux pins it in tmux's global environment with
# `set-environment -g`, and a pane started on one machine can carry it over
# SSH to another). A stale value would silently redirect the version check —
# and the download — to the wrong directory, so it is only honored when it
# matches this plugin's own bin dir.
BIN_DIR="$PLUGIN_DIR/bin"
if [ -n "${TMON_BIN_DIR:-}" ] && [ "$TMON_BIN_DIR" != "$BIN_DIR" ]; then
  echo "tmon: ignoring TMON_BIN_DIR=$TMON_BIN_DIR (not this plugin's bin dir); using $BIN_DIR" >&2
fi
BINARY="$BIN_DIR/tmon"
LOCK_DIR="$BIN_DIR/.bootstrap.lock"
DOWNLOAD_BASE=${TMON_DOWNLOAD_BASE:-"https://github.com/guillaumemeyer/tmon/releases/download"}

VERSION_FILE="$PLUGIN_DIR/VERSION"
[ -f "$VERSION_FILE" ] || {
  echo "tmon: missing VERSION file at $VERSION_FILE" >&2
  exit 1
}
VERSION=$(tr -d '[:space:]' < "$VERSION_FILE")

# up_to_date: the installed binary matches VERSION — or it is a local "dev"
# build, which we never overwrite (that is the `make build` developer path).
# The dev skip is reported instead of being silent so a stale dev build on
# some other machine cannot quietly block updates forever.
up_to_date() {
  [ -x "$BINARY" ] || return 1
  INSTALLED=$("$BINARY" version 2>/dev/null || true)
  [ "$INSTALLED" = "$VERSION" ] && return 0
  if [ "$INSTALLED" = "dev" ]; then
    echo "tmon: installed binary is a dev build (make build); release updates are skipped" >&2
    return 0
  fi
  return 1
}

up_to_date && exit 0

mkdir -p "$BIN_DIR"

# Lock so concurrent tmux reloads don't both download. A stale lock left by a
# killed bootstrap is removed after a short grace period.
tries=0
while ! mkdir "$LOCK_DIR" 2>/dev/null; do
  tries=$((tries + 1))
  if [ "$tries" -ge 100 ]; then
    rmdir "$LOCK_DIR" 2>/dev/null || true
    tries=0
  fi
  sleep 0.1
done
trap 'rmdir "$LOCK_DIR" 2>/dev/null || true' EXIT INT TERM

# Re-check under the lock: another process may have won the race.
up_to_date && exit 0

case "$(uname -s)" in
  Linux) OS=linux ;;
  Darwin) OS=darwin ;;
  *)
    echo "tmon: unsupported OS: $(uname -s) (use WSL2 on Windows)" >&2
    exit 1
    ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *)
    echo "tmon: unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

ASSET="tmon_${VERSION}_${OS}_${ARCH}"
TMP="$BIN_DIR/.${ASSET}.tmp.$$"

fail() {
  echo "tmon: failed to download v${VERSION}: $1" >&2
  if [ -n "${TMUX:-}" ] && command -v tmux >/dev/null 2>&1; then
    tmux display-message "tmon: failed to download v${VERSION} - check network or run scripts/bootstrap.sh"
  fi
  rm -f "$TMP" "$TMP.sha256"
  exit 1
}

# download URL OUT — curl preferred, wget fallback.
download() {
  url=$1
  out=$2
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "$out" "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$out" "$url"
  else
    return 127
  fi
}

# sha256_file PATH — print hex digest of file.
sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    return 127
  fi
}

command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 || fail "curl or wget not found"
command -v sha256sum >/dev/null 2>&1 || command -v shasum >/dev/null 2>&1 || fail "sha256sum or shasum not found"

download "$DOWNLOAD_BASE/v${VERSION}/$ASSET" "$TMP" || fail "network error downloading $ASSET"
download "$DOWNLOAD_BASE/v${VERSION}/checksums.txt" "$TMP.sha256" || fail "network error downloading checksums.txt"

EXPECTED=$(awk -v name="$ASSET" '$2 == name { print $1; exit }' "$TMP.sha256")
[ -n "$EXPECTED" ] || fail "$ASSET not found in checksums.txt"

ACTUAL=$(sha256_file "$TMP") || fail "checksum tool failed"
[ "$ACTUAL" = "$EXPECTED" ] || fail "checksum mismatch (got $ACTUAL)"

chmod +x "$TMP"
mv "$TMP" "$BINARY"
rm -f "$TMP.sha256"

echo "tmon: installed v${VERSION} ($OS/$ARCH)"
