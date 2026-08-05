package connector

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

// claudeCfg returns a config whose state and hook dirs point at temp dirs.
func claudeCfg(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.StateDir = t.TempDir()
	cfg.HookStateDir = filepath.Join(cfg.StateDir, "hooks")
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
	writeFile(t, filepath.Join(dir, "1.json"), `{"pid":1,"sessionId":"sess-1","name":"alpha"}`)
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
	if id := claudeLiveSessions()[1].SessionID; id != "sess-1" {
		t.Errorf("live session id = %q, want sess-1", id)
	}
}

// ─── usage enrichment ────────────────────────────────────────────────────────

// stubClaudeAbsCWD makes claudeUsage resolve the fixture's absolute cwd
// (no real process is running for the fake PID).
func stubClaudeAbsCWD(t *testing.T, abs string) {
	t.Helper()
	old := claudeAbsCWD
	claudeAbsCWD = func(pid int, short string) string { return abs }
	t.Cleanup(func() { claudeAbsCWD = old })
}

func TestClaudeProbeUsageFromTranscript(t *testing.T) {
	cfg := claudeCfg(t)
	writeClaudeHook(t, cfg, "s1", "working", "tool:Bash", "/home/guillaume/code/tmon")
	stubClaudeAgents(t, map[string]int{"code/tmon": 4242})
	home := stubClaudeHome(t)
	stubClaudeAbsCWD(t, "/home/guillaume/code/tmon")

	// Claude's registry names the live session; its sessionId picks the
	// transcript file below.
	writeFile(t, filepath.Join(home, "sessions", "4242.json"),
		`{"pid":4242,"sessionId":"s1","name":"tmon-0b","status":"working"}`)

	dir := filepath.Join(home, "projects", "-home-guillaume-code-tmon")
	writeFile(t, filepath.Join(dir, "s1.jsonl"),
		`{"type":"mode","mode":"normal"}
{"type":"assistant","message":{"model":"claude-sonnet-5","role":"assistant","usage":{"input_tokens":2,"cache_creation_input_tokens":12147,"cache_read_input_tokens":28964,"output_tokens":732}},"sessionId":"s1"}
{"type":"user","content":"hi"}
{"type":"assistant","message":{"model":"claude-sonnet-5","role":"assistant","usage":{"input_tokens":3,"cache_creation_input_tokens":0,"cache_read_input_tokens":100,"output_tokens":20}},"sessionId":"s1"}
`)

	recs, err := (Claude{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	u := recs[0].Usage
	// Each assistant usage block is the full context size for that API
	// call, not an incremental delta. Context-window display must take the
	// latest block only — summing every turn inflates past 100% (often 300%+).
	want := int64(3 + 0 + 100 + 20)
	if u.TokensUsed != want {
		t.Errorf("TokensUsed = %d, want %d (latest usage, not sum of all turns)", u.TokensUsed, want)
	}
	if u.WindowTokens != 1000000 {
		t.Errorf("WindowTokens = %d, want 1000000 (claude-sonnet-5)", u.WindowTokens)
	}
}

// TestClaudeUsageIsLatestNotSum locks the context-window semantics: a long
// session re-reports ~the same growing context on every turn. Summing those
// would report multi-window totals (the >300% bug); the latest line is the
// true fill.
func TestClaudeUsageIsLatestNotSum(t *testing.T) {
	cfg := claudeCfg(t)
	writeClaudeHook(t, cfg, "s1", "working", "tool:Bash", "/home/guillaume/code/tmon")
	stubClaudeAgents(t, map[string]int{"code/tmon": 4242})
	home := stubClaudeHome(t)
	stubClaudeAbsCWD(t, "/home/guillaume/code/tmon")

	dir := filepath.Join(home, "projects", "-home-guillaume-code-tmon")
	// Three turns, each re-reporting the full (growing) context.
	writeFile(t, filepath.Join(dir, "s1.jsonl"),
		`{"type":"assistant","message":{"model":"claude-sonnet-5","role":"assistant","usage":{"input_tokens":1,"cache_read_input_tokens":40000,"output_tokens":10}},"sessionId":"s1"}
{"type":"assistant","message":{"model":"claude-sonnet-5","role":"assistant","usage":{"input_tokens":1,"cache_read_input_tokens":80000,"output_tokens":20}},"sessionId":"s1"}
{"type":"assistant","message":{"model":"claude-sonnet-5","role":"assistant","usage":{"input_tokens":1,"cache_read_input_tokens":90000,"output_tokens":30}},"sessionId":"s1"}
`)
	recs, err := (Claude{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	u := recs[0].Usage
	wantLatest := int64(1 + 90000 + 30)
	sumAll := int64(1 + 40000 + 10 + 1 + 80000 + 20 + 1 + 90000 + 30)
	if u.TokensUsed != wantLatest {
		t.Errorf("TokensUsed = %d, want latest %d (sum would be %d)", u.TokensUsed, wantLatest, sumAll)
	}
	if u.TokensUsed == sumAll {
		t.Error("TokensUsed still sums every turn — context % would exceed 100%")
	}
	if u.WindowTokens != 1000000 {
		t.Errorf("WindowTokens = %d, want 1000000", u.WindowTokens)
	}
}

func TestClaudeProbeUsageSkipsNonUsageLines(t *testing.T) {
	// User messages, mode events and a trailing partial line must not
	// inflate the count.
	cfg := claudeCfg(t)
	writeClaudeHook(t, cfg, "s1", "working", "tool:Bash", "/home/guillaume/code/tmon")
	stubClaudeAgents(t, map[string]int{"code/tmon": 4242})
	home := stubClaudeHome(t)
	stubClaudeAbsCWD(t, "/home/guillaume/code/tmon")

	dir := filepath.Join(home, "projects", "-home-guillaume-code-tmon")
	writeFile(t, filepath.Join(dir, "s1.jsonl"),
		`{"type":"mode","mode":"normal"}
{"type":"user","content":"hi"}
{"type":"assistant","message":{"model":"claude-haiku","role":"assistant","usage":{"input_tokens":10,"output_tokens":5}},"sessionId":"s1"}
`)
	recs, err := (Claude{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	u := recs[0].Usage
	if u.TokensUsed != 15 {
		t.Errorf("TokensUsed = %d, want 15", u.TokensUsed)
	}
	if u.WindowTokens != 200000 {
		t.Errorf("WindowTokens = %d, want 200000 (claude-haiku)", u.WindowTokens)
	}
}

// TestClaudeUsagePrefersLiveSessionTranscript verifies the connector reads
// the live session's own transcript (by registry sessionId) even when an
// unrelated, newer transcript exists in the same project dir — otherwise a
// stale session's tokens would be attributed to the running agent.
func TestClaudeUsagePrefersLiveSessionTranscript(t *testing.T) {
	cfg := claudeCfg(t)
	writeClaudeHook(t, cfg, "s1", "working", "tool:Bash", "/home/guillaume/code/tmon")
	stubClaudeAgents(t, map[string]int{"code/tmon": 4242})
	home := stubClaudeHome(t)
	stubClaudeAbsCWD(t, "/home/guillaume/code/tmon")

	writeFile(t, filepath.Join(home, "sessions", "4242.json"),
		`{"pid":4242,"sessionId":"s1","name":"live","status":"working"}`)
	dir := filepath.Join(home, "projects", "-home-guillaume-code-tmon")
	writeFile(t, filepath.Join(dir, "s1.jsonl"),
		`{"type":"assistant","message":{"model":"claude-sonnet-5","role":"assistant","usage":{"input_tokens":40,"output_tokens":2}},"sessionId":"s1"}`)
	writeFile(t, filepath.Join(dir, "znewer.jsonl"),
		`{"type":"assistant","message":{"model":"claude-sonnet-5","role":"assistant","usage":{"input_tokens":999,"output_tokens":999}},"sessionId":"zother"}`)

	recs, err := (Claude{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	u := recs[0].Usage
	if u.TokensUsed != 42 {
		t.Errorf("TokensUsed = %d, want 42 (live session s1, not the newer znewer)", u.TokensUsed)
	}
}

// TestClaudeParseUsageFormats locks the two accepted transcript formats:
// the current one (usage nested under message) and the legacy top-level
// form, plus the no-usage and partial-line cases.
func TestClaudeParseUsageFormats(t *testing.T) {
	cases := []struct {
		name string
		line string
		want int64
	}{
		{"usage nested under message", `{"type":"assistant","message":{"model":"claude-sonnet-5","role":"assistant","usage":{"input_tokens":2,"cache_creation_input_tokens":10,"cache_read_input_tokens":20,"output_tokens":3}}}`, 35},
		{"legacy top-level usage", `{"message":{"model":"claude-sonnet-5"},"usage":{"input_tokens":5,"output_tokens":4}}`, 9},
		{"user message has no usage", `{"type":"user","content":"hi"}`, 0},
		{"partial line ignored", `{"type":"assistant","message":`, 0},
	}
	for _, c := range cases {
		if got := claudeParseUsage([]byte(c.line)); got != c.want {
			t.Errorf("%s: claudeParseUsage = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestClaudeUsageEmptyWithoutTranscript(t *testing.T) {
	cfg := claudeCfg(t)
	writeClaudeHook(t, cfg, "s1", "working", "tool:Bash", "/home/guillaume/code/tmon")
	stubClaudeAgents(t, map[string]int{"code/tmon": 4242})
	stubClaudeHome(t)
	stubClaudeAbsCWD(t, "/home/guillaume/code/tmon")

	recs, err := (Claude{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	if !recs[0].Usage.Empty() {
		t.Errorf("Usage = %+v, want empty (no transcript)", recs[0].Usage)
	}
}

func TestClaudeUnknownModelHasTokensOnly(t *testing.T) {
	cfg := claudeCfg(t)
	writeClaudeHook(t, cfg, "s1", "working", "tool:Bash", "/home/guillaume/code/tmon")
	stubClaudeAgents(t, map[string]int{"code/tmon": 4242})
	home := stubClaudeHome(t)
	stubClaudeAbsCWD(t, "/home/guillaume/code/tmon")

	dir := filepath.Join(home, "projects", "-home-guillaume-code-tmon")
	writeFile(t, filepath.Join(dir, "s1.jsonl"),
		`{"type":"assistant","message":{"model":"claude-mystery-9","role":"assistant","usage":{"input_tokens":7,"output_tokens":1}},"sessionId":"s1"}
`)
	recs, err := (Claude{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	u := recs[0].Usage
	if u.TokensUsed != 8 || u.WindowTokens != 0 {
		t.Errorf("Usage = %+v, want tokens 8 and unknown window", u)
	}
}

func TestClaudeProjectDirByDecode(t *testing.T) {
	home := stubClaudeHome(t)
	projects := filepath.Join(home, "projects")
	os.MkdirAll(filepath.Join(projects, "-home-other-dir"), 0o755)

	dir := claudeProjectDirByDecode(projects, "/home/other/dir")
	if dir != filepath.Join(projects, "-home-other-dir") {
		t.Errorf("decode scan = %q, want %q", dir, filepath.Join(projects, "-home-other-dir"))
	}
	if got := claudeProjectDirByDecode(projects, "/no/such/path"); got != "" {
		t.Errorf("decode scan for missing path = %q, want empty", got)
	}
}
