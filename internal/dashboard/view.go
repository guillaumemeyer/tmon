package dashboard

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/guillaumemeyer/tmon/internal/agent"
)

// Palette — the same 256-color choices as the bash popup.
var (
	styleDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleOrange = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))
	styleBlue   = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	styleCyan   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styleWhite  = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	styleHL     = lipgloss.NewStyle().Background(lipgloss.Color("236"))
)

// View renders the popup: header, divider, agent list with a always-on
// right-side pane preview (half the popup width), and footer. It always
// emits exactly height lines so the footer lands on the last row.
func (m Model) View() string {
	w, h := m.width, m.height
	if w < 1 {
		w = 80
	}
	if h < 1 {
		h = 24
	}

	// Preview takes half the popup; the list gets the rest minus the │.
	panelW := w / 2
	if panelW < 1 {
		panelW = 1
	}
	listW := w - panelW - 1
	if listW < 1 {
		listW = 1
	}

	lines := make([]string, 0, h)
	lines = append(lines, m.headerLine(w))
	lines = append(lines, fit(styleDim.Render(strings.Repeat("━", w)), w))

	bodyLines := h - 3
	if bodyLines < 1 {
		bodyLines = 1
	}

	listLines := m.listLines(listW, bodyLines)
	prev := m.previewLines(panelW, bodyLines)
	for i := range listLines {
		// Both sides are padded to fixed widths so the │ column stays aligned.
		lines = append(lines, listLines[i]+"│"+prev[i])
	}

	for len(lines) < h-1 {
		lines = append(lines, "")
	}
	lines = append(lines, m.footerLine(w))

	return strings.Join(lines, "\n")
}

// listLines renders the filtered list into at most bodyLines lines, each
// exactly w cells wide so the preview separator stays vertically aligned.
func (m Model) listLines(w, bodyLines int) []string {
	lines := make([]string, 0, bodyLines)
	if len(m.filtered) == 0 {
		msg := "    No agents detected."
		if m.query != "" {
			msg = `    No agents match "` + m.query + `"`
		}
		lines = append(lines, fit(msg, w))
	} else {
		for di, it := range m.items {
			if di >= bodyLines {
				break // no scrolling, like the bash popup
			}
			lines = append(lines, m.renderItem(di, it, w))
		}
	}
	for len(lines) < bodyLines {
		lines = append(lines, fit("", w))
	}
	return lines
}

// sgrReset clears SGR attributes so preview colors cannot bleed into the
// next list row (terminals carry style across newlines).
const sgrReset = "\x1b[0m"

// previewLines renders the right-side preview panel: a header line naming
// the selected agent, then the pane capture with colors preserved. Every
// line is exactly w cells so it joins cleanly with the list column.
func (m Model) previewLines(w, n int) []string {
	out := make([]string, 0, n)
	header := ""
	switch {
	case len(m.selMap) == 0:
		header = " no agents"
	case m.previewPane == "" || m.previewPane == "?":
		header = " no pane (headless)"
	default:
		it := m.items[m.selMap[m.selected]]
		if it.kind == itemAgent {
			r := m.rows[it.rowIdx]
			header = " " + agentFullName(r.Label)
			if r.Detail != "" {
				header += " — " + r.Detail
			}
		}
	}
	out = append(out, fit(styleDim.Render(header), w))
	if m.previewText == "" && m.previewPane != "" && m.previewPane != "?" {
		out = append(out, fit(styleDim.Render("  (empty pane)"), w))
	}
	for _, tl := range strings.Split(m.previewText, "\n") {
		if len(out) >= n {
			break
		}
		// Leading space keeps content off the separator; reset after the
		// line so open SGR from the capture cannot color the next row.
		out = append(out, fit(" "+tl, w)+sgrReset)
	}
	for len(out) < n {
		out = append(out, fit("", w))
	}
	return out
}

// headerLine is the title with the key-hint aligned right.
func (m Model) headerLine(w int) string {
	app := "[@]"
	if !m.ascii {
		app = "🤖"
	}
	title := styleCyan.Bold(true).Render(app + " tmon")
	hint := styleDim.Render("[/] search  [esc/q] quit")
	pad := w - ansi.StringWidth(title) - ansi.StringWidth(hint)
	if pad < 1 {
		return ansi.Truncate(title, w, "")
	}
	return title + strings.Repeat(" ", pad) + hint
}

// renderItem renders one grouped line, always exactly w cells wide. The
// selected agent line gets a background highlight across the full list
// column, like the bash popup's CSI K fill.
func (m Model) renderItem(di int, it item, w int) string {
	switch it.kind {
	case itemSession:
		return fit(styleCyan.Bold(true).Render("  "+it.sessionName), w)
	case itemWindow:
		name := it.windowIdx
		if it.windowName != "" && it.windowName != "?" {
			name = it.windowIdx + ":" + it.windowName
		}
		return fit(styleDim.Render("    "+name), w)
	case itemAgent:
		r := m.rows[it.rowIdx]
		line := fmt.Sprintf("      [%s] %s %s  %s",
			r.PaneIndex, animatedStatusChar(r.Status, m.ascii), agentFullName(r.Label), displayCWD(r.CWD))
		if r.BlockedReason != "" {
			line += "  " + styleOrange.Render(r.BlockedReason)
		}
		if r.Detail != "" {
			line += "  " + styleDim.Render(r.Detail)
		}
		if age := ageString(r.LastTs); age != "" {
			line += "  " + styleDim.Render(age)
		}
		line = fit(line, w)
		if len(m.selMap) > 0 && m.selMap[m.selected] == di {
			return styleHL.Render(line)
		}
		return line
	}
	return fit("", w)
}

