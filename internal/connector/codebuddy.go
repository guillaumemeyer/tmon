// codebuddy.go — CodeBuddy connector (native tier).
//
// CodeBuddy writes one JSON session file per agent process under
// ~/.codebuddy/sessions/, named after the process PID. The PID comes from
// the filename; Collect applies liveness and freshness, so exited agents
// and quiet sessions drop out on their own. The record carries the session
// id so the dashboard can show which session is live.
package connector

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

// CodeBuddy reads CodeBuddy per-PID session files.
type CodeBuddy struct{}

func (CodeBuddy) Name() string { return "codebuddy" }

// Enabled reports whether the CodeBuddy session store exists.
func (CodeBuddy) Enabled(cfg config.Config) bool {
	return dirExists(homePath(".codebuddy", "sessions"))
}

// Probe returns one record per session file, with the PID parsed from the
// filename. Files whose PID is gone or whose mtime is stale are dropped by
// Collect; files for unknown PIDs become connector-only agents (like the
// Hermes gateway) and still get tracked.
func (CodeBuddy) Probe(cfg config.Config) ([]Record, error) {
	dir := homePath(".codebuddy", "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil // missing/unreadable store: nothing to report
	}
	recs := make([]Record, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil || pid <= 0 {
			continue
		}
		fi, err := os.Stat(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		recs = append(recs, Record{
			PID:    pid,
			Label:  "CodeBuddy",
			Status: agent.StatusIdle,
			Detail: "session:" + strings.TrimSuffix(e.Name(), ".json"),
			At:     fi.ModTime(),
		})
	}
	return recs, nil
}
