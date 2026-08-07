package connector

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

const grokSessionID = "abc-123"

const grokCWD = "/home/guillaume/code/tmon"

// grokFixture builds a fake ~/.grok under a temp dir with one active
// session, points the grokHome seam at it, and restores on cleanup.
func grokFixture(t *testing.T, events, openedAt string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, ".grok")
	writeFile(t, filepath.Join(home, "active_sessions.json"),
		fmt.Sprintf(`[{"session_id":%q,"pid":4242,"cwd":%q,"opened_at":%q}]`, grokSessionID, grokCWD, openedAt))
	if events != "" {
		writeFile(t, filepath.Join(home, "sessions", url.PathEscape(grokCWD), grokSessionID, "events.jsonl"), events)
	}
	old := grokHome
	grokHome = func() string { return home }
	t.Cleanup(func() { grokHome = old })
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// grokTestConfig returns a config whose hook state dir is isolated in a
// temp dir, so connector tests are immune to hooks installed on the real
// machine (~/.local/state/tmon/hooks/grok).
func grokTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.HookStateDir = filepath.Join(t.TempDir(), "hooks")
	return cfg
}

// stubRunningByCWD overrides the runningByCWD seam for one label, so hook
// state files pair with a deterministic process table.
func stubRunningByCWD(t *testing.T, label string, by map[string]int) {
	t.Helper()
	old := runningByCWD
	runningByCWD = func(l string) map[string]int {
		if l != label {
			return nil
		}
		return by
	}
	t.Cleanup(func() { runningByCWD = old })
}

// nowTS returns a strictly increasing RFC3339Nano timestamp. The tick
// offset guarantees that consecutive calls order correctly even on clocks
// with coarse granularity (e.g. macOS runners), which the phase-mapping
// logic relies on (tool_completed must follow tool_started).
var nowTick int64

func nowTS() string {
	nowTick++
	return time.Now().UTC().Add(time.Duration(nowTick) * time.Microsecond).Format(time.RFC3339Nano)
}

func TestGrokPhaseMapping(t *testing.T) {
	cases := []struct {
		name   string
		events string
		want   agent.Status
		detail string
	}{
		{"permission prompt", `{"ts":"` + nowTS() + `","type":"phase_changed","phase":"permission_prompt"}` + "\n",
			agent.StatusBlocked, "waiting:approval"},
		{"permission names the tool", `{"ts":"` + nowTS() + `","type":"phase_changed","phase":"permission_prompt"}` + "\n" +
			`{"ts":"` + nowTS() + `","type":"permission_requested","tool_name":"write"}` + "\n",
			agent.StatusBlocked, "permission:write"},
		{"reasoning", `{"ts":"` + nowTS() + `","type":"phase_changed","phase":"streaming_reasoning"}` + "\n",
			agent.StatusWorking, "phase:reasoning"},
		{"responding", `{"ts":"` + nowTS() + `","type":"phase_changed","phase":"streaming_text"}` + "\n",
			agent.StatusWorking, "phase:responding"},
		{"tool running", `{"ts":"` + nowTS() + `","type":"phase_changed","phase":"tool_execution"}` + "\n" +
			`{"ts":"` + nowTS() + `","type":"tool_started","tool_name":"read_file"}` + "\n",
			agent.StatusWorking, "tool:read_file"},
		{"completed tool", `{"ts":"` + nowTS() + `","type":"phase_changed","phase":"tool_execution"}` + "\n" +
			`{"ts":"` + nowTS() + `","type":"tool_started","tool_name":"read_file"}` + "\n" +
			`{"ts":"` + nowTS() + `","type":"tool_completed","tool_name":"read_file"}` + "\n",
			agent.StatusWorking, "tool:running"},
		{"waiting for model", `{"ts":"` + nowTS() + `","type":"phase_changed","phase":"waiting_for_model"}` + "\n",
			agent.StatusWorking, "phase:waiting-model"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			grokFixture(t, c.events, "2026-08-02T09:00:00Z")
			recs, err := (Grok{}).Probe(grokTestConfig(t))
			if err != nil {
				t.Fatal(err)
			}
			if len(recs) != 1 {
				t.Fatalf("records = %+v, want 1", recs)
			}
			r := recs[0]
			if r.Status != c.want {
				t.Errorf("status = %q, want %q", r.Status, c.want)
			}
			if !strings.Contains(r.Detail, c.detail) {
				t.Errorf("Detail = %q, want it to contain %q", r.Detail, c.detail)
			}
			if r.PID != 4242 || r.Label != "Grok" {
				t.Errorf("record = %+v, want PID 4242 label Grok", r)
			}
			if r.CWD != "code/tmon" {
				t.Errorf("CWD = %q, want short form code/tmon", r.CWD)
			}
		})
	}
}

