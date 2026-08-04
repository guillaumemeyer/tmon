// copilot.go — GitHub Copilot connector (HOOKS tier with native fallback).
//
// Copilot agent exposes no readable live state file; `tmon hooks install
// copilot` gives it authoritative hook state (camelCase event names, with
// JSONC-tolerant settings merging). Without hooks, the newest session file
// under ~/.copilot/ (sessions under ~/.copilot/sessions/ when present) is a
// weaker fallback: a running Copilot process plus a freshly touched file
// means the agent is at least "idle". Missing paths emit nothing, so
// Copilot keeps the CPU/IO heuristic path.
package connector

import (
	"github.com/guillaumemeyer/tmon/internal/config"
)

// Copilot reads Copilot agent hook state, falling back to native session
// file activity.
type Copilot struct{}

func (Copilot) Name() string { return "copilot" }

// Enabled reports whether Copilot hook state or the native ~/.copilot tree
// exists.
func (Copilot) Enabled(cfg config.Config) bool {
	return hookDirExists(cfg, "copilot") || dirExists(homePath(".copilot"))
}

// Probe returns one record per live Copilot session with fresh state.
func (Copilot) Probe(cfg config.Config) ([]Record, error) {
	if recs, err := pairHookSessions(cfg, "copilot", "Copilot"); err == nil && len(recs) > 0 {
		return recs, nil
	}
	return nativeSessionRecord(cfg, "Copilot",
		[]string{homePath(".copilot", "sessions"), homePath(".copilot")}, "session:active")
}