// displayCWD renders an agent's working directory for the popup: absolute
// paths under $HOME are shown home-relative ("~/code/tmon", "~" for the
// home dir itself); anything else (short forms, "?", root) passes through
// unchanged.
func displayCWD(cwd string) string {
	if !strings.HasPrefix(cwd, "/") {
		return cwd
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return cwd
	}
	if cwd == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(cwd, home+"/"); ok {
		return "~/" + rest
	}
	return cwd
}

// footerLine varies with the mode: search input, active filter, or the
// navigation hint, with the match count or status counts aligned right.
func (m Model) footerLine(w int) string {
	switch {
	case m.searching:
		left := styleWhite.Render(" ▌ "+m.query) + styleDim.Render("▌")
		right := styleDim.Render(fmt.Sprintf("  %d/%d", len(m.filtered), len(m.rows)))
		return m.twoSided(left, right, w)
	case m.query != "":
		left := styleDim.Render(" ▌ " + m.query)
		if m.filterStatus != "" {
			left += styleDim.Render(" · " + m.filterLabel())
		}
		right := styleDim.Render(fmt.Sprintf("%d/%d", len(m.filtered), len(m.rows)))
		return m.twoSided(left, right, w)
	default:
		left := styleDim.Render(" ▌ / to search")
		if m.filterStatus != "" {
			left += styleDim.Render(" · " + m.filterLabel() + " (press again to clear)")
		}
		right := m.countString() + styleDim.Render("  [1-9] jump")
		return m.twoSided(left, right, w)
	}
}

// filterLabel names the active status filter for the footer.
func (m Model) filterLabel() string {
	switch m.filterStatus {
	case agent.StatusBlocked:
		return "b:blocked"
	case agent.StatusWorking:
		return "w:working"
	case agent.StatusIdle:
		return "i:idle"
	}
	return ""
}

// statusCounts tallies the filtered agents by status (index order: blocked,
// working, idle).
func (m Model) statusCounts() [3]int {
	var c [3]int
	for _, fi := range m.filtered {
		switch m.rows[fi].Status {
		case agent.StatusBlocked:
			c[0]++
		case agent.StatusWorking:
			c[1]++
		case agent.StatusIdle:
			c[2]++
		}
	}
	return c
}

// countString renders the status-bar-style counts over the filtered set,
// with the same visibility rule as the status bar: a segment (icon + count)
// is only shown when its count is non-zero.
func (m Model) countString() string {
	c := m.statusCounts()
	bIcon, wIcon, iIcon := "B", "W", "I"
	if !m.ascii {
		bIcon, wIcon, iIcon = "🛑", "⚡️", "💤"
	}
	segs := make([]string, 0, 3)
	add := func(icon string, style lipgloss.Style, n int) {
		if n <= 0 {
			return
		}
		segs = append(segs, fmt.Sprintf("%s %d", style.Render(icon), n))
	}
	add(bIcon, styleOrange, c[0])
	add(wIcon, styleGreen, c[1])
	add(iIcon, styleBlue, c[2])
	return strings.Join(segs, "  ")
}

// twoSided lays out left and right strings on one line, right-aligned.
func (m Model) twoSided(left, right string, w int) string {
	pad := w - ansi.StringWidth(left) - ansi.StringWidth(right)
	if pad < 1 {
		return ansi.Truncate(left, w, "")
	}
	return left + strings.Repeat(" ", pad) + right
}

// animatedStatusChar returns the per-agent status character. Icons are
// static — there is no pulse animation.
func animatedStatusChar(status agent.Status, ascii bool) string {
	switch status {
	case agent.StatusBlocked:
		if ascii {
			return styleOrange.Render("B")
		}
		return styleOrange.Render("🛑")
	case agent.StatusWorking:
		if ascii {
			return styleGreen.Render("W")
		}
		return styleGreen.Render("⚡️")
	case agent.StatusIdle:
		if ascii {
			return styleBlue.Render("I")
		}
		return styleBlue.Render("💤")
	default:
		return styleDim.Render("·")
	}
}

// ageString renders how long ago the status last changed: "now", "1m",
// "45m", "3h", "2d". Empty when the timestamp is unknown.
func ageString(lastTs int64) string {
	if lastTs <= 0 {
		return ""
	}
	d := time.Since(time.Unix(lastTs, 0))
	switch {
	case d < time.Minute:
		return "now"
	case d < 2*time.Minute:
		return "1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// fit truncates s to at most w cells without breaking ANSI codes, accounting
// for wide characters, then pads with spaces so the result is exactly w cells.
// Fixed-width lines keep the list/preview columns aligned.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "")
	if pad := w - ansi.StringWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}
