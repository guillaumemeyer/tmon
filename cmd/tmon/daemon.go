package main

import (
	"fmt"
	"os"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/poll"
)

// cmdDaemon runs the continuous polling loop that keeps state.json fresh
// and, with --notify, pops tmux messages on state transitions. It is the
// direct replacement for `monitor.sh --notify`.
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

	for {
		res, err := poll.Run(cfg, prevStatus, notifyOn)
		if err != nil {
			fmt.Fprintln(os.Stderr, "tmon: save state:", err)
		}
		prevStatus = make(map[int]agent.Status, len(res.Agents))
		for _, s := range res.Agents {
			prevStatus[s.PID] = s.Status
		}
		time.Sleep(time.Duration(cfg.PollIntervalMs) * time.Millisecond)
	}
}