func TestGrokSessionWithoutEventsIsIdle(t *testing.T) {
	grokFixture(t, "", "2026-08-02T09:00:00Z")
	recs, err := (Grok{}).Probe(grokTestConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	if recs[0].Status != agent.StatusIdle || recs[0].Detail != "started" {
		t.Errorf("record = %+v, want idle started", recs[0])
	}
}

func TestGrokSignalsEnrichment(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, ".grok")
	writeFile(t, filepath.Join(home, "active_sessions.json"),
		fmt.Sprintf(`[{"session_id":%q,"pid":4242,"cwd":%q,"opened_at":"2026-08-02T09:00:00Z"}]`, grokSessionID, grokCWD))
	writeFile(t, filepath.Join(home, "sessions", url.PathEscape(grokCWD), grokSessionID, "events.jsonl"),
		`{"ts":"`+nowTS()+`","type":"phase_changed","phase":"streaming_reasoning"}`+"\n")
	writeFile(t, filepath.Join(home, "sessions", url.PathEscape(grokCWD), grokSessionID, "signals.json"),
		`{"primaryModelId":"grok-4.5","contextWindowUsage":26,"contextTokensUsed":52367,"contextWindowTokens":200000}`)
	old := grokHome
	grokHome = func() string { return home }
	t.Cleanup(func() { grokHome = old })

	recs, err := (Grok{}).Probe(grokTestConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	d := recs[0].Detail
	if !strings.Contains(d, "model:grok-4.5") || !strings.Contains(d, "ctx:26%") {
		t.Errorf("Detail = %q, want model + ctx enrichment", d)
	}
	u := recs[0].Usage
	if u.TokensUsed != 52367 || u.WindowTokens != 200000 {
		t.Errorf("Usage = %+v, want tokens 52367 window 200000", u)
	}
}

func TestGrokSessionTitle(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, ".grok")
	writeFile(t, filepath.Join(home, "active_sessions.json"),
		fmt.Sprintf(`[{"session_id":%q,"pid":4242,"cwd":%q,"opened_at":"2026-08-02T09:00:00Z"}]`, grokSessionID, grokCWD))
	writeFile(t, filepath.Join(home, "sessions", url.PathEscape(grokCWD), grokSessionID, "summary.json"),
		`{"generated_title":"Extract Agent Session Titles","session_summary":"same text"}`)
	old := grokHome
	grokHome = func() string { return home }
	t.Cleanup(func() { grokHome = old })

	recs, err := (Grok{}).Probe(grokTestConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	if recs[0].Title != "Extract Agent Session Titles" {
		t.Errorf("Title = %q, want generated_title", recs[0].Title)
	}
}

func TestGrokSessionTitleFallsBackToSummary(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, ".grok")
	writeFile(t, filepath.Join(home, "active_sessions.json"),
		fmt.Sprintf(`[{"session_id":%q,"pid":4242,"cwd":%q,"opened_at":"2026-08-02T09:00:00Z"}]`, grokSessionID, grokCWD))
	// summary.json without generated_title: session_summary is used.
	writeFile(t, filepath.Join(home, "sessions", url.PathEscape(grokCWD), grokSessionID, "summary.json"),
		`{"session_summary":"fallback title"}`)
	old := grokHome
	grokHome = func() string { return home }
	t.Cleanup(func() { grokHome = old })

	recs, err := (Grok{}).Probe(grokTestConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Title != "fallback title" {
		t.Errorf("records = %+v, want title from session_summary", recs)
	}
}

func TestGrokSessionWithoutSummaryHasNoTitle(t *testing.T) {
	grokFixture(t, "", "2026-08-02T09:00:00Z")
	recs, err := (Grok{}).Probe(grokTestConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Title != "" {
		t.Errorf("records = %+v, want no title without summary.json", recs)
	}
}

func TestGrokMultipleSessions(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, ".grok")
	writeFile(t, filepath.Join(home, "active_sessions.json"),
		`[{"session_id":"s1","pid":1,"cwd":"/a/b","opened_at":"2026-08-02T09:00:00Z"},`+
			`{"session_id":"s2","pid":2,"cwd":"/c/d","opened_at":"2026-08-02T09:00:00Z"}]`)
	writeFile(t, filepath.Join(home, "sessions", url.PathEscape("/a/b"), "s1", "events.jsonl"),
		`{"ts":"`+nowTS()+`","type":"phase_changed","phase":"streaming_reasoning"}`+"\n")
	old := grokHome
	grokHome = func() string { return home }
	t.Cleanup(func() { grokHome = old })

	recs, err := (Grok{}).Probe(grokTestConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("records = %+v, want 2 sessions", recs)
	}
	if recs[0].Status != agent.StatusWorking || recs[1].Status != agent.StatusIdle {
		t.Errorf("statuses = %q,%q want working,idle", recs[0].Status, recs[1].Status)
	}
}

// ─── freshness + registry ────────────────────────────────────────────────────

// stubGrokLive makes Collect see one Grok record: registry wired, PID alive.
func stubGrokLive(t *testing.T) {
	t.Helper()
	oldReg := Registry
	Registry = []Connector{Grok{}}
	t.Cleanup(func() { Registry = oldReg })
	oldAlive := procAlive
	procAlive = func(pid int) bool { return true }
	t.Cleanup(func() { procAlive = oldAlive })
}

func TestGrokFreshPhaseSurvivesCollect(t *testing.T) {
	grokFixture(t, `{"ts":"`+nowTS()+`","type":"phase_changed","phase":"streaming_reasoning"}`+"\n", "2026-08-02T09:00:00Z")
	stubGrokLive(t)
	got := Collect(grokTestConfig(t), time.Now())
	if len(got) != 1 || got[0].Status != agent.StatusWorking {
		t.Fatalf("collect = %+v, want one active record", got)
	}
}

func TestGrokStalePhaseDroppedByCollect(t *testing.T) {
	stale := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano)
	grokFixture(t, `{"ts":"`+stale+`","type":"phase_changed","phase":"streaming_reasoning"}`+"\n", "2026-08-02T09:00:00Z")
	stubGrokLive(t)
	got := Collect(grokTestConfig(t), time.Now())
	if len(got) != 0 {
		t.Fatalf("collect = %+v, want none (stale phase dropped)", got)
	}
}

