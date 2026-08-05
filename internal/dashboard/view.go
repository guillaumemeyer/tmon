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
	bg                                          lipgloss.Style // popup background fill (theme background)
	bgColor                                     lipgloss.Color
	selBg                                       lipgloss.Style // background-only, for padding + wraps
	selBgColor                                  lipgloss.Color
	// The per-line styles below are the selection variants for the agent rows.
	selText lipgloss.Style // selected name row: accent bold on selBg
	selDim  lipgloss.Style // selected cwd/stats rows: dim on selBg
}

// buildStyles converts a theme palette into lipgloss styles. tmux-style
// "colourNNN" values are translated for lipgloss; names and hex pass through.
func buildStyles(pal theme.Palette) styles {
	col := func(c string) lipgloss.Color { return lipgloss.Color(theme.Lipgloss(c)) }
	bg := col(pal.Background)
	sbg := col(pal.SelBg)
	return styles{
		dim:    lipgloss.NewStyle().Foreground(col(pal.Dim)),
		green:  lipgloss.NewStyle().Foreground(col(pal.Working)),
		orange: lipgloss.NewStyle().Foreground(col(pal.Blocked)),
		blue:   lipgloss.NewStyle().Foreground(col(pal.Idle)),
		cyan:   lipgloss.NewStyle().Foreground(col(pal.App)),
		white:  lipgloss.NewStyle().Foreground(col(pal.Accent)),
		warn:   lipgloss.NewStyle().Foreground(col(pal.Warn)),
		bg:     lipgloss.NewStyle().Background(bg),

		bgColor: bg,
		selBg:   lipgloss.NewStyle().Background(sbg),

		selBgColor: sbg,
		selText:    lipgloss.NewStyle().Foreground(col(pal.Accent)).Bold(true).Background(sbg),
		selDim:     lipgloss.NewStyle().Foreground(col(pal.Dim)).Background(sbg),
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

// View renders the popup: a rounded border drawn inside the canvas (the
// tmux popup itself opens borderless), then header, divider, agent list
// with an always-on right-side pane preview, and footer. It always emits
// exactly height lines of width cells so the popup fills its pane. In
// theme mode it shows the theme selector instead.
func (m Model) View() string {
	w, h := m.width, m.height
	if w < 1 {
		w = 80
	}
	if h < 1 {
		h = 24
	}
	if w < 3 {
		w = 3 // the border needs a corner on each side
	}
	if h < 3 {
		h = 3
	}
	if m.themeMode {
		return m.themeView(w, h)
	}

	innerW, innerH := w-2, h-2
	listW, panelW := m.panelWidths(innerW)

	topChrome := m.mainTopChrome()

	lines := make([]string, 0, innerH)
	lines = append(lines, m.headerLines(innerW, [mainHeaderHeight]string{"[t] theme  [esc/q] quit ", "[/] search "})...)
	lines = append(lines, fit(m.st.dim.Render(strings.Repeat("━", innerW)), innerW))

	bodyLines := bodyLinesFor(innerH, topChrome)
	if bodyLines < 1 {
		bodyLines = 1
	}

	if m.searching {
		lines = append(lines, m.searchInputLine(listW, panelW))
	}

	listLines := m.listLines(listW, bodyLines)
	prev := m.previewLines(panelW, bodyLines)
	for i := range listLines {
		// Both sides are padded to fixed widths so the │ column stays aligned.
		lines = append(lines, listLines[i]+"│"+prev[i])
	}

	for len(lines) < innerH-2 {
		lines = append(lines, "")
	}
	lines = append(lines, fit(m.st.dim.Render(strings.Repeat("━", innerW)), innerW))
	lines = append(lines, m.footerLine(innerW))

	return strings.Join(paintRows(w, framed(w, lines, m.st.white), m.st.bg), "\n")
}

// framed draws the popup's rounded border inside the canvas: a ╭─╮/╰─╯
// top/bottom line and a │ on each side of every row. The tmux popup opens
// borderless (display-popup -B), so this is the only frame; it is drawn in
// the theme's accent color. rows must each be w-2 cells wide, and the
// result is exactly w cells per row.
func framed(w int, rows []string, border lipgloss.Style) []string {
	horiz := strings.Repeat("─", w-2)
	out := make([]string, 0, len(rows)+2)
	out = append(out, border.Render("╭"+horiz+"╮"))
	for _, r := range rows {
		out = append(out, border.Render("│")+r+border.Render("│"))
	}
	out = append(out, border.Render("╰"+horiz+"╯"))
	return out
}

// bodyLinesFor is the row count available for the list/preview body of a
// canvas innerH cells tall: topChrome rows above it (title chrome, its
// divider, and — in the agent view while searching — the query input row),
// plus the fixed divider+footer pair below.
func bodyLinesFor(innerH, topChrome int) int {
	n := innerH - topChrome - 2
	if n < 1 {
		return 1
	}
	return n
}

// paintRows fills every row with the theme background so the popup is a
// solid panel rather than a transparent window over the terminal. Each row
// is padded to exactly w cells first. SGR resets inside the content — from
// colored pane captures — would clear the background mid-row, so the
// background sequence is re-asserted after every reset form.
func paintRows(w int, rows []string, bg lipgloss.Style) []string {
	seq := backgroundSeq(bg)
	out := make([]string, len(rows))
	for i, r := range rows {
		r = fit(r, w)
		if seq == "" {
			out[i] = r
			continue
		}
		r = reassertBackground(r, seq)
		out[i] = seq + r + sgrReset
	}
	return out
}

// sgrReset clears all SGR attributes. The popup background re-asserts after
// it (see paintRows) so captures cannot punch transparent holes in the panel.
const sgrReset = "\x1b[0m"

// reassertBackground scans s for CSI sequences and re-asserts the theme
// background (seq) after every SGR sequence whose net effect leaves the
// background at the terminal default. Pane captures use every reset form —
// "\x1b[0m", "\x1b[49m", combined "\x1b[39;49m", bare "\x1b[m",
// "\x1b[0;31m", ... — and any of them would otherwise punch a transparent
// hole through the painted panel. The background state is tracked across
// the whole string, so a deliberate explicit background ("\x1b[48;5;0m")
// is preserved and only a later reset falls back to the theme fill.
func reassertBackground(s, seq string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	bgDefault := false // rows start with the theme background already set
	for i := 0; i < len(s); {
		if s[i] != '\x1b' || i+1 >= len(s) || s[i+1] != '[' {
			b.WriteByte(s[i])
			i++
			continue
		}
		// A CSI sequence: ESC [ parameter bytes, intermediate bytes, final.
		j := i + 2
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3f {
			j++
		}
		if j >= len(s) || s[j] < 0x40 || s[j] > 0x7e {
			b.WriteByte(s[i]) // unterminated/malformed: copy the ESC
			i++
			continue
		}
		esc := s[i : j+1]
		b.WriteString(esc)
		if s[j] == 'm' && sgrLeavesDefaultBg(esc, &bgDefault) {
			b.WriteString(seq)
			bgDefault = false
		}
		i = j + 1
	}
	return b.String()
}

// sgrLeavesDefaultBg applies an SGR sequence ("\x1b[...m") to the tracked
// background state and reports whether it ends at the terminal default.
// Parameters are applied left to right: "0"/"49" reset the background, the
// background codes 40-47/48/100-107 set it explicitly, and everything else
// (foreground, attributes) leaves it alone. Extended color specs after
// "38"/"48" are skipped so their index/component fields (e.g. "5;49" in
// "\x1b[38;5;49m") are not misread as background codes.
func sgrLeavesDefaultBg(esc string, bgDefault *bool) bool {
	body := esc[2 : len(esc)-1] // drop ESC [ and the trailing m
	if body == "" {
		*bgDefault = true // bare "\x1b[m" resets everything
		return true
	}
	fields := strings.Split(body, ";")
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "0", "49":
			*bgDefault = true
		case "48":
			*bgDefault = false
			i += colorSpecLen(fields, i)
		case "38":
			i += colorSpecLen(fields, i) // foreground only; skip its spec
		default:
			if isBgCode(fields[i]) {
				*bgDefault = false
			}
		}
	}
	return *bgDefault
}

