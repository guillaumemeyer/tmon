#!/usr/bin/env bash
# tmon — generic agent lifecycle hook.
#
# Installed by `tmon hooks install <agent>` for HOOKS-tier agents (Claude
# Code, Codex, Cursor, Copilot, Windsurf, Grok). Writes one tiny JSON state
# file per session under $1 (the hook state dir); the Go connector reads it
# back. Agents run hooks synchronously on every event, so this script must
# stay fast (pure bash + sed, no interpreter startup) and silent. Exit 0
# always.
#
# The case statement below covers the union of event names across agents so
# one script serves them all:
#   Claude/Codex/Grok  PascalCase or snake_case event names
#   Cursor             camelCase names (payload fields stay snake_case)
#   Copilot            camelCase names, passed explicitly via --event (the
#                      camelCase payload carries no event name)
#   Windsurf           snake_case names in agent_action_name (no session id;
#                      the conversation id arrives as trajectory_id)
#
# Usage:
#   agent-hook.sh <hook-state-dir>                       # derive from event
#   agent-hook.sh <hook-state-dir> <status> <detail>     # matcher-encoded
#     (Notification entries pass a pre-mapped status/detail, since the
#      matcher — permission_prompt / idle_prompt / agent_needs_input /
#      notification_type — already encodes the mapping)
#   agent-hook.sh <hook-state-dir> --event <name>        # explicit event
#     (Copilot camelCase payloads omit the event name, so the installed
#      command passes it explicitly)
set -u

dir="${1:-}"
[ -n "$dir" ] || exit 0

in="$(cat)"

# Take the last field match (input is compact JSON, so there is exactly
# one; the greedy .* lands on the innermost occurrence, which is what
# Windsurf's nested tool_info needs for cwd). The %%${nl}* guard covers
# pretty-printed multi-line input.
field() {
  v="$(printf '%s' "$in" | sed -n "s/.*\"$1\": *\"\([^\"]*\)\".*/\1/p")"
  printf '%s' "${v%%$'\n'*}"
}

# Session key: snake (Claude/Codex/Cursor), camelCase (Grok/Copilot), or
# trajectory_id (Windsurf conversations).
session="$(field session_id)"
[ -n "$session" ] || session="$(field sessionId)"
[ -n "$session" ] || session="$(field trajectory_id)"
[ -n "$session" ] || exit 0

status=""
detail=""
event=""
if [ "${2:-}" = "--event" ]; then
  event="${3:-}"
elif [ -n "${2:-}" ]; then
  status="$2"
  detail="${3:-}"
fi

if [ -z "$event" ] && [ -z "$status" ]; then
  # Event key: snake, camelCase (Grok), or Windsurf's agent_action_name.
  event="$(field hook_event_name)"
  [ -n "$event" ] || event="$(field hookEventName)"
  [ -n "$event" ] || event="$(field agent_action_name)"
fi

if [ -z "$status" ]; then
  # Tool key: snake (Claude/Codex/Cursor), camelCase (Grok/Copilot), or
  # derived from the Windsurf event name (pre_run_command → run_command).
  # Derivation fires only for snake pre_*/post_* names that encode a tool;
  # PascalCase/camelCase events (PreToolUse, afterAgentResponse) leave the
  # tool empty so the fallback shows "running".
  tool="$(field tool_name)"
  [ -n "$tool" ] || tool="$(field toolName)"
  if [ -z "$tool" ]; then
    case "$event" in
      pre_*|post_*) tool="${event#pre_}"; tool="${tool#post_}" ;;
    esac
  fi

  case "$event" in
    SessionEnd|sessionEnd|session_end) rm -f "$dir/$session.json"; exit 0 ;;
    SessionStart|sessionStart|Setup|session_start) status=idle; detail=started ;;
    UserPromptSubmit|beforeSubmitPrompt|userPromptSubmitted|user_prompt_submit|pre_user_prompt|UserPromptExpansion) status=idle; detail=prompted ;;
    PreToolUse|preToolUse|pre_tool_use|pre_run_command|pre_write_code|pre_read_code|pre_mcp_tool_use|beforeShellExecution|beforeMCPExecution|beforeReadFile) status=working; detail="tool:${tool:-running}" ;;
    PostToolUse|postToolUse|post_tool_use|afterShellExecution|afterMCPExecution|afterFileEdit|post_run_command|post_write_code|post_read_code|post_mcp_tool_use|afterAgentResponse|afterAgentThought|post_setup_worktree) status=working; detail="done:${tool:-running}" ;;
    PostToolUseFailure|postToolUseFailure|post_tool_use_failure) status=working; detail="failed:${tool:-running}" ;;
    PermissionRequest|permissionRequest|PermissionDenied|permissionDenied|permission_denied) status=blocked; detail="permission:${tool:-unknown}" ;;
    Elicitation) status=blocked; detail=needs:input ;;
    ElicitationResult) status=working; detail=elicitation:answered ;;
    Stop|stop|agentStop) status=idle; detail=turn-complete ;;
    StopFailure|stop_failure) status=idle; detail=api-error ;;
    post_cascade_response) status=idle; detail=responded ;;
    post_cascade_response_with_transcript) status=working; detail=transcript ;;
    TeammateIdle) status=idle; detail=teammate-idle ;;
    errorOccurred|error_occurred) status=idle; detail=error ;;
    PostToolBatch) status=working; detail="batch" ;;
    SubagentStart|subagentStart|subagent_start|SubagentStop|subagentStop|subagent_stop) status=working; detail=subagent ;;
    PreCompact|preCompact|pre_compact|PostCompact|post_compact) status=working; detail=compacting ;;
    TaskCreated|TaskCompleted) status=working; detail=task ;;
    WorktreeCreate|WorktreeRemove) status=working; detail=worktree ;;
    MessageDisplay) status=working; detail=streaming ;;
    InstructionsLoaded|ConfigChange|CwdChanged|DirectoryAdded|FileChanged) status=idle; detail="$event" ;;
    pre_*) status=working; detail="tool:${tool:-running}" ;;
    post_*) status=working; detail="done:${tool:-running}" ;;
    *) status=idle; detail=started ;;
  esac
fi

cwd="$(field cwd)"
mkdir -p "$dir"
printf '{"status":"%s","detail":"%s","cwd":"%s","ts":%s}\n' \
  "$status" "$detail" "$cwd" "$(date +%s)" \
  > "$dir/$session.json.tmp" && mv "$dir/$session.json.tmp" "$dir/$session.json"
exit 0
