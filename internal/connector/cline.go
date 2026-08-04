// cline.go — Cline connector (native tier).
//
// Cline stores one directory per session under ~/.cline/data/sessions/,
// writing conversation JSON as it works. The connector watches for the
// session whose files were touched most recently: when a Cline process is
// running and that session was written within the freshness window, the
// agent is working. Session dirs that go quiet simply stop producing
// records, so Cline decays back to the CPU/IO heuristic path.
package connector

import (
	"path/filepath"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

// Cline reads Cline session file activity.
type Cline struct{}

func (Cline) Name() string { return "cline" }

// Enabled reports whether the Cline session store exists.
func (Cline) Enabled(cfg config.Config) bool {
	return dirExists(homePath(".cline", "data", "sessions"))
}

// Probe returns one active record for the most recently written session.
func (Cline) Probe(cfg config.Config) ([]Record, error) {
	pid := firstRunningPID("Cline")
	if pid == 0 {
		return nil, nil
	}
	sessions := homePath(".cline", "data", "sessions")
	at, path, ok := newestSessionFile(sessions, []string{".json", ".jsonl"}, 3, 1000)
	if !ok {
		return nil, nil
	}
	// The session id is the directory holding the newest file.
	id := filepath.Base(filepath.Dir(path))
	if id == "." || id == string(filepath.Separator) || id == "" {
		id = "active"
	}
	return []Record{{
		PID:    pid,
		Label:  "Cline",
		Status: agent.StatusWorking,
		Detail: "session:" + id,
		At:     at,
	}}, nil
}