// colorSpecLen is the number of fields an extended color code ("38"/"48")
// consumes after itself: "5;N" for indexed colors, "2;r;g;b" for truecolor.
func colorSpecLen(fields []string, i int) int {
	if i+1 >= len(fields) {
		return 0
	}
	switch fields[i+1] {
	case "5":
		return 2
	case "2":
		return 4
	}
	return 0
}

// isBgCode reports whether an SGR parameter sets an explicit background:
// 40-47, 48 (extended, handled by the caller), or 100-107.
func isBgCode(f string) bool {
	if len(f) == 2 && f[0] == '4' && f[1] >= '0' && f[1] <= '7' {
		return true
	}
	if len(f) == 3 && f[0] == '1' && f[1] == '0' && f[2] >= '0' && f[2] <= '7' {
		return true
	}
	return false
}

// forceBackground rewrites s so the theme background (seq) always wins over
// the content's own colors: after every SGR sequence that touches the
// background — resets ("\x1b[0m", "\x1b[49m", bare "\x1b[m", ...) and
// explicit backgrounds ("\x1b[48;5;0m", "\x1b[41m", truecolor, ...) alike —
// the theme sequence is re-asserted, overriding whatever the content set.
// Foreground colors and attributes pass through untouched, and because every
// background-touching sequence is neutralized immediately, the background
// between sequences is always the theme's, so foreground-only codes never
// need a re-assertion. Used for the preview pane, where a captured terminal
// may paint its own backgrounds.
func forceBackground(s, seq string) string {
	if seq == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		if s[i] != '\x1b' || i+1 >= len(s) || s[i+1] != '[' {
			b.WriteByte(s[i])
			i++
			continue
		}
		// A CSI sequence: ESC [ parameter bytes, intermediate bytes, final.
		j := i + 2
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3f {
			j++
		}
		if j >= len(s) || s[j] < 0x40 || s[j] > 0x7e {
			b.WriteByte(s[i]) // unterminated/malformed: copy the ESC
			i++
			continue
		}
		esc := s[i : j+1]
		b.WriteString(esc)
		if s[j] == 'm' && sgrTouchesBackground(esc) {
			b.WriteString(seq)
		}
		i = j + 1
	}
	return b.String()
}

