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
	"github.com/guillaumemeyer/tmon/internal/proc"
	"github.com/guillaumemeyer/tmon/internal/tmux"
)

// DefaultLoader builds the real data loader. Every reload re-scans /proc,
// re-snapshots the pane map, re-checks blocked state per agent and reads the
// shared state file for the delta-based statuses and connector detail.
func DefaultLoader(cfg config.Config) Loader {
	return func() (Data, error) {
		return loadFull(cfg)
	}
}

// loadFull performs the complete dashboard refresh: fresh /proc detection,
// fresh pane mapping, a live blocked check per agent (via the shared package
// — the bash dashboard had its own stale copy of the pattern list), and the
// state file for the delta-based statuses and connector detail. Agents
// present in the state file but missed by the /proc signature table
// (connector-only PIDs like the Hermes gateway) are merged in so the
// dashboard never shows fewer agents than the status bar.
func loadFull(cfg config.Config) (Data, error) {
	sf, err := agent.LoadState(cfg.StateFilePath())
	if err != nil {
		sf = agent.NewState() // corrupt state: keep the popup usable
	}

	stateByPID := make(map[int]agent.AgentState, len(sf.Agents))
	for _, s := range sf.Agents {
		stateByPID[s.PID] = s
	}

	var paneMap *pane.Map
	if m, err := refreshPaneMap(); err == nil {
		paneMap = m
	}

	agents, err := refreshDetect()
	if err != nil {
		return Data{}, err
	}

	rows := make([]Row, 0, len(agents)+len(sf.Agents))
	for _, a := range agents {
		st := stateByPID[a.PID]
		r := Row{
			PID:         a.PID,
			Label:       a.Label,
			Title:       st.Title,
			Profile:     st.Profile,
			Cmdline:     a.Cmdline,
			CWD:         a.CWD,
			Status:      st.Status,
			Detail:      st.Detail,
			LastTs:      st.LastTs,
			Pane:        "?",
			SessionID:   "?",
			SessionName: "?",
			WindowIndex: "?",
			WindowName:  "?",
			PaneIndex:   "?",
		}
		if st.Usage != nil {
			r.Usage = *st.Usage
		}
		if r.Status == "" {
			r.Status = agent.StatusIdle // first detection: show it immediately
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

		applyBlockedCheck(&r)
		rows = append(rows, r)
	}

	// Merge agents known only from the state file (connector-only PIDs).
	seen := make(map[int]bool, len(rows))
	for _, r := range rows {
		seen[r.PID] = true
	}
	sessionIDByName := sessionIDsByName(rows)
	for _, st := range sf.Agents {
		if seen[st.PID] {
			continue
		}
		r := rowFromAgentState(st)
		// Reuse the session id of detected agents in the same session so the
		// connector-only agent groups under the same header, not a duplicate.
		if r.SessionName != "" && r.SessionName != "?" {
			if id, ok := sessionIDByName[r.SessionName]; ok {
				r.SessionID = id
			}
		}
		applyBlockedCheck(&r)
		rows = append(rows, r)
	}

	for i := range rows {
		resolveFullCWD(&rows[i])
	}
	sortRows(rows)
	return Data{Rows: rows}, nil
}

// resolveFullCWD upgrades a short-form CWD ("code/tmon") to the agent's
// current absolute working directory, so the popup can render it
// home-relative ("~/code/tmon"). It re-resolves via /proc so detected and
// connector-only agents get the full path uniformly; on failure (process
// gone, unreadable) the stored value is kept as a fallback.
func resolveFullCWD(r *Row) {
	if r.PID <= 0 || strings.HasPrefix(r.CWD, "/") {
		return
	}
	if cwd, err := proc.ReadCWD(r.PID); err == nil && cwd != "" {
		r.CWD = cwd
	}
}

// applyBlockedCheck re-checks the pane live so the popup never shows a stale
// status, and records the matched pattern as the blocked reason.
func applyBlockedCheck(r *Row) {
	if r.Pane == "" || r.Pane == "?" {
		return
	}
	if reason, ok := refreshBlocked(r.Pane); ok {
		r.Status = agent.StatusBlocked
		r.BlockedReason = reason
	}
}

// rowFromAgentState builds a Row for an agent known only from the state
// file: the poll loop already resolved its pane, so the components are
// parsed back out of the stored target.
func rowFromAgentState(s agent.AgentState) Row {
	r := Row{
		PID:       s.PID,
		Label:     s.Label,
		Title:     s.Title,
		Profile:   s.Profile,
		CWD:       s.CWD,
		Status:    s.Status,
		Detail:    s.Detail,
		LastTs:    s.LastTs,
		Pane:      s.Pane,
		SessionID: "?",
	}
	if s.Usage != nil {
		r.Usage = *s.Usage
	}
	if r.Status == "" {
		r.Status = agent.StatusIdle
	}
	if s.Pane != "" && s.Pane != "?" {
		session, win, paneIdx, ok := parsePaneTarget(s.Pane)
		if ok {
			r.SessionName = session
			r.WindowIndex = win
			r.PaneIndex = paneIdx
			r.SessionID = session // reconciled with detected rows later
		}
	}
	return r
}

// parsePaneTarget splits a "session:window.pane" target into its parts.
func parsePaneTarget(target string) (session, window, pane string, ok bool) {
	sw, p, found := strings.Cut(target, ":")
	if !found {
		return "?", "?", "?", false
	}
	w, pi, found := strings.Cut(p, ".")
	if !found {
		return sw, p, "?", true
	}
	return sw, w, pi, true
}

// sessionIDsByName maps session names to their numeric id for the detected
// rows, so connector-only agents can group under the same header.
func sessionIDsByName(rows []Row) map[string]string {
	out := make(map[string]string)
	for _, r := range rows {
		if r.SessionName != "" && r.SessionName != "?" && r.SessionID != "" && r.SessionID != "?" {
			out[r.SessionName] = r.SessionID
		}
	}
	return out
}

// maxPreviewLines bounds how much pane history we pull into memory. Unbounded
// captures grow with agent count and scrollback depth.
const maxPreviewLines = 200

// capturePane grabs the pane's visible content for the preview panel,
// including SGR/color escape sequences (-e) so the preview matches the
// live pane. A seam so tests can inject deterministic captures.
var capturePane = func(paneTarget string) string {
	if paneTarget == "" || paneTarget == "?" || !tmux.Available() {
		return ""
	}
	out, err := tmux.Run("capture-pane", "-t", paneTarget, "-p", "-e", "-S", "-"+strconv.Itoa(maxPreviewLines))
	if err != nil {
		return ""
	}
	return out
}

// Test seams: the full reload touches live system state (process table,
// tmux, pane capture). These package-level vars let tests substitute
// deterministic fakes without a tmux session or real agents.
var (
	refreshDetect  = detect.All
	refreshPaneMap = func() (*pane.Map, error) {
		if !tmux.Available() {
			return nil, nil
		}
		return pane.BuildMap()
	}
	refreshBlocked = func(paneTarget string) (string, bool) {
		if paneTarget == "" || paneTarget == "?" || !tmux.Available() {
			return "", false
		}
		return blocked.DetectPanePattern(paneTarget)
	}
)

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
