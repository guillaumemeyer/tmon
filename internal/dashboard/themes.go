package dashboard

import (
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/guillaumemeyer/tmon/internal/theme"
	"github.com/guillaumemeyer/tmon/internal/tmux"
)

// themeItem is one preset row of the theme selector.
type themeItem string

// FilterValue is part of list.Item; the selector disables filtering, but
// keeping the name means the field never regresses if it is ever enabled.
func (i themeItem) FilterValue() string { return string(i) }

// themeDelegate renders a single-line theme row; the highlighted row gets
// the selection background across the full list width.
type themeDelegate struct {
	st    styles
	width int
}

func (d themeDelegate) Height() int  { return 1 }
func (d themeDelegate) Spacing() int { return 0 }

// Update is part of list.ItemDelegate; theme rows have no per-key behavior.
func (d themeDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

// Render writes one theme row: the name with a leading indent, fitted to
// the list width. The highlighted row carries the selection background.
func (d themeDelegate) Render(w io.Writer, lm list.Model, index int, item list.Item) {
	name, ok := item.(themeItem)
	if !ok {
		return
	}
	line := fit("  "+string(name), d.width)
	if lm.Index() == index {
		line = d.st.selBg.Render(line)
	}
	io.WriteString(w, line)
}

// newThemesList builds the bubbles list used by the theme selector: the
// preset names as single-line items. tmon owns quit/apply, so the list's
// own bindings for those are disabled and only navigation is forwarded.
func newThemesList() list.Model {
	names := theme.Names()
	items := make([]list.Item, 0, len(names))
	for _, n := range names {
		items = append(items, themeItem(n))
	}
	l := list.New(items, themeDelegate{st: defaultStyles}, 40, 10)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowPagination(false)
	l.SetShowHelp(false)
	l.SetShowFilter(false)
	l.SetFilteringEnabled(false)
	l.InfiniteScrolling = true

	km := list.DefaultKeyMap()
	km.PrevPage.SetEnabled(false) // left/h (resize) are ours
	km.NextPage.SetEnabled(false) // right/l (resize) are ours
	km.Filter.SetEnabled(false)   // "/" is the agent search
	km.ClearFilter.SetEnabled(false)
	km.Quit.SetEnabled(false) // q/esc return to the agent list
	km.ForceQuit.SetEnabled(false)
	km.ShowFullHelp.SetEnabled(false)
	km.CloseFullHelp.SetEnabled(false)
	l.KeyMap = km
	return l
}

// themeNames returns the preset names currently in the selector list, in
// list order.
func (m Model) themeNames() []string {
	items := m.themes.Items()
	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, string(it.(themeItem)))
	}
	return names
}

// persistTheme writes the chosen theme back to tmux so the status bar
// matches the popup after it closes: the @tmon-theme option plus the
// TMON_THEME environment (the config reads TMON_THEME first). Both commands
// are best-effort; failures and headless runs are ignored. Package-level so
// tests can capture it.
var persistTheme = func(name string) {
	if !tmux.Available() {
		return
	}
	_, _ = tmux.Run("set-option", "-g", "@tmon-theme", name)
	_, _ = tmux.Run("set-environment", "-g", "TMON_THEME", name)
}

// applyThemeSelection resolves and applies the highlighted theme to the
// whole popup and persists it so the tmux status bar matches after the
// popup closes. The stored resolution options (overrides, ASCII) keep
// applying to the chosen preset.
func (m Model) applyThemeSelection() Model {
	names := m.themeNames()
	if len(names) == 0 {
		return m
	}
	name := names[m.themes.Index()]
	m.themeOpts.Name = name
	persistTheme(name)
	return m.WithTheme(theme.Resolve(m.themeOpts))
}

