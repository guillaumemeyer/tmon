// Package dashboard implements the interactive agent navigation popup
// (`tmon dashboard`), a bubbletea TUI built on the bubbles components.
package dashboard

import (
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/theme"
	"github.com/guillaumemeyer/tmon/internal/tmux"
)

// Refresh cadence, matching the bash popup.
const refreshInterval = 1500 * time.Millisecond // between auto-refresh ticks

// Row is one detected agent and the pane data the popup shows about it.
type Row struct {
	PID           int
	Label         string
	Title         string // session/conversation title, e.g. Grok's generated_title; "" if unknown
	Profile       string // agent profile (Hermes multi-home); "" if unknown
	Cmdline       string
	CWD           string
	Status        agent.Status
	Detail        string // connector detail, e.g. "tool:Bash" or "phase:reasoning"
	LastTs        int64  // unix seconds of last status change (0 = unknown)
	BlockedReason string // matched blocked-pattern text, "" if not blocked
	Pane          string // tmux target "session:window.pane"; "?" if unresolvable
	SessionID     string // "$"-stripped session id; "?" if unpaned
	SessionName   string
	WindowIndex   string
	WindowName    string
	PaneIndex     string
	GitRoot       string      // repository root (dir holding .git); "" if not a repo
	Branch        string      // current branch or short SHA; "" if not a repo
	PR            string      // open GitHub PR number for the branch; "" if unknown
	Usage         agent.Usage // token usage stats; zero = unknown (no stats line)
}

// Data is one dashboard snapshot: the full agent list after a reload.
type Data struct {
	Rows []Row
}

// Loader produces dashboard snapshots. It is a field so tests can inject a
// fake one; DefaultLoader builds the real implementation.
type Loader func() (Data, error)

// Model is the bubbletea state for the dashboard popup.
type Model struct {
	loader Loader

	rows      []Row      // full sorted agent list
	filtered  []int      // indices into rows after the query + status filter
	agentList list.Model // flat, selectable agent list (bubbles)

	// loading is true while a data load is in flight. Loads run off the
	// event loop (see loadCmd) so a slow loader cannot freeze key handling;
	// the guard keeps a tick from stacking a second load on top.
	loading bool

	filterStatus agent.Status // "" = no status filter

	previewText         string            // pane capture of the selected pane (colors preserved)
	previewPane         string            // pane target the preview currently shows
	paneCache           map[string]string // pane target → full capture (for search + preview)
	previewPct          int               // preview panel width as % of popup (default 50)
	draggingSplit       bool              // true while the user is dragging the │ separator
	preview             viewport.Model    // right-side pane preview (scrollable, bottom-pinned)
	previewFollowBottom bool              // keep the viewport on the latest lines (default true)
	previewWrap         bool              // fit-to-width: wrap captured lines to the preview width
	previewWrapWidth    int               // preview width the viewport content is wrapped at (0 = raw)

	// settingsPath is where UI prefs (e.g. preview width) are persisted.
	// Empty disables load/save (tests and ad-hoc construction).
	settingsPath string

	// viewMode is the agent list layout (list / projects / status). Persisted
	// with the other UI prefs. listScroll is the first visible content line
	// of the list column (custom scroll so section headers can be 1 line).
	viewMode   ViewMode
	listScroll int

	query       string
	searching   bool
	searchInput textinput.Model // live query editor at the bottom of the list while searching

	// theme carries the resolved palette and icons (preset + overrides);
	// st are the lipgloss styles built from it. Both are set by New and
	// replaced by WithTheme.
	theme theme.Theme
	st    styles

	// contextWarn is the context-usage percent at which the usage bar and
	// the status bar switch to the warn color; 0 disables the warning.
	contextWarn int

	// spinner animates the status slot of working agents (one glyph, green).
	spinner spinner.Model

	// themeMode is true while the theme selector is open; themes is its
	// preset list, themeOpts the resolution inputs (overrides, ASCII) that
	// keep applying to the highlighted preset.
	themeMode bool
	themes    list.Model
	themeOpts theme.Options

	// themeCommitted is the theme in effect when the selector opened.
	// Browsing previews other presets live; esc/q revert to this, and only
	// enter/space persist the browsed theme.
	themeCommitted theme.Theme

	// version is the tmon release string shown next to the ascii logo on
	// the second header line (e.g. "0.4.2"). Empty hides it (tests and
	// direct construction).
	version string

	width, height int

	// focusCmd switches the tmux client to the selected agent's pane, then
	// quits. Overridable in tests.
	focusCmd func(Row) tea.Cmd
}

