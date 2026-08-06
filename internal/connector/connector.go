// Package connector provides authoritative agent state: signals read from
// the agents' own state surfaces (native phase files, gateway JSON,
// installed hook output) rather than inferred from CPU/IO heuristics.
//
// Each supported agent gets one Connector in this package (grok.go,
// hermes.go, claude.go, ...). Connectors are dormant when the agent is not
// installed: Enabled() gates on the presence of the agent's state paths, so
// uninstalled agents cost nothing. Records carry a timestamp; the poll loop
// drops stale non-idle ones, so a vanished active signal decays back to the
// heuristic path instead of leaving the agent "stuck active". Stale idle
// records from live processes are kept (refreshed) so their cumulative
// enrichment — title, profile, token usage — survives an idle agent that
// has stopped emitting events.
package connector

import (
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

// Record is one authoritative observation of an agent's state.
type Record struct {
	PID     int          // process PID; liveness is checked before use
	Label   string       // must match a detect signature label
	Status  agent.Status // blocked | working | idle
	Detail  string       // "tool:Bash", "phase:reasoning", "permission:Write"
	CWD     string       // optional; empty keeps detect's value
	At      time.Time    // when the signal was observed (freshness check)
	Title   string       // optional session/conversation title, e.g. a Grok generated_title
	Profile string       // optional agent profile (Hermes multi-home), shown as "Hermes - <profile>"
	Usage   agent.Usage  // token usage stats for the dashboard; zero = unknown
}

// Connector is one agent's authoritative state source.
type Connector interface {
	// Name is the agent label, used for TMON_CONNECTORS selection.
	Name() string
	// Enabled reports whether this agent's state paths exist, i.e. the
	// agent is installed and its state is readable.
	Enabled(cfg config.Config) bool
	// Probe reads the agent's state and returns its current records.
	// Per-connector errors are tolerated: Collect drops the failing
	// connector but keeps the rest.
	Probe(cfg config.Config) ([]Record, error)
}

// Registry is the ordered set of connectors, one per supported agent.
// Connectors are added as their agents land (Phases 2-5); an empty registry
// means the poll loop behaves exactly as it did before connectors existed.
var Registry = []Connector{
	Grok{},
	Hermes{},
	Claude{},
	Codex{},
	Cursor{},
	Cline{},
	Copilot{},
	CodeBuddy{},
	Windsurf{},
	OpenClaw{},
	Aider{},
}

// Collect probes the enabled connectors and returns their fresh, alive
// records, deduplicated by PID (newest signal wins). Selection follows
// cfg.Connectors: "auto" enables every connector whose paths exist; a
// comma-separated list enables only the named ones.
func Collect(cfg config.Config, now time.Time) []Record {
	return collect(cfg, now, Registry)
}

func collect(cfg config.Config, now time.Time, conns []Connector) []Record {
	enabled := selectConnectors(cfg, conns)

	byPID := make(map[int]Record)
	for _, c := range enabled {
		recs, err := c.Probe(cfg)
		if err != nil {
			continue // a failing connector never fails the poll
		}
		for _, r := range recs {
			if !procAlive(r.PID) {
				continue // process exited: drop the record
			}
			if now.Sub(r.At) > cfg.ConnectorFreshness {
				// Stale non-idle signals decay to the heuristic path so an
				// agent cannot get "stuck active". A stale idle signal from
				// a live process is kept (refreshed) instead: its
				// cumulative enrichment — session title, profile, token
				// usage — is not time-sensitive and would otherwise vanish
				// the moment an idle agent stops emitting events.
				if r.Status != agent.StatusIdle {
					continue
				}
			}
			cur, seen := byPID[r.PID]
			// Newest observation wins; on an exact tie prefer the record
			// carrying more dashboard signal (usage, title, detail). The
			// staleness refresh below happens after the dedup so two stale
			// idle records of one PID are ordered by their real signal time
			// and not by iteration order (refreshing first would set both
			// to "now" and leave the tie to luck).
			if !seen || r.At.After(cur.At) || (r.At.Equal(cur.At) && informative(r, cur)) {
				byPID[r.PID] = r
			}
		}
	}

	// Refresh surviving stale idle records so their enrichment keeps riding
	// along and a later poll does not re-drop them as stale.
	for pid, r := range byPID {
		if now.Sub(r.At) > cfg.ConnectorFreshness {
			r.At = now
			byPID[pid] = r
		}
	}

	out := make([]Record, 0, len(byPID))
	for _, r := range byPID {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

// informative reports whether record a carries more dashboard enrichment
// than b: token usage first, then a session title, then any detail. It is
// the tie-break when two records of one PID carry identical signal times.
func informative(a, b Record) bool {
	if !a.Usage.Empty() && b.Usage.Empty() {
		return true
	}
	if a.Title != "" && b.Title == "" {
		return true
	}
	return a.Detail != "" && b.Detail == ""
}

// selectConnectors returns the connectors to probe for this poll: those
// named in cfg.Connectors, or every enabled connector under "auto".
func selectConnectors(cfg config.Config, conns []Connector) []Connector {
	want := make(map[string]bool)
	for _, name := range strings.Split(cfg.Connectors, ",") {
		name = strings.TrimSpace(name)
		if name != "" && name != "auto" {
			want[name] = true
		}
	}
	out := make([]Connector, 0, len(conns))
	for _, c := range conns {
		if len(want) > 0 && !want[c.Name()] {
			continue
		}
		if c.Enabled(cfg) {
			out = append(out, c)
		}
	}
	return out
}

// procAlive reports whether pid is a live process. kill(pid, 0) returning
// EPERM means the process exists but belongs to another user — still alive.
// A package var so tests can stub it without spawning processes.
var procAlive = func(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
