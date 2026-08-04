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
)

// View renders the popup: header, divider, agent list with a always-on
// right-side pane preview, and footer. It always emits exactly height lines
// so the footer lands on the last row.
func (m Model) View() string {
	w, h := m.width, m.height
	if w < 1 {
		w = 80
	}
	if h < 1 {
		h = 24
	}

	listW, panelW := m.panelWidths(w)

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

// panelWidths splits the popup width into list | preview columns using the
// current previewPct. Both sides keep at least one cell when possible.
func (m Model) panelWidths(w int) (listW, panelW int) {
	if w < 3 {
		return 1, 1
	}
	pct := m.previewPct
	if pct <= 0 {
		pct = defaultPreviewPct
	}
	panelW = w * pct / 100
	if panelW < 1 {
		panelW = 1
	}
	if panelW > w-2 {
		panelW = w - 2
	}
	listW = w - panelW - 1
	return listW, panelW
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
// the selected agent, then the pane capture with colors preserved. The
// capture is pinned to the bottom by default (most recent output first);
// previewOffset scrolls upward. Every line is exactly w cells so it joins
// cleanly with the list column.
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
			header = " " + agentDisplayName(r)
			if r.Detail != "" {
				header += " — " + r.Detail
			}
		}
	}
	out = append(out, fit(styleDim.Render(header), w))
	if m.previewText == "" && m.previewPane != "" && m.previewPane != "?" {
		out = append(out, fit(styleDim.Render("  (empty pane)"), w))
	}

	content := trimTrailingEmpty(strings.Split(m.previewText, "\n"))
	// Empty capture yields a single "" from Split; treat that as no lines.
	if len(content) == 1 && content[0] == "" {
		content = nil
	}

	visible := n - 1 // body rows after the header
	if visible < 0 {
		visible = 0
	}
	start, end := previewWindow(len(content), visible, m.previewOffset)
	for _, tl := range content[start:end] {
		// Leading space keeps content off the separator; reset after the
		// line so open SGR from the capture cannot color the next row.
		out = append(out, fit(" "+tl, w)+sgrReset)
	}
	for len(out) < n {
		out = append(out, fit("", w))
	}
	return out
}

// previewWindow returns the [start, end) slice of content lines to show
// when pinning to the bottom with the given upward offset. offset is
// clamped so the window stays inside the content.
func previewWindow(total, visible, offset int) (start, end int) {
	if total <= 0 || visible <= 0 {
		return 0, 0
	}
	if total <= visible {
		return 0, total
	}
	maxOff := total - visible
	if offset < 0 {
		offset = 0
	}
	if offset > maxOff {
		offset = maxOff
	}
	end = total - offset
	start = end - visible
	return start, end
}

// trimTrailingEmpty drops empty lines at the end of a pane capture so the
// bottom pin lands on real content rather than blank viewport padding.
func trimTrailingEmpty(lines []string) []string {
	i := len(lines)
	for i > 0 && strings.TrimSpace(lines[i-1]) == "" {
		i--
	}
	return lines[:i]
}

// headerLine is the title with the key-hint aligned right.
func (m Model) headerLine(w int) string {
	app := "[@]"
	if !m.ascii {
		app = "🤖"
	}
	title := styleCyan.Bold(true).Render(" " + app + " tmon")
	hint := styleDim.Render("[/] search  [esc/q] quit ")
	pad := w - ansi.StringWidth(title) - ansi.StringWidth(hint)
	if pad < 1 {
		return ansi.Truncate(title, w, "")
	}
	return title + strings.Repeat(" ", pad) + hint
}

// renderItem renders one grouped line, always exactly w cells wide. The
// selected agent line gets a green bold ">" marker aligned with the window
// index column instead of a full-line background highlight.
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
		selected := len(m.selMap) > 0 && m.selMap[m.selected] == di
		// Both branches are 6 cells wide before "[": the marker keeps the
		// pane bracket aligned with unselected rows.
		marker := "      "
		if selected {
			marker = "    " + styleGreen.Bold(true).Render(">") + " "
		}
		line := fmt.Sprintf("%s[%s] %s %s  %s",
			marker, r.PaneIndex, animatedStatusChar(r.Status, m.ascii), agentDisplayName(r), displayCWD(r.CWD))
		if r.BlockedReason != "" {
			line += "  " + styleOrange.Render(r.BlockedReason)
		}
		if r.Detail != "" {
			line += "  " + styleDim.Render(r.Detail)
		}
		if age := ageString(r.LastTs); age != "" {
			line += "  " + styleDim.Render(age)
		}
		return fit(line, w)
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
// navigation hint, with the match count aligned right. When a selectable
// agent with a real pane is focused, a preview scroll tip is shown.
func (m Model) footerLine(w int) string {
	right := m.footerRight()
	switch {
	case m.searching:
		left := styleWhite.Render(" ▌ "+m.query) + styleDim.Render("▌")
		return m.twoSided(left, right, w)
	case m.query != "":
		left := styleDim.Render(" ▌ " + m.query)
		if m.filterStatus != "" {
			left += styleDim.Render(" · " + m.filterLabel())
		}
		return m.twoSided(left, right, w)
	default:
		left := ""
		if m.filterStatus != "" {
			left = styleDim.Render(" ▌ " + m.filterLabel() + " (press again to clear)")
		}
		return m.twoSided(left, right, w)
	}
}

// footerRight is the right-aligned footer segment: match count while
// filtering, resize/scroll tips for the preview, and the jump hint.
func (m Model) footerRight() string {
	parts := make([]string, 0, 4)
	if m.query != "" || m.searching {
		parts = append(parts, fmt.Sprintf("%d/%d", len(m.filtered), len(m.rows)))
	}
	parts = append(parts, "[←/→] resize")
	if m.previewNavTipVisible() {
		parts = append(parts, "[C-u/C-d] scroll")
	}
	parts = append(parts, "[1-9] jump ")
	return styleDim.Render(strings.Join(parts, "  "))
}

// previewNavTipVisible is true when an agent with a resolvable pane is
// selected so the right-hand preview (and its scroll keys) are available.
func (m Model) previewNavTipVisible() bool {
	if len(m.selMap) == 0 {
		return false
	}
	return m.previewPane != "" && m.previewPane != "?"
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
