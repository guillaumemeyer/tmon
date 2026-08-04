// Package blocked detects when an agent's pane shows a prompt meaning it is
// waiting for the user: permission prompts, y/n questions, approval screens.
//
// This is the single source of truth for blocked detection, shared by
// `tmon status` and `tmon dashboard` — the bash plugin had two divergent
// copies of this list that had already drifted apart.
package blocked

import (
	"regexp"
	"strings"

	"github.com/guillaumemeyer/tmon/internal/tmux"
)

var patterns = []string{
	// Formal selectors / clarifications
	`❯ 1\.`,
	`❯ Yes`,
	`❯ No`,
	`❯ Approve`,
	`❯ Confirm`,
	`\[y/N\]`,
	`\[Y/n\]`,
	`\[yes/no\]`,
	// Permission / approval prompts
	`Do you want to proceed`,
	`Do you approve`,
	`Proceed anyway`,
	`Continue anyway`,
	`Continue\?`, // literal "Continue?" — bare ? would match "continue" (optional e)
	`Would you like to`,
	`Press any key`,
	`Press Enter`,
	`Press.*to continue`,
	// Plan approval mode
	`approval.*required`,
	`requires approval`,
	`waiting for approval`,
	`plan.*approv`,
	`\[approve\]`,
	`\[confirm\]`,
	`\[reject\]`,
	// Chat questions (agent asked a question and is waiting)
	`Waiting for input`,
	`Waiting for your`,
	`What would you like`,
	`How would you like`,
	`Can I proceed`,
	`Should I`,
	`Do you want me to`,
}

// combined is the precomputed case-insensitive union regex (bash used
// grep -Ei over the same list).
var combined = regexp.MustCompile("(?i)" + strings.Join(patterns, "|"))

// Matches reports whether the given pane content looks like a blocked prompt.
func Matches(content string) bool {
	return combined.MatchString(content)
}

// MatchedPattern returns the first blocked-pattern text found in content
// (empty string, false when nothing matches). The matched text is what the
// dashboard shows as the "blocked reason", e.g. "[y/N]" or "Press any key".
func MatchedPattern(content string) (string, bool) {
	m := combined.FindString(content)
	return m, m != ""
}

// DetectPane reports whether the pane's visible content matches any blocked
// pattern. A failed capture (pane gone, not inside tmux) is never "blocked".
func DetectPane(paneTarget string) bool {
	_, ok := DetectPanePattern(paneTarget)
	return ok
}

// DetectPanePattern reports whether the pane's visible content matches any
// blocked pattern, returning the matched text (the "blocked reason" the
// dashboard shows, e.g. "[y/N]" or "Press any key").
func DetectPanePattern(paneTarget string) (string, bool) {
	out, err := tmux.Run("capture-pane", "-t", paneTarget, "-p")
	if err != nil || out == "" {
		return "", false
	}
	return MatchedPattern(out)
}
