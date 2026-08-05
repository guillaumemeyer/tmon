#!/usr/bin/env bash
# Record the tmon demo GIF with VHS.
# Usage (from anywhere):
#   ./docs/demo/demo.sh
#   bash docs/demo/demo.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
TAPE="$SCRIPT_DIR/demo.tape"
OUT="$SCRIPT_DIR/demo.gif"

if ! command -v vhs >/dev/null 2>&1; then
  echo "error: vhs is not installed or not on PATH" >&2
  echo "  https://github.com/charmbracelet/vhs#installation" >&2
  exit 1
fi

if [[ ! -f "$TAPE" ]]; then
  echo "error: tape not found: $TAPE" >&2
  exit 1
fi

# VHS (go-rod) needs a Chromium-family browser to render frames.
# On linux/amd64 go-rod can auto-download one; on aarch64 the snapshot
# hosts often fail (empty platform prefix + outdated Playwright rev).
# Prefer a system browser; otherwise reuse Playwright's cached binary.
ensure_chromium() {
  local name
  for name in chromium chromium-browser google-chrome google-chrome-stable chrome; do
    if command -v "$name" >/dev/null 2>&1; then
      return 0
    fi
  done

  local pw_chrome=""
  if [[ -d "${HOME}/.cache/ms-playwright" ]]; then
    # Newest chrome-linux/chrome under the Playwright cache (ARM + x86).
    pw_chrome="$(
      find "${HOME}/.cache/ms-playwright" -path '*/chrome-linux/chrome' -type f 2>/dev/null \
        | sort -V \
        | tail -1
    )"
  fi

  if [[ -n "$pw_chrome" && -x "$pw_chrome" ]]; then
    local bindir
    bindir="$(mktemp -d "${TMPDIR:-/tmp}/vhs-chromium.XXXXXX")"
    # go-rod LookPath searches for "chromium" / "google-chrome" on PATH.
    ln -sf "$pw_chrome" "$bindir/chromium"
    ln -sf "$pw_chrome" "$bindir/google-chrome"
    export PATH="$bindir:$PATH"
    # shellcheck disable=SC2064
    trap "rm -rf '$bindir'" EXIT
    echo "using Playwright Chromium: $pw_chrome"
    return 0
  fi

  echo "error: no Chromium browser found for VHS (needed by go-rod)" >&2
  echo "  Install one of: chromium, google-chrome, or Playwright Chromium." >&2
  echo "  On aarch64 Linux, auto-download often fails; try:" >&2
  echo "    npx playwright install chromium" >&2
  echo "    # or: sudo snap install chromium" >&2
  echo "  Then re-run this script." >&2
  exit 1
}

ensure_chromium

# Ubuntu 23.10+ (and similar) can block Chromium's user-namespace sandbox via
# AppArmor. VHS honors VHS_NO_SANDBOX=1 → go-rod --no-sandbox.
if [[ -z "${VHS_NO_SANDBOX:-}" ]]; then
  userns_restricted=""
  if [[ -r /proc/sys/kernel/apparmor_restrict_unprivileged_userns ]]; then
    userns_restricted="$(cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns 2>/dev/null || true)"
  fi
  if [[ "$userns_restricted" == "1" ]]; then
    export VHS_NO_SANDBOX=1
    echo "note: unprivileged userns restricted → VHS_NO_SANDBOX=1"
  fi
fi

# VHS resolves Output paths relative to the process cwd; run from the repo
# root so docs/demo/demo.gif lands next to this script.
cd "$REPO_ROOT"

echo "recording with vhs → $OUT"
vhs "$TAPE"

if [[ ! -f "$OUT" ]]; then
  echo "error: expected output missing: $OUT" >&2
  exit 1
fi

echo "done: $OUT"
