package connector

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// codexRollout writes a rollout file under the fixture home's sessions dir
// (in today's YYYY/MM/DD directory, like Codex does) with a session_meta
// line for cwdFull followed by the given event lines. Returns the path.
func codexRollout(t *testing.T, home, cwdFull, sessionID string, events ...string) string {
	t.Helper()
	now := time.Now()
	dir := filepath.Join(home, "sessions", now.Format("2006"), now.Format("01"), now.Format("02"))
	path := filepath.Join(dir, "rollout-"+now.Format("2006-01-02T15-04-05")+"-"+sessionID+".jsonl")
	meta := fmt.Sprintf(`{"timestamp":"%s","type":"session_meta","payload":{"session_id":"%s","cwd":"%s"}}`,
		now.UTC().Format(time.RFC3339), sessionID, cwdFull)
	writeFile(t, path, strings.Join(append([]string{meta}, events...), "\n")+"\n")
	return path
}

// codexTokenEvent renders a token_count event line with the given usage.
func codexTokenEvent(input, cached, output, reasoning, window int64) string {
	return fmt.Sprintf(
		`{"timestamp":"2026-08-06T23:09:49.724Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d,"reasoning_output_tokens":%d},"model_context_window":%d}}}`,
		input, cached, output, reasoning, window)
}

