package connector

import (
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

func freshTS() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func TestHermesRunningGateway(t *testing.T) {
	hermesFixture(t, `{"pid":4242,"gateway_state":"running","active_agents":0,"updated_at":"`+freshTS()+`"}`)
	recs, err := (Hermes{}).Probe(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	r := recs[0]
	if r.Status != agent.StatusRunning || r.Detail != "gateway" {
		t.Errorf("record = %+v, want running gateway", r)
	}
	if r.PID != 4242 || r.Label != "Hermes" {
		t.Errorf("record = %+v, want PID 4242 label Hermes", r)
	}
}

func TestHermesActiveAgents(t *testing.T) {
	hermesFixture(t, `{"pid":4242,"gateway_state":"running","active_agents":2,"updated_at":"`+freshTS()+`"}`)
	recs, err := (Hermes{}).Probe(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if recs[0].Status != agent.StatusActive || recs[0].Detail != "2 active agents" {
		t.Errorf("record = %+v, want active with 2 active agents", recs[0])
	}
}

func TestHermesNonRunningStateIsIdle(t *testing.T) {
	hermesFixture(t, `{"pid":4242,"gateway_state":"stopped","active_agents":0,"updated_at":"`+freshTS()+`"}`)
	recs, err := (Hermes{}).Probe(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if recs[0].Status != agent.StatusIdle || recs[0].Detail != "gateway:stopped" {
		t.Errorf("record = %+v, want idle gateway:stopped", recs[0])
	}
}

func TestHermesPIDFallback(t *testing.T) {
	// gateway_state.json missing entirely: gateway.pid alone still yields a
	// running record (Enabled gates on gateway_state.json, so this path only
	// triggers when that file becomes unreadable mid-flight).
	home := hermesFixture(t, "")
	writeFile(t, filepath.Join(home, "gateway.pid"), `{"pid":4242,"kind":"hermes-gateway"}`)
	recs, err := (Hermes{}).Probe(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Status != agent.StatusRunning || recs[0].Detail != "gateway" {
		t.Fatalf("records = %+v, want running gateway from pid file", recs)
	}
}

func TestHermesCorruptStateFallsBackToPID(t *testing.T) {
	home := hermesFixture(t, "not json at all")
	writeFile(t, filepath.Join(home, "gateway.pid"), `{"pid":4242}`)
	recs, err := (Hermes{}).Probe(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].PID != 4242 {
		t.Fatalf("records = %+v, want PID 4242 from fallback", recs)
	}
}

func TestHermesFreshStateSurvivesCollect(t *testing.T) {
	hermesFixture(t, `{"pid":4242,"gateway_state":"running","active_agents":1,"updated_at":"`+freshTS()+`"}`)
	stubHermesLive(t)
	got := Collect(config.Defaults(), time.Now())
	if len(got) != 1 || got[0].Status != agent.StatusActive {
		t.Fatalf("collect = %+v, want one active record", got)
	}
}

func TestHermesStaleStateDroppedByCollect(t *testing.T) {
	stale := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano)
	hermesFixture(t, `{"pid":4242,"gateway_state":"running","active_agents":1,"updated_at":"`+stale+`"}`)
	stubHermesLive(t)
	got := Collect(config.Defaults(), time.Now())
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
