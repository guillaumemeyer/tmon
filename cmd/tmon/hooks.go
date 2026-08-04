package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/guillaumemeyer/tmon/internal/config"
)

//go:embed hooks/agent-hook.sh
var agentHookScript string

// cmdHooks manages opt-in hook installation for HOOKS-tier agents. Hooks
// give tmon authoritative state for agents that do not expose a readable
// state file (Claude Code, Codex, Cursor, Copilot, Windsurf). Installing
// modifies the agent's own config, so it is strictly opt-in:
// `tmon hooks install <agent>` with a backup, `tmon hooks remove <agent>`
// to strip, `tmon hooks status` to inspect.
func cmdHooks(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, hooksUsage)
		return 2
	}
	switch args[0] {
	case "install":
		if len(args) != 2 {
			fmt.Fprint(os.Stderr, hooksUsage)
			return 2
		}
		return hooksInstall(args[1])
	case "auto":
		return hooksAuto()
	case "remove":
		if len(args) != 2 {
			fmt.Fprint(os.Stderr, hooksUsage)
			return 2
		}
		return hooksRemove(args[1])
	case "status":
		return hooksStatus()
	default:
		fmt.Fprintf(os.Stderr, "tmon: unknown hooks command %q\n\n", args[0])
		fmt.Fprint(os.Stderr, hooksUsage)
		return 2
	}
}

const hooksUsage = `tmon hooks — install lifecycle hook scripts into AI agents

Hooks give tmon authoritative agent state (exact phase, running tool,
permission waits) for agents that expose no readable state file. They are
strictly opt-in: installing edits the agent's own config file.

Usage:
  tmon hooks install <agent>   Install hooks (backup + merge config)
  tmon hooks remove  <agent>   Strip tmon's hook entries from the config
  tmon hooks auto              Install hooks for every supported agent found
                               on this machine (run at plugin load unless
                               @tmon-auto-hooks is off)
  tmon hooks status            Show which agents have hooks installed

Supported agents: claude codex cursor copilot windsurf
`

// hookTarget describes one installable agent's hook wiring.
type hookTarget struct {
	name       string                 // CLI name ("claude")
	settings   func() (string, error) // absolute config file path
	events     []hookEvent            // events to install
	scriptName string                 // embedded script filename
	useMatcher bool                   // group objects carry a matcher
	jsonc      bool                   // config is JSONC (strip comments)
	binaries   []string               // CLI names used to detect the agent on this machine
}

// hookEvent is one hook entry to install. For Notification entries the
// matcher encodes the mapping, so the script gets a pre-mapped status/detail.
type hookEvent struct {
	event   string // agent event name
	matcher string
	status  string // pre-mapped status for Notification entries, else ""
	detail  string
}

// claudeTarget wires Claude Code's hooks.
var claudeTarget = hookTarget{
	name:       "claude",
	scriptName: "agent-hook.sh",
	useMatcher: true,
	binaries:   []string{"claude"},
	settings: func() (string, error) {
		return homeFile(".claude", "settings.json")
	},
	events: []hookEvent{
		{event: "SessionStart"},
		{event: "UserPromptSubmit"},
		{event: "PreToolUse"},
		{event: "PostToolUse"},
		{event: "PostToolUseFailure"},
		{event: "PermissionRequest"},
		{event: "PermissionDenied"},
		{event: "Notification", matcher: "permission_prompt", status: "blocked", detail: "waiting:permission"},
		{event: "Notification", matcher: "idle_prompt", status: "paused", detail: "paused"},
		{event: "Notification", matcher: "agent_needs_input", status: "blocked", detail: "needs:input"},
		{event: "Stop"},
		{event: "SubagentStart"},
		{event: "SubagentStop"},
		{event: "PreCompact"},
		{event: "PostCompact"},
		{event: "SessionEnd"},
	},
}

// codexTarget wires Codex CLI's hooks (~/.codex/hooks.json). Codex requires
// the hooks to be trusted in-session via /hooks before they run; that step
// is manual and documented in the README.
var codexTarget = hookTarget{
	name:       "codex",
	scriptName: "agent-hook.sh",
	useMatcher: false,
	binaries:   []string{"codex"},
	settings: func() (string, error) {
		return homeFile(".codex", "hooks.json")
	},
	events: []hookEvent{
		{event: "SessionStart"},
		{event: "PreToolUse"},
		{event: "PostToolUse"},
		{event: "PermissionRequest"},
		{event: "Stop"},
		{event: "SessionEnd"},
	},
}

