#!/bin/sh
# tmon bootstrap — download the pinned tmon binary into the plugin's bin dir.
#
# Pure POSIX sh; the only external tools needed are curl and sha256sum. The
# binary, the temp file and the lock all live inside the plugin directory
# (default <plugin>/bin) — nothing is written to ~/.cache or /tmp. Safe to run
# repeatedly: exits 0 immediately when the installed version matches the repo
# VERSION file. Called by tmon.tmux at load time, never from the #() status
# path.
#
# Environment:
#   TMON_BIN_DIR        binary dir (default <plugin>/bin)
#   TMON_DOWNLOAD_BASE  release base URL override (for testing)

set -eu

# CDPATH= (empty) prevents `cd` from printing the resolved path when CDPATH
# is set in the caller's environment — the construct is intentional.
# shellcheck disable=SC1007
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck disable=SC1007
PLUGIN_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
BIN_DIR=${TMON_BIN_DIR:-"$PLUGIN_DIR/bin"}
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
up_to_date() {
  [ -x "$BINARY" ] || return 1
  INSTALLED=$("$BINARY" version 2>/dev/null || true)
  [ "$INSTALLED" = "$VERSION" ] || [ "$INSTALLED" = "dev" ]
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

case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *)
    echo "tmon: unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

ASSET="tmon_${VERSION}_linux_${ARCH}"
TMP="$BIN_DIR/.${ASSET}.tmp.$$"

fail() {
  echo "tmon: failed to download v${VERSION}: $1" >&2
  if [ -n "${TMUX:-}" ] && command -v tmux >/dev/null 2>&1; then
    tmux display-message "tmon: failed to download v${VERSION} - check network or run scripts/bootstrap.sh"
  fi
  rm -f "$TMP" "$TMP.sha256"
  exit 1
}

command -v curl >/dev/null 2>&1 || fail "curl not found"

curl -fsSL -o "$TMP" "$DOWNLOAD_BASE/v${VERSION}/$ASSET" || fail "network error downloading $ASSET"
curl -fsSL -o "$TMP.sha256" "$DOWNLOAD_BASE/v${VERSION}/checksums.txt" || fail "network error downloading checksums.txt"

EXPECTED=$(awk -v name="$ASSET" '$2 == name { print $1; exit }' "$TMP.sha256")
[ -n "$EXPECTED" ] || fail "$ASSET not found in checksums.txt"

ACTUAL=$(sha256sum "$TMP" | awk '{print $1}')
[ "$ACTUAL" = "$EXPECTED" ] || fail "checksum mismatch (got $ACTUAL)"

chmod +x "$TMP"
mv "$TMP" "$BINARY"
rm -f "$TMP.sha256"

echo "tmon: installed v${VERSION} ($ARCH)"
