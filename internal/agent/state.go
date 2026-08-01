// Package agent implements the four-state activity machine and the JSON
// snapshot shared between `tmon status` (writer) and `tmon dashboard`
// (reader), so the two always agree — a fix for the drift between the two
// bash scripts.
package agent

import (
	"sort"

	"github.com/guillaumemeyer/tmon/internal/config"
)

// Status is one of the four activity levels.
type Status string

const (
	StatusRunning Status = "running"
	StatusActive  Status = "active"
	StatusBlocked Status = "blocked"
	StatusIdle    Status = "idle"
)

// AgentState is the per-process tracked state, persisted in state.json.
type AgentState struct {
	PID        int    `json:"pid"`
	Label      string `json:"label"`
	Status     Status `json:"status"`
	CPU        int64  `json:"cpu"` // cumulative ticks (utime+stime+cutime+cstime)
	IO         int64  `json:"io"`  // cumulative bytes (rchar+wchar)
	IdleStreak int    `json:"idleStreak"`
	Pane       string `json:"pane,omitempty"`
	CWD        string `json:"cwd,omitempty"`
}

// Options configures the tracker.
type Options struct {
	PollIntervalSec     int   // seconds between full scans
	ActivityThresholdMs int   // CPU ms/s to consider "active"
	IOThreshold         int64 // min IO bytes/poll to consider "active"
	IdleDecayPolls      int   // consecutive idle polls before "idle"
	CLKTicksPerSec      int   // kernel clock ticks per second
}

// NewOptions derives tracker options from a config.
func NewOptions(cfg config.Config) Options {
	return Options{
		PollIntervalSec:     cfg.PollIntervalSec(),
		ActivityThresholdMs: cfg.ActivityThresholdMs,
		IOThreshold:         cfg.IOThreshold,
		IdleDecayPolls:      cfg.IdleDecayPolls,
		CLKTicksPerSec:      cfg.CLKTicks,
	}
}

// Tracker evaluates per-poll activity transitions across polls.
type Tracker struct {
	opts Options
	prev map[int]*AgentState // state from the previous poll
	curr map[int]*AgentState // state being accumulated this poll
}

// NewTracker returns an empty tracker.
func NewTracker(opts Options) *Tracker {
	return &Tracker{opts: opts, prev: map[int]*AgentState{}}
}

// BeginPoll starts a new poll cycle.
func (t *Tracker) BeginPoll() {
	t.curr = make(map[int]*AgentState)
}

// Evaluate computes the activity status for a detected agent given its
// current cumulative counters and whether its pane looks blocked.
func (t *Tracker) Evaluate(pid int, label, cwd, pane string, cpuNow, ioNow int64, blocked bool) Status {
	oldStatus := StatusRunning
	oldStreak := 0
	prev := t.prev[pid]
	if prev != nil {
		oldStatus = prev.Status
		oldStreak = prev.IdleStreak
	}

	next := &AgentState{PID: pid, Label: label, CPU: cpuNow, IO: ioNow, Pane: pane, CWD: cwd}

	// Blocked overrides everything — a waiting agent is waiting even if it's
	// burning CPU.
	if blocked {
		next.Status, next.IdleStreak = StatusBlocked, 0
		t.curr[pid] = next
		return StatusBlocked
	}

	// First sighting (or a process that previously showed zero CPU): show it
	// as running immediately — agents often think remotely with near-zero
	// local CPU, and the delta math needs a baseline poll anyway.
	if prev == nil || prev.CPU == 0 {
		next.Status, next.IdleStreak = StatusRunning, 0
		t.curr[pid] = next
		return StatusRunning
	}

	// Convert the per-second CPU threshold to ticks per poll interval,
	// e.g. 500 ms/s * 3 s * 100 ticks/s / 1000 = 150 ticks minimum.
	threshold := t.opts.ActivityThresholdMs * t.opts.PollIntervalSec * t.opts.CLKTicksPerSec / 1000
	if threshold < 1 {
		threshold = 1
	}

	cpuDelta := cpuNow - prev.CPU
	ioDelta := ioNow - prev.IO
	if cpuDelta >= int64(threshold) || ioDelta >= t.opts.IOThreshold {
		next.Status, next.IdleStreak = StatusActive, 0
		t.curr[pid] = next
		return StatusActive
	}

	// No meaningful activity: apply the idle-decay grace period so agents
	// between API calls don't flicker.
	streak := oldStreak + 1
	next.IdleStreak = streak
	if streak < t.opts.IdleDecayPolls {
		next.Status = oldStatus
		t.curr[pid] = next
		return oldStatus
	}
	next.Status = StatusIdle
	t.curr[pid] = next
	return StatusIdle
}

// EndPoll commits the current poll as the previous one, dropping any agents
// that were not detected this poll (their processes have exited).
func (t *Tracker) EndPoll() {
	t.prev = t.curr
	t.curr = nil
}

// Snapshot returns the current poll's states sorted by PID, for persistence.
func (t *Tracker) Snapshot() []AgentState {
	states := make([]AgentState, 0, len(t.curr))
	for _, s := range t.curr {
		states = append(states, *s)
	}
	sort.Slice(states, func(i, j int) bool { return states[i].PID < states[j].PID })
	return states
}