// sgrTouchesBackground reports whether an SGR sequence changes the
// background at all: a reset ("0"/"49"/bare "\x1b[m") or an explicit
// background (40-47, 48, 100-107). Extended color specs after "38"/"48" are
// skipped so their fields (e.g. "5;49" in "\x1b[38;5;49m") are not misread.
func sgrTouchesBackground(esc string) bool {
	body := esc[2 : len(esc)-1] // drop ESC [ and the trailing m
	if body == "" {
		return true
	}
	fields := strings.Split(body, ";")
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "0", "49":
			return true
		case "48":
			return true
		case "38":
			i += colorSpecLen(fields, i) // foreground only; skip its spec
		default:
			if isBgCode(fields[i]) {
				return true
			}
		}
	}
	return false
}

// backgroundSeq extracts the ANSI background sequence a lipgloss style emits
// (e.g. "\x1b[48;5;235m"), or "" when the style sets no background.
func backgroundSeq(bg lipgloss.Style) string {
	rendered := bg.Render("X")
	seq, _, ok := strings.Cut(rendered, "X")
	if !ok {
		return ""
	}
	return seq
}

// searchInputLine renders the live query editor as a full-width body row
// (list column + separator + blank preview column) shown above the agent
// list while searching. Styled fresh from the current theme on this copy,
// matching the pattern used for the list delegate.
func (m Model) searchInputLine(listW, panelW int) string {
	ti := m.searchInput
	ti.PromptStyle = m.st.cyan.Bold(true)
	ti.TextStyle = m.st.white
	ti.Width = listW - ansi.StringWidth(ti.Prompt) - 2
	if ti.Width < 1 {
		ti.Width = 1
	}
	return fit(" "+ti.View(), listW) + "│" + fit("", panelW)
}

