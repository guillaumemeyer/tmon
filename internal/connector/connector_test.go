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
			{PID: 1, Label: "Grok", Status: agent.StatusActive, Detail: "tool:Bash", At: now.Add(-10 * time.Second)},  // fresh (30s gate)
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

func TestCollectDropsDeadPIDs(t *testing.T) {
	old := procAlive
	procAlive = func(pid int) bool { return pid == 1 }
	t.Cleanup(func() { procAlive = old })

	now := time.Now()
	conns := []Connector{fakeConn{
		name: "grok", enabled: true,
		recs: []Record{
			{PID: 1, Label: "Grok", Status: agent.StatusActive, At: now},
			{PID: 2, Label: "Grok", Status: agent.StatusActive, At: now},
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
			{PID: 1, Label: "Grok", Status: agent.StatusActive, Detail: "old", At: now.Add(-5 * time.Second)},
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

func TestCollectToleratesConnectorErrors(t *testing.T) {
	now := time.Now()
	conns := []Connector{
		fakeConn{name: "broken", enabled: true, err: errBoom},
		fakeConn{name: "grok", enabled: true, recs: []Record{
			{PID: 1, Label: "Grok", Status: agent.StatusActive, At: now},
		}},
	}
	got := collect(testConfig(), now, conns)
	if len(got) != 1 || got[0].PID != 1 {
		t.Fatalf("collect after a connector error = %+v, want the healthy connector's record", got)
	}
}

func TestCollectSelectionByName(t *testing.T) {
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
