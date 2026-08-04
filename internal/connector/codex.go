// codex.go — Codex CLI connector (HOOKS tier).
//
// Codex exposes no readable live state file; its authoritative state comes
// from lifecycle hooks (`tmon hooks install codex` writes ~/.codex/hooks.json
// and installs the shared agent-hook.sh). Codex additionally requires the
// hooks to be trusted in-session via /hooks before they run — that step is
// manual and documented in the README.
package connector

import (
	"github.com/guillaumemeyer/tmon/internal/config"
)

// Codex reads Codex hook state written by the installed hook script.
type Codex struct{}

func (Codex) Name() string { return "codex" }

// Enabled reports whether any Codex hook state has been written.
func (Codex) Enabled(cfg config.Config) bool {
	return hookDirExists(cfg, "codex")
}

// Probe returns one record per live Codex session with fresh hook state.
func (Codex) Probe(cfg config.Config) ([]Record, error) {
	return pairHookSessions(cfg, "codex", "Codex")
}
