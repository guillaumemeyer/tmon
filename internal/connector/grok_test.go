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
			recs, err := (Grok{}).Probe(config.Defaults())
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
	recs, err := (Grok{}).Probe(config.Defaults())
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

	recs, err := (Grok{}).Probe(config.Defaults())
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

	recs, err := (Grok{}).Probe(config.Defaults())
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

	recs, err := (Grok{}).Probe(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Title != "fallback title" {
		t.Errorf("records = %+v, want title from session_summary", recs)
	}
}

func TestGrokSessionWithoutSummaryHasNoTitle(t *testing.T) {
	grokFixture(t, "", "2026-08-02T09:00:00Z")
	recs, err := (Grok{}).Probe(config.Defaults())
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

	recs, err := (Grok{}).Probe(config.Defaults())
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
	got := Collect(config.Defaults(), time.Now())
	if len(got) != 1 || got[0].Status != agent.StatusWorking {
		t.Fatalf("collect = %+v, want one active record", got)
	}
}

func TestGrokStalePhaseDroppedByCollect(t *testing.T) {
	stale := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano)
	grokFixture(t, `{"ts":"`+stale+`","type":"phase_changed","phase":"streaming_reasoning"}`+"\n", "2026-08-02T09:00:00Z")
	stubGrokLive(t)
	got := Collect(config.Defaults(), time.Now())
	if len(got) != 0 {
		t.Fatalf("collect = %+v, want none (stale phase dropped)", got)
	}
}

func TestGrokEnabledGatesOnStatePath(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, ".grok")
	old := grokHome
	grokHome = func() string { return home }
	t.Cleanup(func() { grokHome = old })

	// Empty fixture home: the state surface is absent, so not enabled.
	if (Grok{}).Enabled(config.Defaults()) {
		t.Error("enabled before active_sessions.json exists")
	}
	writeFile(t, filepath.Join(home, "active_sessions.json"), "[]")
	if !(Grok{}).Enabled(config.Defaults()) {
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