// New returns the dashboard model. loader supplies data snapshots; if nil
// the dashboard stays empty (safe zero value for direct construction).
// ascii renders the status icons as ASCII (B/W/I) instead of emoji, using
// the default theme; call WithTheme to apply a resolved theme (preset +
// overrides), which supersedes the ascii flag for icons.
func New(loader Loader, ascii bool) Model {
	m := Model{
		loader:              loader,
		focusCmd:            defaultFocusCmd,
		previewPct:          defaultPreviewPct,
		contextWarn:         defaultContextWarn,
		previewFollowBottom: true,
	}
	m.theme = theme.Resolve(theme.Options{ASCII: ascii})
	m.st = buildStyles(m.theme.Palette)
	m.agentList = newAgentList()
	m.themes = newThemesList()
	m.spinner = newSpinner(m.theme.Palette)
	m.preview = viewport.New(40, 20) // sized per render in View
	m.searchInput = newSearchInput()
	return m
}

// newSearchInput builds the query editor shown at the bottom of the agent
// list while searching. Styles are refreshed per render from the current
// theme (see View), matching the pattern used for the agent list delegate.
func newSearchInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.CharLimit = 200
	return ti
}

// newSpinner builds the working-agent spinner, colored with the theme's
// working color. The frames are ASCII (|/-\) so ASCII mode stays consistent.
func newSpinner(pal theme.Palette) spinner.Model {
	return spinner.New(spinner.WithStyle(
		lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Lipgloss(pal.Working))),
	))
}

// selectedRow returns the Row currently selected in the agent list, or nil
// when nothing is selectable (empty list or selection out of range).
func (m Model) selectedRow() *Row {
	if len(m.filtered) == 0 {
		return nil
	}
	idx := m.agentList.Index()
	if idx < 0 || idx >= len(m.filtered) {
		return nil
	}
	return &m.rows[m.filtered[idx]]
}

// spinnerFrame returns the current spinner glyph for working agents. The
// frame carries the green foreground style, so rows render it directly.
func (m Model) spinnerFrame() string { return m.spinner.View() }

// WithTheme replaces the model's resolved theme (palette + icons) and
// rebuilds the lipgloss styles from it.
func (m Model) WithTheme(t theme.Theme) Model {
	m.theme = t
	m.st = buildStyles(t.Palette)
	m.spinner = newSpinner(t.Palette)
	return m
}

// WithThemeOptions records the resolution inputs (preset name, color/icon
// overrides, ASCII) so the theme selector can re-resolve presets while
// honoring the user's @tmon-color-* / @tmon-icon-* overrides.
func (m Model) WithThemeOptions(opts theme.Options) Model {
	m.themeOpts = opts
	return m
}

// WithContextWarn sets the context-usage percent at which the usage bar
// switches to the warn color; 0 disables the warning.
func (m Model) WithContextWarn(n int) Model {
	m.contextWarn = n
	return m
}

// WithSettingsPath sets the JSON file used to persist UI preferences
// (preview width, list view) and loads any existing values.
func (m Model) WithSettingsPath(path string) Model {
	m.settingsPath = path
	m.loadSettings()
	return m
}

// WithVersion sets the version string shown next to the ascii logo.
func (m Model) WithVersion(v string) Model {
	m.version = v
	return m
}

// Init kicks off the initial full load and the working-agent spinner
// animation. The auto-refresh tick is not armed here: applyLoad re-arms it
// after every completed load, so the refresh cadence is anchored to load
// completion and never stacks a second load on an in-flight one.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		func() tea.Msg { return initMsg{} },
		func() tea.Msg { return m.spinner.Tick() },
	)
}

// defaultFocusCmd switches the tmux client to the agent's pane (the bash
// popup's focus_agent) and then quits, closing the popup.
func defaultFocusCmd(r Row) tea.Cmd {
	return tea.Sequence(
		func() tea.Msg {
			if r.Pane != "" && r.Pane != "?" && tmux.Available() {
				_, _ = tmux.Run("switch-client", "-t", r.Pane)
			}
			return nil
		},
		tea.Quit,
	)
}
