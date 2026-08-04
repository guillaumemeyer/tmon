package dashboard

import "testing"

func TestFuzzyMatch(t *testing.T) {
	cases := []struct {
		q, text string
		want    bool
	}{
		{"", "anything", true},
		{"gb", "Grok Build", true},
		{"GB", "Grok Build", true},
		{"gxb", "Grok Build", false},
		{"code", "Claude Code", true},
		{"blog", "~/blog", true},
		{"xyz", "Claude Code", false},
	}
	for _, c := range cases {
		if got := fuzzyMatch(c.q, c.text); got != c.want {
			t.Errorf("fuzzyMatch(%q, %q) = %v, want %v", c.q, c.text, got, c.want)
		}
	}
}

func TestFuzzyMatchTerms(t *testing.T) {
	if !fuzzyMatchTerms("grok build", "Grok Build agent") {
		t.Fatal("expected multi-term AND match")
	}
	if fuzzyMatchTerms("grok xyz", "Grok Build") {
		t.Fatal("expected failed term to reject")
	}
}

func TestFuzzyScorePrefersTighterMatches(t *testing.T) {
	// Contiguous "code" in "Codex" vs gapped in a longer string — both match,
	// but a tighter / boundary match should score higher.
	tight := fuzzyScore("code", "Codex CLI")
	loose := fuzzyScore("code", "c...o...d...e far apart")
	if tight < 0 || loose < 0 {
		t.Fatalf("both should match: tight=%d loose=%d", tight, loose)
	}
	if tight <= loose {
		t.Fatalf("tight score %d should beat loose %d", tight, loose)
	}
}
