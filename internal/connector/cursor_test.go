package connector

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

// withHome points $HOME at a fresh temp dir (os.UserHomeDir reads $HOME on
// Linux) and returns the dir.
func withHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// stubPIDs overrides the runningPIDs seam for one label.
func stubPIDs(t *testing.T, label string, pids []int) {
	t.Helper()
	old := runningPIDs
	runningPIDs = func(l string) []int {
		if l != label {
			return nil
		}
		return pids
	}
	t.Cleanup(func() { runningPIDs = old })
}

func TestCursorEnabledGatesOnNativeOrHookState(t *testing.T) {
	home := withHome(t)
	cfg := config.Defaults()
	if (Cursor{}).Enabled(cfg) {
		t.Error("enabled with no ~/.cursor and no hook state")
	}
	writeFile(t, filepath.Join(home, ".cursor", "cli", "s.jsonl"), "{}")
	if !(Cursor{}).Enabled(cfg) {
		t.Error("not enabled with ~/.cursor present")
	}
}

func TestCursorNativeFallbackPairsRunningProcessWithFreshFile(t *testing.T) {
	home := withHome(t)
	cfg := config.Defaults()
	writeFile(t, filepath.Join(home, ".cursor", "cli", "s1.jsonl"), "{}")
	stubPIDs(t, "Cursor", []int{4242})

	recs, err := (Cursor{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	r := recs[0]
	if r.PID != 4242 || r.Label != "Cursor" || r.Status != agent.StatusIdle || r.Detail != "session:active" {
		t.Errorf("record = %+v, want PID 4242 Cursor idle session:active", r)
	}
	if time.Since(r.At) > 5*time.Second {
		t.Errorf("At = %v, want recent file mtime", r.At)
	}
}

func TestCursorNativeSilentWithoutProcess(t *testing.T) {
	home := withHome(t)
	cfg := config.Defaults()
	writeFile(t, filepath.Join(home, ".cursor", "cli", "s1.jsonl"), "{}")
	stubPIDs(t, "Cursor", nil)

	recs, err := (Cursor{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("records = %+v, want none (no cursor process)", recs)
	}
}

func TestCursorHookStateWinsOverNativeFallback(t *testing.T) {
	home := withHome(t)
	cfg := config.Defaults()
	cfg.StateDir = t.TempDir()
	cfg.HookStateDir = filepath.Join(cfg.StateDir, "hooks")
	// Hook state present: authoritative blocked record.
	writeFile(t, filepath.Join(cfg.HookStateDir, "cursor", "s1.json"),
		`{"status":"blocked","detail":"permission:Bash","cwd":"/home/guillaume/code/tmon"}`)
	// Native file also present and fresh.
	writeFile(t, filepath.Join(home, ".cursor", "cli", "s1.jsonl"), "{}")

	old := runningByCWD
	runningByCWD = func(label string) map[string]int {
		if label != "Cursor" {
			return nil
		}
		return map[string]int{"code/tmon": 4242}
	}
	t.Cleanup(func() { runningByCWD = old })

	recs, err := (Cursor{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Status != agent.StatusBlocked || recs[0].Detail != "permission:Bash" {
		t.Fatalf("records = %+v, want the hook record to win", recs)
	}
}
