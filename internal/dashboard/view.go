package dashboard

import (
	"fmt"
	"os"
	"strconv"
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
// An agent occupies two rows (name + cwd/pause line) — three when token
// usage is known — and headers take one.
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
			rendered := m.renderItem(di, it, w)
			if len(lines)+len(rendered) > bodyLines {
				break // no scrolling, like the bash popup
			}
			lines = append(lines, rendered...)
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

// renderItem renders one display item into one or more lines, each exactly
// w cells wide. Session and window headers take one line; an agent takes
// two — a bold "Title (Name)" line and a dimmed cwd + pause-status line —
// plus a third dim stats line when the connector reported token usage. The
// selected agent line gets a green bold ">" marker aligned with the window
// index column instead of a full-line background highlight.
func (m Model) renderItem(di int, it item, w int) []string {
	switch it.kind {
	case itemSession:
		return []string{fit(styleCyan.Bold(true).Render("  "+it.sessionName), w)}
	case itemWindow:
		name := it.windowIdx
		if it.windowName != "" && it.windowName != "?" {
			name = it.windowIdx + ":" + it.windowName
		}
		return []string{fit(styleDim.Render("    "+name), w)}
	case itemAgent:
		r := m.rows[it.rowIdx]
		selected := len(m.selMap) > 0 && m.selMap[m.selected] == di
		// Both branches are 6 cells wide before the name: the marker keeps
		// the name aligned with unselected rows.
		marker := "      "
		if selected {
			marker = "    " + styleGreen.Bold(true).Render(">") + " "
		}
		// Line 1: the session title (or plain name) in bold.
		line1 := marker + styleWhite.Bold(true).Render(agentDisplayName(r))
		// Line 2: the working directory, plus the pause status when the
		// agent is blocked — the prompt it is waiting on, or "paused".
		line2 := "      " + styleDim.Render(displayCWD(r.CWD))
		if r.Status == agent.StatusBlocked {
			pause := r.BlockedReason
			if pause == "" {
				pause = "paused"
			}
			line2 += "  " + styleOrange.Render(pause)
		}
		lines := []string{fit(line1, w), fit(line2, w)}
		// Line 3 (when the connector reported usage): the token stats line.
		// It is indented to the marker column like line 2 and stays dim.
		if l := usageLine(r.Usage); l != "" {
			lines = append(lines, fit("      "+styleDim.Render(l), w))
		}
		return lines
	}
	return []string{fit("", w)}
}

// usageLine renders the per-agent stats line: context tokens used over the
// model's context window with the used percentage, then the account-quota
// remaining percentage and next reset time when known. Returns "" when no
// stat is available, so the agent stays at two lines.
func usageLine(u agent.Usage) string {
	if u.Empty() {
		return ""
	}
	parts := make([]string, 0, 2)
	if u.TokensUsed > 0 || u.WindowTokens > 0 {
		ctx := "ctx " + humanTokens(u.TokensUsed)
		if u.WindowTokens > 0 {
			ctx += "/" + humanTokens(u.WindowTokens) + " (" + strconv.Itoa(u.ContextPct()) + "%)"
		}
		parts = append(parts, ctx)
	}
	if u.QuotaPct > 0 {
		left := 100 - u.QuotaPct
		if left < 0 {
			left = 0
		}
		q := strconv.Itoa(left) + "% left"
		if u.QuotaReset != "" {
			q += " · reset " + u.QuotaReset
		}
		parts = append(parts, q)
	}
	return strings.Join(parts, " · ")
}

// humanTokens formats a token count compactly: "2.5M", "262k", "51.7k",
// "13k", or the plain number below 1000. Counts of at least 100k use whole
// thousands (context windows are 200k/1M), smaller counts keep one decimal.
func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return trimZero(fmt.Sprintf("%.1fM", float64(n)/1_000_000))
	case n >= 100_000:
		return strconv.FormatInt(n/1000, 10) + "k"
	case n >= 1_000:
		return trimZero(fmt.Sprintf("%.1fk", float64(n)/1_000))
	default:
		return strconv.FormatInt(n, 10)
	}
}

// trimZero drops a redundant ".0" from a formatted number ("13.0k" → "13k").
func trimZero(s string) string {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		j := i + 1
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if strings.TrimRight(s[i+1:j], "0") == "" {
			return s[:i] + s[j:]
		}
	}
	return s
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
	// Version pinned bottom-left, one cell in from the border.
	left := ""
	if m.version != "" {
		left = " " + styleDim.Render(m.version) + "  "
	}
	switch {
	case m.searching:
		left += styleWhite.Render("▌ "+m.query) + styleDim.Render("▌")
		return m.twoSided(left, right, w)
	case m.query != "":
		left += styleDim.Render("▌ " + m.query)
		if m.filterStatus != "" {
			left += styleDim.Render(" · " + m.filterLabel())
		}
		return m.twoSided(left, right, w)
	default:
		if m.filterStatus != "" {
			left += styleDim.Render("▌ " + m.filterLabel() + " (press again to clear)")
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
	parts = append(parts, "[↑/↓ j/k] navigate")
	parts = append(parts, "[←/→ h/l] resize preview")
	if m.previewNavTipVisible() {
		parts = append(parts, "[C-u/C-d] scroll preview")
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