// cursorTarget wires Cursor's hooks (camelCase event names).
var cursorTarget = hookTarget{
	name:       "cursor",
	scriptName: "agent-hook.sh",
	useMatcher: true,
	binaries:   []string{"cursor-agent", "cursor"},
	settings: func() (string, error) {
		return homeFile(".cursor", "hooks.json")
	},
	events: []hookEvent{
		{event: "sessionStart"},
		{event: "beforeSubmitPrompt"},
		{event: "preToolUse"},
		{event: "afterShellExecution"},
		{event: "afterFileEdit"},
		{event: "beforeMCPExecution"},
		{event: "postToolUseFailure"},
		{event: "stop"},
		{event: "sessionEnd"},
	},
}

// copilotTarget wires GitHub Copilot CLI's hooks. Its settings.json is
// JSONC, so comments are stripped before parsing.
var copilotTarget = hookTarget{
	name:       "copilot",
	scriptName: "agent-hook.sh",
	useMatcher: true,
	jsonc:      true,
	binaries:   []string{"copilot"},
	settings: func() (string, error) {
		return homeFile(".copilot", "settings.json")
	},
	events: []hookEvent{
		{event: "sessionStart"},
		{event: "preToolUse"},
		{event: "postToolUse"},
		{event: "permissionRequest"},
		{event: "agentStop"},
		{event: "sessionEnd"},
	},
}

// windsurfTarget wires Windsurf's hooks (snake_case event names).
var windsurfTarget = hookTarget{
	name:       "windsurf",
	scriptName: "agent-hook.sh",
	useMatcher: false,
	binaries:   []string{"windsurf"},
	settings: func() (string, error) {
		return homeFile(".codeium", "windsurf", "hooks.json")
	},
	events: []hookEvent{
		{event: "pre_user_prompt"},
		{event: "pre_run_command"},
		{event: "pre_write_code"},
		{event: "pre_mcp_tool_use"},
		{event: "post_cascade_response"},
		{event: "post_cascade_response_with_transcript"},
	},
}

// homeFile joins the user's home directory with the given components.
func homeFile(parts ...string) (string, error) {
	h, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{h}, parts...)...), nil
}

// userHomeDir is a seam for tests.
var userHomeDir = os.UserHomeDir

// hookTargets is the registry of installable agents.
var hookTargets = map[string]*hookTarget{
	"claude":   &claudeTarget,
	"codex":    &codexTarget,
	"cursor":   &cursorTarget,
	"copilot":  &copilotTarget,
	"windsurf": &windsurfTarget,
}

// hooksInstall writes the embedded hook script into the plugin dir and
// merges the hook entries into the agent's settings file (with a backup).
func hooksInstall(name string) int {
	cfg := config.FromEnv()
	target, ok := hookTargets[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "tmon: no hook target %q (supported: %s)\n", name, hookTargetList())
		return 2
	}

	pluginDir := filepath.Dir(cfg.BinDir)
	scriptPath := filepath.Join(pluginDir, "hooks", target.scriptName)
	stateDir := filepath.Join(cfg.HookStateDir, name)

	// 1. Extract the embedded hook script.
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "tmon: hooks install:", err)
		return 1
	}
	if err := os.WriteFile(scriptPath, []byte(agentHookScript), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "tmon: hooks install:", err)
		return 1
	}

	// 2. Backup + merge the agent config.
	settingsPath, err := target.settings()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tmon: hooks install:", err)
		return 1
	}
	backup := settingsPath + ".tmon.bak"
	if _, err := os.Stat(backup); os.IsNotExist(err) {
		if data, err := os.ReadFile(settingsPath); err == nil {
			if err := os.WriteFile(backup, data, 0o644); err != nil {
				fmt.Fprintln(os.Stderr, "tmon: hooks install: backup:", err)
				return 1
			}
		}
	}
	if err := mergeHooks(settingsPath, scriptPath, stateDir, target); err != nil {
		fmt.Fprintln(os.Stderr, "tmon: hooks install:", err)
		return 1
	}

	fmt.Printf("tmon: installed %s hooks\n  script:  %s\n  state:   %s\n  config:  %s (backup: %s)\n",
		name, scriptPath, stateDir, settingsPath, backup)
	fmt.Println("Restart your agent session for the hooks to take effect.")
	if name == "codex" {
		fmt.Println("Codex requires hook trust: accept them in-session with /hooks.")
	}
	return 0
}

