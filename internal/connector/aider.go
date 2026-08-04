// aider.go — Aider connector (native tier).
//
// Aider appends the conversation to .aider.chat.history.md in the project
// directory after each exchange, so the file's mtime is a direct activity
// signal. The connector checks it per running Aider process (the real CWD,
// since detect only carries the short form): an mtime within the last
// ~10s means the agent is mid-turn. Between turns aider goes quiet and the
// connector emits nothing, leaving the CPU/IO heuristic in charge.
package connector

import (
	"os"
	"path/filepath"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/proc"
)

// aiderTurnWindow is how recently .aider.chat.history.md must have been
// written for the agent to count as actively editing.
const aiderTurnWindow = 10 * time.Second

// Aider reads per-project .aider.chat.history.md activity.
type Aider struct{}

func (Aider) Name() string { return "aider" }

// Enabled reports whether aider has ever written its home history/config
// files, i.e. aider is (or was) installed and used. Per-project history
// files live in arbitrary project dirs, so this home-dir gate is the only
// cheap presence check available.
func (Aider) Enabled(cfg config.Config) bool {
	return dirExists(homePath(".aider")) || fileExists(homePath(".aider.history.md"))
}

// Probe returns one active record per Aider process whose project history
// file was written within aiderTurnWindow.
func (Aider) Probe(cfg config.Config) ([]Record, error) {
	recs := []Record{}
	for _, pid := range runningPIDs("Aider") {
		cwd, err := proc.ReadCWD(pid)
		if err != nil {
			continue
		}
		fi, err := os.Stat(filepath.Join(cwd, ".aider.chat.history.md"))
		if err != nil {
			continue
		}
		at := fi.ModTime()
		if time.Since(at) > aiderTurnWindow {
			continue
		}
		recs = append(recs, Record{
			PID:    pid,
			Label:  "Aider",
			Status: agent.StatusActive,
			Detail: "editing",
			CWD:    proc.CWDShort(cwd),
			At:     at,
		})
	}
	return recs, nil
}
