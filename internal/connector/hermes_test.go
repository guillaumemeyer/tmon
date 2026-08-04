package connector

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

const hermesFixtureHome = "fixture/.hermes"

// hermesFixture points the hermesHome seam at a temp dir; content is the
// gateway_state.json body, or "" to omit the file.
func hermesFixture(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, hermesFixtureHome)
	if content != "" {
		writeFile(t, filepath.Join(home, "gateway_state.json"), content)
	}
	old := hermesHome
	hermesHome = func() string { return home }
	t.Cleanup(func() { hermesHome = old })
	return home
}

// stubHermesLive makes Collect see Hermes records: registry wired, PID alive.
func stubHermesLive(t *testing.T) {
	t.Helper()
	oldReg := Registry
	Registry = []Connector{Hermes{}}
	t.Cleanup(func() { Registry = oldReg })
	oldAlive := procAlive
	procAlive = func(pid int) bool { return true }
	t.Cleanup(func() { procAlive = oldAlive })
}

// hermesTestCfg returns a config with a temp StateDir so usage caching and
// insights stubs never touch the real plugin state.
func hermesTestCfg(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.StateDir = t.TempDir()
	return cfg
}

// stubHermesInsights replaces the insights CLI with a fixture so tests
// never spawn the real hermes binary.
func stubHermesInsights(t *testing.T, out string, err error) {
	t.Helper()
	old := hermesInsightsOutput
	hermesInsightsOutput = func(config.Config) (string, error) { return out, err }
	t.Cleanup(func() { hermesInsightsOutput = old })
}

// insightsFixture is a trimmed `hermes insights --days 1` box table.
const insightsFixture = `  📋 Overview
  ────────────────────────────────────────────────────────
  Sessions:          1             Messages:        99
  Tool calls:        46            User messages:   7
  Input tokens:      79,757        Output tokens:   51,660
  Total tokens:      2,632,153
`

