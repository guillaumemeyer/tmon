package connector

import (
	"path/filepath"
	"testing"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

// codexCfg returns a config whose state and hook dirs point at temp dirs.
func codexCfg(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.StateDir = t.TempDir()
	cfg.HookStateDir = filepath.Join(cfg.StateDir, "hooks")
	return cfg
}

// stubCodexHome points the Codex config dir at a temp dir for the test.
func stubCodexHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	old := codexHome
	codexHome = func() string { return home }
	t.Cleanup(func() { codexHome = old })
	return home
}

// stubCodexAgents overrides the process-table seam for Codex processes.
func stubCodexAgents(t *testing.T, byCWD map[string]int) {
	t.Helper()
	old := runningByCWD
	runningByCWD = func(label string) map[string]int {
		if label != "Codex" {
			return nil
		}
		return byCWD
	}
	t.Cleanup(func() { runningByCWD = old })
}

func TestCodexProbeUsageFromHistory(t *testing.T) {
	cfg := codexCfg(t)
	writeFile(t, filepath.Join(cfg.HookStateDir, "codex", "s1.json"),
		`{"status":"working","detail":"tool:Bash","cwd":"code/tmon","ts":123}`)
	stubCodexAgents(t, map[string]int{"code/tmon": 9090})

	home := stubCodexHome(t)
	dir := filepath.Join(home, "sessions", "code-tmon", "sess-1")
	writeFile(t, filepath.Join(dir, "history.jsonl"),
		`{"type":"turn_start","ts":"2026-08-04T00:00:00Z"}
{"type":"response_item","usage":{"input_tokens":250,"cached_input_tokens":1000,"output_tokens":80,"reasoning_output_tokens":0}}
{"type":"response_item","usage":{"input_tokens":10,"output_tokens":5}}
`)

	recs, err := (Codex{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	if recs[0].PID != 9090 || recs[0].Status != agent.StatusWorking {
		t.Errorf("record = %+v, want PID 9090 working", recs[0])
	}
	u := recs[0].Usage
	want := int64(250 + 1000 + 80 + 10 + 5)
	if u.TokensUsed != want {
		t.Errorf("TokensUsed = %d, want %d", u.TokensUsed, want)
	}
	if u.WindowTokens != 0 {
		t.Errorf("WindowTokens = %d, want 0 (unknown for codex)", u.WindowTokens)
	}
}

func TestCodexUsageEmptyWithoutHistory(t *testing.T) {
	cfg := codexCfg(t)
	writeFile(t, filepath.Join(cfg.HookStateDir, "codex", "s1.json"),
		`{"status":"idle","detail":"started","cwd":"code/tmon","ts":123}`)
	stubCodexAgents(t, map[string]int{"code/tmon": 9090})
	stubCodexHome(t) // no sessions dir

	recs, err := (Codex{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || !recs[0].Usage.Empty() {
		t.Fatalf("records = %+v, want 1 record with empty usage", recs)
	}
}

func TestCodexHistoryMatchIgnoresUnrelatedProjects(t *testing.T) {
	home := stubCodexHome(t)
	other := filepath.Join(home, "sessions", "other-proj", "sess-9")
	writeFile(t, filepath.Join(other, "history.jsonl"), `{"usage":{"input_tokens":999}}
`)

	if got := newestCodexHistory(filepath.Join(home, "sessions"), "code/tmon"); got != "" {
		t.Errorf("matched unrelated project: %q, want empty", got)
	}

	mine := filepath.Join(home, "sessions", "code-tmon", "sess-1", "history.jsonl")
	writeFile(t, mine, `{"usage":{"input_tokens":1}}
`)
	if got := newestCodexHistory(filepath.Join(home, "sessions"), "code/tmon"); got != mine {
		t.Errorf("matched = %q, want %q", got, mine)
	}
}
