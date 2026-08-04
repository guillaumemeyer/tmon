package connector

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

func TestCodeBuddyEnabledGatesOnSessionStore(t *testing.T) {
	home := withHome(t)
	cfg := config.Defaults()
	if (CodeBuddy{}).Enabled(cfg) {
		t.Error("enabled with no ~/.codebuddy/sessions")
	}
	writeFile(t, filepath.Join(home, ".codebuddy", "sessions", "4242.json"), "{}")
	if !(CodeBuddy{}).Enabled(cfg) {
		t.Error("not enabled with ~/.codebuddy/sessions present")
	}
}

func TestCodeBuddyEmitsRecordPerSessionFile(t *testing.T) {
	home := withHome(t)
	cfg := config.Defaults()
	writeFile(t, filepath.Join(home, ".codebuddy", "sessions", "4242.json"), "{}")
	writeFile(t, filepath.Join(home, ".codebuddy", "sessions", "5150.json"), "{}")
	writeFile(t, filepath.Join(home, ".codebuddy", "sessions", "not-a-pid.json"), "{}")

	recs, err := (CodeBuddy{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("records = %+v, want 2 (non-numeric filename ignored)", recs)
	}
	for _, r := range recs {
		if r.Label != "CodeBuddy" || r.Status != agent.StatusRunning {
			t.Errorf("record = %+v, want CodeBuddy running", r)
		}
		if r.PID != 4242 && r.PID != 5150 {
			t.Errorf("PID = %d, want one of 4242/5150", r.PID)
		}
		if r.Detail != "session:4242" && r.Detail != "session:5150" {
			t.Errorf("Detail = %q, want session:<pid>", r.Detail)
		}
	}
}

func TestCodeBuddyExitedProcessDroppedByCollect(t *testing.T) {
	home := withHome(t)
	cfg := config.Defaults()
	writeFile(t, filepath.Join(home, ".codebuddy", "sessions", "4242.json"), "{}")

	oldReg := Registry
	Registry = []Connector{CodeBuddy{}}
	t.Cleanup(func() { Registry = oldReg })
	oldAlive := procAlive
	procAlive = func(pid int) bool { return false }
	t.Cleanup(func() { procAlive = oldAlive })

	got := Collect(cfg, time.Now())
	if len(got) != 0 {
		t.Fatalf("collect = %+v, want none (pid from filename not alive)", got)
	}
}
