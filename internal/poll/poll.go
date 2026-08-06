// Package poll implements the single poll loop behind `tmon status`.
// Consolidating the loop here keeps evaluation and persistence in one
// place — the bash plugin had divergent copies that had already drifted.
package poll

import (
	"strings"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/blocked"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/connector"
	"github.com/guillaumemeyer/tmon/internal/detect"
	"github.com/guillaumemeyer/tmon/internal/hide"
	"github.com/guillaumemeyer/tmon/internal/pane"
	"github.com/guillaumemeyer/tmon/internal/proc"
	"github.com/guillaumemeyer/tmon/internal/theme"
	"github.com/guillaumemeyer/tmon/internal/tmux"
	"github.com/guillaumemeyer/tmon/internal/worker"
)

// Result carries one poll's output: the statuses for the status bar and the
// persisted snapshot. The json tags make `tmon status --json` (and the
// README's jq examples) read naturally.
type Result struct {
	Statuses []agent.Status     `json:"statuses"`
	Agents   []agent.AgentState `json:"agents"`
}

// Run performs one full poll: load the previous snapshot, detect baseline
// agents, overlay fresh connector records, evaluate, and persist.
func Run(cfg config.Config) (Result, error) {
	return run(cfg, connector.Collect(cfg, time.Now()))
}

// run is Run with the connector records supplied, so tests can inject
// records without touching the connector registry.
func run(cfg config.Config, records []connector.Record) (Result, error) {
	sf, err := agent.LoadState(cfg.StateFilePath())
	if err != nil {
		sf = agent.NewState() // corrupt state: start fresh rather than fail
	}

	// Pane mapping only matters inside tmux.
	var paneMap *pane.Map
	if tmux.Available() {
		paneMap, _ = pane.BuildMap()
	}

	tracker := agent.NewTracker(agent.NewOptions(cfg))
	// Seed from the last persisted snapshot so one-shot `tmon status`
	// invocations can compute CPU/IO deltas (the tracker is otherwise
	// empty on every process start).
	tracker.SeedPrev(sf.Agents)
	tracker.BeginPoll()

	// Account quota is written by the usage worker (<state>/usage.json).
	// Attach it to the newest live record per agent label so the status
	// bar and dashboard both show quota. With the worker disabled, the
	// poll probes quota itself, TTL-gated — the explicit opt-out that
	// authorizes network use in the poll.
	attachQuota(cfg, records)

	connByPID := make(map[int]connector.Record, len(records))
	for _, r := range records {
		connByPID[r.PID] = r
	}

	agents, _ := detectAgents()
	baseByPID := make(map[int]detect.Agent, len(agents))
	statuses := make([]agent.Status, 0, len(agents)+len(records))

	for _, a := range agents {
		baseByPID[a.PID] = a
		paneTarget := resolvePane(paneMap, a.PID)

		var st agent.Status
		if rec, ok := connByPID[a.PID]; ok {
			// Authoritative status wins over pane-based blocked detection:
			// the agent's own event is ground truth.
			cwd := a.CWD
			if rec.CWD != "" {
				cwd = rec.CWD
			}
			st = tracker.EvaluateAuthoritative(a.PID, a.Label, cwd, paneTarget, rec.Status, rec.Detail, rec.Title)
		} else {
			cpu, _ := readCPU(a.PID)
			io, _ := readIO(a.PID)
			st = tracker.Evaluate(a.PID, a.Label, a.CWD, paneTarget, cpu, io, paneBlocked(paneTarget))
		}
		statuses = append(statuses, st)
	}

	// Connector records whose PID the /proc signature table missed (e.g.
	// the Hermes gateway process) become new agents, resolved to a pane
	// like any other.
	for _, rec := range records {
		if _, seen := baseByPID[rec.PID]; seen {
			continue
		}
		paneTarget := resolvePane(paneMap, rec.PID)
		cwd := rec.CWD
		if cwd == "" {
			if c, err := proc.ReadCWD(rec.PID); err == nil {
				cwd = proc.CWDShort(c)
			}
		}
		st := tracker.EvaluateAuthoritative(rec.PID, rec.Label, cwd, paneTarget, rec.Status, rec.Detail, rec.Title)
		statuses = append(statuses, st)
	}

	snapshot := tracker.Snapshot()
	// Usage and profile ride along with connector records; the tracker only
	// evaluates statuses, so copy each record's enrichment onto the snapshot
	// by PID (connector-only agents included).
	for _, rec := range records {
		for i := range snapshot {
			if snapshot[i].PID != rec.PID {
				continue
			}
			if !rec.Usage.Empty() {
				u := rec.Usage
				snapshot[i].Usage = &u
			}
			if rec.Profile != "" {
				snapshot[i].Profile = rec.Profile
			}
			break
		}
	}
	tracker.EndPoll()

	// Opt-in border status strip: blocked/working panes get a colored
	// pane-border-status line; idle and exited panes clear it so the
	// border reverts to the default (empty) strip.
	if cfg.PaneBorder {
		resolved := theme.Resolve(theme.Options{
			Name:           cfg.Theme,
			ColorOverrides: cfg.ColorOverrides,
			IconOverrides:  cfg.IconOverrides,
			ASCII:          cfg.ASCII,
		})
		applyBorders(resolved, cfg.PaneBorderPosition, cfg.HidePatterns, sf.Agents, snapshot)
	}

	res := Result{Statuses: statuses, Agents: snapshot}

	sf.Agents = snapshot
	if err := sf.Save(cfg.StateFilePath()); err != nil {
		return res, err
	}
	return res, nil
}

