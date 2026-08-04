// claude.go — Claude Code connector (HOOKS tier).
//
// Claude Code exposes no readable live state file; its authoritative state
// comes from lifecycle hooks. `tmon hooks install claude` installs the
// generic agent-hook.sh (embedded in the binary) that writes one JSON state
// file per session under <state>/hooks/claude/ on every hook event. The
// shared machinery in hookstate.go reads those files back and pairs them
// with running Claude Code processes by working directory.
//
// Session titles come from Claude's own registry (~/.claude/sessions/<pid>.json,
// also the source for `claude agents --json`): each live session records a
// short name (derived, or set with /rename). The registry exists without
// tmon's hooks, so titles show whenever Claude is running.
package connector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/guillaumemeyer/tmon/internal/config"
)

// Claude reads Claude Code hook state written by the installed hook script
// and enriches each record with the session name from Claude's registry.
type Claude struct{}

// claudeHome returns the Claude config directory (~/.claude). A var so
// tests can point it at a fixture directory.
var claudeHome = func() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".claude")
}

// claudeSession is one entry of ~/.claude/sessions/<pid>.json, written by
// Claude Code itself. Only the fields the connector needs are decoded.
type claudeSession struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
}

func (Claude) Name() string { return "claude" }

// Enabled reports whether any Claude hook state has been written.
func (Claude) Enabled(cfg config.Config) bool {
	return hookDirExists(cfg, "claude")
}

// Probe returns one record per live Claude session with fresh hook state,
// enriched with the session title from Claude's own session registry.
func (Claude) Probe(cfg config.Config) ([]Record, error) {
	recs, err := pairHookSessions(cfg, "claude", "Claude")
	if err != nil {
		return nil, err
	}
	names := claudeSessionNames()
	for i := range recs {
		if n := names[recs[i].PID]; n != "" {
			recs[i].Title = n
		}
	}
	return recs, nil
}

// claudeSessionNames maps live Claude PIDs to their session names by
// reading ~/.claude/sessions/*.json (the registry `claude agents --json`
// is built from). A missing registry or unreadable entries yield no names.
func claudeSessionNames() map[int]string {
	dir := filepath.Join(claudeHome(), "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make(map[int]string)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil || pid <= 0 {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s claudeSession
		if json.Unmarshal(b, &s) != nil || s.Name == "" {
			continue
		}
		out[pid] = s.Name
	}
	return out
}
