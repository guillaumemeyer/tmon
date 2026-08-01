package main

import (
	"fmt"
	"os"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/blocked"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/detect"
	"github.com/guillaumemeyer/tmon/internal/pane"
	"github.com/guillaumemeyer/tmon/internal/proc"
	"github.com/guillaumemeyer/tmon/internal/tmux"
)

// cmdDaemon runs the continuous polling loop that keeps state.json fresh and,
// with --notify, pops tmux messages on state transitions. It is the direct
// replacement for `monitor.sh --notify`.
func cmdDaemon(args []string) int {
	cfg := config.FromEnv()
	notifyOn := false
	for _, a := range args {
		switch a {
		case "--notify":
			notifyOn = true
		default:
			fmt.Fprintf(os.Stderr, "tmon: unknown daemon flag %q\n", a)
			return 2
		}
	}

	// Seed "previous statuses" from the state file so a daemon started while
	// agents are already running can announce them on the first poll.
	prevStatus := map[int]agent.Status{}
	if sf, err := agent.LoadState(cfg.StateFilePath()); err == nil {
		for _, s := range sf.Agents {
			prevStatus[s.PID] = s.Status
		}
	}

	tracker := agent.NewTracker(agent.NewOptions(cfg))

	for {
		sf, err := agent.LoadState(cfg.StateFilePath())
		if err != nil {
			sf = agent.NewState() // corrupt state: start fresh
		}
		frame := sf.Frame + 1

		var paneMap *pane.Map
		if tmux.Available() {
			paneMap, _ = pane.BuildMap()
		}

		tracker.BeginPoll()
		agents, _ := detect.All()
		statuses := make([]agent.Status, 0, len(agents))
		for _, a := range agents {
			paneTarget := "?"
			if paneMap != nil {
				if e, ok := paneMap.Resolve(a.PID); ok {
					paneTarget = e.Target
				}
			}
			isBlocked := paneTarget != "?" && tmux.Available() && blocked.DetectPane(paneTarget)

			cpu, _ := proc.ReadCPUTicks(a.PID)
			io, _ := proc.ReadIOBytes(a.PID)
			st := tracker.Evaluate(a.PID, a.Label, a.CWD, paneTarget, cpu, io, isBlocked)

			if notifyOn {
				// The bash daemon compared each transition against the status
				// it had just written during evaluation, so its notifications
				// could never fire. Compare against the previous poll instead.
				if old, seen := prevStatus[a.PID]; seen && st != old {
					notifyTransition(a.Label, old, st, a.CWD)
				}
				prevStatus[a.PID] = st
			}
			statuses = append(statuses, st)
		}
		snapshot := tracker.Snapshot()
		tracker.EndPoll()

		sf.Frame = frame % 100
		sf.Agents = snapshot
		if err := sf.Save(cfg.StateFilePath()); err != nil {
			fmt.Fprintln(os.Stderr, "tmon: save state:", err)
		}

		time.Sleep(time.Duration(cfg.PollIntervalMs) * time.Millisecond)
	}
}

// notifyTransition pops a tmux display-message on notable transitions,
// mirroring the bash plugin's notify_state_change. Only "started" (running)
// and "active" transitions are announced; idling is silent.
func notifyTransition(label string, old, new agent.Status, cwd string) {
	var msg string
	switch new {
	case agent.StatusActive:
		msg = label + " is now active"
	case agent.StatusRunning:
		msg = label + " started"
	default:
		return
	}
	if cwd != "" {
		msg += " in " + cwd
	}
	if tmux.Available() {
		tmux.Run("display-message", msg)
	}
}
