package dashboard

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/theme"
)

// styles bundles the lipgloss styles built from a theme palette. The
// dashboard renders through these so the popup always matches the theme.
type styles struct {
	dim, green, orange, blue, cyan, white, warn lipgloss.Style
	selBg                                       lipgloss.Style // background-only, for padding + wraps
	// selBgColor is the raw selection background color; the per-line styles
	// below are the selection variants for the agent rows.
	selBgColor lipgloss.Color
	selText    lipgloss.Style // selected name row: accent bold on selBg
	selDim     lipgloss.Style // selected cwd/stats rows: dim on selBg
}

// buildStyles converts a theme palette into lipgloss styles. tmux-style
// "colourNNN" values are translated for lipgloss; names and hex pass through.
func buildStyles(pal theme.Palette) styles {
	col := func(c string) lipgloss.Color { return lipgloss.Color(theme.Lipgloss(c)) }
	bg := col(pal.SelBg)
	return styles{
		dim:    lipgloss.NewStyle().Foreground(col(pal.Dim)),
		green:  lipgloss.NewStyle().Foreground(col(pal.Working)),
		orange: lipgloss.NewStyle().Foreground(col(pal.Blocked)),
		blue:   lipgloss.NewStyle().Foreground(col(pal.Idle)),
		cyan:   lipgloss.NewStyle().Foreground(col(pal.App)),
		white:  lipgloss.NewStyle().Foreground(col(pal.Accent)),
		warn:   lipgloss.NewStyle().Foreground(col(pal.Warn)),
		selBg:  lipgloss.NewStyle().Background(bg),

		selBgColor: bg,
		selText:    lipgloss.NewStyle().Foreground(col(pal.Accent)).Bold(true).Background(bg),
		selDim:     lipgloss.NewStyle().Foreground(col(pal.Dim)).Background(bg),
	}
}

// defaultStyles is the built-in theme's styles; the package-level style*
// aliases keep the classic names for the view tests.
var defaultStyles = buildStyles(theme.Default.Palette)

var (
	styleDim    = defaultStyles.dim
	styleGreen  = defaultStyles.green
	styleOrange = defaultStyles.orange
	styleBlue   = defaultStyles.blue
	styleCyan   = defaultStyles.cyan
	styleWhite  = defaultStyles.white
)

