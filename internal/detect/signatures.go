package detect

import (
	"regexp"
	"strings"
)

// Signature pairs a display label with a regex matched against a process
// command line (argv joined with spaces).
type Signature struct {
	Label string
	Re    *regexp.Regexp
}

// Signatures is the agent detection table. Order matters: the first matching
// signature wins, exactly as in the original bash plugin.
//
// To add an agent: add its signatures here, add a display name/icon in the
// dashboard, and add rows to the test matrix in signatures_test.go — one
// file instead of the two the bash plugin needed.
var Signatures = []Signature{
	{"Grok", re(`^grok( |$)`)},
	{"Grok", re(`/grok[-_]build`)},
	{"Grok", re(`grok[-_](build|agent|chat|run)`)},
	{"Claude", re(`(^|/)claude( |$)`)},
	{"Claude", re(`claude( |-)(code|agent|chat|run)`)},
	{"Claude", re(`claude-code`)},
	{"Claude", re(`/claude-code/`)},
	{"Claude", re(`node.*@anthropic.*claude`)},
	{"Codex", re(`^codex( |$)`)},
	{"Codex", re(`codex( |-)(chat|agent|run)`)},
	{"Codex", re(`/codex-cli/`)},
	{"Cursor", re(`cursor( |-)agent`)},
	{"Cursor", re(`/cursor[-_]agent/`)},
	{"Cline", re(`^cline( |$)`)},
	{"Cline", re(`cline( |-)(agent|chat|run)`)},
	{"Cline", re(`/cline`)},
	{"Aider", re(`^aider( |$)`)},
	{"Aider", re(`aider( |-)(agent|chat|run)`)},
	{"Aider", re(`python.*aider`)},
	{"Copilot", re(`copilot( |-)agent`)},
	{"CodeBuddy", re(`^codebuddy( |$)`)},
	{"CodeBuddy", re(`codebuddy( |-)(agent|chat|run)`)},
	{"CodeBuddy", re(`/codebuddy/`)},
	{"Windsurf", re(`^windsurf( |$)`)},
	{"Windsurf", re(`windsurf( |-)(agent|chat|run)`)},
	{"Windsurf", re(`/windsurf/`)},
	{"Hermes", re(`^hermes( |$)`)},
	{"Hermes", re(`/hermes( |$)`)},
	{"Hermes", re(`hermes (agent|chat|run)`)},
	{"OpenClaw", re(`^openclaw( |$)`)},
	{"OpenClaw", re(`openclaw-gateway`)},
	{"OpenClaw", re(`openclaw (agent|chat|run|gateway)`)},
	{"Prime", re(`^prime-agent( |$)`)},
}

func re(p string) *regexp.Regexp { return regexp.MustCompile(p) }

// combined is the precomputed union regex used as a cheap first filter so we
// don't run every individual signature regex against every process.
var combined = regexp.MustCompile(strings.Join(allPatterns(), "|"))

func allPatterns() []string {
	ps := make([]string, 0, len(Signatures))
	for _, s := range Signatures {
		ps = append(ps, s.Re.String())
	}
	return ps
}