// TestGrokCompletedTurnKeptAsIdleWithUsage is the regression for idle grok
// sessions losing their context usage: grok ends a turn with turn_ended
// while the last phase_changed is still a streaming phase. The record must
// read as idle (so the freshness gate keeps it, refreshed) and keep the
// signals.json enrichment — model + context usage — on the dashboard.
func TestGrokCompletedTurnKeptAsIdleWithUsage(t *testing.T) {
	stale := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano)
	root := t.TempDir()
	home := filepath.Join(root, ".grok")
	writeFile(t, filepath.Join(home, "active_sessions.json"),
		fmt.Sprintf(`[{"session_id":%q,"pid":4242,"cwd":%q,"opened_at":"2026-08-02T09:00:00Z"}]`, grokSessionID, grokCWD))
	dir := filepath.Join(home, "sessions", url.PathEscape(grokCWD), grokSessionID)
	writeFile(t, filepath.Join(dir, "events.jsonl"),
		`{"ts":"`+stale+`","type":"phase_changed","phase":"streaming_text"}`+"\n"+
			`{"ts":"`+stale+`","type":"turn_ended","outcome":"completed"}`+"\n")
	writeFile(t, filepath.Join(dir, "signals.json"),
		`{"primaryModelId":"grok-4.5","contextWindowUsage":10,"contextTokensUsed":54446,"contextWindowTokens":500000}`)
	old := grokHome
	grokHome = func() string { return home }
	t.Cleanup(func() { grokHome = old })
	stubGrokLive(t)

	got := Collect(grokTestConfig(t), time.Now())
	if len(got) != 1 {
		t.Fatalf("collect = %+v, want 1 (completed turn kept as idle)", got)
	}
	r := got[0]
	if r.Status != agent.StatusIdle {
		t.Errorf("status = %q, want idle after turn_ended", r.Status)
	}
	if !strings.Contains(r.Detail, "turn-complete") {
		t.Errorf("Detail = %q, want turn-complete", r.Detail)
	}
	if !strings.Contains(r.Detail, "model:grok-4.5") || !strings.Contains(r.Detail, "ctx:10%") {
		t.Errorf("Detail = %q, want model + ctx enrichment", r.Detail)
	}
	if u := r.Usage; u.TokensUsed != 54446 || u.WindowTokens != 500000 {
		t.Errorf("Usage = %+v, want tokens 54446 window 500000", u)
	}
	if r.At.Before(time.Now().Add(-time.Second)) {
		t.Errorf("At = %v, want refreshed to now (stale idle kept)", r.At)
	}
}

