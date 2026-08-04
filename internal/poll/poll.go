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
	"github.com/guillaumemeyer/tmon/internal/tmux"
)

// Result carries one poll's output: the statuses for the status bar, the
// persisted snapshot, and the frame that drives the animated characters.
type Result struct {
	Frame    int
	Statuses []agent.Status
	Agents   []agent.AgentState
}

// Run performs one full poll: load the previous frame, detect baseline
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
	frame := sf.Frame + 1

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
			st = tracker.EvaluateAuthoritative(a.PID, a.Label, cwd, paneTarget, rec.Status, rec.Detail)
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
		st := tracker.EvaluateAuthoritative(rec.PID, rec.Label, cwd, paneTarget, rec.Status, rec.Detail)
		statuses = append(statuses, st)
	}

	snapshot := tracker.Snapshot()
	tracker.EndPoll()

	if notify {
		notifyTransitions(prevStatus, snapshot)
	}

	res := Result{Frame: frame, Statuses: statuses, Agents: snapshot}

	sf.Frame = frame % 100
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
func notifyTransitions(prev map[int]agent.Status, snap []agent.AgentState) {
	for _, s := range snap {
		old, seen := prev[s.PID]
		if !seen {
			continue
		}
		if s.Status != old {
			announce(s.Label, old, s.Status, s.CWD)
		}
	}
}

// announce is the notification sink, overridable in tests.
var announce = notifyTransition

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
// silent ones. Only "started" (running) and "active" are announced; blocked
// and idling are quiet.
func transitionMessage(label string, old, new agent.Status, cwd string) string {
	if old == new {
		return ""
	}
	var msg string
	switch new {
	case agent.StatusActive:
		msg = label + " is now active"
	case agent.StatusRunning:
		msg = label + " started"
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
)
