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
	writeClaudeHook(t, cfg, "s1", "working", "tool:Bash", "/home/guillaume/code/tmon")
	stubClaudeAgents(t, map[string]int{"code/tmon": 4242})

	recs, err := (Claude{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	r := recs[0]
	if r.PID != 4242 || r.Label != "Claude" || r.Status != agent.StatusWorking || r.Detail != "tool:Bash" {
		t.Errorf("record = %+v, want PID 4242 Claude working tool:Bash", r)
	}
	if r.CWD != "code/tmon" {
		t.Errorf("CWD = %q, want short form code/tmon", r.CWD)
	}
}

func TestClaudeSkipsSessionWithoutProcess(t *testing.T) {
	cfg := claudeCfg(t)
	writeClaudeHook(t, cfg, "s1", "working", "tool:Bash", "/home/guillaume/code/tmon")
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
	writeClaudeHook(t, cfg, "s1", "working", "tool:Bash", "/a/b")
	if !(Claude{}).Enabled(cfg) {
		t.Error("not enabled with hook state present")
	}
}

func TestClaudeStaleHookFileDroppedByCollect(t *testing.T) {
	cfg := claudeCfg(t)
	writeClaudeHook(t, cfg, "s1", "working", "tool:Bash", "/home/guillaume/code/tmon")
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

// ─── session-title enrichment ────────────────────────────────────────────────

// stubClaudeHome points the Claude config dir at a temp dir for the test.
func stubClaudeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	old := claudeHome
	claudeHome = func() string { return home }
	t.Cleanup(func() { claudeHome = old })
	return home
}

// TestClaudeProbeEnrichesSessionName verifies the hook-paired records pick
// up the session name from Claude's own registry (~/.claude/sessions).
func TestClaudeProbeEnrichesSessionName(t *testing.T) {
	cfg := claudeCfg(t)
	writeClaudeHook(t, cfg, "s1", "working", "tool:Bash", "/home/guillaume/code/tmon")
	stubClaudeAgents(t, map[string]int{"code/tmon": 4242})

	home := stubClaudeHome(t)
	// Claude's own registry, keyed by PID.
	writeFile(t, filepath.Join(home, "sessions", "4242.json"),
		`{"pid":4242,"sessionId":"s1","name":"tmon-0b","status":"working"}`)

	recs, err := (Claude{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	if recs[0].Title != "tmon-0b" {
		t.Errorf("Title = %q, want tmon-0b", recs[0].Title)
	}
	if recs[0].Status != agent.StatusWorking || recs[0].Detail != "tool:Bash" {
		t.Errorf("record = %+v, want working tool:Bash preserved", recs[0])
	}
}

// TestClaudeProbeWithoutRegistryKeepsRecords verifies a missing session
// registry (e.g. an agent version that does not write it) never drops or
// breaks hook records — the title is simply absent.
func TestClaudeProbeWithoutRegistryKeepsRecords(t *testing.T) {
	cfg := claudeCfg(t)
	writeClaudeHook(t, cfg, "s1", "idle", "started", "/home/guillaume/code/tmon")
	stubClaudeAgents(t, map[string]int{"code/tmon": 4242})
	stubClaudeHome(t) // no sessions registry written

	recs, err := (Claude{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Title != "" {
		t.Fatalf("records = %+v, want 1 record without a title", recs)
	}
}

// TestClaudeSessionNamesSkipsUnnamedAndMalformed verifies the registry
// reader tolerates bad files and unnamed sessions.
func TestClaudeSessionNamesSkipsUnnamedAndMalformed(t *testing.T) {
	home := stubClaudeHome(t)
	dir := filepath.Join(home, "sessions")
	writeFile(t, filepath.Join(dir, "1.json"), `{"pid":1,"name":"alpha"}`)
	writeFile(t, filepath.Join(dir, "2.json"), `{"pid":2}`)                  // unnamed
	writeFile(t, filepath.Join(dir, "3.json"), `not json`)                   // malformed
	writeFile(t, filepath.Join(dir, "notapid.json"), `{"pid":9,"name":"x"}`) // non-numeric name

	names := claudeSessionNames()
	if names[1] != "alpha" {
		t.Errorf("names[1] = %q, want alpha", names[1])
	}
	if _, ok := names[2]; ok {
		t.Error("unnamed session should be skipped")
	}
	if _, ok := names[9]; ok {
		t.Error("non-numeric filename should be skipped")
	}
}
