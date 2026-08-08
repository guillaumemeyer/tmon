package dashboard

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
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
	_, _ = io.WriteString(w, line)
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

// selectThemeByName moves the theme list cursor onto name when present.
// Unknown names leave the selection unchanged.
func (m Model) selectThemeByName(name string) Model {
	for i, n := range m.themeNames() {
		if n == name {
			m.themes.Select(i)
			break
		}
	}
	return m
}

// persistTheme writes the chosen theme back to tmux so the status bar
// matches the popup after it closes. Both the global environment and every
// live session are updated: tmux copies globals into each session at
// creation time, so set-environment -g alone leaves existing sessions on
// the previous theme (session values shadow globals). Best-effort;
// failures and headless runs are ignored. Package-level so tests can
// capture it.
var persistTheme = func(name string) {
	if !tmux.Available() {
		return
	}
	_, _ = tmux.Run("set-environment", "-g", "TMON_THEME", name)
	out, err := tmux.Run("list-sessions", "-F", "#{session_name}")
	if err != nil {
		// Fall back to the current session when listing fails.
		_, _ = tmux.Run("set-environment", "TMON_THEME", name)
		return
	}
	for _, s := range strings.Split(strings.TrimSpace(out), "\n") {
		if s == "" {
			continue
		}
		_, _ = tmux.Run("set-environment", "-t", s, "TMON_THEME", name)
	}
}

// themeStateDir is where the persisted theme file lives: the same state
// directory as the dashboard settings file. Empty when no settings path is
// set (tests and ad-hoc construction), which disables file persistence.
func (m Model) themeStateDir() string {
	if m.settingsPath == "" {
		return ""
	}
	return filepath.Dir(m.settingsPath)
}

// writeThemeFile persists the chosen theme name next to the dashboard
// settings so it survives a tmux server restart — tmux's TMON_THEME variable
// is server-state only and is wiped on restart. tmon.tmux restores from
// this file at load. Failures are ignored so a read-only state dir never
// breaks the popup.
func writeThemeFile(dir, name string) {
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "theme"), []byte(name), 0o644)
}

// applyThemeSelection resolves and applies the highlighted theme to the
// whole popup and persists it so the tmux status bar matches after the
// popup closes — live in tmux (see persistTheme) and to state/theme for the
// next tmux server start. The stored resolution options (overrides, ASCII)
// keep applying to the chosen preset. Only this path (enter/space)
// persists.
func (m Model) applyThemeSelection() Model {
	names := m.themeNames()
	if len(names) == 0 {
		return m
	}
	name := names[m.themes.Index()]
	m.themeOpts.Name = name
	persistTheme(name)
	writeThemeFile(m.themeStateDir(), name)
	return m.WithTheme(theme.Resolve(m.themeOpts))
}

// applyThemePreview restyles the whole popup with the highlighted preset —
// the live preview while browsing — without persisting anything. The
// stored overrides/ASCII keep applying; themeOpts.Name is left untouched
// so a later esc/q revert has nothing to undo.
func (m Model) applyThemePreview() Model {
	names := m.themeNames()
	if len(names) == 0 {
		return m
	}
	opts := m.themeOpts
	opts.Name = names[m.themes.Index()]
	return m.WithTheme(theme.Resolve(opts))
}

// themeView renders the theme selector: the preset list on the left and the
// live palette preview for the highlighted theme on the right, framed by the
// same rounded border as the agent view. Its title chrome matches the doctor
// report's: the ascii wordmark with "🎨 Themes" where the agent view shows
// the version, and the apply/revert hints right-aligned with one cell of
// margin. It always emits exactly h lines so the footer lands on the last
// row.
func (m Model) themeView(w, h int) string {
	innerW, innerH := w-2, h-2
	lines := make([]string, 0, innerH)
	lines = append(lines, m.headerLines(innerW, [mainHeaderHeight]string{"[enter/space] apply  [esc/q] revert ", ""}, "🎨 Themes")...)
	lines = append(lines, fit(m.st.dim.Render(strings.Repeat("━", innerW)), innerW))

	bodyLines := bodyLinesFor(innerH, mainHeaderHeight+1)
	if bodyLines < 1 {
		bodyLines = 1
	}
	listW, panelW := m.panelWidths(innerW)

	sel := ""
	if names := m.themeNames(); len(names) > 0 {
		sel = names[m.themes.Index()]
	}
	left := m.themeListLines(listW, bodyLines)
	prev := m.themePreviewLines(panelW, bodyLines, sel)
	for i := range left {
		lines = append(lines, left[i]+"│"+prev[i])
	}
	for len(lines) < innerH-2 {
		lines = append(lines, "")
	}
	lines = append(lines, fit(m.st.dim.Render(strings.Repeat("━", innerW)), innerW))
	lines = append(lines, m.themeFooterLine(innerW))
	return strings.Join(paintRows(w, framed(w, lines, m.st.white), m.st.bg), "\n")
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

// themeFooterLine is the theme selector's bottom status bar: the browse
// and apply hints, right-aligned with one cell of right margin — matching
// the doctor report's footer. Hints that do not fit the popin width are
// truncated from the end.
func (m Model) themeFooterLine(w int) string {
	text := "[↑/↓ j/k] preview  [enter/space] apply  [esc/q] revert"
	if ansi.StringWidth(text)+1 <= w {
		text += " "
	} else {
		text = ansi.Truncate(text, w, "")
	}
	pad := w - ansi.StringWidth(text)
	if pad > 0 {
		text = strings.Repeat(" ", pad) + text
	}
	return m.st.dim.Render(text)
}
