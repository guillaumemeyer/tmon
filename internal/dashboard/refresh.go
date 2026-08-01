package dashboard

import (
	"sort"
	"strconv"
	"strings"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/blocked"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/detect"
	"github.com/guillaumemeyer/tmon/internal/pane"
	"github.com/guillaumemeyer/tmon/internal/tmux"
)

// DefaultLoader builds the real data loader. A full reload re-scans /proc,
// re-snapshots the pane map, re-checks blocked state per agent and reads the
// shared state file (statuses + animation frame); a light refresh only reads
// the state file for the frame so the popup animates in lockstep with the
// status bar between reloads.
func DefaultLoader(cfg config.Config) Loader {
	return func(mode Mode) (Data, error) {
		if mode == ModeLight {
			sf, err := agent.LoadState(cfg.StateFilePath())
			if err != nil {
				return Data{}, err
			}
			return Data{Frame: sf.Frame}, nil
		}
		return loadFull(cfg)
	}
}

// loadFull performs the complete dashboard refresh: fresh /proc detection,
// fresh pane mapping, a live blocked check per agent (via the shared package
// — the bash dashboard had its own stale copy of the pattern list), and the
// state file for the delta-based statuses and animation frame.
func loadFull(cfg config.Config) (Data, error) {
	sf, err := agent.LoadState(cfg.StateFilePath())
	if err != nil {
		sf = agent.NewState() // corrupt state: keep the popup usable
	}

	statusByPID := make(map[int]agent.Status, len(sf.Agents))
	for _, s := range sf.Agents {
		statusByPID[s.PID] = s.Status
	}

	var paneMap *pane.Map
	if tmux.Available() {
		paneMap, _ = pane.BuildMap()
	}

	agents, err := detect.All()
	if err != nil {
		return Data{}, err
	}

	rows := make([]Row, 0, len(agents))
	for _, a := range agents {
		r := Row{
			PID:         a.PID,
			Label:       a.Label,
			Cmdline:     a.Cmdline,
			CWD:         a.CWD,
			Status:      statusByPID[a.PID],
			Pane:        "?",
			SessionID:   "?",
			SessionName: "?",
			WindowIndex: "?",
			WindowName:  "?",
			PaneIndex:   "?",
		}
		if r.Status == "" {
			r.Status = agent.StatusRunning // first detection: show it immediately
		}

		if paneMap != nil {
			if e, ok := paneMap.Resolve(a.PID); ok {
				r.Pane = e.Target
				r.SessionID = strings.TrimPrefix(e.SessionID, "$")
				r.SessionName = e.SessionName
				r.WindowIndex = e.WindowIndex
				r.WindowName = e.WindowName
				r.PaneIndex = e.PaneIndex
			}
		}

		// Blocked is re-checked live so the popup never shows a stale status.
		if r.Pane != "?" && tmux.Available() && blocked.DetectPane(r.Pane) {
			r.Status = agent.StatusBlocked
		}

		rows = append(rows, r)
	}

	sortRows(rows)
	return Data{Rows: rows, Frame: sf.Frame}, nil
}

// sortRows orders agents the way the bash popup did: by session id, window
// index, then pane index — numerically, so windows sort 0,1,2,… not 0,10,2.
func sortRows(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if sa, sb := numField(a.SessionID), numField(b.SessionID); sa != sb {
			return sa < sb
		}
		if wa, wb := numField(a.WindowIndex), numField(b.WindowIndex); wa != wb {
			return wa < wb
		}
		if pa, pb := numField(a.PaneIndex), numField(b.PaneIndex); pa != pb {
			return pa < pb
		}
		return a.PID < b.PID
	})
}

// numField parses a tmux numeric field ("$12", "3"; "?" or "" for unpaned)
// as a sortable number; anything unparseable sorts as 0.
func numField(s string) int {
	s = strings.TrimPrefix(s, "$")
	if s == "" || s == "?" {
		return 0
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return 0
}
