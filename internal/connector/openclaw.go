// openclaw.go — OpenClaw connector (stub).
//
// OpenClaw keeps its runtime state in a SQLite database and exposes a
// WebSocket control API; neither surface is stable enough to read yet, so
// this connector is a reserved placeholder. It gates on ~/.openclaw/ so the
// name is recognized by TMON_CONNECTORS and shows up in the connector
// matrix, but Probe emits nothing: OpenClaw agents keep the CPU/IO
// heuristic path, which is exactly how they behaved before connectors.
//
// TODO(follow-up): read ~/.openclaw/state (db.sqlite) or the WS API once
// the schema is documented.
package connector

import (
	"github.com/guillaumemeyer/tmon/internal/config"
)

// OpenClaw is a dormant placeholder for OpenClaw's native state.
type OpenClaw struct{}

func (OpenClaw) Name() string { return "openclaw" }

// Enabled reports whether an OpenClaw data directory exists.
func (OpenClaw) Enabled(cfg config.Config) bool {
	return dirExists(homePath(".openclaw"))
}

// Probe always emits nothing — see the package comment.
func (OpenClaw) Probe(cfg config.Config) ([]Record, error) {
	return nil, nil
}
