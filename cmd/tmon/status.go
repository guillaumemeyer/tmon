package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/hide"
	"github.com/guillaumemeyer/tmon/internal/poll"
	"github.com/guillaumemeyer/tmon/internal/statusbar"
	"github.com/guillaumemeyer/tmon/internal/worker"
)

// cmdStatus performs one poll and prints the status-bar indicator for tmux
// #() interpolation. It must complete quickly and must never touch the
// network — all distribution concerns live in tmon.tmux and bootstrap.sh.
// The only exception is the worker-off fallback: with TMON_WORKER=off the
// poll itself runs the quota probes, TTL-gated to once per 15 minutes.
// With --json it prints the full poll result instead (statuses plus each
// agent's pane, cwd, detail and usage), for scripts, polybar and the like.
func cmdStatus(args []string) int {
	jsonOut := false
	if len(args) > 0 {
		if len(args) == 1 && args[0] == "--json" {
			jsonOut = true
		} else {
			fmt.Fprintln(os.Stderr, "usage: tmon status [--json]")
			return 2
		}
	}

	cfg := config.FromEnv()

	// Ensure the background usage worker is running (cheap: one fork+exec
	// when the heartbeat is missing or stale; a no-op otherwise). The poll
	// below reads the quota data it writes; the worker owns all network
	// access, so this status command never touches the network.
	worker.EnsureSpawned(cfg)

	res, err := poll.Run(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tmon: save state:", err)
	}

	// Hide patterns apply to the indicator and the --json view alike, so the
	// status bar and the dashboard never disagree about what is visible.
	res.Agents = filterHiddenAgents(res.Agents, cfg)

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fmt.Fprintln(os.Stderr, "tmon: json:", err)
			return 1
		}
		return 0
	}

	fmt.Print(statusbar.Render(res.Agents, cfg.BoldCounts, resolveTheme(cfg), cfg.ContextWarn))
	return 0
}

// filterHiddenAgents drops agents that match the configured hide patterns.
// The session name is parsed from the agent's stored pane target, matching
// how the dashboard filters rows.
func filterHiddenAgents(agents []agent.AgentState, cfg config.Config) []agent.AgentState {
	if len(cfg.HidePatterns) == 0 {
		return agents
	}
	kept := agents[:0]
	for _, a := range agents {
		if !hide.ShouldHide(cfg.HidePatterns, a.Label, a.CWD, hide.SessionFromPane(a.Pane)) {
			kept = append(kept, a)
		}
	}
	return kept
}
