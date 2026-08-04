package connector

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

// claudeCfg returns a config whose HookStateDir points at a temp dir.
func claudeCfg(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.HookStateDir = filepath.Join(t.TempDir(), "state", "hooks")
	return cfg
}

func writeClaudeHook(t *testing.T, cfg config.Config, sessionID, status, detail, cwd string) {
	t.Helper()
	writeFile(t, filepath.Join(cfg.HookStateDir, "claude", sessionID+".json"),
		`{"status":"`+status+`","detail":"`+detail+`","cwd":"`+cwd+`","ts":123}`)
}

// stubClaudeAgents overrides the process-table seam.
func stubClaudeAgents(t *testing.T, byCWD map[string]int) {
	t.Helper()
	old := runningByCWD
	runningByCWD = func(label string) map[string]int {
		if label != "Claude" {
			return nil
		}
		return byCWD
	}
	t.Cleanup(func() { runningByCWD = old })
}

func TestClaudePairsSessionToProcessByCWD(t *testing.T) {
	cfg := claudeCfg(t)
	writeClaudeHook(t, cfg, "s1", "active", "tool:Bash", "/home/guillaume/code/tmon")
	stubClaudeAgents(t, map[string]int{"code/tmon": 4242})

	recs, err := (Claude{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	r := recs[0]
	if r.PID != 4242 || r.Label != "Claude" || r.Status != agent.StatusActive || r.Detail != "tool:Bash" {
		t.Errorf("record = %+v, want PID 4242 Claude active tool:Bash", r)
	}
	if r.CWD != "code/tmon" {
		t.Errorf("CWD = %q, want short form code/tmon", r.CWD)
	}
}

func TestClaudeSkipsSessionWithoutProcess(t *testing.T) {
	cfg := claudeCfg(t)
	writeClaudeHook(t, cfg, "s1", "active", "tool:Bash", "/home/guillaume/code/tmon")
	stubClaudeAgents(t, map[string]int{}) // claude not running

	recs, err := (Claude{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("records = %+v, want none (no claude process)", recs)
	}
}

func TestClaudeIgnoresMalformedFiles(t *testing.T) {
	cfg := claudeCfg(t)
	writeClaudeHook(t, cfg, "good", "blocked", "permission:Bash", "/home/guillaume/code/tmon")
	writeFile(t, filepath.Join(cfg.HookStateDir, "claude", "garbage.json"), "not json")
	writeFile(t, filepath.Join(cfg.HookStateDir, "claude", "bad-status.json"),
		`{"status":"warp","detail":"x","cwd":"/x"}`)
	stubClaudeAgents(t, map[string]int{"code/tmon": 4242, "x": 4243})

	recs, err := (Claude{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Status != agent.StatusBlocked {
		t.Fatalf("records = %+v, want only the valid blocked session", recs)
	}
}

func TestClaudeEmptyHookDir(t *testing.T) {
	cfg := claudeCfg(t) // no hook files written
	stubClaudeAgents(t, map[string]int{"code/tmon": 4242})
	recs, err := (Claude{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("records = %+v, want none", recs)
	}
}

func TestClaudeEnabledGatesOnHookDir(t *testing.T) {
	cfg := claudeCfg(t)
	if (Claude{}).Enabled(cfg) {
		t.Error("enabled before any hook state exists")
	}
	writeClaudeHook(t, cfg, "s1", "active", "tool:Bash", "/a/b")
	if !(Claude{}).Enabled(cfg) {
		t.Error("not enabled with hook state present")
	}
}

func TestClaudeStaleHookFileDroppedByCollect(t *testing.T) {
	cfg := claudeCfg(t)
	writeClaudeHook(t, cfg, "s1", "active", "tool:Bash", "/home/guillaume/code/tmon")
	stubClaudeAgents(t, map[string]int{"code/tmon": 4242})

	// Age the hook file past the freshness gate.
	p := filepath.Join(cfg.HookStateDir, "claude", "s1.json")
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}

	oldReg := Registry
	Registry = []Connector{Claude{}}
	t.Cleanup(func() { Registry = oldReg })
	oldAlive := procAlive
	procAlive = func(pid int) bool { return true }
	t.Cleanup(func() { procAlive = oldAlive })

	got := Collect(cfg, time.Now())
	if len(got) != 0 {
		t.Fatalf("collect = %+v, want none (stale hook state dropped)", got)
	}
}
