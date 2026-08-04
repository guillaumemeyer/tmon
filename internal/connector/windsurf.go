// windsurf.go — Windsurf connector (HOOKS tier).
//
// Windsurf exposes no readable live state file; its authoritative state
// comes from lifecycle hooks (`tmon hooks install windsurf` writes
// ~/.codeium/windsurf/hooks.json and installs the shared agent-hook.sh).
package connector

import (
	"github.com/guillaumemeyer/tmon/internal/config"
)

// Windsurf reads Windsurf hook state written by the installed hook script.
type Windsurf struct{}

func (Windsurf) Name() string { return "windsurf" }

// Enabled reports whether any Windsurf hook state has been written.
func (Windsurf) Enabled(cfg config.Config) bool {
	return hookDirExists(cfg, "windsurf")
}

// Probe returns one record per live Windsurf session with fresh hook state.
func (Windsurf) Probe(cfg config.Config) ([]Record, error) {
	return pairHookSessions(cfg, "windsurf", "Windsurf")
}
