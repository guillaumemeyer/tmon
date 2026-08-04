package blocked

import "testing"

// TestPatternsMatches ensures every pattern in the table actually fires on a
// representative prompt — guards against a pattern that never matches
// anything (e.g. a broken escape).
func TestPatternsMatches(t *testing.T) {
	representatives := []string{
		"❯ 1. Use this tool",
		"❯ Yes",
		"❯ No",
		"❯ Approve",
		"❯ Confirm",
		"Proceed? [y/N]",
		"Overwrite file? [Y/n]",
		"Continue? [yes/no]",
		"Do you want to proceed?",
		"Do you approve this change?",
		"Proceed anyway?",
		"Continue anyway?",
		"Continue?",
		"Would you like to run this?",
		"Press any key to continue",
		"Press Enter to continue",
		"Press space to continue",
		"Plan approval required",
		"This change requires approval",
		"waiting for approval",
		"plan approval pending",
		"[approve]",
		"[confirm]",
		"[reject]",
		"Waiting for input",
		"Waiting for your response",
		"What would you like to do?",
		"How would you like to proceed?",
		"Can I proceed?",
		"Should I continue?",
		"Do you want me to create it?",
	}
	for _, content := range representatives {
		if !Matches(content) {
			t.Errorf("expected %q to match blocked patterns", content)
		}
	}
}

func TestDoesNotMatchNoise(t *testing.T) {
	noise := []string{
		"refactoring the parser module…",
		"$ npm install",
		"Claude Code is thinking…",
		"y",
		"approve", // bare word without prompt context
		"planning the release",
		// Claude Code welcome / plan promo (was a false positive on unescaped Continue?)
		"If you hit your limit, you can continue on Fable 5 with usage credits.",
		"you can continue on",
		"continue working",
	}
	for _, content := range noise {
		if Matches(content) {
			t.Errorf("expected %q NOT to match blocked patterns", content)
		}
	}
}

func TestMatchesCaseInsensitive(t *testing.T) {
	if !Matches("SHOULD I RUN THIS?") {
		t.Error("blocked detection should be case-insensitive (bash used grep -Ei)")
	}
}

func TestMatchedPattern(t *testing.T) {
	content := "Overwrite file? [y/N]"
	m, ok := MatchedPattern(content)
	if !ok {
		t.Fatalf("MatchedPattern(%q) = _, false, want a match", content)
	}
	if m != "[y/N]" {
		t.Errorf("MatchedPattern(%q) = %q, want %q", content, m, "[y/N]")
	}

	if _, ok := MatchedPattern("just compiling stuff"); ok {
		t.Error("MatchedPattern matched noise")
	}
}
