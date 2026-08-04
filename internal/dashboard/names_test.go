package dashboard

import "testing"

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
