package dashboard

import "testing"

func TestAgentIdentityColor(t *testing.T) {
	want := map[string]string{
		"Claude":    "#D97757",
		"Codex":     "#10B981",
		"Hermes":    "#22D3EE",
		"Grok":      "#A78BFA",
		"Cursor":    "#E879F9",
		"Copilot":   "#79C0FF",
		"Cline":     "#FBBF24",
		"CodeBuddy": "#2DD4BF",
		"Windsurf":  "#38BDF8",
		"Aider":     "#A3E635",
		"OpenClaw":  "#FB7185",
		"Prime":     "#8B5CF6",
	}
	for label, color := range want {
		if got := agentIdentityColor(label); got != color {
			t.Errorf("agentIdentityColor(%q) = %q, want %q", label, got, color)
		}
	}
	if got := agentIdentityColor("Unknown"); got != "" {
		t.Errorf("agentIdentityColor(Unknown) = %q, want empty", got)
	}
	if got := agentIdentityColor(""); got != "" {
		t.Errorf("agentIdentityColor(\"\") = %q, want empty", got)
	}
}

func TestAgentDisplayNameHermesProfile(t *testing.T) {
	cases := []struct {
		name string
		row  Row
		want string
	}{
		{"profile only", Row{Label: "Hermes", Profile: "default"}, "Hermes - default"},
		{"title and profile", Row{Label: "Hermes", Profile: "coder", Title: "Build feature"}, "Build feature (Hermes - coder)"},
		{"no profile falls back", Row{Label: "Hermes", Title: "X"}, "X (Hermes Agent)"},
		{"grok unchanged", Row{Label: "Grok", Title: "Refactor"}, "Refactor (Grok Build)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentDisplayName(tc.row); got != tc.want {
				t.Errorf("agentDisplayName = %q, want %q", got, tc.want)
			}
		})
	}
}
