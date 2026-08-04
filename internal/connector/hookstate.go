// hookstate.go — shared machinery for HOOKS-tier connectors.
//
// Agents that expose no readable state file (Claude Code, Codex, Cursor,
// Copilot, Windsurf) get their authoritative state from lifecycle hooks:
// `tmon hooks install <agent>` installs the generic agent-hook.sh, which
// writes one JSON state file per session under <state>/hooks/<agent>/ on
// every event. All five connectors share this reader.
//
// The hook state files do not carry a PID, so each session file is paired
// with a running agent process by working directory — the agent runs in the
// session's project dir. Sessions whose process is gone (or whose hooks were
// never installed) emit nothing.
package connector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/detect"
	"github.com/guillaumemeyer/tmon/internal/proc"
)

// hookState is the JSON the hook script writes per session.
type hookState struct {
	Status string `json:"status"`
	Detail string `json:"detail"`
	CWD    string `json:"cwd"`
}

// hookSessionFile is one parsed hook state file.
type hookSessionFile struct {
	path   string
	status agent.Status
	detail string
	cwd    string // short form, matching detect's CWD display
}

// hookDirExists reports whether any hook state has been written for agent.
func hookDirExists(cfg config.Config, agent string) bool {
	fi, err := os.Stat(filepath.Join(cfg.HookStateDir, agent))
	return err == nil && fi.IsDir()
}

// pairHookSessions reads the hook state files for one agent and pairs them
// with running processes of the matching detect label.
func pairHookSessions(cfg config.Config, agent, label string) ([]Record, error) {
	files, err := readHookSessionFiles(filepath.Join(cfg.HookStateDir, agent))
	if err != nil || len(files) == 0 {
		return nil, err
	}
	byCWD := runningByCWD(label)
	recs := make([]Record, 0, len(files))
	for _, f := range files {
		pid, ok := byCWD[f.cwd]
		if !ok {
			continue // no running <label> process for this session
		}
		recs = append(recs, Record{
			PID:    pid,
			Label:  label,
			Status: f.status,
			Detail: f.detail,
			CWD:    f.cwd,
			At:     fileModTime(f.path), // freshness = last hook event
		})
	}
	return recs, nil
}

// readHookSessionFiles parses every *.json under dir, skipping unreadable
// or malformed files. A missing dir (hooks never fired) is empty, not an
// error.
func readHookSessionFiles(dir string) ([]hookSessionFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []hookSessionFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var hs hookState
		if json.Unmarshal(b, &hs) != nil {
			continue
		}
		status := agent.Status(hs.Status)
		switch status {
		case agent.StatusBlocked, agent.StatusWorking, agent.StatusIdle:
		default:
			continue // unknown status string: not one of ours
		}
		cwd := hs.CWD
		if cwd != "" {
			cwd = proc.CWDShort(cwd)
		}
		files = append(files, hookSessionFile{path: path, status: status, detail: hs.Detail, cwd: cwd})
	}
	return files, nil
}

// runningByCWD maps each running process of the given label's CWD (short
// form) to its PID. A seam so tests can inject a deterministic process table.
var runningByCWD = func(label string) map[string]int {
	agents, err := detect.All()
	if err != nil {
		return nil
	}
	byCWD := make(map[string]int)
	for _, a := range agents {
		if a.Label == label {
			byCWD[a.CWD] = a.PID
		}
	}
	return byCWD
}

// runningPIDs returns the PIDs of detected agents with the given label. A
// seam so tests can inject a deterministic process table.
var runningPIDs = func(label string) []int {
	agents, err := detect.All()
	if err != nil {
		return nil
	}
	var pids []int
	for _, a := range agents {
		if a.Label == label {
			pids = append(pids, a.PID)
		}
	}
	return pids
}

// firstRunningPID returns the first detected PID of label, or 0.
func firstRunningPID(label string) int {
	pids := runningPIDs(label)
	if len(pids) == 0 {
		return 0
	}
	return pids[0]
}

// homePath joins the user's home directory with the given components.
func homePath(parts ...string) string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(append([]string{h}, parts...)...)
}
