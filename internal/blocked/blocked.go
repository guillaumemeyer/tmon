// Package blocked detects when an agent's pane shows a prompt meaning it is
// waiting for the user: permission prompts, y/n questions, approval screens.
//
// This is the single source of truth for blocked detection, shared by
// `tmon status` and `tmon dashboard` — the bash plugin had two divergent
// copies of this list that had already drifted apart.
package blocked

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/guillaumemeyer/tmon/internal/tmux"
)

// captureTailLines is how many trailing pane lines we scan. Older scrollback
// would otherwise keep a stale "Should I…?" visible forever after the user
// already answered.
const captureTailLines = 10

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
	`Continue\?$`, // line-final; bare "continue" in prose must not match
	`Would you like to.*\?$`,
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
	// Hermes dangerous-command approval panel (CLI/TUI)
	`Approve Once`,
	`Always Approve`,
	`dangerous command`,
	`/approve`,
	`Use /approve`,
	// Chat questions — line-final so old scrollback questions do not stick
	`Waiting for input`,
	`Waiting for your`,
	`What would you like.*\?$`,
	`How would you like.*\?$`,
	`Can I proceed.*\?$`,
	`Should I.*\?$`,
	`Do you want me to.*\?$`,
}

// combined is the precomputed case-insensitive union regex (bash used
// grep -Ei over the same list). Applied per non-empty line so $ anchors
// mean end-of-line, not end-of-scrollback.
var combined = regexp.MustCompile("(?i)" + strings.Join(patterns, "|"))

// Matches reports whether the given pane content looks like a blocked prompt.
// Each non-empty line is tested on its own so line-final patterns work and a
// multi-line blob does not keep matching on an old question in the middle.
func Matches(content string) bool {
	_, ok := MatchedPattern(content)
	return ok
}

// MatchedPattern returns the first blocked-pattern text found in content
// (empty string, false when nothing matches). The matched text is what the
// dashboard shows as the "blocked reason", e.g. "[y/N]" or "Press any key".
func MatchedPattern(content string) (string, bool) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" {
			continue
		}
		if m := combined.FindString(line); m != "" {
			return m, true
		}
	}
	return "", false
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
	// -S -N captures only the last N lines so stale prompts in scrollback
	// do not keep an agent marked blocked after the user has moved on.
	out, err := tmux.Run("capture-pane", "-t", paneTarget, "-p", "-S", "-"+strconv.Itoa(captureTailLines))
	if err != nil || out == "" {
		return "", false
	}
	return MatchedPattern(out)
}
