package main

import (
	"fmt"
	"os"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/blocked"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/detect"
	"github.com/guillaumemeyer/tmon/internal/pane"
	"github.com/guillaumemeyer/tmon/internal/proc"
	"github.com/guillaumemeyer/tmon/internal/statusbar"
	"github.com/guillaumemeyer/tmon/internal/tmux"
)

// cmdStatus performs one poll and prints the status-bar indicator for tmux
// #() interpolation. It must complete quickly and must never touch the
// network — all distribution concerns live in tmon.tmux and bootstrap.sh.
func cmdStatus(args []string) int {
	cfg := config.FromEnv()

	sf, err := agent.LoadState(cfg.StateFilePath())
	if err != nil {
		sf = agent.NewState() // corrupt state: start fresh rather than fail the status bar
	}
	frame := sf.Frame + 1

	// Pane mapping only matters inside tmux.
	var paneMap *pane.Map
	if tmux.Available() {
		paneMap, _ = pane.BuildMap()
	}

	tracker := agent.NewTracker(agent.NewOptions(cfg))
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
		statuses = append(statuses, tracker.Evaluate(a.PID, a.Label, a.CWD, paneTarget, cpu, io, isBlocked))
	}

	snapshot := tracker.Snapshot()
	tracker.EndPoll()

	sf.Frame = frame % 100
	sf.Agents = snapshot
	if err := sf.Save(cfg.StateFilePath()); err != nil {
		fmt.Fprintln(os.Stderr, "tmon: save state:", err)
	}

	fmt.Print(statusbar.Render(statuses, frame))
	return 0
}