// TestGrokMidTurnStaleStillDropped guards the other side of the gate: a
// session that stalled mid-turn (streaming phase, no turn_ended) must still
// decay to the heuristic path — it is not provably finished.
func TestGrokMidTurnStaleStillDropped(t *testing.T) {
	stale := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano)
	grokFixture(t, `{"ts":"`+stale+`","type":"phase_changed","phase":"streaming_text"}`+"\n", "2026-08-02T09:00:00Z")
	stubGrokLive(t)
	got := Collect(grokTestConfig(t), time.Now())
	if len(got) != 0 {
		t.Fatalf("collect = %+v, want none (stalled mid-turn dropped)", got)
	}
}

func TestGrokEnabledGatesOnStatePath(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, ".grok")
	old := grokHome
	grokHome = func() string { return home }
	t.Cleanup(func() { grokHome = old })

	// Empty fixture home: the state surface is absent, so not enabled.
	if (Grok{}).Enabled(grokTestConfig(t)) {
		t.Error("enabled before active_sessions.json exists")
	}
	writeFile(t, filepath.Join(home, "active_sessions.json"), "[]")
	if !(Grok{}).Enabled(grokTestConfig(t)) {
		t.Error("not enabled with active_sessions.json present")
	}
}

func TestGrokSessionDirFallback(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, ".grok")

	// Session stored under a directory name that differs from PathEscape:
	// the glob fallback must still find it.
	weird := filepath.Join(home, "sessions", "some-other-name", grokSessionID)
	writeFile(t, filepath.Join(weird, "marker"), "x")
	if got := sessionDir(home, grokCWD, grokSessionID); got != weird {
		t.Errorf("sessionDir = %q, want glob fallback %q", got, weird)
	}

	// When the encoded path exists too, it wins (primary layout).
	direct := filepath.Join(home, "sessions", url.PathEscape(grokCWD), grokSessionID)
	writeFile(t, filepath.Join(direct, "marker"), "x")
	if got := sessionDir(home, grokCWD, grokSessionID); got != direct {
		t.Errorf("sessionDir = %q, want direct path %q", got, direct)
	}
}

// ─── hook state (dir-kind install) ──────────────────────────────────────────

