package connector

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

func TestCopilotEnabledGatesOnNativeOrHookState(t *testing.T) {
	home := withHome(t)
	cfg := config.Defaults()
	if (Copilot{}).Enabled(cfg) {
		t.Error("enabled with no ~/.copilot and no hook state")
	}
	writeFile(t, filepath.Join(home, ".copilot", "sessions", "s1.jsonl"), "{}")
	if !(Copilot{}).Enabled(cfg) {
		t.Error("not enabled with ~/.copilot present")
	}
}

func TestCopilotNativeFallbackPairsRunningProcessWithFreshFile(t *testing.T) {
	home := withHome(t)
	cfg := config.Defaults()
	writeFile(t, filepath.Join(home, ".copilot", "sessions", "s1.jsonl"), "{}")
	stubPIDs(t, "Copilot", []int{777})

	recs, err := (Copilot{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	r := recs[0]
	if r.PID != 777 || r.Label != "Copilot" || r.Status != agent.StatusIdle || r.Detail != "session:active" {
		t.Errorf("record = %+v, want PID 777 Copilot idle session:active", r)
	}
	if time.Since(r.At) > 5*time.Second {
		t.Errorf("At = %v, want recent file mtime", r.At)
	}
}

func TestCopilotNativeSilentWithoutProcess(t *testing.T) {
	home := withHome(t)
	cfg := config.Defaults()
	writeFile(t, filepath.Join(home, ".copilot", "sessions", "s1.jsonl"), "{}")
	stubPIDs(t, "Copilot", nil)

	recs, err := (Copilot{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("records = %+v, want none (no copilot process)", recs)
	}
}
