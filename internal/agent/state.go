// Package agent implements the three-state activity machine and the JSON
// snapshot shared between `tmon status` (writer) and `tmon dashboard`
// (reader), so the two always agree — a fix for the drift between the two
// bash scripts.
package agent

import (
	"math"
	"sort"
	"time"

	"github.com/guillaumemeyer/tmon/internal/config"
)

// Status is one of the three activity levels.
type Status string

const (
	StatusBlocked Status = "blocked" // expecting a user action; overrides everything
	StatusWorking Status = "working" // actively thinking or writing
	StatusIdle    Status = "idle"    // alive but not working and not waiting
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
	Detail     string `json:"detail,omitempty"`  // connector detail, e.g. "tool:Bash"
	Title      string `json:"title,omitempty"`   // session/conversation title from a connector
	Profile    string `json:"profile,omitempty"` // agent profile (Hermes multi-home); "" = unknown
	LastTs     int64  `json:"lastTs,omitempty"`  // unix seconds of last status change
	Usage      *Usage `json:"usage,omitempty"`   // token usage stats for the dashboard; nil = unknown
}

// Usage is the per-agent token usage stats shown by the dashboard's stats
// line. Connectors populate what their agent exposes; zero/empty fields mean
// "unknown" and are rendered as n/a. Quota fields (QuotaPct, QuotaReset,
// QuotaWindows) cover account/plan limits (e.g. a 5-hour window); no current
// agent exposes those locally, so they stay empty until a source exists.
// QuotaWindows carries one entry per quota window the provider reports
// (session, weekly all-models, weekly per-model); QuotaPct/QuotaReset mirror
// the first window for status --json and older consumers.
type Usage struct {
	TokensUsed   int64         `json:"tokensUsed,omitempty"`   // context tokens used in this conversation
	WindowTokens int64         `json:"windowTokens,omitempty"` // context window size; 0 = unknown
	QuotaPct     int           `json:"quotaPct,omitempty"`     // first quota window used %; 0 = unknown
	QuotaReset   string        `json:"quotaReset,omitempty"`   // first quota window reset, e.g. "14:00"; "" = unknown
	QuotaWindows []QuotaWindow `json:"quotaWindows,omitempty"` // one per reported quota window (session, weekly, per-model)
}

// QuotaWindow is one account quota window reported by a provider: the
// percent of the window used, a display label naming the window (e.g.
// "Current session", "Current week (all models)", "Current week (Fable)"),
// and the next reset as RFC3339 when the provider reports one.
type QuotaWindow struct {
	Pct     int    `json:"pct"`
	Label   string `json:"label"`
	ResetAt string `json:"resetAt"`
}

// Empty reports whether no usage stat is known at all.
func (u Usage) Empty() bool {
	return u.TokensUsed == 0 && u.WindowTokens == 0 && u.QuotaPct == 0 && u.QuotaReset == "" && len(u.QuotaWindows) == 0
}

// ContextPct returns the context-window usage percent (0 when the window
// size is unknown). Values are rounded to the nearest integer (half up),
// matching Hermes CLI / typical status bars — truncation made sub-1%
// sessions show as 0% while the agent itself showed 1%.
func (u Usage) ContextPct() int {
	if u.WindowTokens <= 0 {
		return 0
	}
	pct := int(math.Round(float64(u.TokensUsed) * 100 / float64(u.WindowTokens)))
	if pct < 0 {
		return 0
	}
	return pct
}

