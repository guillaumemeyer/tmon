package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// testHooksEnv points HOME, TMON_BIN_DIR and TMON_HOOK_STATE_DIR at temp
// dirs so the install/remove commands never touch the real machine.
func testHooksEnv(t *testing.T) (home, plugin string) {
	t.Helper()
	home = t.TempDir()
	plugin = filepath.Join(t.TempDir(), "tmon")
	t.Setenv("HOME", home)
	t.Setenv("TMON_BIN_DIR", filepath.Join(plugin, "bin"))
	t.Setenv("TMON_HOOK_STATE_DIR", filepath.Join(plugin, "state", "hooks"))
	return home, plugin
}

func TestHooksInstallRemoveEndToEnd(t *testing.T) {
	home, plugin := testHooksEnv(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	writeTestFile(t, settings, "{\n  \"skipDangerousModePermissionPrompt\": true\n}\n")

	if code := hooksInstall("claude"); code != 0 {
		t.Fatalf("hooksInstall = %d, want 0", code)
	}

	// Script extracted into the plugin dir.
	script := filepath.Join(plugin, "hooks", "agent-hook.sh")
	if fi, err := os.Stat(script); err != nil || fi.IsDir() {
		t.Fatalf("hook script not written: %v", err)
	}

	// Backup of the original settings created.
	if _, err := os.Stat(settings + ".tmon.bak"); err != nil {
		t.Fatalf("backup missing: %v", err)
	}

	// Settings merged; unrelated field preserved; one group per event.
	root := readSettings(settings, false)
	if _, ok := root["skipDangerousModePermissionPrompt"]; !ok {
		t.Error("unrelated settings field lost during merge")
	}
	hooks := readHooks(root)
	for _, ev := range claudeTarget.events {
		if _, ok := hooks[ev.event]; !ok {
			t.Errorf("no hooks entry for %s", ev.event)
		}
	}

	// Idempotent: reinstalling adds nothing.
	if code := hooksInstall("claude"); code != 0 {
		t.Fatalf("reinstall = %d, want 0", code)
	}
	data, _ := os.ReadFile(settings)
	if got := strings.Count(string(data), script); got != len(claudeTarget.events) {
		t.Errorf("script referenced %d times after reinstall, want %d", got, len(claudeTarget.events))
	}

	// Status reports installed.
	if ok, err := hooksInstalled(&claudeTarget); err != nil || !ok {
		t.Errorf("hooksInstalled = %v, %v; want true", ok, err)
	}

	// Remove strips our entries, keeps the unrelated field.
	if code := hooksRemove("claude"); code != 0 {
		t.Fatalf("hooksRemove = %d, want 0", code)
	}
	root = readSettings(settings, false)
	if _, ok := root["skipDangerousModePermissionPrompt"]; !ok {
		t.Error("unrelated settings field lost during remove")
	}
	if len(readHooks(root)) != 0 {
		t.Errorf("hooks remain after remove: %v", readHooks(root))
	}
	if ok, _ := hooksInstalled(&claudeTarget); ok {
		t.Error("hooksInstalled = true after remove")
	}

	// Remove again is a no-op success.
	if code := hooksRemove("claude"); code != 0 {
		t.Fatalf("second remove = %d, want 0", code)
	}
}

func TestHooksInstallCreatesSettingsIfMissing(t *testing.T) {
	home, plugin := testHooksEnv(t)
	_ = plugin
	if code := hooksInstall("claude"); code != 0 {
		t.Fatalf("hooksInstall = %d, want 0", code)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	if _, err := os.Stat(settings); err != nil {
		t.Fatalf("settings.json not created: %v", err)
	}
}

func TestMergePreservesExistingHookGroups(t *testing.T) {
	settings := filepath.Join(t.TempDir(), "settings.json")
	writeTestFile(t, settings,
		`{"hooks":{"PreToolUse":[{"matcher":"Edit|Write","hooks":[{"type":"command","command":"prettier --write"}]}]}}`)
	script := "/plugin/hooks/agent-hook.sh"
	state := "/plugin/state/hooks/claude"

	if err := mergeHooks(settings, script, state, &claudeTarget); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(settings)
	if !strings.Contains(string(data), "prettier") {
		t.Error("existing hook group lost during merge")
	}
	if !strings.Contains(string(data), script) {
		t.Error("tmon hook groups missing after merge")
	}

	removed, err := stripHooks(settings, script, false)
	if err != nil || !removed {
		t.Fatalf("stripHooks = removed:%v err:%v, want true,nil", removed, err)
	}
	data, _ = os.ReadFile(settings)
	if strings.Contains(string(data), script) {
		t.Error("tmon hook groups remain after strip")
	}
	if !strings.Contains(string(data), "prettier") {
		t.Error("existing hook group lost during strip")
	}
}

func TestHooksUnknownAgent(t *testing.T) {
	if code := hooksInstall("not-an-agent"); code == 0 {
		t.Error("hooksInstall of unknown agent succeeded")
	}
}

// ─── Prime extension hook ───────────────────────────────────────────────────

func TestPrimeExtensionTemplatePlaceholder(t *testing.T) {
	if got := strings.Count(primeExtensionTemplate, "__TMON_HOOK_STATE_DIR__"); got != 1 {
		t.Fatalf("template has %d placeholders, want exactly 1", got)
	}
}

func TestPrimeHooksInstallRemoveEndToEnd(t *testing.T) {
	home, plugin := testHooksEnv(t)
	ext := filepath.Join(home, ".prime", "agent", "extensions", "tmon-status.ts")
	state := filepath.Join(plugin, "state", "hooks", "prime")

	if code := primeHooksInstall(); code != 0 {
		t.Fatalf("primeHooksInstall = %d, want 0", code)
	}
	if _, err := os.Stat(ext); err != nil {
		t.Fatalf("extension not written: %v", err)
	}
	data, err := os.ReadFile(ext)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "__TMON_HOOK_STATE_DIR__") {
		t.Error("placeholder not replaced in installed extension")
	}
	// The state dir must be rendered as a valid TS string literal
	// (quotes from the JSON marshal, not doubled template quotes).
	if !strings.Contains(string(data), "const STATE_DIR = \""+state+"\";") {
		t.Errorf("state dir not baked in as a string literal; want `const STATE_DIR = %q;`\n%s", state, data)
	}
	if !primeHooksInstalled() {
		t.Error("primeHooksInstalled = false after install")
	}

	// Idempotent reinstall.
	if code := primeHooksInstall(); code != 0 {
		t.Fatalf("reinstall = %d, want 0", code)
	}

	// Remove deletes the extension and the tmon-owned state dir.
	if code := primeHooksRemove(); code != 0 {
		t.Fatalf("primeHooksRemove = %d, want 0", code)
	}
	if _, err := os.Stat(ext); !os.IsNotExist(err) {
		t.Errorf("extension still present after remove: %v", err)
	}
	if primeHooksInstalled() {
		t.Error("primeHooksInstalled = true after remove")
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Errorf("state dir still present after remove: %v", err)
	}

	// Remove again is a no-op success.
	if code := primeHooksRemove(); code != 0 {
		t.Fatalf("second remove = %d, want 0", code)
	}
}

func TestPrimeHooksCustomAgentDir(t *testing.T) {
	home, _ := testHooksEnv(t)
	_ = home
	agentDir := filepath.Join(t.TempDir(), "prime-agent-home")
	t.Setenv("PRIME_AGENT_CODING_AGENT_DIR", agentDir)

	if code := primeHooksInstall(); code != 0 {
		t.Fatalf("primeHooksInstall = %d, want 0", code)
	}
	ext := filepath.Join(agentDir, "extensions", "tmon-status.ts")
	if _, err := os.Stat(ext); err != nil {
		t.Errorf("extension not written to PRIME_AGENT_CODING_AGENT_DIR: %v", err)
	}
	if code := primeHooksRemove(); code != 0 {
		t.Fatalf("primeHooksRemove = %d, want 0", code)
	}
}

func TestStripJSONCPreservesURLs(t *testing.T) {
	in := `{
  // copilot settings
  "proxy": "https://example.com/api", /* keep this */
  "hooks": { "sessionStart": [ { "hooks": [ { "type": "command", "command": "x" } ] } ] }
}`
	got := string(stripJSONC([]byte(in)))
	if !strings.Contains(got, `"proxy": "https://example.com/api"`) {
		t.Errorf("URL mangled by comment strip: %s", got)
	}
	if strings.Contains(got, "copilot settings") {
		t.Errorf("line comment not stripped: %s", got)
	}
	if strings.Contains(got, "keep this") {
		t.Errorf("block comment not stripped: %s", got)
	}
	// Result must parse as JSON.
	var v map[string]any
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Errorf("stripped output not valid JSON: %v\n%s", err, got)
	}
}