// listLines renders the flat agent list into exactly bodyLines lines of
// listW cells each, so the preview separator stays vertically aligned. The
// bubbles list is sized and given a fresh delegate on this copy — rendering
// state (theme, spinner frame) is always current, with no shared state.
func (m Model) listLines(w, bodyLines int) []string {
	if len(m.filtered) == 0 {
		msg := " No agents detected."
		if m.query != "" {
			msg = ` No agents match "` + m.query + `"`
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
	out := strings.Split(m.agentList.View(), "\n")
	// The bubbles list can emit more or fewer lines than requested at small
	// heights (e.g. its own minimum for pagination chrome); clamp to exactly
	// bodyLines so it stays zip-aligned with the preview column in View().
	if len(out) > bodyLines {
		out = out[:bodyLines]
	}
	for len(out) < bodyLines {
		out = append(out, fit("", w))
	}
	return out
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
	// Size the viewport to the body under the header. The default height
	// from New (20) is not the real panel height — re-pin using the real
	// height so the tail stays visible when the popup is taller or shorter
	// than that default. previewFollowBottom is the source of truth: the
	// viewport's AtBottom() is unreliable here because Height may have
	// changed since the last GotoBottom.
	bodyH := n - len(out)
	if bodyH < 1 {
		bodyH = 1
	}
	m.preview.Width = w
	m.preview.Height = bodyH
	if m.previewFollowBottom {
		m.preview.GotoBottom()
	}
	vp := strings.Split(m.preview.View(), "\n")
	// lipgloss width/height padding turns blank rows into space-filled
	// cells (and may leave a trailing empty split element). Treat
	// whitespace-only rows as blank so short content can be re-aligned.
	isBlank := func(s string) bool {
		return strings.TrimSpace(ansi.Strip(s)) == ""
	}
	for len(vp) > 0 && isBlank(vp[len(vp)-1]) {
		vp = vp[:len(vp)-1]
	}
	// Short captures are top-aligned by the viewport. When following the
	// tail, pad above so the last real line sits on the bottom of the panel
	// (terminal-style) instead of floating mid-panel.
	if m.previewFollowBottom {
		for len(vp) < bodyH {
			vp = append([]string{""}, vp...)
		}
	}
	// The captured pane may paint its own backgrounds; force them all
	// back to the theme fill so the preview is a solid theme-colored
	// panel with no transparency.
	seq := backgroundSeq(m.st.bg)
	for _, l := range vp {
		if len(out) >= n {
			break
		}
		out = append(out, fit(forceBackground(l, seq), w))
	}
	for len(out) < n {
		out = append(out, fit("", w))
	}
	return out
}

// trimEmptyEdges drops blank lines at both ends of a pane capture so the
// bottom pin lands on real content (tmux pads empty rows and ends with a
// newline). Blank means empty after stripping ANSI and whitespace.
func trimEmptyEdges(lines []string) []string {
	isBlank := func(s string) bool {
		return strings.TrimSpace(ansi.Strip(s)) == ""
	}
	start := 0
	for start < len(lines) && isBlank(lines[start]) {
		start++
	}
	end := len(lines)
	for end > start && isBlank(lines[end-1]) {
		end--
	}
	return lines[start:end]
}

// trimTrailingEmpty drops empty lines at the end of a pane capture.
// Kept as a thin wrapper for older tests and call sites.
func trimTrailingEmpty(lines []string) []string {
	i := len(lines)
	for i > 0 && strings.TrimSpace(lines[i-1]) == "" {
		i--
	}
	return lines[:i]
}

// mainHeaderHeight is the row count of the popup's title chrome: the
// asciiLogo lines, one per row.
const mainHeaderHeight = len(asciiLogo)

// asciiLogo is the tmon wordmark drawn across the header's two rows.
var asciiLogo = [2]string{
	"▀█▀ █▀▄▀█ █▀█ █▄░█",
	"░█░ █░▀░█ █▄█ █░▀█",
}

// mainTopChrome is the row count above the agent-view body: the wordmark
// rows, their divider, and — while searching — the query input row.
func (m Model) mainTopChrome() int {
	n := mainHeaderHeight + 1
	if m.searching {
		n++
	}
	return n
}

// headerLines renders the popup title chrome shared by the agent view and
// the theme selector: the ascii wordmark on the left spanning both
// mainHeaderHeight rows, with hints[i] right-aligned on row i (an empty
// entry leaves that row's right side blank).
func (m Model) headerLines(w int, hints [mainHeaderHeight]string) []string {
	lines := make([]string, mainHeaderHeight)
	for i, glyph := range asciiLogo {
		logo := m.st.cyan.Bold(true).Render(" " + glyph)
		right := m.st.dim.Render(hints[i])
		pad := w - ansi.StringWidth(logo) - ansi.StringWidth(right)
		if pad < 1 {
			lines[i] = ansi.Truncate(logo, w, "")
			continue
		}
		lines[i] = logo + strings.Repeat(" ", pad) + right
	}
	return lines
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

// footerLine shows the active status filter (if any) with the match count
// aligned right; the live query itself is edited in its own row above the
// list, not here. When a selectable agent with a real pane is focused, a
// preview scroll tip is shown. The right-side tips get a width budget after
// the left segment so narrow footers degrade gracefully.
func (m Model) footerLine(w int) string {
	// Version pinned bottom-left, one cell in from the border.
	left := ""
	if m.version != "" {
		left = " " + m.st.dim.Render(m.version) + "  "
	}
	if m.filterStatus != "" {
		left += m.st.dim.Render("▌ " + m.filterLabel() + " (press again to clear)")
	}
	// Reserve one cell for a right margin after the tips plus one so
	// twoSided always has a pad between the segments.
	right := m.footerRight(w-ansi.StringWidth(left)-2) + " "
	return m.twoSided(left, right, w)
}

// footerRight is the right-aligned footer segment: match count while
// filtering, and resize/scroll tips for the preview. Tips are joined
// most-useful first and trailing ones are dropped until the segment fits
// its budget, so the count and navigation hint always survive. The theme
// hint lives in the header, not here.
func (m Model) footerRight(w int) string {
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
	text := strings.Join(parts, "  ")
	for ansi.StringWidth(text) > w && len(parts) > 1 {
		parts = parts[:len(parts)-1]
		text = strings.Join(parts, "  ")
	}
	return m.st.dim.Render(text)
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
