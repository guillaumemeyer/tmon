// Package statusbar renders the tmux status-bar indicator.
package statusbar

import (
	"fmt"
	"strings"

	"github.com/guillaumemeyer/tmon/internal/agent"
)

// tmux style directives (identical to the bash plugin's).
const (
	fgGreen  = "#[fg=green]"
	fgOrange = "#[fg=colour208]"
	fgBlue   = "#[fg=blue]"
	fgCyan   = "#[fg=cyan]"
	boldOn   = "#[bold]"
	reset    = "#[default]"
)

// Render builds the indicator line: "🤖-🚨1-⚡️2-💤3 " in emoji mode (ascii
// false) or "[@]-B1-W2-I3 " in ASCII mode. Each status segment (icon + count)
// is rendered only when that status has at least one agent, so an empty fleet
// renders as just the app icon. Icons are static — there is no pulse
// animation. When bold is true, each count digit is wrapped in #[bold].
func Render(statuses []agent.Status, ascii bool, bold bool) string {
	var blocked, working, idle int
	for _, s := range statuses {
		switch s {
		case agent.StatusBlocked:
			blocked++
		case agent.StatusWorking:
			working++
		case agent.StatusIdle:
			idle++
		}
	}

	app := "[@]"
	bGlyph, wGlyph, iGlyph := "B", "W", "I"
	if !ascii {
		app = "🤖"
		bGlyph, wGlyph, iGlyph = "🚨", "⚡️", "💤"
	}

	var segs []string
	add := func(glyph, color string, n int) {
		if n <= 0 {
			return
		}
		count := fmt.Sprintf("%d", n)
		if bold {
			count = boldOn + count
		}
		segs = append(segs, color+glyph+count+reset)
	}
	add(bGlyph, fgOrange, blocked)
	add(wGlyph, fgGreen, working)
	add(iGlyph, fgBlue, idle)

	line := fgCyan + app + reset
	if len(segs) > 0 {
		line += "-" + strings.Join(segs, "-")
	}
	return line + " "
}
