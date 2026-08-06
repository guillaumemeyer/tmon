package hide

import "testing"

func TestShouldHide(t *testing.T) {
	cases := []struct {
		name     string
		patterns []string
		label    string
		cwd      string
		session  string
		want     bool
	}{
		{
			name:     "no patterns hides nothing",
			patterns: nil,
			label:    "Aider", cwd: "/repo", session: "main",
			want: false,
		},
		{
			name:     "empty patterns hide nothing",
			patterns: []string{"", "  "},
			label:    "Aider", cwd: "/repo", session: "main",
			want: false,
		},
		{
			name:     "label exact match",
			patterns: []string{"aider"},
			label:    "Aider", cwd: "/repo", session: "main",
			want: true,
		},
		{
			name:     "label case-insensitive",
			patterns: []string{"AIDER"},
			label:    "aider", cwd: "/repo", session: "main",
			want: true,
		},
		{
			name:     "label glob",
			patterns: []string{"aid*"},
			label:    "Aider", cwd: "/repo", session: "main",
			want: true,
		},
		{
			name:     "label no match",
			patterns: []string{"claude"},
			label:    "Aider", cwd: "/repo", session: "main",
			want: false,
		},
		{
			name:     "cwd glob",
			patterns: []string{"*/scratch/*"},
			label:    "Claude", cwd: "/home/u/scratch/probe", session: "main",
			want: true,
		},
		{
			name:     "cwd glob requires the literal separator",
			patterns: []string{"*/scratch/*"},
			label:    "Claude", cwd: "/home/u/scratchprobe", session: "main",
			want: false,
		},
		{
			name:     "cwd exact path",
			patterns: []string{"/home/u/blog"},
			label:    "Claude", cwd: "/home/u/blog", session: "main",
			want: true,
		},
		{
			name:     "session glob",
			patterns: []string{"tool-*"},
			label:    "Codex", cwd: "/repo", session: "tool-codex",
			want: true,
		},
		{
			name:     "session exact",
			patterns: []string{"main"},
			label:    "Codex", cwd: "/repo", session: "main",
			want: true,
		},
		{
			name:     "any pattern wins",
			patterns: []string{"aider", "*/tmp/*"},
			label:    "Grok", cwd: "/tmp/x", session: "main",
			want: true,
		},
		{
			name:     "wildcard star matches any field",
			patterns: []string{"*"},
			label:    "Grok", cwd: "/repo", session: "main",
			want: true,
		},
		{
			name:     "question mark one char",
			patterns: []string{"?ider"},
			label:    "Aider", cwd: "/repo", session: "main",
			want: true,
		},
		{
			name:     "empty cwd never matches a cwd glob",
			patterns: []string{"*/scratch/*"},
			label:    "Claude", cwd: "", session: "main",
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ShouldHide(c.patterns, c.label, c.cwd, c.session); got != c.want {
				t.Errorf("ShouldHide(%v, %q, %q, %q) = %v, want %v",
					c.patterns, c.label, c.cwd, c.session, got, c.want)
			}
		})
	}
}

func TestSessionFromPane(t *testing.T) {
	cases := map[string]string{
		"main:0.0": "main",
		"$5:0.1":   "$5",
		"dev:2.3":  "dev",
		"":         "",
		"?":        "",
		"nowindow": "",
		"a:b.c.d":  "a",
	}
	for in, want := range cases {
		if got := SessionFromPane(in); got != want {
			t.Errorf("SessionFromPane(%q) = %q, want %q", in, got, want)
		}
	}
}
