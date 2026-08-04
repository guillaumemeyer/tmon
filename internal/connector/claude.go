// claude.go — Claude Code connector (HOOKS tier).
//
// Claude Code exposes no readable live state file; its authoritative state
// comes from lifecycle hooks. `tmon hooks install claude` installs the
// generic agent-hook.sh (embedded in the binary) that writes one JSON state
// file per session under <state>/hooks/claude/ on every hook event. The
// shared machinery in hookstate.go reads those files back and pairs them
// with running Claude Code processes by working directory.
package connector

import (
	"github.com/guillaumemeyer/tmon/internal/config"
)

// Claude reads Claude Code hook state written by the installed hook script.
type Claude struct{}

func (Claude) Name() string { return "claude" }

// Enabled reports whether any Claude hook state has been written.
func (Claude) Enabled(cfg config.Config) bool {
	return hookDirExists(cfg, "claude")
}

// Probe returns one record per live Claude session with fresh hook state.
func (Claude) Probe(cfg config.Config) ([]Record, error) {
	return pairHookSessions(cfg, "claude", "Claude")
}
