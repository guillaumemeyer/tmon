package dashboard

import (
	"fmt"
	"strings"

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

// View renders the popup: header, divider, grouped list, footer. It always
// emits exactly height lines so the footer lands on the last row.
func (m Model) View() string {
	w, h := m.width, m.height
	if w < 1 {
		w = 80
	}
	if h < 1 {
		h = 24
	}

	lines := make([]string, 0, h)
	lines = append(lines, m.headerLine(w))
	lines = append(lines, fit(styleDim.Render(strings.Repeat("━", w)), w))

	if len(m.filtered) == 0 {
		msg := "    No agents detected."
		if m.query != "" {
			msg = `    No agents match "` + m.query + `"`
		}
		lines = append(lines, fit(msg, w))
	} else {
		bodyLines := h - 3
		if bodyLines < 1 {
			bodyLines = 1
		}
		for di, it := range m.items {
			if di >= bodyLines {
				break // no scrolling, like the bash popup
			}
			lines = append(lines, m.renderItem(di, it, w))
		}
	}

	for len(lines) < h-1 {
		lines = append(lines, "")
	}
	lines = append(lines, m.footerLine(w))

	return strings.Join(lines, "\n")
}

// headerLine is the title with the key-hint aligned right.
func (m Model) headerLine(w int) string {
	title := styleCyan.Bold(true).Render("[@] TMON")
	hint := styleDim.Render("[/] search  [esc/q] quit")
	pad := w - ansi.StringWidth(title) - ansi.StringWidth(hint)
	if pad < 1 {
		return ansi.Truncate(title, w, "")
	}
	return title + strings.Repeat(" ", pad) + hint
}

// renderItem renders one grouped line. The selected agent line gets the
// background highlight padded across the full width, like the bash popup's
// CSI K fill.
func (m Model) renderItem(di int, it item, w int) string {
	switch it.kind {
	case itemSession:
		return styleCyan.Bold(true).Render("  " + it.sessionName)
	case itemWindow:
		name := it.windowIdx
		if it.windowName != "" && it.windowName != "?" {
			name = it.windowIdx + ":" + it.windowName
		}
		return styleDim.Render("    " + name)
	case itemAgent:
		r := m.rows[it.rowIdx]
		line := fmt.Sprintf("      [%s] %s %s %s  %s",
			r.PaneIndex, animatedStatusChar(r.Status, m.frame), agentIcon(r.Label), agentFullName(r.Label), r.CWD)
		if len(m.selMap) > 0 && m.selMap[m.selected] == di {
			line = fit(line, w)
			return styleHL.Render(line + strings.Repeat(" ", w-ansi.StringWidth(line)))
		}
		return fit(line, w)
	}
	return ""
}

// footerLine varies with the mode: search input, active filter, or the
// navigation hint, with the match count aligned right.
func (m Model) footerLine(w int) string {
	switch {
	case m.searching:
		left := styleWhite.Render(" ▌ "+m.query) + styleDim.Render("▌")
		right := styleDim.Render(fmt.Sprintf("  %d/%d", len(m.filtered), len(m.rows)))
		return m.twoSided(left, right, w)
	case m.query != "":
		left := styleDim.Render(" ▌ " + m.query)
		right := styleDim.Render(fmt.Sprintf("%d/%d", len(m.filtered), len(m.rows)))
		return m.twoSided(left, right, w)
	default:
		left := styleDim.Render(" ▌ / to search")
		right := styleDim.Render("  j/k/↑↓ nav  ↩/spc/l focus  esc/q quit")
		return m.twoSided(left, right, w)
	}
}

// twoSided lays out left and right strings on one line, right-aligned.
func (m Model) twoSided(left, right string, w int) string {
	pad := w - ansi.StringWidth(left) - ansi.StringWidth(right)
	if pad < 1 {
		return ansi.Truncate(left, w, "")
	}
	return left + strings.Repeat(" ", pad) + right
}

// animatedStatusChar returns the per-agent status character, toggling ?/●
// between "!" on odd frames exactly like the bash popup.
func animatedStatusChar(status agent.Status, frame int) string {
	odd := frame%2 != 0
	switch status {
	case agent.StatusBlocked:
		if odd {
			return styleOrange.Render("!")
		}
		return styleOrange.Render("?")
	case agent.StatusActive, agent.StatusRunning:
		if odd {
			return styleGreen.Render("!")
		}
		return styleGreen.Render("●")
	case agent.StatusIdle:
		return styleBlue.Render("‖")
	default:
		return styleDim.Render("·")
	}
}

// fit truncates s to at most w cells without breaking ANSI codes and while
// accounting for wide characters (emoji icons).
func fit(s string, w int) string {
	return ansi.Truncate(s, w, "")
}
