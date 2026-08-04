#!/usr/bin/env bash
# tmon — generic agent lifecycle hook.
#
# Installed by `tmon hooks install <agent>` for HOOKS-tier agents (Claude
# Code, Codex, Cursor, Copilot, Windsurf). Writes one tiny JSON state file
# per session under $1 (the hook state dir); the Go connector reads it back.
# Agents run hooks synchronously on every event, so this script must stay
# fast (pure bash + sed, no interpreter startup) and silent. Exit 0 always.
#
# The case statement below covers the union of event names across agents
# (Claude/Codex use PascalCase, Cursor/Copilot camelCase, Windsurf
# snake_case) so one script serves them all.
#
# Usage:
#   agent-hook.sh <hook-state-dir>                       # derive from event
#   agent-hook.sh <hook-state-dir> <status> <detail>     # matcher-encoded
#     (Notification entries pass a pre-mapped status/detail, since the
#      matcher — permission_prompt / idle_prompt / agent_needs_input —
#      already encodes the mapping)
set -u

dir="${1:-}"
[ -n "$dir" ] || exit 0

in="$(cat)"

# Take the first field match (input is compact JSON, so there is exactly
# one; the %%${nl}* guard covers pretty-printed multi-line input).
field() {
  v="$(printf '%s' "$in" | sed -n "s/.*\"$1\": *\"\([^\"]*\)\".*/\1/p")"
  printf '%s' "${v%%$'\n'*}"
}

session="$(field session_id)"
[ -n "$session" ] || exit 0

if [ -n "${2:-}" ]; then
  status="$2"
  detail="${3:-}"
else
  event="$(field hook_event_name)"
  tool="$(field tool_name)"
  case "$event" in
    SessionEnd|sessionEnd) rm -f "$dir/$session.json"; exit 0 ;;
    SessionStart|sessionStart) status=idle; detail=started ;;
    UserPromptSubmit|beforeSubmitPrompt|pre_user_prompt) status=idle; detail=prompted ;;
    PreToolUse|preToolUse|pre_run_command|pre_write_code|pre_mcp_tool_use) status=working; detail="tool:${tool:-running}" ;;
    PostToolUse|postToolUse|afterShellExecution|afterFileEdit|beforeMCPExecution) status=working; detail="done:${tool:-running}" ;;
    PostToolUseFailure|postToolUseFailure) status=working; detail="failed:${tool:-running}" ;;
    PermissionRequest|permissionRequest|PermissionDenied|permissionDenied) status=blocked; detail="permission:${tool:-unknown}" ;;
    Stop|stop|agentStop) status=idle; detail=turn-complete ;;
    post_cascade_response) status=idle; detail=responded ;;
    post_cascade_response_with_transcript) status=working; detail=transcript ;;
    SubagentStart|SubagentStop) status=working; detail=subagent ;;
    PreCompact|PostCompact) status=working; detail=compacting ;;
    *) status=idle; detail=started ;;
  esac
fi

cwd="$(field cwd)"
mkdir -p "$dir"
printf '{"status":"%s","detail":"%s","cwd":"%s","ts":%s}\n' \
  "$status" "$detail" "$cwd" "$(date +%s)" \
  > "$dir/$session.json.tmp" && mv "$dir/$session.json.tmp" "$dir/$session.json"
exit 0
