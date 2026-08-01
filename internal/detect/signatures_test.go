package detect

import "testing"

// TestMatchLabel is the regression safety net the bash plugin never had.
// Every cmdline is run through the real signature table; positives must
// resolve to the expected label, negatives must resolve to "".
func TestMatchLabel(t *testing.T) {
	positives := map[string]string{
		// Grok
		"grok":                                "Grok",
		"grok build --refactor":               "Grok",
		"/home/me/.grok-build/bin/grok build": "Grok",
		"grok-agent --task refactor":          "Grok",
		// Claude
		"claude":          "Claude",
		"claude code":     "Claude",
		"claude --resume": "Claude",
		"node /usr/lib/node_modules/@anthropic-ai/claude-code/cli.js": "Claude",
		"/opt/claude-code/cli.js":                                     "Claude",
		"claude-agent":                                                "Claude",
		// Codex
		"codex":                "Codex",
		"codex chat":           "Codex",
		"codex agent":          "Codex",
		"/opt/codex-cli/codex": "Codex", // the /codex-cli/ pattern needs slashes
		// Cursor
		"cursor agent --yolo":   "Cursor",
		"/opt/cursor-agent/run": "Cursor",
		// Cline
		"cline":          "Cline",
		"cline agent":    "Cline",
		"/usr/bin/cline": "Cline",
		// Aider
		"aider":                    "Aider",
		"aider --model sonnet":     "Aider",
		"python3 -m aider --watch": "Aider",
		// Copilot
		"copilot agent": "Copilot",
		"copilot-agent": "Copilot",
		// CodeBuddy
		"codebuddy":                    "CodeBuddy",
		"codebuddy agent":              "CodeBuddy",
		"/opt/codebuddy/bin/codebuddy": "CodeBuddy",
		// Windsurf
		"windsurf":               "Windsurf",
		"windsurf agent":         "Windsurf",
		"/opt/windsurf/launcher": "Windsurf",
		// Hermes
		"hermes":                    "Hermes",
		"hermes agent --task plan":  "Hermes",
		"/usr/local/bin/hermes run": "Hermes",
		// OpenClaw
		"openclaw":       "OpenClaw",
		"openclaw chat":  "OpenClaw",
		"openclaw agent": "OpenClaw",
	}

	for cmdline, want := range positives {
		if got := MatchLabel(cmdline); got != want {
			t.Errorf("MatchLabel(%q) = %q, want %q", cmdline, got, want)
		}
	}

	// Close negatives: things that look agent-adjacent but must NOT match.
	// Note: "cat claude-code.txt" is intentionally NOT here — the unanchored
	// `claude-code` signature matches it, a faithful (if imperfect) port of
	// the bash plugin's behavior.
	negatives := []string{
		"grokking through the code", // no space/end after "grok"
		"claudette",                 // not "claude"
		"codexing",                  // no space/end after "codex"
		"aiderr",                    // no space/end after "aider"
		"codebuddying",              // no space/end after "codebuddy"
		"windsurfer",                // no space/end after "windsurf"
		"hermes-agent",              // hyphenated form isn't in the table
		"claudecode",                // no hyphen, no anchors
		"codex-cli",                 // bare word: /codex-cli/ needs slashes
		"/usr/bin/grok run",         // "grok run" isn't a hyphenated form
		"grep claude",               // anchors prevent substring matches
		"node server.js",            // unrelated node process
		"tmux",                      // obviously not an agent
		"CLAUDE",                    // detection is case-sensitive, like bash
	}
	for _, cmdline := range negatives {
		if got := MatchLabel(cmdline); got != "" {
			t.Errorf("MatchLabel(%q) = %q, want no match", cmdline, got)
		}
	}
}

// TestSignaturesCompile guards against a typo breaking the table at runtime:
// every entry must already be a valid regexp (they're compiled at init).
func TestSignaturesCompile(t *testing.T) {
	if len(Signatures) == 0 {
		t.Fatal("signature table is empty")
	}
	seen := map[string]bool{}
	for _, s := range Signatures {
		if s.Label == "" {
			t.Error("signature with empty label")
		}
		if seen[s.Re.String()] {
			t.Errorf("duplicate pattern %q", s.Re.String())
		}
		seen[s.Re.String()] = true
	}
}
