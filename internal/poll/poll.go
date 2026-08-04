// Package poll implements the single shared poll loop behind both `tmon
// status` and `tmon daemon`. Consolidating the loop here guarantees the two
// subcommands can never drift apart — the bash plugin had two divergent
// copies of this logic that had already drifted.
package poll

import (
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/blocked"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/connector"
	"github.com/guillaumemeyer/tmon/internal/detect"
	"github.com/guillaumemeyer/tmon/internal/pane"
	"github.com/guillaumemeyer/tmon/internal/proc"
	"github.com/guillaumemeyer/tmon/internal/theme"
	"github.com/guillaumemeyer/tmon/internal/tmux"
)

// Result carries one poll's output: the statuses for the status bar and the
// persisted snapshot. The json tags make `tmon status --json` (and the
// README's jq examples) read naturally.
type Result struct {
	Statuses []agent.Status     `json:"statuses"`
	Agents   []agent.AgentState `json:"agents"`
}

// Run performs one full poll: load the previous snapshot, detect baseline
// agents, overlay fresh connector records, evaluate, persist, and (with
// notify) announce transitions against prevStatus. prevStatus maps PID to
// the previous poll's status; pass nil to skip notifications.
func Run(cfg config.Config, prevStatus map[int]agent.Status, notify bool) (Result, error) {
	return run(cfg, prevStatus, notify, connector.Collect(cfg, time.Now()))
}

// run is Run with the connector records supplied, so tests can inject
// records without touching the connector registry.
func run(cfg config.Config, prevStatus map[int]agent.Status, notify bool, records []connector.Record) (Result, error) {
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
	tracker.BeginPoll()

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
			cpu, _ := proc.ReadCPUTicks(a.PID)
			io, _ := proc.ReadIOBytes(a.PID)
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

	if notify {
		notifyTransitions(prevStatus, snapshot, cfg.BlockedBell)
	}

	// Opt-in pane glow: when a pane's agent status changed, tint the pane's
	// content area so a blocked agent is visible across the room. Only
	// changed panes generate tmux round-trips; steady state is silent.
	if cfg.PaneTint {
		pal := theme.Resolve(theme.Options{
			Name:           cfg.Theme,
			ColorOverrides: cfg.ColorOverrides,
			IconOverrides:  cfg.IconOverrides,
			ASCII:          cfg.ASCII,
		}).Palette
		applyTints(pal, sf.Agents, snapshot)
	}

	res := Result{Statuses: statuses, Agents: snapshot}

	sf.Agents = snapshot
	if err := sf.Save(cfg.StateFilePath()); err != nil {
		return res, err
	}
	return res, nil
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

// notifyTransitions compares the fresh snapshot against the previous poll's
// statuses and announces each change. Agents not present in prev (newly
// seen, or the daemon's first poll after a fresh state file) are silent.
// When bellOn is set, a transition into blocked also rings the terminal
// bell — only transitions, never steady state (the daemon path carries
// prevStatus; `tmon status` is transition-free by design).
func notifyTransitions(prev map[int]agent.Status, snap []agent.AgentState, bellOn bool) {
	for _, s := range snap {
		old, seen := prev[s.PID]
		if !seen {
			continue
		}
		if s.Status != old {
			announce(s.Label, old, s.Status, s.CWD)
			if bellOn && s.Status == agent.StatusBlocked {
				ringBell()
			}
		}
	}
}

// announce is the notification sink, overridable in tests.
var announce = notifyTransition

// ringBell rings the terminal bell via tmux, overridable in tests.
var ringBell = func() {
	if tmux.Available() {
		tmux.Run("run-shell", `printf '\a'`)
	}
}

// notifyTransition pops a tmux display-message on notable transitions,
// mirroring the bash plugin's notify_state_change.
func notifyTransition(label string, old, new agent.Status, cwd string) {
	msg := transitionMessage(label, old, new, cwd)
	if msg == "" {
		return
	}
	if tmux.Available() {
		tmux.Run("display-message", msg)
	}
}

// transitionMessage returns the display-message for a transition, or "" for
// silent ones. Only "working" is announced; blocked and idling are quiet.
// First sightings land in idle, so there is no "started" toast anymore.
func transitionMessage(label string, old, new agent.Status, cwd string) string {
	if old == new {
		return ""
	}
	var msg string
	switch new {
	case agent.StatusWorking:
		msg = label + " is now working"
	default:
		return ""
	}
	if cwd != "" {
		msg += " in " + cwd
	}
	return msg
}

// Test seams: the poll loop touches live system state (process table, tmux,
// pane capture). These package-level vars let tests substitute deterministic
// fakes without a tmux session or real agents.
var (
	detectAgents = detect.All
	paneBlocked  = func(paneTarget string) bool {
		return paneTarget != "?" && tmux.Available() && blocked.DetectPane(paneTarget)
	}
	tintPane = func(pane, style string) {
		if pane == "" || pane == "?" || !tmux.Available() {
			return
		}
		tmux.Run("select-pane", "-t", pane, "-P", style) // errors ignored: pane may have vanished
	}
)

// applyTints diffs the previous poll's agents (loaded from state.json)
// against the fresh snapshot and recolors the panes whose status changed:
// blocked and working panes get a darkened palette tint, idle panes are
// cleared, and panes whose agent exited are restored to default colors.
// Unchanged panes are skipped entirely — tinting only pays for transitions.
// New agents are left alone when they first land in idle (their pane may
// never have been tinted); a connector reporting working/blocked on first
// sight still tints immediately.
func applyTints(pal theme.Palette, prev, snap []agent.AgentState) {
	prevByPane := make(map[string]agent.Status, len(prev))
	for _, a := range prev {
		if a.Pane != "" && a.Pane != "?" {
			prevByPane[a.Pane] = a.Status
		}
	}
	snapByPane := make(map[string]agent.Status, len(snap))
	for _, a := range snap {
		if a.Pane != "" && a.Pane != "?" {
			snapByPane[a.Pane] = a.Status
		}
	}

	// Agents that exited: put their panes back to normal.
	for pane := range prevByPane {
		if _, ok := snapByPane[pane]; !ok {
			tintPane(pane, tintStyle(pal, agent.StatusIdle))
		}
	}

	for pane, st := range snapByPane {
		old, seen := prevByPane[pane]
		if seen {
			if old == st {
				continue // unchanged: no tmux traffic
			}
		} else if st == agent.StatusIdle {
			continue // brand-new idle pane: never tinted, leave it alone
		}
		tintPane(pane, tintStyle(pal, st))
	}
}

// tintStyle is the select-pane -P style for a status: a darkened palette
// background for blocked/working, default colors for idle/exited.
func tintStyle(pal theme.Palette, st agent.Status) string {
	switch st {
	case agent.StatusBlocked:
		return "bg=" + theme.Tint(pal.Blocked) + ",fg=default"
	case agent.StatusWorking:
		return "bg=" + theme.Tint(pal.Working) + ",fg=default"
	default:
		return "bg=default,fg=default"
	}
}
