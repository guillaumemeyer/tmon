#!/usr/bin/env bash
# tmon — Hermes dangerous-command approval hook.
#
# Installed by `tmon hooks install hermes` as shell hooks on
# pre_approval_request / post_approval_response. Writes pending approvals
# under $1 (hook state dir for hermes) so the Go connector can mark CLI/TUI
# sessions as blocked.
#
# stdin: Hermes shell-hook JSON
#   { "hook_event_name": "...", "extra": { "session_key", "pattern_key",
#     "description", "surface", "choice", ... } }
#
# Exit 0 always; never block the approval flow.
set -u

dir="${1:-}"
[ -n "$dir" ] || exit 0

in="$(cat)"

# Extract a JSON string field from a flat or nested-ish compact object.
# Prefer extra.<key> then top-level <key>.
field() {
  local key="$1"
  local v
  v="$(printf '%s' "$in" | sed -n "s/.*\"extra\"[^}]*\"$key\": *\"\([^\"]*\)\".*/\1/p" | head -n1)"
  if [ -z "$v" ]; then
    v="$(printf '%s' "$in" | sed -n "s/.*\"$key\": *\"\([^\"]*\)\".*/\1/p" | head -n1)"
  fi
  printf '%s' "${v%%$'\n'*}"
}

event="$(field hook_event_name)"
surface="$(field surface)"
# Smart-mode auto decisions should not flash blocked.
if [ "$surface" = "smart" ]; then
  exit 0
fi
# Prefer CLI (and accept gateway for future multi-surface use).
case "$surface" in
  cli|gateway|"") ;;
  *) exit 0 ;;
esac

session="$(field session_key)"
[ -n "$session" ] || session="$(field session_id)"
[ -n "$session" ] || exit 0

pattern="$(field pattern_key)"
desc="$(field description)"
# Sanitize for single-line JSON.
pattern="$(printf '%s' "$pattern" | tr -d '\n\r"')"
desc="$(printf '%s' "$desc" | tr -d '\n\r"' | cut -c1-120)"
home="${HERMES_HOME:-}"

mkdir -p "$dir"
path="$dir/approvals.json"
tmp="$path.tmp.$$"

# Read existing pending map (best-effort pure bash).
pending_body=""
if [ -f "$path" ]; then
  pending_body="$(cat "$path" 2>/dev/null || true)"
fi

case "$event" in
  post_approval_response)
    # Drop the session_key entry.
    if [ -n "$pending_body" ] && command -v python3 >/dev/null 2>&1; then
      HERMES_APPR_PATH="$path" HERMES_APPR_KEY="$session" python3 - <<'PY'
import json, os
path = os.environ["HERMES_APPR_PATH"]
key = os.environ["HERMES_APPR_KEY"]
try:
    with open(path) as f:
        data = json.load(f)
except Exception:
    data = {"pending": {}}
pending = data.get("pending") or {}
pending.pop(key, None)
data["pending"] = pending
tmp = path + ".tmp"
with open(tmp, "w") as f:
    json.dump(data, f, separators=(",", ":"))
os.replace(tmp, path)
PY
    fi
    exit 0
    ;;
  pre_approval_request|*)
    if command -v python3 >/dev/null 2>&1; then
      HERMES_APPR_PATH="$path" \
      HERMES_APPR_KEY="$session" \
      HERMES_APPR_PATTERN="$pattern" \
      HERMES_APPR_DESC="$desc" \
      HERMES_APPR_SURFACE="$surface" \
      HERMES_APPR_HOME="$home" \
      python3 - <<'PY'
import json, os, time
path = os.environ["HERMES_APPR_PATH"]
try:
    with open(path) as f:
        data = json.load(f)
except Exception:
    data = {"pending": {}}
pending = data.get("pending") or {}
pending[os.environ["HERMES_APPR_KEY"]] = {
    "pattern_key": os.environ.get("HERMES_APPR_PATTERN", ""),
    "description": os.environ.get("HERMES_APPR_DESC", ""),
    "surface": os.environ.get("HERMES_APPR_SURFACE", ""),
    "hermes_home": os.environ.get("HERMES_APPR_HOME", ""),
    "ts": int(time.time()),
}
data["pending"] = pending
tmp = path + ".tmp"
with open(tmp, "w") as f:
    json.dump(data, f, separators=(",", ":"))
os.replace(tmp, path)
PY
    else
      # Minimal fallback without python: overwrite with a single entry.
      ts="$(date +%s)"
      printf '{"pending":{"%s":{"pattern_key":"%s","description":"%s","surface":"%s","hermes_home":"%s","ts":%s}}}\n' \
        "$session" "$pattern" "$desc" "$surface" "$home" "$ts" > "$tmp" && mv "$tmp" "$path"
    fi
    exit 0
    ;;
esac
exit 0
