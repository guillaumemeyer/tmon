package dashboard

import (
	"io"
	"strings"

	bkey "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
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
	return strings.Join([]string{
		name, r.Profile, r.Label, r.Title,
		r.SessionName, r.WindowName, tmuxPath(r),
		r.CWD, displayCWD(r.CWD), r.Branch, r.PR,
		i.capture,
	}, "\n")
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
	// g and G are the preview's jump keys (gg = top, G = tail), so the list
	// keeps only home/end for go-to-start/end.
	km.GoToStart = bkey.NewBinding(bkey.WithKeys("home"), bkey.WithHelp("home", "go to start"))
	km.GoToEnd = bkey.NewBinding(bkey.WithKeys("end"), bkey.WithHelp("end", "go to end"))
	l.KeyMap = km
	return l
}

// agentDelegate renders an agent row. The height is uniform because bubbles
// lists require uniform item heights: the three lines are name, project and
// pane. The context and account-quota usage lines moved to the preview pane,
// so the delegate no longer renders them. The delegate is a plain value —
// styles, icons and the current spinner frame are baked in by View/WithTheme,
// so there are no stale references between model copies.
type agentDelegate struct {
	st      styles
	icons   theme.Icons
	width   int
	spinner string // current spinner frame; "" when unused
}

func (d agentDelegate) Height() int  { return agentItemHeight }
func (d agentDelegate) Spacing() int { return 0 }

// Update is part of list.ItemDelegate; tmon items have no per-key behavior.
func (d agentDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

// Render writes the three lines of one agent row to w. Like the default
// delegate, it must not end with a trailing newline: bubbles adds the
// inter-item separator itself, so a trailing newline would double up.
func (d agentDelegate) Render(w io.Writer, lm list.Model, index int, item list.Item) {
	it, ok := item.(agentItem)
	if !ok {
		return
	}
	_, _ = io.WriteString(w, strings.Join(d.renderRow(it.row, lm.Index() == index), "\n"))
}

// renderRow renders the lines of one agent row, each exactly d.width cells
// wide. Every line shares a one-cell left margin so the list sits flush
// left against the popup edge. Line 1 carries the status icon +
// identity-colored name; line 2 the cwd and (for blocked agents) the pause
// reason; line 3 the tmux Session/Window/Pane location in dim. The selected
// row gets the full-line selection background. The context and quota usage
// lines render at the top of the preview pane, not here.
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

	// Line 2: "project: CWD" plus the git context tag, then the blocked
	// pause reason (orange). No status-age suffix.
	proj := projectLineText(r)
	pause := ""
	if r.Status == agent.StatusBlocked {
		pause = r.BlockedReason
		if pause == "" {
			pause = "paused"
		}
	}
	var line2 string
	if selected {
		text := " " + proj
		if pause != "" {
			text += "  " + pause
		}
		line2 = st.selDim.Render(fit(text, d.width))
	} else {
		line2 = " " + st.dim.Render(proj)
		if pause != "" {
			line2 += st.orange.Render("  " + pause)
		}
		line2 = fit(line2, d.width)
	}

	// Line 3: the tmux location, dimmed — "location: main / shell / 0".
	// The selected row styles text and padding in one pass, mirroring the
	// project line, so an inner SGR reset cannot drop the selection
	// background from the trailing padding.
	loc := "location: " + tmuxPath(r)
	var line3 string
	if selected {
		line3 = st.selDim.Render(fit(" "+loc, d.width))
	} else {
		line3 = fit(" "+st.dim.Render(loc), d.width)
	}

	return []string{line1, line2, line3}
}

// projectLineText is the agent detail project field: "project: CWD" with
// the git context tag when known (e.g. "project: ~/code/tmon (main · #42)").
// Unknown cwd becomes "?".
func projectLineText(r Row) string {
	cwd := displayCWD(r.CWD)
	if cwd == "" {
		cwd = "?"
	}
	return "project: " + cwd + gitTagString(r)
}

// gitTagString renders the git context tag for an agent row: "(main)" when
// only the branch is known, "(main · #42)" when the open PR number is known.
// Empty when the agent is not in a repository, so rows without git context
// render the project path alone.
func gitTagString(r Row) string {
	if r.Branch == "" {
		return ""
	}
	if r.PR != "" {
		return " (" + r.Branch + " · #" + r.PR + ")"
	}
	return " (" + r.Branch + ")"
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
