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
const (
	refreshInterval = 1500 * time.Millisecond // between auto-refresh ticks
	fullReloadTicks = 4                       // full data reload every N ticks (~6s)
)

// Row is one detected agent and the pane data the popup shows about it.
type Row struct {
	PID         int
	Label       string
	Cmdline     string
	CWD         string
	Status      agent.Status
	Pane        string // tmux target "session:window.pane"; "?" if unresolvable
	SessionID   string // "$"-stripped session id; "?" if unpaned
	SessionName string
	WindowIndex string
	WindowName  string
	PaneIndex   string
}

// Data is one dashboard snapshot. A light refresh returns only Frame (Rows
// nil) so the model keeps its cached rows; a full reload returns both.
type Data struct {
	Rows  []Row
	Frame int
}

// Mode selects how much work a refresh does.
type Mode bool

const (
	ModeLight Mode = false // re-read the shared state file for the animation frame
	ModeFull  Mode = true  // full reload: detection, pane map, blocked checks
)

// Loader produces dashboard snapshots. It is a field so tests can inject a
// fake one; DefaultLoader builds the real implementation.
type Loader func(mode Mode) (Data, error)

// itemKind discriminates the grouped display items.
type itemKind int

const (
	itemSession itemKind = iota // non-selectable session header
	itemWindow                  // non-selectable window sub-header
	itemAgent                   // selectable agent line
)

// item is one line of the grouped list.
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
	filtered []int  // indices into rows after the query filter
	items    []item // grouped display items
	selMap   []int  // item index per selectable position
	selected int    // index into selMap

	query     string
	searching bool

	frame int // animation frame (toggles ?/● ↔ !)
	ticks int // auto-refresh tick counter

	width, height int

	// focusCmd switches the tmux client to the selected agent's pane, then
	// quits. Overridable in tests.
	focusCmd func(Row) tea.Cmd
}

// New returns the dashboard model. loader supplies data snapshots; if nil
// the dashboard stays empty (safe zero value for direct construction).
func New(loader Loader) Model {
	return Model{loader: loader, focusCmd: defaultFocusCmd}
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
