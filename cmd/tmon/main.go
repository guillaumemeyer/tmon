// tmon — a tmux status-bar monitor for AI coding agents.
//
// Subcommands are registered here as they are implemented; the full set is
// documented in the usage text.
package main

import (
	"fmt"
	"os"
)

const usageText = `tmon — tmux AI agent monitor

Usage:
  tmon status            Print the status-bar indicator (used by tmux #())
  tmon daemon [--notify] Run the polling loop (optionally with notifications)
  tmon dashboard         Open the interactive agent navigation popup
  tmon version           Print the installed version

Environment (set by tmon.tmux from the @tmon-* tmux options):
  TMON_STATE_DIR              State dir; default <plugin>/state
  TMON_BIN_DIR                Binary dir; default <plugin>/bin
  TMON_POLL_INTERVAL_MS       Poll interval in ms (default 3000)
  TMON_ACTIVITY_THRESHOLD_MS  CPU floor for "active" in ms/s (default 500)
  TMON_IO_ACTIVITY_THRESHOLD  Min IO bytes/poll for "active" (default 102400)
  TMON_IDLE_DECAY_POLLS       Idle grace period in polls (default 3)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "status":
		os.Exit(cmdStatus(os.Args[2:]))
	// Subcommands are registered here as they are implemented.
	case "version":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Print(usageText)
	default:
		fmt.Fprintf(os.Stderr, "tmon: unknown command %q\n\n", os.Args[1])
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
}
