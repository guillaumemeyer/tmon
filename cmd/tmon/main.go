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
  tmon hooks <cmd>       Install/remove agent lifecycle hooks:
                           tmon hooks install <agent>   (claude|codex|cursor|copilot|windsurf)
                           tmon hooks remove  <agent>
                           tmon hooks auto             Install for agents found on this machine
                           tmon hooks status
  tmon version           Print the installed version

Environment (set by tmon.tmux from the @tmon-* tmux options):
  TMON_STATE_DIR              State dir; default <plugin>/state
  TMON_BIN_DIR                Binary dir; default <plugin>/bin
  TMON_POLL_INTERVAL_MS       Poll interval in ms (default 3000)
  TMON_ACTIVITY_THRESHOLD_MS  CPU floor for "working" in ms/s (default 500)
  TMON_IO_ACTIVITY_THRESHOLD  Min IO bytes/poll for "working" (default 102400)
  TMON_IDLE_DECAY_POLLS       Grace polls before flagging "idle" (default 3)
  TMON_ASCII_ICONS            Render status icons as ASCII instead of emoji
                              (default 0 = emoji 🤖 🛑 ⚡️ 💤; 1 = [@] B W I)
  TMON_CONNECTORS             Connector selection: "auto" or a comma list
                              (default auto; agents' own state sources)
  TMON_CONNECTOR_FRESHNESS    Seconds a connector signal stays valid (default 30)
  TMON_HOOK_STATE_DIR         Dir where installed hooks write session state
                              (default <state>/hooks)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "status":
		os.Exit(cmdStatus(os.Args[2:]))
	case "daemon":
		os.Exit(cmdDaemon(os.Args[2:]))
	case "dashboard":
		os.Exit(cmdDashboard(os.Args[2:]))
	case "hooks":
		os.Exit(cmdHooks(os.Args[2:]))
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
