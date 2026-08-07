package connector

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

// windsurfTestConfig returns a config whose hook state dir is isolated in a
// temp dir, so the tests are immune to hooks installed on the real machine.
func windsurfTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.HookStateDir = filepath.Join(t.TempDir(), "hooks")
	return cfg
}

// TestWindsurfEnabledGatesOnHookState: Windsurf exposes no readable live
// state, so Enabled is purely the hook state dir.
func TestWindsurfEnabledGatesOnHookState(t *testing.T) {
	cfg := windsurfTestConfig(t)
	if (Windsurf{}).Enabled(cfg) {
		t.Error("enabled with no hook state")
	}
	if err := os.MkdirAll(filepath.Join(cfg.HookStateDir, "windsurf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !(Windsurf{}).Enabled(cfg) {
		t.Error("not enabled with hook state dir present")
	}
}

// TestWindsurfProbePairsHookState: a hook state file pairs with a running
// Windsurf process by CWD (short form) and becomes a record.
func TestWindsurfProbePairsHookState(t *testing.T) {
	cfg := windsurfTestConfig(t)
	writeFile(t, filepath.Join(cfg.HookStateDir, "windsurf", "conv-1.json"),
		`{"status":"working","detail":"tool:run_command","cwd":"/home/guillaume/code/tmon"}`)
	stubRunningByCWD(t, "Windsurf", map[string]int{"code/tmon": 5050})

	recs, err := (Windsurf{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	r := recs[0]
	if r.PID != 5050 || r.Label != "Windsurf" {
		t.Errorf("record = %+v, want PID 5050 label Windsurf", r)
	}
	if r.Status != agent.StatusWorking || r.Detail != "tool:run_command" || r.CWD != "code/tmon" {
		t.Errorf("record = %+v, want working tool:run_command at code/tmon", r)
	}
}

// TestWindsurfProbeSkipsUnpaired: hook state for a session with no running
// Windsurf process in that cwd emits nothing.
func TestWindsurfProbeSkipsUnpaired(t *testing.T) {
	cfg := windsurfTestConfig(t)
	writeFile(t, filepath.Join(cfg.HookStateDir, "windsurf", "conv-1.json"),
		`{"status":"idle","detail":"responded","cwd":"/home/guillaume/code/elsewhere"}`)
	stubRunningByCWD(t, "Windsurf", map[string]int{"code/tmon": 5050})

	recs, err := (Windsurf{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("records = %+v, want none (no windsurf process in that cwd)", recs)
	}
}

// TestWindsurfProbeSkipsMalformed: unreadable state is skipped, not an error.
func TestWindsurfProbeSkipsMalformed(t *testing.T) {
	cfg := windsurfTestConfig(t)
	writeFile(t, filepath.Join(cfg.HookStateDir, "windsurf", "bad.json"), `not json`)
	stubRunningByCWD(t, "Windsurf", map[string]int{"code/tmon": 5050})

	recs, err := (Windsurf{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("records = %+v, want none (malformed state skipped)", recs)
	}
}