// attachQuota enriches the newest live record per agent label with the
// account quota from usage.json (or, with the worker disabled, a TTL-gated
// lazy probe). Quota is account-level: multiple sessions of one agent share
// one window, so only the newest record carries it. The dashboard renders
// the fields from the persisted snapshot; nothing here ever blocks on the
// network except the explicit worker-off fallback.
func attachQuota(cfg config.Config, records []connector.Record) {
	var quota map[string]worker.Quota
	if worker.Disabled(cfg.StateDir, cfg) {
		quota = worker.LazyQuota(cfg)
	} else if uf, err := worker.LoadUsageFile(cfg.StateDir); err == nil {
		quota = uf.Quota
	}
	if len(quota) == 0 {
		return
	}
	newest := make(map[string]int) // label → index into records
	for i := range records {
		key := strings.ToLower(records[i].Label)
		if _, ok := quota[key]; !ok {
			continue
		}
		if j, seen := newest[key]; !seen || records[i].At.After(records[j].At) {
			newest[key] = i
		}
	}
	for key, i := range newest {
		q := quota[key]
		// A parsed window always sets Label; failed probes leave it empty.
		// Attach even at 0% used so the reset time stays visible.
		if q.Label == "" {
			continue
		}
		u := records[i].Usage
		u.QuotaPct = q.Pct
		if t, err := time.Parse(time.RFC3339, q.ResetAt); err == nil {
			u.QuotaReset = t.Local().Format("15:04")
		}
		records[i].Usage = u
	}
}

// resolvePane maps a PID to its tmux pane target, or "?" outside tmux.
func resolvePane(paneMap *pane.Map, pid int) string {
	if paneMap == nil {
		return "?"
	}
	if e, ok := paneMap.Resolve(pid); ok {
		return e.Target
	}
	return "?"
}

// Test seams: the poll loop touches live system state (process table, tmux,
// pane capture). These package-level vars let tests substitute deterministic
// fakes without a tmux session or real agents.
var (
	detectAgents = detect.All
	readCPU      = proc.ReadCPUTicks
	readIO       = proc.ReadIOBytes
	paneBlocked  = func(paneTarget string) bool {
		return paneTarget != "?" && tmux.Available() && blocked.DetectPane(paneTarget)
	}
	// Border-strip seams: per-pane user option holds a themed format fragment;
	// #{E:@tmon_border} re-expands #[fg=…] styles in pane-border-format.
	setPaneBorder = func(pane, value string) {
		if pane == "" || pane == "?" || !tmux.Available() {
			return
		}
		_, _ = tmux.Run("set-option", "-p", "-t", pane, "@tmon_border", value)
	}
	clearPaneBorder = func(pane string) {
		if pane == "" || pane == "?" || !tmux.Available() {
			return
		}
		_, _ = tmux.Run("set-option", "-u", "-p", "-t", pane, "@tmon_border")
	}
	ensureBorderChrome = func(position string) {
		if !tmux.Available() {
			return
		}
		if position != "bottom" {
			position = "top"
		}
		_, _ = tmux.Run("set-option", "-g", "pane-border-status", position)
		_, _ = tmux.Run("set-option", "-g", "pane-border-format", "#{E:@tmon_border}")
	}
)

// applyBorders syncs the pane-border status strip with the current snapshot.
// Every poll rewrites blocked/working strips so enabling the feature
// mid-session still paints borders. Idle and exited panes clear
// @tmon_border so the strip reverts to the default (empty) appearance.
// Agents matching the hide patterns get no strip; a strip left over from a
// previously visible agent is cleared (prev is intentionally unfiltered so
// the clear pass can reach it).
func applyBorders(t theme.Theme, position string, hidePatterns []string, prev, snap []agent.AgentState) {
	ensureBorderChrome(position)

	prevByPane := make(map[string]agent.Status, len(prev))
	for _, a := range prev {
		if a.Pane != "" && a.Pane != "?" {
			prevByPane[a.Pane] = a.Status
		}
	}
	snapByPane := make(map[string]agent.Status, len(snap))
	for _, a := range snap {
		if a.Pane == "" || a.Pane == "?" {
			continue
		}
		if hide.ShouldHide(hidePatterns, a.Label, a.CWD, hide.SessionFromPane(a.Pane)) {
			continue
		}
		snapByPane[a.Pane] = a.Status
	}

	for pane := range prevByPane {
		if _, ok := snapByPane[pane]; !ok {
			clearPaneBorder(pane)
		}
	}

	for pane, st := range snapByPane {
		switch st {
		case agent.StatusBlocked, agent.StatusWorking:
			setPaneBorder(pane, borderLine(t, st))
		default:
			clearPaneBorder(pane)
		}
	}
}

// borderLine is the pane-border-format fragment for a status. Idle is not
// rendered here — callers clear the option instead so the border reverts
// to default. Working uses the static theme working icon (not the spinner).
func borderLine(t theme.Theme, st agent.Status) string {
	var color, icon string
	switch st {
	case agent.StatusBlocked:
		color, icon = t.Palette.Blocked, t.Icons.Blocked
	case agent.StatusWorking:
		color, icon = t.Palette.Working, t.Icons.Working
	default:
		return ""
	}
	return "#[fg=" + color + "] " + icon + " " + string(st) + " "
}