// hooksRemove strips tmon's hook entries from the agent's settings file.
func hooksRemove(name string) int {
	cfg := config.FromEnv()
	target, ok := hookTargets[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "tmon: no hook target %q (supported: %s)\n", name, hookTargetList())
		return 2
	}

	pluginDir := filepath.Dir(cfg.BinDir)
	scriptPath := filepath.Join(pluginDir, "hooks", target.scriptName)

	settingsPath, err := target.settings()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tmon: hooks remove:", err)
		return 1
	}
	removed, err := stripHooks(settingsPath, scriptPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tmon: hooks remove:", err)
		return 1
	}
	if removed {
		fmt.Printf("tmon: removed %s hooks from %s\n", name, settingsPath)
		fmt.Printf("tmon: a pre-install backup remains at %s.tmon.bak\n", settingsPath)
	} else {
		fmt.Printf("tmon: no %s hooks were installed in %s\n", name, settingsPath)
	}
	return 0
}

// hooksStatus reports which agents have tmon hooks installed.
func hooksStatus() int {
	for name, target := range hookTargets {
		installed, err := hooksInstalled(target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tmon: hooks status: %s: %v\n", name, err)
			continue
		}
		if installed {
			fmt.Printf("%-8s installed\n", name)
		} else {
			fmt.Printf("%-8s not installed\n", name)
		}
	}
	return 0
}

// hooksAuto installs hooks for every supported agent found on this machine —
// a binary on PATH, or the agent's config file already present. It is the
// startup path: tmon.tmux runs it at plugin load unless @tmon-auto-hooks is
// off. Idempotent: agents already configured are skipped silently, so a
// steady-state tmux reload prints nothing.
func hooksAuto() int {
	names := make([]string, 0, len(hookTargets))
	for n := range hookTargets {
		names = append(names, n)
	}
	sort.Strings(names)

	var installed, skipped []string
	for _, name := range names {
		target := hookTargets[name]
		if ok, err := hooksInstalled(target); err == nil && ok {
			continue // already configured: silent
		}
		if !agentPresent(target) {
			skipped = append(skipped, name)
			continue
		}
		if hooksInstall(name) != 0 {
			skipped = append(skipped, name) // hooksInstall already printed the error
			continue
		}
		installed = append(installed, name)
	}

	if len(installed) > 0 {
		fmt.Printf("tmon: hooks auto: installed %s\n", strings.Join(installed, ", "))
	} else if len(skipped) == len(names) {
		fmt.Println("tmon: hooks auto: no agents found (install manually with `tmon hooks install <agent>`)")
	}
	return 0
}

// agentPresent reports whether the agent is installed on this machine: any of
// its CLI binaries resolvable on PATH, or its config file already on disk
// (a fallback for agents whose launcher lives outside PATH).
func agentPresent(target *hookTarget) bool {
	for _, b := range target.binaries {
		if p, err := exec.LookPath(b); err == nil && p != "" {
			return true
		}
	}
	if settings, err := target.settings(); err == nil {
		if _, err := os.Stat(settings); err == nil {
			return true
		}
	}
	return false
}

func hooksInstalled(target *hookTarget) (bool, error) {
	settingsPath, err := target.settings()
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return strings.Contains(string(data), target.scriptName), nil
}

func hookTargetList() string {
	names := make([]string, 0, len(hookTargets))
	for n := range hookTargets {
		names = append(names, n)
	}
	return strings.Join(names, " ")
}

// ─── settings merge (preserves all unknown fields) ──────────────────────────

