package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/guillaumemeyer/tmon/internal/tmux"
)

// cmdBorder is the escape hatch for the pane-border-status feature:
// `tmon border off` unsets every pane's @tmon_border user option and turns
// pane-border-status off globally. tmon.tmux runs it at plugin load whenever
// @tmon-pane-border is off, so disabling the feature cleans up after itself.
//
//	tmon border off
func cmdBorder(args []string) int {
	if len(args) != 1 || args[0] != "off" {
		fmt.Fprintln(os.Stderr, "usage: tmon border off")
		return 2
	}
	if !tmux.Available() {
		return 0 // outside tmux there are no panes to restore
	}

	panes, err := tmux.Run("list-panes", "-a", "-F", "#{session_name}:#{window_index}.#{pane_index}")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tmon: border: list-panes:", err)
		return 1
	}
	for _, target := range strings.Fields(panes) {
		// Errors ignored per pane: a pane may vanish mid-loop.
		tmux.Run("set-option", "-u", "-p", "-t", target, "@tmon_border")
	}
	// Turn the border status strip off and clear the format we installed so
	// a custom pane-border-format is not left pointing at @tmon_border.
	tmux.Run("set-option", "-g", "pane-border-status", "off")
	tmux.Run("set-option", "-gu", "pane-border-format")
	return 0
}