// themeView renders the theme selector: the preset list on the left and the
// live palette preview for the highlighted theme on the right. It always
// emits exactly h lines so the footer lands on the last row.
func (m Model) themeView(w, h int) string {
	lines := make([]string, 0, h)
	lines = append(lines, m.themeHeaderLine(w))
	lines = append(lines, fit(m.st.dim.Render(strings.Repeat("━", w)), w))

	bodyLines := h - 3
	if bodyLines < 1 {
		bodyLines = 1
	}
	listW, panelW := m.panelWidths(w)

	sel := ""
	if names := m.themeNames(); len(names) > 0 {
		sel = names[m.themes.Index()]
	}
	left := m.themeListLines(listW, bodyLines)
	prev := m.themePreviewLines(panelW, bodyLines, sel)
	for i := range left {
		lines = append(lines, left[i]+"│"+prev[i])
	}
	for len(lines) < h-1 {
		lines = append(lines, "")
	}
	lines = append(lines, m.themeFooterLine(w))
	return strings.Join(lines, "\n")
}

// themeListLines renders the theme list into exactly bodyLines lines of
// listW cells each, so the preview separator stays vertically aligned. The
// delegate is refreshed on this copy so the selection styles are current.
func (m Model) themeListLines(w, bodyLines int) []string {
	m.themes.SetSize(w, bodyLines)
	m.themes.SetDelegate(themeDelegate{st: m.st, width: w})
	out := strings.Split(m.themes.View(), "\n")
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	for len(out) < bodyLines {
		out = append(out, fit("", w))
	}
	return out
}

// themePreviewLines renders the palette preview panel for the highlighted
// theme: its name, the eight color swatches, and the emoji/ASCII sample
// status lines. The theme is re-resolved from the stored options so the
// user's @tmon-color-* / @tmon-icon-* overrides keep applying. Every line
// is exactly w cells so it joins cleanly with the theme list column.
func (m Model) themePreviewLines(w, n int, name string) []string {
	out := make([]string, 0, n)
	if name == "" {
		out = append(out, fit(m.st.dim.Render(" no themes"), w))
	} else {
		t := theme.Resolve(theme.Options{
			Name:           name,
			ColorOverrides: m.themeOpts.ColorOverrides,
			IconOverrides:  m.themeOpts.IconOverrides,
			ASCII:          m.themeOpts.ASCII,
		})
		out = append(out, fit(m.st.cyan.Bold(true).Render(" "+t.Name), w))
		out = append(out, fit("", w))
		for _, sw := range theme.SwatchLines(t) {
			out = append(out, fit(sw, w))
		}
		out = append(out, fit("", w))
		emoji := theme.Resolve(theme.Options{
			Name:           name,
			ColorOverrides: m.themeOpts.ColorOverrides,
			IconOverrides:  m.themeOpts.IconOverrides,
		})
		ascii := theme.Resolve(theme.Options{
			Name:           name,
			ColorOverrides: m.themeOpts.ColorOverrides,
			IconOverrides:  m.themeOpts.IconOverrides,
			ASCII:          true,
		})
		out = append(out, fit(m.st.dim.Render(" emoji: ")+theme.SampleLine(emoji, emoji.Icons), w))
		out = append(out, fit(m.st.dim.Render(" ascii: ")+theme.SampleLine(ascii, ascii.Icons), w))
	}
	for len(out) < n {
		out = append(out, fit("", w))
	}
	return out
}

// themeHeaderLine is the selector title with the back hint aligned right.
func (m Model) themeHeaderLine(w int) string {
	title := m.st.cyan.Bold(true).Render(" " + m.theme.Icons.App + " tmon — themes")
	hint := m.st.dim.Render("[enter] apply  [esc] back ")
	pad := w - ansi.StringWidth(title) - ansi.StringWidth(hint)
	if pad < 1 {
		return ansi.Truncate(title, w, "")
	}
	return title + strings.Repeat(" ", pad) + hint
}

// themeFooterLine is the selector footer: browse/apply/back hints with the
// highlighted theme name aligned right.
func (m Model) themeFooterLine(w int) string {
	left := " " + m.st.dim.Render("[↑/↓ j/k] browse  [enter] apply  [esc] back")
	right := ""
	if names := m.themeNames(); len(names) > 0 {
		right = m.st.dim.Render(" " + names[m.themes.Index()])
	}
	return m.twoSided(left, right, w)
}