// mergeHooks adds one hook group per event to the settings file, skipping
// any group whose command already references the script. Idempotent.
func mergeHooks(settingsPath, scriptPath, stateDir string, target *hookTarget) error {
	root := readSettings(settingsPath, target.jsonc)
	hooks := readHooks(root)

	for _, ev := range target.events {
		cmd := hookCommandString(scriptPath, stateDir, ev)
		if containsCommand(hooks[ev.event], cmd) {
			continue
		}
		group := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd}}}
		if target.useMatcher {
			matcher := ev.matcher
			if matcher == "" {
				matcher = "*"
			}
			group["matcher"] = matcher
		}
		b, err := json.Marshal(group)
		if err != nil {
			return err
		}
		groups := append(rawSlice(hooks[ev.event]), b)
		raw, err := json.Marshal(groups)
		if err != nil {
			return err
		}
		hooks[ev.event] = raw
	}
	return writeSettings(settingsPath, root, hooks)
}

// stripHooks removes every hook group whose command references the script.
// Returns whether anything was removed.
func stripHooks(settingsPath, scriptPath string) (bool, error) {
	root := readSettings(settingsPath, false)
	hooks := readHooks(root)
	removed := false

	for event, raw := range hooks {
		groups := rawSlice(raw)
		kept := groups[:0]
		for _, g := range groups {
			if groupReferencesScript(g, scriptPath) {
				removed = true
				continue
			}
			kept = append(kept, g)
		}
		if len(kept) == 0 {
			delete(hooks, event)
		} else {
			b, err := json.Marshal(kept)
			if err != nil {
				return false, err
			}
			hooks[event] = b
		}
	}
	if removed {
		if err := writeSettings(settingsPath, root, hooks); err != nil {
			return false, err
		}
	}
	return removed, nil
}

// hookCommandString builds the installed command, e.g.
// "/plugin/hooks/agent-hook.sh /plugin/state/hooks/claude blocked waiting:permission".
func hookCommandString(scriptPath, stateDir string, ev hookEvent) string {
	cmd := scriptPath + " " + stateDir
	if ev.status != "" {
		cmd += " " + ev.status + " " + ev.detail
	}
	return cmd
}

// readSettings parses a settings file into a field-preserving map. A missing
// file yields an empty map; JSONC files (Copilot) have comments stripped.
func readSettings(path string, jsonc bool) map[string]json.RawMessage {
	root := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if err != nil {
		return root
	}
	if jsonc {
		data = stripJSONC(data)
	}
	_ = json.Unmarshal(data, &root) // permissive: a corrupt config stays as-is if we can't write it back safely
	return root
}

func readHooks(root map[string]json.RawMessage) map[string]json.RawMessage {
	hooks := map[string]json.RawMessage{}
	if raw, ok := root["hooks"]; ok {
		_ = json.Unmarshal(raw, &hooks)
	}
	return hooks
}

func rawSlice(raw json.RawMessage) []json.RawMessage {
	var out []json.RawMessage
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func containsCommand(groupsRaw json.RawMessage, cmd string) bool {
	for _, g := range rawSlice(groupsRaw) {
		var group struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		}
		if json.Unmarshal(g, &group) == nil {
			for _, h := range group.Hooks {
				if h.Command == cmd {
					return true
				}
			}
		}
	}
	return false
}

func groupReferencesScript(g json.RawMessage, scriptPath string) bool {
	var group struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if json.Unmarshal(g, &group) != nil {
		return false
	}
	for _, h := range group.Hooks {
		if strings.Contains(h.Command, scriptPath) {
			return true
		}
	}
	return false
}

func writeSettings(path string, root map[string]json.RawMessage, hooks map[string]json.RawMessage) error {
	if len(hooks) == 0 {
		delete(root, "hooks")
	} else {
		b, err := json.Marshal(hooks)
		if err != nil {
			return err
		}
		root["hooks"] = b
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// stripJSONC removes // and /* */ comments from a JSONC file while keeping
// string contents intact (a naive regex would corrupt URLs like https://).
func stripJSONC(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inStr := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inStr {
			out = append(out, c)
			if c == '\\' && i+1 < len(b) {
				out = append(out, b[i+1])
				i++
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch {
		case c == '"':
			inStr = true
			out = append(out, c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i++ // skip the closing '/'
		default:
			out = append(out, c)
		}
	}
	return out
}
