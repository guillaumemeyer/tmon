// Package statusbar renders the tmux status-bar indicator.
package statusbar

import (
	"fmt"

	"github.com/guillaumemeyer/tmon/internal/agent"
)

// tmux style directives (identical to the bash plugin's).
const (
	fgGreen  = "#[fg=green]"
	fgOrange = "#[fg=colour208]"
	fgBlue   = "#[fg=blue]"
	fgCyan   = "#[fg=cyan]"
	reset    = "#[default]"
)

// Render builds the indicator line, e.g. "[@] ? 2 - ● 3 - ‖ 1".
// frame toggles the animated characters on odd values. The empty state is
// the fixed-width "[@] ? 0 - ● 0 - ‖ 0" so the status bar never jumps.
func Render(statuses []agent.Status, frame int) string {
	var blocked, active, idle int
	for _, s := range statuses {
		switch s {
		case agent.StatusBlocked:
			blocked++
		case agent.StatusActive, agent.StatusRunning:
			active++ // the green bucket includes both
		case agent.StatusIdle:
			idle++
		}
	}

	if len(statuses) == 0 {
		return fgCyan + "[@]" + reset + " " +
			fgOrange + "?" + " 0" + reset + " - " +
			fgGreen + "●" + " 0" + reset + " - " +
			fgBlue + "‖" + " 0" + reset
	}

	odd := frame%2 != 0
	bChar, aChar := "?", "●"
	if blocked > 0 && odd {
		bChar = "!"
	}
	if active > 0 && odd {
		aChar = "!"
	}

	return fgCyan + "[@]" + reset + " " +
		fgOrange + bChar + fmt.Sprintf("%2d", blocked) + reset + " - " +
		fgGreen + aChar + fmt.Sprintf("%2d", active) + reset + " - " +
		fgBlue + "‖" + fmt.Sprintf("%2d", idle) + reset
}
