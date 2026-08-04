package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/guillaumemeyer/tmon/internal/tmux"
)

// cmdTint is the escape hatch for the pane-glow feature: `tmon tint off`
// force-restores every pane in every session to default colors. It undoes
// stale tints — panes left colored by a previous session, or after turning
// @tmon-pane-tint off. tmon.tmux runs it at plugin load whenever the option
// is off, so disabling the feature also cleans up after itself.
//
//	tmon tint off
func cmdTint(args []string) int {
	if len(args) != 1 || args[0] != "off" {
		fmt.Fprintln(os.Stderr, "usage: tmon tint off")
		return 2
	}
	if !tmux.Available() {
		return 0 // outside tmux there are no panes to restore
	}

	panes, err := tmux.Run("list-panes", "-a", "-F", "#{session_name}:#{window_index}.#{pane_index}")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tmon: tint: list-panes:", err)
		return 1
	}
	for _, target := range strings.Fields(panes) {
		// Errors are ignored per pane: a pane may vanish mid-loop, and a
		// failed restore on one target shouldn't abort the cleanup.
		tmux.Run("select-pane", "-t", target, "-P", "bg=default,fg=default")
	}
	return 0
}
