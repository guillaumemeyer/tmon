package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/poll"
	"github.com/guillaumemeyer/tmon/internal/statusbar"
)

// cmdStatus performs one poll and prints the status-bar indicator for tmux
// #() interpolation. It must complete quickly and must never touch the
// network — all distribution concerns live in tmon.tmux and bootstrap.sh.
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

	res, err := poll.Run(cfg, nil, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tmon: save state:", err)
	}

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
