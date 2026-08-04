package main

import (
	"encoding/json"
	"os"
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

	removed, err := stripHooks(settings, script)
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

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
