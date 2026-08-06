// Package hide implements the @tmon-hide pattern filtering. A hidden agent
// disappears from the status-bar indicator, the dashboard list, and the pane
// border strips. The agent process keeps running; hiding is display-only.
package hide

import "strings"

// ShouldHide reports whether an agent with the given label, working
// directory, and tmux session name should be hidden by any of the patterns.
// Matching rules per pattern:
//   - the agent label, case-insensitive glob;
//   - the working directory, case-sensitive glob;
//   - the tmux session name, case-sensitive glob.
//
// A pattern with no wildcard matches the exact string. Empty patterns are
// ignored, so an empty pattern list hides nothing.
func ShouldHide(patterns []string, label, cwd, session string) bool {
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if globMatchFold(p, label) {
			return true
		}
		if globMatch(p, cwd) {
			return true
		}
		if globMatch(p, session) {
			return true
		}
	}
	return false
}

// SessionFromPane returns the tmux session name from a pane target of the
// form "session:window.pane", or "" when the target is unparseable. The "?"
// unknown marker also yields "".
func SessionFromPane(pane string) string {
	if pane == "" || pane == "?" {
		return ""
	}
	session, _, ok := strings.Cut(pane, ":")
	if !ok {
		return ""
	}
	return session
}

// globMatch reports whether pattern matches text with Ghosthub-style glob
// semantics: "*" matches any run of characters (including "/"), "?" matches
// exactly one character, and every other character matches itself. A pattern
// with no wildcard is an exact match.
func globMatch(pattern, text string) bool {
	return globMatchRunes([]rune(pattern), []rune(text))
}

// globMatchRunes is the recursive glob engine. A "*" consumes the rest of
// the pattern eagerly, then backtracks over every suffix of the text; the
// pattern is short, so the worst case stays trivial.
func globMatchRunes(p, s []rune) bool {
	for len(p) > 0 {
		switch p[0] {
		case '*':
			for len(p) > 0 && p[0] == '*' {
				p = p[1:]
			}
			if len(p) == 0 {
				return true
			}
			for i := 0; i <= len(s); i++ {
				if globMatchRunes(p, s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(s) == 0 {
				return false
			}
			p, s = p[1:], s[1:]
		default:
			if len(s) == 0 || s[0] != p[0] {
				return false
			}
			p, s = p[1:], s[1:]
		}
	}
	return len(s) == 0
}

// globMatchFold is globMatch with case folding on both sides, used for
// agent labels ("Aider" matches "aider").
func globMatchFold(pattern, text string) bool {
	return globMatch(strings.ToLower(pattern), strings.ToLower(text))
}
