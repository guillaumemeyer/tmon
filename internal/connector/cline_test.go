package connector

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

func TestClineEnabledGatesOnSessionStore(t *testing.T) {
	home := withHome(t)
	cfg := config.Defaults()
	if (Cline{}).Enabled(cfg) {
		t.Error("enabled with no ~/.cline/data/sessions")
	}
	writeFile(t, filepath.Join(home, ".cline", "data", "sessions", "sess-1", "x.json"), "{}")
	if !(Cline{}).Enabled(cfg) {
		t.Error("not enabled with ~/.cline/data/sessions present")
	}
}

func TestClineEmitsActiveForNewestSession(t *testing.T) {
	home := withHome(t)
	cfg := config.Defaults()
	// Two sessions; sess-b is written more recently.
	writeFile(t, filepath.Join(home, ".cline", "data", "sessions", "sess-a", "a.json"), "{}")
	b := filepath.Join(home, ".cline", "data", "sessions", "sess-b", "b.json")
	writeFile(t, b, "{}")
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(filepath.Join(home, ".cline", "data", "sessions", "sess-a", "a.json"), old, old); err != nil {
		t.Fatal(err)
	}
	stubPIDs(t, "Cline", []int{31337})

	recs, err := (Cline{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	r := recs[0]
	if r.PID != 31337 || r.Label != "Cline" || r.Status != agent.StatusActive || r.Detail != "session:sess-b" {
		t.Errorf("record = %+v, want PID 31337 Cline active session:sess-b", r)
	}
}

func TestClineSilentWithoutProcess(t *testing.T) {
	home := withHome(t)
	cfg := config.Defaults()
	writeFile(t, filepath.Join(home, ".cline", "data", "sessions", "sess-1", "x.json"), "{}")
	stubPIDs(t, "Cline", nil)

	recs, err := (Cline{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("records = %+v, want none (no cline process)", recs)
	}
}

func TestClineStaleSessionDroppedByCollect(t *testing.T) {
	home := withHome(t)
	cfg := config.Defaults()
	f := filepath.Join(home, ".cline", "data", "sessions", "sess-1", "x.json")
	writeFile(t, f, "{}")
	stubPIDs(t, "Cline", []int{31337})
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(f, old, old); err != nil {
		t.Fatal(err)
	}

	oldReg := Registry
	Registry = []Connector{Cline{}}
	t.Cleanup(func() { Registry = oldReg })
	oldAlive := procAlive
	procAlive = func(pid int) bool { return true }
	t.Cleanup(func() { procAlive = oldAlive })

	got := Collect(cfg, time.Now())
	if len(got) != 0 {
		t.Fatalf("collect = %+v, want none (stale session dropped)", got)
	}
}
