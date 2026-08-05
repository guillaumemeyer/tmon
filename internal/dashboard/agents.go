package dashboard

import (
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/theme"
)

// agentItem is one flat row of the agent list: the agent plus the stripped
// pane capture baked in at rebuild time (so search can match preview content
// that is not currently visible).
type agentItem struct {
	row     Row
	capture string
}

// FilterValue is what the list would filter on if its built-in filter were
// enabled. tmon drives filtering itself (fuzzy search), but keeping the
// value correct means the fields never regress.
func (i agentItem) FilterValue() string {
	r := i.row
	name := agentDisplayName(r)
	return strings.Join([]string{r.Title, name, r.Profile, r.CWD, displayCWD(r.CWD), i.capture}, "\n")
}

// newAgentList builds the bubbles list used for the agent pane. tmon owns
// search, filtering, quit and resize keys, so the list's own bindings for
// those are disabled and only navigation is forwarded to it.
func newAgentList() list.Model {
	l := list.New(nil, agentDelegate{}, 40, 20)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowPagination(false)
	l.SetShowHelp(false)
	l.SetShowFilter(false)
	l.SetFilteringEnabled(false)
	l.InfiniteScrolling = true // wrap at the ends, like the bash popup

	km := list.DefaultKeyMap()
	km.PrevPage.SetEnabled(false) // left/h (resize) and b (filter) are ours
	km.NextPage.SetEnabled(false) // right/l (resize) and d are ours
	km.Filter.SetEnabled(false)   // "/" is tmon's fuzzy search
	km.ClearFilter.SetEnabled(false)
	km.Quit.SetEnabled(false)      // q/esc are ours
	km.ForceQuit.SetEnabled(false) // ctrl+c is ours
	km.ShowFullHelp.SetEnabled(false)
	km.CloseFullHelp.SetEnabled(false)
	l.KeyMap = km
	return l
}

// agentDelegate renders a four-line agent row. The height is fixed because
// bubbles lists require uniform item heights: name, cwd/age/pause, the tmux
// pane location, and the usage line (blank when the connector reported
// none). The delegate is a plain value — styles, icons and the current
// spinner frame are baked in by View/WithTheme, so there are no stale
// references between model copies.
type agentDelegate struct {
	st          styles
	icons       theme.Icons
	contextWarn int
	width       int
	spinner     string // current spinner frame; "" when unused
}

func (d agentDelegate) Height() int  { return 4 }
func (d agentDelegate) Spacing() int { return 0 }

// Update is part of list.ItemDelegate; tmon items have no per-key behavior.
func (d agentDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

// Render writes the four lines of one agent row to w. Like the default
// delegate, it must not end with a trailing newline: bubbles adds the
// inter-item separator itself, so a trailing newline would double up.
func (d agentDelegate) Render(w io.Writer, lm list.Model, index int, item list.Item) {
	it, ok := item.(agentItem)
	if !ok {
		return
	}
	io.WriteString(w, strings.Join(d.renderRow(it.row, lm.Index() == index), "\n"))
}

// renderRow renders the four lines of one agent row, each exactly d.width
// cells wide. Every line shares a one-cell left margin so the list sits
// flush left against the popup edge. Line 1 carries the status icon +
// identity-colored name; line 2 the cwd, status age and (for blocked
// agents) the pause reason; line 3 the tmux Session/Window/Pane location in
// dim; line 4 the usage line or a blank row when the connector reported no
// stats. The selected row gets the full-line selection background.
func (d agentDelegate) renderRow(r Row, selected bool) []string {
	st := d.st

	// Line 1: status icon then the display name in the agent's identity
	// color, bold. The selected row keeps the selection background on
	// every piece so the highlight spans the full width.
	icon := d.statusIcon(r.Status)
	disp := agentDisplayName(r)
	var line1 string
	if selected {
		bg := st.selBgColor
		line1 = selFit(st,
			st.selBg.Render(" ")+
				statusStyle(st, r.Status).Background(bg).Render(icon)+
				identityStyle(st, r.Label).Bold(true).Background(bg).Render(disp),
			d.width,
		)
	} else {
		line1 = fit(
			" "+
				statusStyle(st, r.Status).Render(icon)+
				identityStyle(st, r.Label).Bold(true).Render(disp),
			d.width,
		)
	}

	// Line 2: cwd, status age, and the blocked pause reason (orange).
	cwd := displayCWD(r.CWD)
	age := ""
	if a := ageString(r.LastTs); a != "" {
		age = "  " + a
	}
	pause := ""
	if r.Status == agent.StatusBlocked {
		pause = r.BlockedReason
		if pause == "" {
			pause = "paused"
		}
	}
	var line2 string
	if selected {
		text := " " + cwd + age
		if pause != "" {
			text += "  " + pause
		}
		line2 = st.selDim.Render(fit(text, d.width))
	} else {
		line2 = " " + st.dim.Render(cwd)
		if age != "" {
			line2 += st.dim.Render(age)
		}
		if pause != "" {
			line2 += st.orange.Render("  " + pause)
		}
		line2 = fit(line2, d.width)
	}

	// Line 3: the tmux location, dimmed — "tmux: main / shell / 0".
	line3 := " " + st.dim.Render("tmux: "+tmuxPath(r))
	if selected {
		line3 = st.selDim.Render(fit(line3, d.width))
	} else {
		line3 = fit(line3, d.width)
	}

	// Line 4: usage stats or a blank row (uniform item height). The bar
	// width is derived from the row width, and the line is padded to the
	// full width so the selection highlight and alignment stay uniform.
	// The left margin is budgeted out of the row width so the bar still
	// fits; on the selected row the margin sits on the selection
	// background too, keeping the highlight continuous.
	usage := usageLine(st, d.contextWarn, r.Usage, selected, d.width-1)
	var line4 string
	switch {
	case usage == "" && selected:
		line4 = st.selDim.Render(fit(" ", d.width))
	case usage == "":
		line4 = fit(" ", d.width)
	case selected:
		line4 = st.selBg.Render(" ") + selFit(st, usage, d.width-1)
	default:
		line4 = fit(" "+usage, d.width)
	}

	return []string{line1, line2, line3, line4}
}

// statusIcon returns the glyph for the agent's status: the working icon is
// the animated spinner frame when one is available, otherwise the theme
// icon. Blocked and idle always use their theme icons.
func (d agentDelegate) statusIcon(s agent.Status) string {
	if s == agent.StatusWorking && d.spinner != "" {
		return d.spinner + " "
	}
	return d.icons.ForStatus(s) + " "
}

// tmuxPath renders the agent's tmux location as "Session / Window / Pane":
// each segment prefers the human name when one is known and falls back to
// the numeric index (session id, window index, pane index). "?" when the
// agent has no pane; the raw target when no structured segment is known.
func tmuxPath(r Row) string {
	if r.Pane == "" || r.Pane == "?" {
		return "?"
	}
	session := firstKnown(r.SessionName, r.SessionID)
	window := firstKnown(r.WindowName, r.WindowIndex)
	pane := firstKnown(r.PaneIndex)
	if session == "" && window == "" && pane == "" {
		return r.Pane
	}
	return strings.Join([]string{session, window, pane}, " / ")
}

// firstKnown returns the first non-empty value that is not the unknown
// marker "?".
func firstKnown(vals ...string) string {
	for _, v := range vals {
		if v != "" && v != "?" {
			return v
		}
	}
	return ""
}
