package connector

import (
	"errors"
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

// fakeConn is a minimal Connector for tests.
type fakeConn struct {
	name    string
	enabled bool
	recs    []Record
	err     error
}

func (f fakeConn) Name() string                          { return f.name }
func (f fakeConn) Enabled(config.Config) bool            { return f.enabled }
func (f fakeConn) Probe(config.Config) ([]Record, error) { return f.recs, f.err }

var errBoom = errors.New("boom")

func testConfig() config.Config {
	c := config.Defaults()
	c.Connectors = "auto"
	return c
}

func TestCollectDropsStaleRecords(t *testing.T) {
	now := time.Now()
	conns := []Connector{fakeConn{
		name: "grok", enabled: true,
		recs: []Record{
			{PID: 1, Label: "Grok", Status: agent.StatusWorking, Detail: "tool:Bash", At: now.Add(-10 * time.Second)}, // fresh (30s gate)
			{PID: 2, Label: "Grok", Status: agent.StatusBlocked, Detail: "permission", At: now.Add(-2 * time.Minute)}, // stale
		},
	}}
	got := collect(testConfig(), now, conns)
	if len(got) != 1 {
		t.Fatalf("collect = %d records, want 1 (stale dropped)", len(got))
	}
	if got[0].PID != 1 {
		t.Errorf("kept PID %d, want 1", got[0].PID)
	}
}

// TestCollectKeepsStaleIdleRecord verifies a stale idle record from a live
// process survives the freshness gate (refreshed to now) so its cumulative
// enrichment — title and token usage — is not lost the moment an idle
// agent stops emitting hook events.
func TestCollectKeepsStaleIdleRecord(t *testing.T) {
	old := procAlive
	procAlive = func(pid int) bool { return true }
	t.Cleanup(func() { procAlive = old })

	now := time.Now()
	conns := []Connector{fakeConn{
		name: "claude", enabled: true,
		recs: []Record{
			{PID: 1, Label: "Claude", Status: agent.StatusIdle, Detail: "started", At: now.Add(-10 * time.Minute),
				Title: "tmon-73", Usage: agent.Usage{TokensUsed: 40809, WindowTokens: 1000000}},
		},
	}}
	got := collect(testConfig(), now, conns)
	if len(got) != 1 {
		t.Fatalf("collect = %d records, want 1 (stale idle kept)", len(got))
	}
	if got[0].Title != "tmon-73" || got[0].Usage.TokensUsed != 40809 {
		t.Errorf("kept record lost enrichment: %+v", got[0])
	}
	if got[0].At.Before(now.Add(-time.Second)) {
		t.Errorf("kept record should be refreshed to now, At = %v", got[0].At)
	}
}

func TestCollectDropsDeadPIDs(t *testing.T) {
	old := procAlive
	procAlive = func(pid int) bool { return pid == 1 }
	t.Cleanup(func() { procAlive = old })

	now := time.Now()
	conns := []Connector{fakeConn{
		name: "grok", enabled: true,
		recs: []Record{
			{PID: 1, Label: "Grok", Status: agent.StatusWorking, At: now},
			{PID: 2, Label: "Grok", Status: agent.StatusWorking, At: now},
		},
	}}
	got := collect(testConfig(), now, conns)
	if len(got) != 1 || got[0].PID != 1 {
		t.Fatalf("collect = %+v, want only the live PID 1", got)
	}
}

func TestCollectNewestWinsOnDuplicatePID(t *testing.T) {
	now := time.Now()
	conns := []Connector{
		fakeConn{name: "grok", enabled: true, recs: []Record{
			{PID: 1, Label: "Grok", Status: agent.StatusWorking, Detail: "old", At: now.Add(-5 * time.Second)},
		}},
		fakeConn{name: "claude", enabled: true, recs: []Record{
			{PID: 1, Label: "Claude", Status: agent.StatusBlocked, Detail: "new", At: now},
		}},
	}
	got := collect(testConfig(), now, conns)
	if len(got) != 1 {
		t.Fatalf("collect = %d records, want 1 after dedupe", len(got))
	}
	if got[0].Detail != "new" || got[0].Status != agent.StatusBlocked {
		t.Errorf("duplicate PID kept the older record: %+v", got[0])
	}
}

// TestCollectIdleSamePIDNewestWins covers two idle sessions of one PID (a
// stale companion session beside the live one): both survive the freshness
// gate, and the dedup must keep the record with the newer signal time, not
// whichever session iterated first.
func TestCollectIdleSamePIDNewestWins(t *testing.T) {
	old := procAlive
	procAlive = func(pid int) bool { return true }
	t.Cleanup(func() { procAlive = old })

	now := time.Now()
	conns := []Connector{fakeConn{
		name: "grok", enabled: true,
		recs: []Record{
			{PID: 1, Label: "Grok", Status: agent.StatusIdle, Detail: "started", At: now.Add(-2 * time.Hour)},
			{PID: 1, Label: "Grok", Status: agent.StatusIdle, Detail: "turn-complete", At: now.Add(-5 * time.Minute),
				Usage: agent.Usage{TokensUsed: 127772, WindowTokens: 200000}},
		},
	}}
	got := collect(testConfig(), now, conns)
	if len(got) != 1 {
		t.Fatalf("collect = %d records, want 1 after dedupe", len(got))
	}
	if got[0].Detail != "turn-complete" || got[0].Usage.TokensUsed != 127772 {
		t.Errorf("duplicate PID kept the older session: %+v", got[0])
	}
	if got[0].At.Before(now.Add(-time.Second)) {
		t.Errorf("survivor should be refreshed to now, At = %v", got[0].At)
	}
}

// TestCollectIdleSamePIDTiePrefersEnriched pins the exact-tie tie-break:
// when two records of one PID carry identical signal times, the one with
// dashboard enrichment (usage) wins over the bare record.
func TestCollectIdleSamePIDTiePrefersEnriched(t *testing.T) {
	old := procAlive
	procAlive = func(pid int) bool { return true }
	t.Cleanup(func() { procAlive = old })

	now := time.Now()
	conns := []Connector{fakeConn{
		name: "grok", enabled: true,
		recs: []Record{
			{PID: 1, Label: "Grok", Status: agent.StatusIdle, Detail: "started", At: now.Add(-5 * time.Minute)},
			{PID: 1, Label: "Grok", Status: agent.StatusIdle, Detail: "turn-complete", At: now.Add(-5 * time.Minute),
				Usage: agent.Usage{TokensUsed: 127772, WindowTokens: 200000}},
		},
	}}
	got := collect(testConfig(), now, conns)
	if len(got) != 1 {
		t.Fatalf("collect = %d records, want 1 after dedupe", len(got))
	}
	if got[0].Detail != "turn-complete" {
		t.Errorf("tie kept the bare record: %+v", got[0])
	}
}

func TestCollectToleratesConnectorErrors(t *testing.T) {
	now := time.Now()
	conns := []Connector{
		fakeConn{name: "broken", enabled: true, err: errBoom},
		fakeConn{name: "grok", enabled: true, recs: []Record{
			{PID: 1, Label: "Grok", Status: agent.StatusWorking, At: now},
		}},
	}
	got := collect(testConfig(), now, conns)
	if len(got) != 1 || got[0].PID != 1 {
		t.Fatalf("collect after a connector error = %+v, want the healthy connector's record", got)
	}
}

func TestCollectSelectionByName(t *testing.T) {
	old := procAlive
	procAlive = func(pid int) bool { return true } // selection test: every PID is live
	t.Cleanup(func() { procAlive = old })

	cfg := testConfig()
	cfg.Connectors = "grok, hermes" // comma list, whitespace tolerated
	now := time.Now()
	conns := []Connector{
		fakeConn{name: "grok", enabled: true, recs: []Record{{PID: 1, Label: "Grok", At: now}}},
		fakeConn{name: "hermes", enabled: true, recs: []Record{{PID: 2, Label: "Hermes", At: now}}},
		fakeConn{name: "claude", enabled: true, recs: []Record{{PID: 3, Label: "Claude", At: now}}},
	}
	got := collect(cfg, now, conns)
	if len(got) != 2 {
		t.Fatalf("collect = %d records, want 2 (claude excluded)", len(got))
	}
}

func TestCollectSkipsDisabledConnectors(t *testing.T) {
	old := procAlive
	procAlive = func(pid int) bool { return true } // selection test: every PID is live
	t.Cleanup(func() { procAlive = old })

	now := time.Now()
	conns := []Connector{
		fakeConn{name: "grok", enabled: false, recs: []Record{{PID: 1, Label: "Grok", At: now}}},
		fakeConn{name: "claude", enabled: true, recs: []Record{{PID: 2, Label: "Claude", At: now}}},
	}
	got := collect(testConfig(), now, conns)
	if len(got) != 1 || got[0].PID != 2 {
		t.Fatalf("collect = %+v, want only the enabled connector's record", got)
	}
}

func TestCollectEmptyRegistryIsNoop(t *testing.T) {
	now := time.Now()
	if got := collect(testConfig(), now, nil); len(got) != 0 {
		t.Fatalf("collect with no connectors = %+v, want none", got)
	}
}