func freshTS() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func TestHermesRunningGateway(t *testing.T) {
	stubHermesInsights(t, insightsFixture, nil)
	hermesFixture(t, `{"pid":4242,"gateway_state":"running","active_agents":0,"updated_at":"`+freshTS()+`"}`)
	recs, err := (Hermes{}).Probe(hermesTestCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	r := recs[0]
	if r.Status != agent.StatusIdle || r.Detail != "gateway" {
		t.Errorf("record = %+v, want idle gateway", r)
	}
	if r.PID != 4242 || r.Label != "Hermes" {
		t.Errorf("record = %+v, want PID 4242 label Hermes", r)
	}
}

func TestHermesActiveAgents(t *testing.T) {
	stubHermesInsights(t, insightsFixture, nil)
	hermesFixture(t, `{"pid":4242,"gateway_state":"running","active_agents":2,"updated_at":"`+freshTS()+`"}`)
	recs, err := (Hermes{}).Probe(hermesTestCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	if recs[0].Status != agent.StatusWorking || recs[0].Detail != "2 active agents" {
		t.Errorf("record = %+v, want working with 2 active agents", recs[0])
	}
}

func TestHermesNonRunningStateIsIdle(t *testing.T) {
	stubHermesInsights(t, insightsFixture, nil)
	hermesFixture(t, `{"pid":4242,"gateway_state":"stopped","active_agents":0,"updated_at":"`+freshTS()+`"}`)
	recs, err := (Hermes{}).Probe(hermesTestCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	if recs[0].Status != agent.StatusIdle || recs[0].Detail != "gateway:stopped" {
		t.Errorf("record = %+v, want idle gateway:stopped", recs[0])
	}
}

func TestHermesPIDFallback(t *testing.T) {
	// gateway_state.json missing entirely: gateway.pid alone still yields an
	// idle record (Enabled gates on gateway_state.json, so this path only
	// triggers when that file becomes unreadable mid-flight).
	stubHermesInsights(t, insightsFixture, nil)
	home := hermesFixture(t, "")
	writeFile(t, filepath.Join(home, "gateway.pid"), `{"pid":4242,"kind":"hermes-gateway"}`)
	recs, err := (Hermes{}).Probe(hermesTestCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Status != agent.StatusIdle || recs[0].Detail != "gateway" {
		t.Fatalf("records = %+v, want idle gateway from pid file", recs)
	}
}

func TestHermesCorruptStateFallsBackToPID(t *testing.T) {
	stubHermesInsights(t, insightsFixture, nil)
	home := hermesFixture(t, "not json at all")
	writeFile(t, filepath.Join(home, "gateway.pid"), `{"pid":4242}`)
	recs, err := (Hermes{}).Probe(hermesTestCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].PID != 4242 {
		t.Fatalf("records = %+v, want PID 4242 from fallback", recs)
	}
}

func TestHermesFreshStateSurvivesCollect(t *testing.T) {
	stubHermesInsights(t, insightsFixture, nil)
	hermesFixture(t, `{"pid":4242,"gateway_state":"running","active_agents":1,"updated_at":"`+freshTS()+`"}`)
	stubHermesLive(t)
	got := Collect(hermesTestCfg(t), time.Now())
	if len(got) != 1 || got[0].Status != agent.StatusWorking {
		t.Fatalf("collect = %+v, want one active record", got)
	}
}

func TestHermesStaleStateDroppedByCollect(t *testing.T) {
	stubHermesInsights(t, insightsFixture, nil)
	stale := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano)
	hermesFixture(t, `{"pid":4242,"gateway_state":"running","active_agents":1,"updated_at":"`+stale+`"}`)
	stubHermesLive(t)
	got := Collect(hermesTestCfg(t), time.Now())
	if len(got) != 0 {
		t.Fatalf("collect = %+v, want none (stale gateway state dropped)", got)
	}
}

func TestHermesEnabledGatesOnStatePath(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, hermesFixtureHome)
	old := hermesHome
	hermesHome = func() string { return home }
	t.Cleanup(func() { hermesHome = old })

	if (Hermes{}).Enabled(config.Defaults()) {
		t.Error("enabled before gateway_state.json exists")
	}
	writeFile(t, filepath.Join(home, "gateway_state.json"), "{}")
	if !(Hermes{}).Enabled(config.Defaults()) {
		t.Error("not enabled with gateway_state.json present")
	}
}

// ─── usage enrichment ────────────────────────────────────────────────────────

func TestHermesProbeUsageFromInsights(t *testing.T) {
	stubHermesInsights(t, insightsFixture, nil)
	hermesFixture(t, `{"pid":4242,"gateway_state":"running","active_agents":1,"updated_at":"`+freshTS()+`"}`)

	recs, err := (Hermes{}).Probe(hermesTestCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	if u := recs[0].Usage; u.TokensUsed != 79757+51660 {
		t.Errorf("TokensUsed = %d, want %d", u.TokensUsed, 79757+51660)
	}
}

func TestHermesUsageInsightsCachedWithinTTL(t *testing.T) {
	calls := 0
	stubHermesInsights(t, "", nil)
	old := hermesInsightsOutput
	hermesInsightsOutput = func(config.Config) (string, error) {
		calls++
		return insightsFixture, nil
	}
	t.Cleanup(func() { hermesInsightsOutput = old })

	hermesFixture(t, `{"pid":4242,"gateway_state":"running","active_agents":0,"updated_at":"`+freshTS()+`"}`)
	cfg := hermesTestCfg(t)

	for i := 0; i < 3; i++ {
		recs, err := (Hermes{}).Probe(cfg)
		if err != nil || len(recs) != 1 {
			t.Fatalf("probe %d: recs=%+v err=%v", i, recs, err)
		}
	}
	if calls != 1 {
		t.Errorf("insights ran %d times, want 1 (TTL cache)", calls)
	}
}

func TestHermesUsageEmptyWhenInsightsFails(t *testing.T) {
	stubHermesInsights(t, "", os.ErrNotExist)
	hermesFixture(t, `{"pid":4242,"gateway_state":"running","active_agents":0,"updated_at":"`+freshTS()+`"}`)

	recs, err := (Hermes{}).Probe(hermesTestCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	if !recs[0].Usage.Empty() {
		t.Errorf("Usage = %+v, want empty when insights fails", recs[0].Usage)
	}
}

func TestParseInsightsTokens(t *testing.T) {
	in, out := parseInsightsTokens(insightsFixture)
	if in != 79757 || out != 51660 {
		t.Errorf("parse = %d/%d, want 79757/51660", in, out)
	}
	if in, out := parseInsightsTokens("no numbers here"); in != 0 || out != 0 {
		t.Errorf("unparseable = %d/%d, want 0/0", in, out)
	}
}