func TestCodexTargetWritesMatcherlessGroups(t *testing.T) {
	home, plugin := testHooksEnv(t)
	_ = plugin
	if code := hooksInstall("codex"); code != 0 {
		t.Fatalf("hooksInstall codex = %d, want 0", code)
	}
	settings := filepath.Join(home, ".codex", "hooks.json")
	root := readSettings(settings, false)
	hooks := readHooks(root)
	for _, ev := range codexTarget.events {
		if _, ok := hooks[ev.event]; !ok {
			t.Errorf("no hooks entry for codex %s", ev.event)
		}
	}
	data, _ := os.ReadFile(settings)
	if strings.Contains(string(data), "matcher") {
		t.Errorf("codex hooks.json contains a matcher field, which codex may reject:\n%s", data)
	}
	if strings.Contains(string(data), "agent-hook.sh") {
		// good — command present
	} else {
		t.Error("codex hooks.json missing the hook command")
	}
}

func TestHooksStatusShowsAllTargets(t *testing.T) {
	home, _ := testHooksEnv(t)
	_ = home
	if code := hooksStatus(); code != 0 {
		t.Fatalf("hooksStatus = %d, want 0", code)
	}
}

// fakeBinary drops an executable named name into dir and returns dir.
func fakeBinary(t *testing.T, dir, name string) {
	t.Helper()
	p := filepath.Join(dir, name)
	writeTestFile(t, p, "#!/bin/sh\n")
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestHooksAutoInstallsPresentAgents(t *testing.T) {
	home, _ := testHooksEnv(t)

	// Only this dir is on PATH, so the test machine's real agents (e.g. a
	// claude on the user's PATH) can't leak into the assertions.
	bin := t.TempDir()
	fakeBinary(t, bin, "claude")
	fakeBinary(t, bin, "cursor-agent")
	fakeBinary(t, bin, "grok")
	fakeBinary(t, bin, "prime-agent")
	t.Setenv("PATH", bin)

	// copilot has no binary here but its config file exists.
	writeTestFile(t, filepath.Join(home, ".copilot", "settings.json"), "{}\n")

	if code := hooksAuto(); code != 0 {
		t.Fatalf("hooksAuto = %d, want 0", code)
	}
	if ok, _ := hooksInstalled(&claudeTarget); !ok {
		t.Error("claude hooks not installed by auto (binary on PATH)")
	}
	if ok, _ := hooksInstalled(&cursorTarget); !ok {
		t.Error("cursor hooks not installed by auto (cursor-agent on PATH)")
	}
	if ok, _ := hooksInstalled(&grokTarget); !ok {
		t.Error("grok hooks not installed by auto (grok on PATH)")
	}
	if !primeHooksInstalled() {
		t.Error("prime hooks not installed by auto (prime-agent on PATH)")
	}
	if ok, _ := hooksInstalled(&copilotTarget); !ok {
		t.Error("copilot hooks not installed by auto (config file present)")
	}
	for name, target := range map[string]*hookTarget{
		"codex":    &codexTarget,
		"windsurf": &windsurfTarget,
	} {
		if ok, _ := hooksInstalled(target); ok {
			t.Errorf("%s hooks installed despite no binary or config", name)
		}
	}
}

func TestHooksAutoNothingFound(t *testing.T) {
	testHooksEnv(t)
	t.Setenv("PATH", t.TempDir()) // empty dir: no agents resolvable

	if code := hooksAuto(); code != 0 {
		t.Fatalf("hooksAuto = %d, want 0", code)
	}
	for name, target := range hookTargets {
		if ok, _ := hooksInstalled(target); ok {
			t.Errorf("%s hooks installed despite no agent present", name)
		}
	}
	if primeHooksInstalled() {
		t.Error("prime hooks installed despite no agent present")
	}
}

func TestHooksAutoSkipsInstalled(t *testing.T) {
	home, plugin := testHooksEnv(t)
	_ = home
	if code := hooksInstall("claude"); code != 0 {
		t.Fatalf("hooksInstall = %d, want 0", code)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	script := filepath.Join(plugin, "hooks", "agent-hook.sh")
	before, _ := os.ReadFile(settings)

	if code := hooksAuto(); code != 0 {
		t.Fatalf("hooksAuto = %d, want 0", code)
	}
	after, _ := os.ReadFile(settings)
	if got, want := strings.Count(string(after), script), strings.Count(string(before), script); got != want {
		t.Errorf("script references changed by auto: %d → %d, want unchanged", want, got)
	}
}

// ─── agent-hook.sh coverage ─────────────────────────────────────────────────

// hookCoverageCase is one row of the event→status mapping table (plan §3):
// the event name as delivered by the agent payload, the payload format
// ("snake" = Claude/Codex/Cursor keys, "camel" = Grok/Copilot keys,
// "windsurf" = trajectory_id/agent_action_name), the tool name expected in
// the payload, and the state file contents the script must write. deleted
// marks events that remove the session file instead of writing it.
type hookCoverageCase struct {
	name    string
	format  string
	event   string
	tool    string
	status  string
	detail  string
	deleted bool
}

// agentHookCoverage is the full §3 union. Every installed target event must
// appear here (checked by TestEveryInstalledEventHasCoverage), so no event
// can silently fall to the wrong default.
var agentHookCoverage = []hookCoverageCase{
	// Claude/Codex PascalCase (snake_case payload keys).
	{"claude session start", "snake", "SessionStart", "", "idle", "started", false},
	{"claude setup", "snake", "Setup", "", "idle", "started", false},
	{"claude prompt", "snake", "UserPromptSubmit", "", "idle", "prompted", false},
	{"claude prompt expansion", "snake", "UserPromptExpansion", "", "idle", "prompted", false},
	{"claude pre tool", "snake", "PreToolUse", "run_command", "working", "tool:run_command", false},
	{"claude post tool", "snake", "PostToolUse", "run_command", "working", "done:run_command", false},
	{"claude tool failure", "snake", "PostToolUseFailure", "run_command", "working", "failed:run_command", false},
	{"claude post batch", "snake", "PostToolBatch", "", "working", "batch", false},
	{"claude permission request", "snake", "PermissionRequest", "write", "blocked", "permission:write", false},
	{"claude permission denied", "snake", "PermissionDenied", "write", "blocked", "permission:write", false},
	{"claude elicitation", "snake", "Elicitation", "", "blocked", "needs:input", false},
	{"claude elicitation result", "snake", "ElicitationResult", "", "working", "elicitation:answered", false},
	{"claude stop", "snake", "Stop", "", "idle", "turn-complete", false},
	{"claude stop failure", "snake", "StopFailure", "", "idle", "api-error", false},
	{"claude subagent start", "snake", "SubagentStart", "", "working", "subagent", false},
	{"claude subagent stop", "snake", "SubagentStop", "", "working", "subagent", false},
	{"claude task created", "snake", "TaskCreated", "", "working", "task", false},
	{"claude task completed", "snake", "TaskCompleted", "", "working", "task", false},
	{"claude worktree create", "snake", "WorktreeCreate", "", "working", "worktree", false},
	{"claude worktree remove", "snake", "WorktreeRemove", "", "working", "worktree", false},
	{"claude teammate idle", "snake", "TeammateIdle", "", "idle", "teammate-idle", false},
	{"claude pre compact", "snake", "PreCompact", "", "working", "compacting", false},
	{"claude post compact", "snake", "PostCompact", "", "working", "compacting", false},
	{"claude streaming", "snake", "MessageDisplay", "", "working", "streaming", false},
	{"claude instructions loaded", "snake", "InstructionsLoaded", "", "idle", "InstructionsLoaded", false},
	{"claude config change", "snake", "ConfigChange", "", "idle", "ConfigChange", false},
	{"claude cwd changed", "snake", "CwdChanged", "", "idle", "CwdChanged", false},
	{"claude directory added", "snake", "DirectoryAdded", "", "idle", "DirectoryAdded", false},
	{"claude file changed", "snake", "FileChanged", "", "idle", "FileChanged", false},
	{"claude session end", "snake", "SessionEnd", "", "", "", true},
	{"claude before shell", "snake", "beforeShellExecution", "run_command", "working", "tool:run_command", false},
	{"claude before mcp", "snake", "beforeMCPExecution", "mcp__tools", "working", "tool:mcp__tools", false},
	{"claude before read", "snake", "beforeReadFile", "read_file", "working", "tool:read_file", false},
	{"claude after shell", "snake", "afterShellExecution", "run_command", "working", "done:run_command", false},
	{"claude after mcp", "snake", "afterMCPExecution", "mcp__tools", "working", "done:mcp__tools", false},
	{"claude after edit", "snake", "afterFileEdit", "write", "working", "done:write", false},
	{"claude after response", "snake", "afterAgentResponse", "", "working", "done:running", false},
	{"claude after thought", "snake", "afterAgentThought", "", "working", "done:running", false},

	// Cursor camelCase names (payload keys stay snake_case).
	{"cursor session start", "snake", "sessionStart", "", "idle", "started", false},
	{"cursor before submit", "snake", "beforeSubmitPrompt", "", "idle", "prompted", false},
	{"cursor pre tool", "snake", "preToolUse", "read_file", "working", "tool:read_file", false},
	{"cursor post tool", "snake", "postToolUse", "read_file", "working", "done:read_file", false},
	{"cursor tool failure", "snake", "postToolUseFailure", "read_file", "working", "failed:read_file", false},
	{"cursor subagent start", "snake", "subagentStart", "", "working", "subagent", false},
	{"cursor subagent stop", "snake", "subagentStop", "", "working", "subagent", false},
	{"cursor pre compact", "snake", "preCompact", "", "working", "compacting", false},
	{"cursor stop", "snake", "stop", "", "idle", "turn-complete", false},
	{"cursor session end", "snake", "sessionEnd", "", "", "", true},

	// Grok camelCase payload keys with snake_case event values.
	{"grok session start", "camel", "session_start", "", "idle", "started", false},
	{"grok prompt", "camel", "user_prompt_submit", "", "idle", "prompted", false},
	{"grok pre tool", "camel", "pre_tool_use", "read_file", "working", "tool:read_file", false},
	{"grok post tool", "camel", "post_tool_use", "read_file", "working", "done:read_file", false},
	{"grok tool failure", "camel", "post_tool_use_failure", "read_file", "working", "failed:read_file", false},
	{"grok permission denied", "camel", "permission_denied", "write", "blocked", "permission:write", false},
	{"grok stop", "camel", "stop", "", "idle", "turn-complete", false},
	{"grok stop failure", "camel", "stop_failure", "", "idle", "api-error", false},
	{"grok subagent start", "camel", "subagent_start", "", "working", "subagent", false},
	{"grok subagent stop", "camel", "subagent_stop", "", "working", "subagent", false},
	{"grok pre compact", "camel", "pre_compact", "", "working", "compacting", false},
	{"grok post compact", "camel", "post_compact", "", "working", "compacting", false},
	{"grok session end", "camel", "session_end", "", "", "", true},

	// Copilot camelCase event names (delivered via --event, but the script
	// also maps them when they appear in the payload).
	{"copilot session start", "camel", "sessionStart", "", "idle", "started", false},
	{"copilot prompt", "camel", "userPromptSubmitted", "", "idle", "prompted", false},
	{"copilot pre tool", "camel", "preToolUse", "read_file", "working", "tool:read_file", false},
	{"copilot post tool", "camel", "postToolUse", "read_file", "working", "done:read_file", false},
	{"copilot tool failure", "camel", "postToolUseFailure", "read_file", "working", "failed:read_file", false},
	{"copilot permission request", "camel", "permissionRequest", "read_file", "blocked", "permission:read_file", false},
	{"copilot agent stop", "camel", "agentStop", "", "idle", "turn-complete", false},
	{"copilot subagent start", "camel", "subagentStart", "", "working", "subagent", false},
	{"copilot subagent stop", "camel", "subagentStop", "", "working", "subagent", false},
	{"copilot pre compact", "camel", "preCompact", "", "working", "compacting", false},
	{"copilot error", "camel", "errorOccurred", "", "idle", "error", false},
	{"copilot session end", "camel", "sessionEnd", "", "", "", true},
	{"error occurred snake", "snake", "error_occurred", "", "idle", "error", false},

	// Windsurf trajectory_id + agent_action_name.
	{"windsurf pre prompt", "windsurf", "pre_user_prompt", "", "idle", "prompted", false},
	{"windsurf pre read", "windsurf", "pre_read_code", "", "working", "tool:read_code", false},
	{"windsurf post read", "windsurf", "post_read_code", "", "working", "done:read_code", false},
	{"windsurf pre write", "windsurf", "pre_write_code", "", "working", "tool:write_code", false},
	{"windsurf post write", "windsurf", "post_write_code", "", "working", "done:write_code", false},
	{"windsurf pre run", "windsurf", "pre_run_command", "", "working", "tool:run_command", false},
	{"windsurf post run", "windsurf", "post_run_command", "", "working", "done:run_command", false},
	{"windsurf pre mcp", "windsurf", "pre_mcp_tool_use", "", "working", "tool:mcp_tool_use", false},
	{"windsurf post mcp", "windsurf", "post_mcp_tool_use", "", "working", "done:mcp_tool_use", false},
	{"windsurf setup worktree", "windsurf", "post_setup_worktree", "", "working", "done:setup_worktree", false},
	{"windsurf cascade response", "windsurf", "post_cascade_response", "", "idle", "responded", false},
	{"windsurf cascade transcript", "windsurf", "post_cascade_response_with_transcript", "", "working", "transcript", false},

	// Fallbacks: unknown pre_*/post_* derive the tool from the event name.
	{"unknown pre falls to tool", "snake", "pre_unknown_tool", "", "working", "tool:unknown_tool", false},
	{"unknown post falls to done", "snake", "post_unknown_tool", "", "working", "done:unknown_tool", false},
	{"unknown event defaults idle", "snake", "totally_unknown", "", "idle", "started", false},
	{"pre tool without name", "snake", "PreToolUse", "", "working", "tool:running", false},
}

// runHookScript execs the embedded agent-hook.sh with the given stdin and
// args against stateDir, returning combined output.
func runHookScript(t *testing.T, stateDir, stdin string, args ...string) (string, error) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "agent-hook.sh")
	if err := os.WriteFile(script, []byte(agentHookScript), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", append([]string{script, stateDir}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// hookPayload builds the stdin JSON for one lifecycle event in the payload
// format the given agent family uses. format "arg" omits the event key —
// the event arrives via the --event argument (Copilot).
func hookPayload(format, session, event, tool, cwd string) string {
	m := map[string]string{"cwd": cwd}
	switch format {
	case "snake":
		m["session_id"] = session
		m["hook_event_name"] = event
		if tool != "" {
			m["tool_name"] = tool
		}
	case "camel":
		m["sessionId"] = session
		m["hookEventName"] = event
		if tool != "" {
			m["toolName"] = tool
		}
	case "windsurf":
		m["trajectory_id"] = session
		m["agent_action_name"] = event
	case "arg":
		m["sessionId"] = session
		if tool != "" {
			m["toolName"] = tool
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// readHookStateFile parses one session state file written by the script.
func readHookStateFile(t *testing.T, path string) (status, detail, cwd string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("session file missing: %v", err)
	}
	var got struct {
		Status string `json:"status"`
		Detail string `json:"detail"`
		CWD    string `json:"cwd"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("bad state json: %v\n%s", err, data)
	}
	return got.Status, got.Detail, got.CWD
}

func TestAgentHookScriptEventCoverage(t *testing.T) {
	for _, c := range agentHookCoverage {
		t.Run(c.name, func(t *testing.T) {
			stateDir := t.TempDir()
			payload := hookPayload(c.format, "sess-1", c.event, c.tool, "/home/u/code/tmon")
			if out, err := runHookScript(t, stateDir, payload); err != nil {
				t.Fatalf("hook script failed: %v\n%s", err, out)
			}
			path := filepath.Join(stateDir, "sess-1.json")
			if c.deleted {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("session file exists after %s, want deleted", c.event)
				}
				return
			}
			status, detail, cwd := readHookStateFile(t, path)
			if status != c.status || detail != c.detail {
				t.Errorf("state = %q/%q, want %q/%q", status, detail, c.status, c.detail)
			}
			if cwd != "/home/u/code/tmon" {
				t.Errorf("cwd = %q, want payload cwd", cwd)
			}
		})
	}
}

func TestAgentHookScriptMatcherEncoded(t *testing.T) {
	cases := []struct {
		status, detail string
	}{
		{"blocked", "waiting:permission"}, // copilot permission_prompt
		{"blocked", "needs:input"},        // copilot elicitation_dialog / claude agent_needs_input
		{"idle", "idle"},                  // copilot agent_idle / claude idle_prompt
		{"working", "notified"},           // copilot shell/agent completions
	}
	for _, c := range cases {
		t.Run(c.status+"_"+c.detail, func(t *testing.T) {
			stateDir := t.TempDir()
			payload := hookPayload("arg", "sess-1", "", "", "/home/u/code/tmon")
			if out, err := runHookScript(t, stateDir, payload, c.status, c.detail); err != nil {
				t.Fatalf("hook script failed: %v\n%s", err, out)
			}
			status, detail, _ := readHookStateFile(t, filepath.Join(stateDir, "sess-1.json"))
			if status != c.status || detail != c.detail {
				t.Errorf("state = %q/%q, want %q/%q", status, detail, c.status, c.detail)
			}
		})
	}
}

func TestAgentHookScriptCopilotEventArg(t *testing.T) {
	cases := []struct {
		event        string
		status, want string
		deleted      bool
	}{
		{"sessionStart", "idle", "started", false},
		{"userPromptSubmitted", "idle", "prompted", false},
		{"preToolUse", "working", "tool:read_file", false},
		{"postToolUse", "working", "done:read_file", false},
		{"postToolUseFailure", "working", "failed:read_file", false},
		{"permissionRequest", "blocked", "permission:read_file", false},
		{"agentStop", "idle", "turn-complete", false},
		{"subagentStart", "working", "subagent", false},
		{"subagentStop", "working", "subagent", false},
		{"preCompact", "working", "compacting", false},
		{"errorOccurred", "idle", "error", false},
		{"sessionEnd", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.event, func(t *testing.T) {
			stateDir := t.TempDir()
			payload := hookPayload("arg", "sess-1", "", "read_file", "/home/u/code/tmon")
			if out, err := runHookScript(t, stateDir, payload, "--event", c.event); err != nil {
				t.Fatalf("hook script failed: %v\n%s", err, out)
			}
			path := filepath.Join(stateDir, "sess-1.json")
			if c.deleted {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("session file exists after %s, want deleted", c.event)
				}
				return
			}
			status, detail, _ := readHookStateFile(t, path)
			if status != c.status || detail != c.want {
				t.Errorf("state = %q/%q, want %q/%q", status, detail, c.status, c.want)
			}
		})
	}
}

// grokSnakeCase converts a PascalCase grok event name to the snake_case
// value grok actually delivers in hookEventName ("PreToolUse" →
// "pre_tool_use").
func grokSnakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

// TestEveryInstalledEventHasCoverage is the structural guard for 100%
// coverage: every event every target installs (that reaches the script —
// matcher-encoded Notification entries never do) must have a coverage row,
// so a new install can never silently fall to the wrong default. Grok
// payloads deliver snake_case values, so each grok event's snake form must
// be covered too.
func TestEveryInstalledEventHasCoverage(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range agentHookCoverage {
		covered[c.event] = true
	}
	for name, target := range hookTargets {
		for _, ev := range target.events {
			if ev.status != "" {
				continue // matcher-encoded: the command passes status/detail
			}
			if !covered[ev.event] {
				t.Errorf("%s installs %q with no coverage row", name, ev.event)
			}
		}
	}
	for _, ev := range grokTarget.events {
		if snake := grokSnakeCase(ev.event); !covered[snake] {
			t.Errorf("grok snake value %q (from %s) has no coverage row", snake, ev.event)
		}
	}
}

// ─── grok dir-kind target ───────────────────────────────────────────────────

func TestGrokDirTargetInstallRemove(t *testing.T) {
	home, plugin := testHooksEnv(t)
	dir := filepath.Join(home, ".grok", "hooks")
	stateDir := filepath.Join(plugin, "state", "hooks", "grok")

	if code := hooksInstall("grok"); code != 0 {
		t.Fatalf("hooksInstall grok = %d, want 0", code)
	}
	script := filepath.Join(dir, "tmon-grok-hook.sh")
	if fi, err := os.Stat(script); err != nil || fi.IsDir() {
		t.Fatalf("grok hook script not written: %v", err)
	}
	jsonPath := filepath.Join(dir, "tmon-grok.json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("tmon-grok.json not written: %v", err)
	}

	// Shape: {"hooks": {Event: [{hooks:[{type:"command",command:"…"}]}]}},
	// one group per installed event, command relative to the JSON file.
	var root struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("tmon-grok.json not valid JSON: %v\n%s", err, data)
	}
	if len(root.Hooks) != len(grokTarget.events) {
		t.Errorf("events = %d, want %d", len(root.Hooks), len(grokTarget.events))
	}
	wantCmd := "./tmon-grok-hook.sh " + stateDir
	for _, ev := range grokTarget.events {
		groups := root.Hooks[ev.event]
		if len(groups) != 1 {
			t.Errorf("%s: groups = %d, want 1", ev.event, len(groups))
			continue
		}
		hooks := groups[0].Hooks
		if len(hooks) != 1 || hooks[0].Type != "command" || hooks[0].Command != wantCmd {
			t.Errorf("%s: hooks = %+v, want command %q", ev.event, hooks, wantCmd)
		}
	}
	if strings.Contains(string(data), "matcher") {
		t.Errorf("tmon-grok.json contains a matcher field, which grok rejects:\n%s", data)
	}
	// No backup: the two files are tmon's own.
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.bak")); len(matches) != 0 {
		t.Errorf("backup files created in grok hooks dir: %v", matches)
	}

	// Idempotent reinstall: byte-identical output.
	if code := hooksInstall("grok"); code != 0 {
		t.Fatalf("reinstall = %d, want 0", code)
	}
	again, _ := os.ReadFile(jsonPath)
	if string(again) != string(data) {
		t.Error("reinstall changed tmon-grok.json")
	}

	// Status reports installed.
	if ok, err := hooksInstalled(&grokTarget); err != nil || !ok {
		t.Errorf("hooksInstalled = %v, %v; want true", ok, err)
	}

	// Remove deletes exactly the two tmon files, keeps foreign ones.
	writeTestFile(t, filepath.Join(dir, "other.json"), "{}\n")
	if code := hooksRemove("grok"); code != 0 {
		t.Fatalf("hooksRemove = %d, want 0", code)
	}
	for _, f := range []string{"tmon-grok.json", "tmon-grok-hook.sh"} {
		if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
			t.Errorf("%s still present after remove", f)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "other.json")); err != nil {
		t.Errorf("foreign hook file lost during remove: %v", err)
	}
	if ok, _ := hooksInstalled(&grokTarget); ok {
		t.Error("hooksInstalled = true after remove")
	}

	// Remove again is a no-op success.
	if code := hooksRemove("grok"); code != 0 {
		t.Fatalf("second remove = %d, want 0", code)
	}
}

func TestGrokDirTargetUnknownAgent(t *testing.T) {
	testHooksEnv(t)
	if code := hooksInstall("grokz"); code == 0 {
		t.Error("hooksInstall of unknown agent succeeded")
	}
}

// ─── copilot flat entries ───────────────────────────────────────────────────

func TestCopilotFlatEntries(t *testing.T) {
	home, plugin := testHooksEnv(t)
	if code := hooksInstall("copilot"); code != 0 {
		t.Fatalf("hooksInstall copilot = %d, want 0", code)
	}
	settings := filepath.Join(home, ".copilot", "settings.json")
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(plugin, "hooks", "agent-hook.sh")
	stateDir := filepath.Join(plugin, "state", "hooks", "copilot")

	var root struct {
		Hooks map[string][]struct {
			Type    string `json:"type"`
			Bash    string `json:"bash"`
			Matcher string `json:"matcher"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("settings.json not valid JSON: %v\n%s", err, data)
	}

	// Expected entries per event key. The four notification matchers share
	// the single "notification" key (one flat entry each), so the map has
	// 13 keys but 16 entries total.
	type flatExpect struct{ bash, matcher string }
	expected := map[string][]flatExpect{}
	for _, ev := range copilotTarget.events {
		var bash string
		if ev.status != "" {
			bash = fmt.Sprintf("%s %s %s %s", script, stateDir, ev.status, ev.detail)
		} else {
			bash = fmt.Sprintf("%s %s --event %s", script, stateDir, ev.event)
		}
		expected[ev.event] = append(expected[ev.event], flatExpect{bash, ev.matcher})
	}

	total := 0
	for key, want := range expected {
		entries := root.Hooks[key]
		total += len(entries)
		if len(entries) != len(want) {
			t.Errorf("%s: entries = %d, want %d", key, len(entries), len(want))
		}
		for _, w := range want {
			found := false
			for _, e := range entries {
				if e.Type != "command" {
					t.Errorf("%s: type = %q, want command", key, e.Type)
				}
				if e.Bash == w.bash && e.Matcher == w.matcher {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: no entry with bash %q matcher %q", key, w.bash, w.matcher)
			}
		}
	}
	if total != len(copilotTarget.events) {
		t.Errorf("total entries = %d, want %d", total, len(copilotTarget.events))
	}

	// Idempotent reinstall: no duplicate entries.
	if code := hooksInstall("copilot"); code != 0 {
		t.Fatalf("reinstall = %d, want 0", code)
	}
	again, _ := os.ReadFile(settings)
	if strings.Count(string(again), "--event ") != 12 {
		t.Errorf("--event references = %d after reinstall, want 12 (one per non-notification event)",
			strings.Count(string(again), "--event "))
	}
}

func TestCopilotFlatRemove(t *testing.T) {
	home, plugin := testHooksEnv(t)
	settings := filepath.Join(home, ".copilot", "settings.json")
	writeTestFile(t, settings, `{
  "proxy": "https://example.com/api",
  "hooks": {
    "postToolUse": [
      {"type": "command", "bash": "lint --fix"}
    ]
  }
}`)
	if code := hooksInstall("copilot"); code != 0 {
		t.Fatalf("hooksInstall = %d, want 0", code)
	}
	script := filepath.Join(plugin, "hooks", "agent-hook.sh")
	if code := hooksRemove("copilot"); code != 0 {
		t.Fatalf("hooksRemove = %d, want 0", code)
	}
	data, _ := os.ReadFile(settings)
	if strings.Contains(string(data), script) {
		t.Error("tmon copilot entries remain after remove")
	}
	if !strings.Contains(string(data), "lint --fix") {
		t.Error("foreign copilot hook lost during remove")
	}
	if !strings.Contains(string(data), "example.com") {
		t.Error("unrelated field lost during remove")
	}
	if ok, _ := hooksInstalled(&copilotTarget); ok {
		t.Error("hooksInstalled = true after remove")
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
