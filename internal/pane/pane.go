// Package pane maps agent PIDs to tmux panes using three strategies, in
// order: direct pane_pid match, TTY match, then a walk up the process tree.
// Ported from the bash plugin's pane-map logic.
package pane

import (
	"strconv"
	"strings"

	"github.com/guillaumemeyer/tmon/internal/proc"
	"github.com/guillaumemeyer/tmon/internal/tmux"
)

// Entry describes a single tmux pane.
type Entry struct {
	Target      string // session_name:window_index.pane_index
	SessionID   string // e.g. "$1"
	SessionName string
	WindowIndex string
	WindowName  string
	PaneIndex   string
}

// Map is a snapshot of all panes with O(1) lookups by TTY and pane PID.
type Map struct {
	byTTY map[string]Entry
	byPID map[int]Entry
}

// listFormat is the tmux -F format used to snapshot panes.
const listFormat = "#{pane_tty}|#{session_name}:#{window_index}.#{pane_index}|#{pane_pid}|#{session_id}|#{session_name}|#{window_index}|#{window_name}|#{pane_index}"

// BuildMap snapshots every pane via `tmux list-panes -a` and indexes it.
func BuildMap() (*Map, error) {
	out, err := tmux.Run("list-panes", "-a", "-F", listFormat)
	if err != nil {
		return nil, err
	}
	return Parse(out)
}

// Parse indexes raw `tmux list-panes -a -F <listFormat>` output. Exported
// for tests with fake output.
func Parse(output string) (*Map, error) {
	m := &Map{
		byTTY: map[string]Entry{},
		byPID: map[int]Entry{},
	}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 8 {
			continue
		}
		pid, err := strconv.Atoi(parts[2])
		if err != nil {
			continue
		}
		e := Entry{
			Target:      parts[1],
			SessionID:   parts[3],
			SessionName: parts[4],
			WindowIndex: parts[5],
			WindowName:  parts[6],
			PaneIndex:   parts[7],
		}
		m.byTTY[parts[0]] = e
		m.byPID[pid] = e
	}
	return m, nil
}

// Resolve finds the pane for a PID using the three strategies in order.
func (m *Map) Resolve(pid int) (Entry, bool) {
	// 1. Direct PID match.
	if e, ok := m.byPID[pid]; ok {
		return e, true
	}

	// 2. TTY match.
	if tty := proc.TTYForPID(pid); tty != "" {
		if e, ok := m.byTTY[tty]; ok {
			return e, true
		}
	}

	// 3. Walk up the process tree looking for a pane_pid ancestor
	// (agents are often children of a shell or wrapper).
	const maxDepth = 10
	anc := pid
	for depth := 0; depth < maxDepth; depth++ {
		ppid, err := proc.ParentPID(anc)
		if err != nil || ppid <= 1 {
			break
		}
		anc = ppid
		if e, ok := m.byPID[anc]; ok {
			return e, true
		}
	}

	return Entry{}, false
}
