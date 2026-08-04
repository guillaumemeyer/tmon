package main

import (
	"fmt"
	"os"

	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/poll"
	"github.com/guillaumemeyer/tmon/internal/statusbar"
)

// cmdStatus performs one poll and prints the status-bar indicator for tmux
// #() interpolation. It must complete quickly and must never touch the
// network — all distribution concerns live in tmon.tmux and bootstrap.sh.
func cmdStatus(args []string) int {
	cfg := config.FromEnv()

	res, err := poll.Run(cfg, nil, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tmon: save state:", err)
	}

	fmt.Print(statusbar.Render(res.Statuses, cfg.ASCII, cfg.BoldCounts))
	return 0
}
