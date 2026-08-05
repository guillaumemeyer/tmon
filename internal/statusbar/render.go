// Package statusbar renders the tmux status-bar indicator.
package statusbar

import (
	"fmt"
	"strings"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/theme"
)

// tmux style directives.
const (
	boldOn = "#[bold]"
	reset  = "#[default]"
)

// Render builds the indicator line — "🤖-🚨1-|2-💤3 " in emoji mode or
// "[@]-B1-|2-I3 " in ASCII mode — colored by the theme palette. Working
// agents show the same bubbles Line spinner the dashboard animates: the
// frame advances with wall-clock time (tmux re-renders `tmon status` every
// status-interval, so each status refresh shows the next frame). Each status
// segment (icon + count) appears only when that status has at least one
// agent, so an empty fleet renders as just the app icon. Icons and colors
// come from the resolved theme, including any @tmon-icon-* / @tmon-color-*
// overrides. When bold is true, each count digit is wrapped in #[bold].
//
// warnPct is the context-usage percent at which a warning segment (⚠️/! in
// the theme's warn color) is appended to the indicator — e.g. "🤖-|2-⚠️" —
// when any agent's context usage reaches it. 0 disables the warning.
func Render(agents []agent.AgentState, bold bool, t theme.Theme, warnPct int) string {
	var blocked, working, idle int
	for _, a := range agents {
		switch a.Status {
		case agent.StatusBlocked:
			blocked++
		case agent.StatusWorking:
			working++
		case agent.StatusIdle:
			idle++
		}
	}

	pal, ic := t.Palette, t.Icons

	var segs []string
	add := func(glyph, color string, n int) {
		if n <= 0 {
			return
		}
		count := fmt.Sprintf("%d", n)
		if bold {
			count = boldOn + count
		}
		segs = append(segs, "#[fg="+color+"]"+glyph+count+reset)
	}
	add(ic.Blocked, pal.Blocked, blocked)
	add(theme.SpinnerFrame(), pal.Working, working)
	add(ic.Idle, pal.Idle, idle)

	if warnPct > 0 {
		for _, a := range agents {
			if a.Usage != nil && a.Usage.ContextPct() >= warnPct {
				segs = append(segs, "#[fg="+pal.Warn+"]"+ic.Warn+reset)
				break
			}
		}
	}

	line := "#[fg=" + pal.App + "]" + ic.App + reset
	if len(segs) > 0 {
		line += "-" + strings.Join(segs, "-")
	}
	return line + " "
}