// View renders the popup: header, divider, agent list with a always-on
// right-side pane preview, and footer. It always emits exactly height lines
// so the footer lands on the last row. In theme mode it shows the theme
// selector instead.
func (m Model) View() string {
	w, h := m.width, m.height
	if w < 1 {
		w = 80
	}
	if h < 1 {
		h = 24
	}
	if m.themeMode {
		return m.themeView(w, h)
	}

	listW, panelW := m.panelWidths(w)

	lines := make([]string, 0, h)
	lines = append(lines, m.headerLine(w))
	lines = append(lines, fit(m.st.dim.Render(strings.Repeat("━", w)), w))

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

// listLines renders the flat agent list into exactly bodyLines lines of
// listW cells each, so the preview separator stays vertically aligned. The
// bubbles list is sized and given a fresh delegate on this copy — rendering
// state (theme, spinner frame) is always current, with no shared state.
func (m Model) listLines(w, bodyLines int) []string {
	if len(m.filtered) == 0 {
		msg := "    No agents detected."
		if m.query != "" {
			msg = `    No agents match "` + m.query + `"`
		}
		out := []string{fit(m.st.dim.Render(msg), w)}
		for len(out) < bodyLines {
			out = append(out, fit("", w))
		}
		return out
	}
	m.agentList.SetSize(w, bodyLines)
	m.agentList.SetDelegate(agentDelegate{
		st:          m.st,
		icons:       m.theme.Icons,
		contextWarn: m.contextWarn,
		width:       w,
		spinner:     m.spinnerFrame(),
	})
	return strings.Split(m.agentList.View(), "\n")
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

// sgrReset clears SGR attributes so preview colors cannot bleed into the
// next list row (terminals carry style across newlines).
const sgrReset = "\x1b[0m"

// previewLines renders the right-side preview panel: a header line naming
// the selected agent, then the bubbles viewport holding the pane capture
// (scrollable with ctrl+u/ctrl+d and the mouse wheel). Every line is
// exactly w cells so it joins cleanly with the list column.
func (m Model) previewLines(w, n int) []string {
	out := make([]string, 0, n)
	switch {
	case len(m.filtered) == 0:
		out = append(out, fit(m.st.dim.Render(" no agents"), w))
	case m.previewPane == "" || m.previewPane == "?":
		out = append(out, fit(m.st.dim.Render(" no pane (headless)"), w))
	default:
		r := m.selectedRow()
		if r != nil {
			header := " " + identityStyle(m.st, r.Label).Bold(true).Render(agentDisplayName(*r))
			if r.Detail != "" {
				header += m.st.dim.Render(" — " + r.Detail)
			}
			out = append(out, fit(header, w))
		} else {
			out = append(out, fit("", w))
		}
	}
	// Re-pin a bottom-pinned viewport after a size change: growing or
	// shrinking the panel changes the maximum scroll offset, so a pinned
	// preview must follow the new bottom. A preview the user scrolled away
	// from stays where it is.
	pinned := m.preview.AtBottom()
	m.preview.Width = w
	m.preview.Height = n - 1
	if pinned {
		m.preview.GotoBottom()
	}
	if vp := strings.Split(m.preview.View(), "\n"); len(vp) > 0 {
		// lipgloss's height padding can leave a trailing empty element.
		if vp[len(vp)-1] == "" {
			vp = vp[:len(vp)-1]
		}
		for _, l := range vp {
			out = append(out, fit(l, w))
		}
	}
	for len(out) < n {
		out = append(out, fit("", w))
	}
	return out
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
	app := m.theme.Icons.App
	title := m.st.cyan.Bold(true).Render(" " + app + " tmon")
	hint := m.st.dim.Render("[/] search  [esc/q] quit ")
	pad := w - ansi.StringWidth(title) - ansi.StringWidth(hint)
	if pad < 1 {
		return ansi.Truncate(title, w, "")
	}
	return title + strings.Repeat(" ", pad) + hint
}

// identityStyle is the lipgloss style for an agent's brand color, falling
// back to the theme accent when the label is unknown.
func identityStyle(st styles, label string) lipgloss.Style {
	if c := agentIdentityColor(label); c != "" {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(c))
	}
	return st.white
}

// statusStyle is the lipgloss style for a status icon: orange blocked,
// green working, blue idle — matching the status bar.
func statusStyle(st styles, s agent.Status) lipgloss.Style {
	switch s {
	case agent.StatusBlocked:
		return st.orange
	case agent.StatusWorking:
		return st.green
	case agent.StatusIdle:
		return st.blue
	default:
		return st.dim
	}
}

// selFit truncates s to at most w cells and pads the remainder with the
// selection background, so a selected row's highlight spans the full fitted
// width. The content pieces must already carry the background themselves.
func selFit(st styles, s string, w int) string {
	s = ansi.Truncate(s, w, "")
	if pad := w - ansi.StringWidth(s); pad > 0 {
		s += st.selBg.Render(strings.Repeat(" ", pad))
	}
	return s
}

// defaultContextWarn is the context-usage percent at which the usage bar
// switches to the warn color when no @tmon-context-warn is configured.
const defaultContextWarn = 85

// usageLine renders the per-agent stats line: context tokens used over the
// model's context window with a progress-bar usage bar and the used
// percentage, then the account-quota remaining percentage and next reset
// time when known. Returns "" when no stat is available, so the agent stays
// at four lines. When sel is true every piece carries the selection
// background so the highlight covers the whole line. maxWidth bounds the
// row so the bar width can be derived from the remaining space.
func usageLine(st styles, contextWarn int, u agent.Usage, sel bool, maxWidth int) string {
	if u.Empty() {
		return ""
	}
	dim, green, warn := st.dim, st.green, st.warn
	if sel {
		bg := st.selBgColor
		dim, green, warn = dim.Background(bg), green.Background(bg), warn.Background(bg)
	}
	// The trailing quota text is rendered after the bar; its width reserves
	// room so the bar never pushes the line past maxWidth.
	suffix := ""
	if u.QuotaPct > 0 {
		left := 100 - u.QuotaPct
		if left < 0 {
			left = 0
		}
		q := strconv.Itoa(left) + "% left"
		if u.QuotaReset != "" {
			q += " · reset " + u.QuotaReset
		}
		suffix = q
	}
	prefix := ""
	if u.TokensUsed > 0 || u.WindowTokens > 0 {
		prefix = "ctx " + humanTokens(u.TokensUsed)
		if u.WindowTokens > 0 {
			prefix += "/" + humanTokens(u.WindowTokens) + " "
		}
	}
	var b strings.Builder
	if prefix != "" {
		b.WriteString(dim.Render(prefix))
		if u.WindowTokens > 0 {
			pct := u.ContextPct()
			sufW := 0
			if suffix != "" {
				sufW = ansi.StringWidth(suffix) + 3 // " · " glue
			}
			barW := usageBarWidth(maxWidth, ansi.StringWidth(prefix), sufW)
			b.WriteString(usageBar(pct, barW, contextWarn, green, warn, dim, sel, st))
			b.WriteString(dim.Render(" " + strconv.Itoa(pct) + "%"))
		}
	}
	if suffix != "" {
		if prefix != "" {
			suffix = " · " + suffix
		}
		b.WriteString(dim.Render(suffix))
	}
	return b.String()
}

// usageBarWidth picks the context-bar width for a row of maxWidth cells:
// the space left after the "ctx X/Y" prefix and any trailing quota text,
// floored at 10 cells and capped at 30 so the bar never dominates the line.
func usageBarWidth(maxWidth, prefixW, suffixW int) int {
	rem := maxWidth - prefixW - suffixW - 4 // " NN%" after the bar
	if rem < 10 {
		rem = 10
	}
	if rem > 30 {
		rem = 30
	}
	return rem
}

// usageBar renders the context-usage bar with the bubbles progress
// component: solid fill, dim empty cells, green below the warn threshold
// (warn at or above it, disabled when contextWarn is 0). On a selected row
// the bar is wrapped so the selection background spans it.
func usageBar(pct, width, contextWarn int, green, warn, dim lipgloss.Style, sel bool, st styles) string {
	p := progress.New(progress.WithWidth(width), progress.WithSolidFill(usageBarColor(pct, contextWarn, green, warn)))
	p.ShowPercentage = false
	p.EmptyColor = styleHex(dim)
	bar := p.ViewAs(float64(pct) / 100)
	if sel {
		bar = st.selBg.Render(bar)
	}
	return bar
}

// usageBarColor returns the fill color for a context percentage: the warn
// color at or above the threshold (when set), the working color otherwise.
func usageBarColor(pct, contextWarn int, green, warn lipgloss.Style) string {
	if contextWarn > 0 && pct >= contextWarn {
		return styleHex(warn)
	}
	return styleHex(green)
}

// styleHex extracts the raw color string from a style's foreground, or ""
// when the style has none (NoColor).
func styleHex(s lipgloss.Style) string {
	if c, ok := s.GetForeground().(lipgloss.Color); ok {
		return string(c)
	}
	return ""
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
		left = " " + m.st.dim.Render(m.version) + "  "
	}
	switch {
	case m.searching:
		left += m.st.white.Render("▌ "+m.query) + m.st.dim.Render("▌")
		return m.twoSided(left, right, w)
	case m.query != "":
		left += m.st.dim.Render("▌ " + m.query)
		if m.filterStatus != "" {
			left += m.st.dim.Render(" · " + m.filterLabel())
		}
		return m.twoSided(left, right, w)
	default:
		if m.filterStatus != "" {
			left += m.st.dim.Render("▌ " + m.filterLabel() + " (press again to clear)")
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
	// Keyboard + drag share one tip so the footer fits at common widths.
	parts = append(parts, "[←/→ h/l · drag │] resize")
	if m.previewNavTipVisible() {
		parts = append(parts, "[C-u/C-d] scroll preview")
	}
	parts = append(parts, "[1-9] jump ")
	parts = append(parts, "[t] theme")
	return m.st.dim.Render(strings.Join(parts, "  "))
}

// previewNavTipVisible is true when an agent with a resolvable pane is
// selected so the right-hand preview (and its scroll keys) are available.
func (m Model) previewNavTipVisible() bool {
	if m.selectedRow() == nil {
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
