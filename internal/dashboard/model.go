// Package dashboard implements the interactive agent navigation popup
// (`tmon dashboard`), a bubbletea TUI ported from scripts/dashboard.sh.
package dashboard

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/tmux"
)

// Refresh cadence, matching the bash popup.
const refreshInterval = 1500 * time.Millisecond // between auto-refresh ticks

// Row is one detected agent and the pane data the popup shows about it.
type Row struct {
	PID           int
	Label         string
	Title         string // session/conversation title, e.g. Grok's generated_title; "" if unknown
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
}

// Data is one dashboard snapshot: the full agent list after a reload.
type Data struct {
	Rows []Row
}

// Loader produces dashboard snapshots. It is a field so tests can inject a
// fake one; DefaultLoader builds the real implementation.
type Loader func() (Data, error)

// itemKind discriminates the grouped display items.
type itemKind int

const (
	itemSession itemKind = iota // non-selectable session header
	itemWindow                  // non-selectable window sub-header
	itemAgent                   // selectable agent line
)

// item is one line of the grouped list (session → window → agent).
type item struct {
	kind        itemKind
	sessionName string // itemSession
	windowIdx   string // itemWindow
	windowName  string // itemWindow
	rowIdx      int    // itemAgent: index into Model.rows
}

// Model is the bubbletea state for the dashboard popup.
type Model struct {
	loader Loader

	rows     []Row  // full sorted agent list
	filtered []int  // indices into rows after the query + status filter
	items    []item // grouped display items
	selMap   []int  // item index per selectable position
	selected int    // index into selMap

	filterStatus agent.Status // "" = no status filter

	previewText   string            // pane capture of the selected pane (colors preserved)
	previewPane   string            // pane target the preview currently shows
	previewOffset int               // lines scrolled up from the bottom (0 = pin to end)
	paneCache     map[string]string // pane target → full capture (for search + preview)
	previewPct    int               // preview panel width as % of popup (default 50)

	// settingsPath is where UI prefs (e.g. preview width) are persisted.
	// Empty disables load/save (tests and ad-hoc construction).
	settingsPath string

	query     string
	searching bool

	ascii bool // render status icons as ASCII (B/W/I) instead of emoji

	width, height int

	// focusCmd switches the tmux client to the selected agent's pane, then
	// quits. Overridable in tests.
	focusCmd func(Row) tea.Cmd
}

// New returns the dashboard model. loader supplies data snapshots; if nil
// the dashboard stays empty (safe zero value for direct construction).
// ascii renders the status icons as ASCII (B/W/I) instead of emoji.
func New(loader Loader, ascii bool) Model {
	return Model{
		loader:     loader,
		ascii:      ascii,
		focusCmd:   defaultFocusCmd,
		previewPct: defaultPreviewPct,
	}
}

// WithSettingsPath sets the JSON file used to persist UI preferences
// (preview width) and loads any existing values.
func (m Model) WithSettingsPath(path string) Model {
	m.settingsPath = path
	m.loadSettings()
	return m
}

// Init kicks off the initial full load and the auto-refresh ticker.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		func() tea.Msg { return initMsg{} },
		tickCmd(),
	)
}

// defaultFocusCmd switches the tmux client to the agent's pane (the bash
// popup's focus_agent) and then quits, closing the popup.
func defaultFocusCmd(r Row) tea.Cmd {
	return tea.Sequence(
		func() tea.Msg {
			if r.Pane != "" && r.Pane != "?" && tmux.Available() {
				tmux.Run("switch-client", "-t", r.Pane)
			}
			return nil
		},
		tea.Quit,
	)
}
