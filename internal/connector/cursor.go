// cursor.go — Cursor connector (HOOKS tier with native fallback).
//
// Cursor agent exposes no readable live state file; `tmon hooks install
// cursor` gives it authoritative hook state (camelCase event names). When
// hooks are not installed, the newest session file under ~/.cursor/ (the
// CLI data lives under ~/.cursor/cli/ when present) serves as a weaker
// fallback: if a Cursor agent process is running and that file was touched
// within the freshness window, the agent is at least "idle". If the
// paths drift, the connector emits nothing and Cursor stays on the
// CPU/IO heuristic path.
package connector

import (
	"github.com/guillaumemeyer/tmon/internal/config"
)

// Cursor reads Cursor agent hook state, falling back to native session
// file activity.
type Cursor struct{}

func (Cursor) Name() string { return "cursor" }

// Enabled reports whether Cursor hook state or the native ~/.cursor tree
// exists.
func (Cursor) Enabled(cfg config.Config) bool {
	return hookDirExists(cfg, "cursor") || dirExists(homePath(".cursor"))
}

// Probe returns one record per live Cursor session with fresh state.
func (Cursor) Probe(cfg config.Config) ([]Record, error) {
	if recs, err := pairHookSessions(cfg, "cursor", "Cursor"); err == nil && len(recs) > 0 {
		return recs, nil
	}
	return nativeSessionRecord(cfg, "Cursor",
		[]string{homePath(".cursor", "cli"), homePath(".cursor")}, "session:active")
}