// TestGrokEnabledOnHookDir guards the Enabled surface added for the hooks
// integration: a hook state dir alone (no active_sessions.json yet) must
// enable the connector — that is the background-session case.
func TestGrokEnabledOnHookDir(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, ".grok")
	old := grokHome
	grokHome = func() string { return home }
	t.Cleanup(func() { grokHome = old })

	cfg := grokTestConfig(t)
	if (Grok{}).Enabled(cfg) {
		t.Error("enabled with no state surface at all")
	}
	if err := os.MkdirAll(filepath.Join(cfg.HookStateDir, "grok"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !(Grok{}).Enabled(cfg) {
		t.Error("not enabled with hook state dir present")
	}
}

// TestGrokHookMerge: a native session (active_sessions.json + phase log +
// signals/summary enrichment) and installed hook state for the same PID.
// The hook record's status/detail win (it sees permission waits and running
// tools the phase log misses); the native record keeps supplying usage,
// title, and the " · model:… · ctx:…%" detail suffix.
func TestGrokHookMerge(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, ".grok")
	writeFile(t, filepath.Join(home, "active_sessions.json"),
		fmt.Sprintf(`[{"session_id":%q,"pid":4242,"cwd":%q,"opened_at":"2026-08-02T09:00:00Z"}]`, grokSessionID, grokCWD))
	dir := filepath.Join(home, "sessions", url.PathEscape(grokCWD), grokSessionID)
	writeFile(t, filepath.Join(dir, "events.jsonl"),
		`{"ts":"`+nowTS()+`","type":"phase_changed","phase":"streaming_reasoning"}`+"\n")
	writeFile(t, filepath.Join(dir, "signals.json"),
		`{"primaryModelId":"grok-4.5","contextWindowUsage":26,"contextTokensUsed":52367,"contextWindowTokens":200000}`)
	writeFile(t, filepath.Join(dir, "summary.json"),
		`{"generated_title":"Hook Merge Session","session_summary":"same"}`)
	old := grokHome
	grokHome = func() string { return home }
	t.Cleanup(func() { grokHome = old })

	// Hook state for the same session: blocked on a permission prompt.
	cfg := grokTestConfig(t)
	writeFile(t, filepath.Join(cfg.HookStateDir, "grok", grokSessionID+".json"),
		fmt.Sprintf(`{"status":"blocked","detail":"permission:write","cwd":%q}`, grokCWD))
	stubRunningByCWD(t, "Grok", map[string]int{"code/tmon": 4242})

	recs, err := (Grok{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	r := recs[0]
	if r.Status != agent.StatusBlocked {
		t.Errorf("status = %q, want blocked (hook wins)", r.Status)
	}
	if !strings.Contains(r.Detail, "permission:write") {
		t.Errorf("Detail = %q, want hook permission:write", r.Detail)
	}
	if !strings.Contains(r.Detail, "model:grok-4.5") || !strings.Contains(r.Detail, "ctx:26%") {
		t.Errorf("Detail = %q, want native model+ctx suffix carried over", r.Detail)
	}
	if u := r.Usage; u.TokensUsed != 52367 || u.WindowTokens != 200000 {
		t.Errorf("Usage = %+v, want native usage carried over", u)
	}
	if r.Title != "Hook Merge Session" {
		t.Errorf("Title = %q, want native title carried over", r.Title)
	}
	if r.CWD != "code/tmon" {
		t.Errorf("CWD = %q, want code/tmon", r.CWD)
	}
}

// TestGrokHookOnlyBackgroundSession: the exact case the hooks exist for — a
// background grok session absent from active_sessions.json ("[]") whose
// state arrives only through hook events. It must surface, paired to the
// running process by CWD.
func TestGrokHookOnlyBackgroundSession(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, ".grok")
	writeFile(t, filepath.Join(home, "active_sessions.json"), "[]")
	old := grokHome
	grokHome = func() string { return home }
	t.Cleanup(func() { grokHome = old })

	cfg := grokTestConfig(t)
	writeFile(t, filepath.Join(cfg.HookStateDir, "grok", "bg-1.json"),
		`{"status":"working","detail":"tool:read_file","cwd":"/home/guillaume/code/decant"}`)
	stubRunningByCWD(t, "Grok", map[string]int{"code/decant": 782479})

	recs, err := (Grok{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1 background session from hooks", recs)
	}
	r := recs[0]
	if r.PID != 782479 || r.Label != "Grok" {
		t.Errorf("record = %+v, want PID 782479 label Grok", r)
	}
	if r.Status != agent.StatusWorking || r.Detail != "tool:read_file" || r.CWD != "code/decant" {
		t.Errorf("record = %+v, want working tool:read_file at code/decant", r)
	}
}

// TestGrokHookUnpairedNotListed: hook state for a session whose process is
// not running (or runs elsewhere) emits nothing — the pairing is by CWD.
func TestGrokHookUnpairedNotListed(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, ".grok")
	writeFile(t, filepath.Join(home, "active_sessions.json"), "[]")
	old := grokHome
	grokHome = func() string { return home }
	t.Cleanup(func() { grokHome = old })

	cfg := grokTestConfig(t)
	writeFile(t, filepath.Join(cfg.HookStateDir, "grok", "bg-1.json"),
		`{"status":"working","detail":"tool:read_file","cwd":"/home/guillaume/code/decant"}`)
	stubRunningByCWD(t, "Grok", map[string]int{"code/tmon": 4242})

	recs, err := (Grok{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("records = %+v, want none (no grok process in that cwd)", recs)
	}
}