func TestCodexProbeRolloutIdleWithUsage(t *testing.T) {
	cfg := codexCfg(t)
	stubCodexAgents(t, map[string]int{"code/tmon": 9090})
	home := stubCodexHome(t)
	codexRollout(t, home, "/home/u/code/tmon", "s1",
		`{"timestamp":"2026-08-06T23:09:48.065Z","type":"event_msg","payload":{"type":"task_started"}}`,
		`{"timestamp":"2026-08-06T23:09:49.696Z","type":"event_msg","payload":{"type":"agent_message","phase":"final_answer"}}`,
		codexTokenEvent(13376, 9984, 13, 0, 258400),
		`{"timestamp":"2026-08-06T23:09:49.731Z","type":"event_msg","payload":{"type":"task_complete"}}`,
	)

	recs, err := (Codex{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	r := recs[0]
	if r.PID != 9090 || r.Status != agent.StatusIdle {
		t.Errorf("record = %+v, want PID 9090 idle", r)
	}
	if r.Detail != "turn-complete" {
		t.Errorf("Detail = %q, want turn-complete", r.Detail)
	}
	u := r.Usage
	if want := int64(13376 + 9984 + 13); u.TokensUsed != want {
		t.Errorf("TokensUsed = %d, want %d", u.TokensUsed, want)
	}
	if u.WindowTokens != 258400 {
		t.Errorf("WindowTokens = %d, want 258400", u.WindowTokens)
	}
}

func TestCodexProbeRolloutWorking(t *testing.T) {
	cfg := codexCfg(t)
	stubCodexAgents(t, map[string]int{"code/tmon": 9090})
	home := stubCodexHome(t)
	codexRollout(t, home, "/home/u/code/tmon", "s1",
		`{"timestamp":"2026-08-06T23:09:48.065Z","type":"event_msg","payload":{"type":"task_started"}}`,
		`{"timestamp":"2026-08-06T23:09:48.347Z","type":"response_item","payload":{"type":"message","role":"assistant","phase":"thinking"}}`,
	)

	recs, err := (Codex{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	if recs[0].Status != agent.StatusWorking {
		t.Errorf("Status = %s, want working", recs[0].Status)
	}
	if recs[0].Detail != "phase:thinking" {
		t.Errorf("Detail = %q, want phase:thinking", recs[0].Detail)
	}
}

func TestCodexProbeFreshSessionIsIdle(t *testing.T) {
	cfg := codexCfg(t)
	stubCodexAgents(t, map[string]int{"code/tmon": 9090})
	home := stubCodexHome(t)
	codexRollout(t, home, "/home/u/code/tmon", "s1") // session_meta only

	recs, err := (Codex{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	if recs[0].Status != agent.StatusIdle || recs[0].Detail != "started" {
		t.Errorf("record = %+v, want idle started", recs[0])
	}
	if !recs[0].Usage.Empty() {
		t.Errorf("Usage = %+v, want empty for a fresh session", recs[0].Usage)
	}
}

func TestCodexProbeRolloutMatchesByCWD(t *testing.T) {
	cfg := codexCfg(t)
	stubCodexAgents(t, map[string]int{"code/tmon": 9090})
	home := stubCodexHome(t)
	// An unrelated project's session must not be paired with the process.
	codexRollout(t, home, "/home/u/code/other", "other",
		codexTokenEvent(999, 0, 0, 0, 100000))
	mine := codexRollout(t, home, "/home/u/code/tmon", "mine",
		codexTokenEvent(100, 200, 30, 0, 258400))

	recs, err := (Codex{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want only the matching session", recs)
	}
	if recs[0].Usage.TokensUsed != 100+200+30 {
		t.Errorf("TokensUsed = %d, want the matching session's usage", recs[0].Usage.TokensUsed)
	}
	if got := newestCodexRollout(filepath.Join(home, "sessions"), "code/tmon"); got != mine {
		t.Errorf("newestCodexRollout = %q, want %q", got, mine)
	}
}

func TestCodexProbeNewestRolloutWins(t *testing.T) {
	cfg := codexCfg(t)
	stubCodexAgents(t, map[string]int{"code/tmon": 9090})
	home := stubCodexHome(t)
	old := codexRollout(t, home, "/home/u/code/tmon", "old",
		codexTokenEvent(10, 0, 0, 0, 258400))
	newer := codexRollout(t, home, "/home/u/code/tmon", "newer",
		codexTokenEvent(500, 0, 0, 0, 258400))
	// Make the newer session's file strictly newer on disk.
	base := time.Now().Add(-time.Hour)
	_ = os.Chtimes(old, base, base)
	_ = os.Chtimes(newer, base.Add(time.Minute), base.Add(time.Minute))

	recs, err := (Codex{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Usage.TokensUsed != 500 {
		t.Errorf("records = %+v, want the newer session's usage (500)", recs)
	}
}

func TestCodexProbeHookStatusOverridesRollout(t *testing.T) {
	cfg := codexCfg(t)
	writeFile(t, filepath.Join(cfg.HookStateDir, "codex", "s1.json"),
		`{"status":"blocked","detail":"waiting:permission","cwd":"code/tmon","ts":123}`)
	stubCodexAgents(t, map[string]int{"code/tmon": 9090})
	home := stubCodexHome(t)
	codexRollout(t, home, "/home/u/code/tmon", "s1",
		`{"timestamp":"2026-08-06T23:09:49.731Z","type":"event_msg","payload":{"type":"task_complete"}}`,
		codexTokenEvent(13376, 9984, 13, 0, 258400),
	)

	recs, err := (Codex{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	if recs[0].Status != agent.StatusBlocked || recs[0].Detail != "waiting:permission" {
		t.Errorf("record = %+v, want hook status blocked/waiting:permission to win", recs[0])
	}
	u := recs[0].Usage
	if want := int64(13376 + 9984 + 13); u.TokensUsed != want {
		t.Errorf("TokensUsed = %d, want %d from the rollout", u.TokensUsed, want)
	}
	if u.WindowTokens != 258400 {
		t.Errorf("WindowTokens = %d, want 258400 from the rollout", u.WindowTokens)
	}
}

func TestCodexProbeEmptyWithoutSessions(t *testing.T) {
	cfg := codexCfg(t)
	stubCodexAgents(t, map[string]int{"code/tmon": 9090})
	stubCodexHome(t) // no sessions dir, no hooks

	recs, err := (Codex{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("records = %+v, want none without a rollout or hook state", recs)
	}
}

func TestCodexEnabled(t *testing.T) {
	cfg := codexCfg(t)

	// No Codex state at all: dormant.
	home := stubCodexHome(t)
	if (Codex{}).Enabled(cfg) {
		t.Error("Enabled = true without ~/.codex/sessions or hook state")
	}

	// Rollout sessions dir present: native tier.
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !(Codex{}).Enabled(cfg) {
		t.Error("Enabled = false with ~/.codex/sessions present")
	}

	// Hook state present (older setups): still enabled.
	if err := os.MkdirAll(filepath.Join(cfg.HookStateDir, "codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !(Codex{}).Enabled(cfg) {
		t.Error("Enabled = false with hook state present")
	}
}