// Options configures the tracker.
type Options struct {
	PollIntervalSec     int   // seconds between full scans
	ActivityThresholdMs int   // CPU ms/s to consider "working"
	IOThreshold         int64 // min IO bytes/poll to consider "working"
	IdleDecayPolls      int   // consecutive quiet polls before "idle"
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

// SeedPrev loads prior agent states so the first Evaluate can compute
// CPU/IO deltas without a warm-up poll. Call before BeginPoll.
// Each entry is copied so the caller may reuse or mutate the slice.
func (t *Tracker) SeedPrev(agents []AgentState) {
	if len(agents) == 0 {
		return
	}
	t.prev = make(map[int]*AgentState, len(agents))
	for i := range agents {
		a := agents[i] // copy
		t.prev[a.PID] = &a
	}
}

// BeginPoll starts a new poll cycle.
func (t *Tracker) BeginPoll() {
	t.curr = make(map[int]*AgentState)
}

// Evaluate computes the activity status for a detected agent given its
// current cumulative counters and whether its pane looks blocked.
func (t *Tracker) Evaluate(pid int, label, cwd, pane string, cpuNow, ioNow int64, blocked bool) Status {
	oldStatus := StatusIdle
	oldStreak := 0
	oldLastTs := int64(0)
	prev := t.prev[pid]
	if prev != nil {
		oldStatus = prev.Status
		oldStreak = prev.IdleStreak
		oldLastTs = prev.LastTs
	}

	next := &AgentState{PID: pid, Label: label, CPU: cpuNow, IO: ioNow, Pane: pane, CWD: cwd}

	// Blocked overrides everything — a waiting agent is waiting even if it's
	// burning CPU.
	if blocked {
		next.Status, next.IdleStreak = StatusBlocked, 0
		next.LastTs = stamp(oldStatus, StatusBlocked, oldLastTs)
		t.curr[pid] = next
		return StatusBlocked
	}

	// First sighting (or a process that previously showed zero CPU): show it
	// as idle immediately — agents often think remotely with near-zero local
	// CPU, and the delta math needs a baseline poll anyway.
	if prev == nil || prev.CPU == 0 {
		next.Status, next.IdleStreak = StatusIdle, 0
		next.LastTs = stamp(oldStatus, StatusIdle, oldLastTs)
		t.curr[pid] = next
		return StatusIdle
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
		next.Status, next.IdleStreak = StatusWorking, 0
		next.LastTs = stamp(oldStatus, StatusWorking, oldLastTs)
		t.curr[pid] = next
		return StatusWorking
	}

	// No meaningful activity: apply the decay grace period so agents
	// between API calls don't flicker, then flag the agent as idle —
	// alive, but not actively thinking or writing. Agents expecting user
	// action never reach here: blocked above overrides everything.
	streak := oldStreak + 1
	next.IdleStreak = streak
	if streak < t.opts.IdleDecayPolls {
		next.Status = oldStatus
		next.LastTs = oldLastTs
		t.curr[pid] = next
		return oldStatus
	}
	next.Status = StatusIdle
	next.LastTs = stamp(oldStatus, StatusIdle, oldLastTs)
	t.curr[pid] = next
	return StatusIdle
}

// EvaluateAuthoritative records a status supplied by a connector — the
// agent's own state surface (native phase files or installed hooks) — instead
// of the CPU/IO heuristic. Detail is carried into the snapshot for the
// dashboard. title is the optional session/conversation title shown by the
// dashboard as "Title (Name)". The idle streak resets and LastTs is stamped
// on status changes; otherwise previous CPU/IO values are preserved so a
// later fallback to the heuristic path has a baseline.
func (t *Tracker) EvaluateAuthoritative(pid int, label, cwd, pane string, st Status, detail, title string) Status {
	oldStatus := StatusIdle
	oldLastTs := int64(0)
	prev := t.prev[pid]
	if prev != nil {
		oldStatus = prev.Status
		oldLastTs = prev.LastTs
	}

	next := &AgentState{PID: pid, Label: label, Status: st, Pane: pane, CWD: cwd, Detail: detail, Title: title}
	if prev != nil {
		next.CPU = prev.CPU
		next.IO = prev.IO
		next.IdleStreak = prev.IdleStreak
	}
	if st != oldStatus {
		next.IdleStreak = 0
		next.LastTs = time.Now().Unix()
	} else {
		next.LastTs = oldLastTs
	}
	t.curr[pid] = next
	return st
}

// stamp returns now when the status changed, otherwise the previous stamp.
func stamp(old, new Status, oldLastTs int64) int64 {
	if old != new {
		return time.Now().Unix()
	}
	return oldLastTs
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
