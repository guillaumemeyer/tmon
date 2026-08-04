package connector

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

// openclawFixture points openclawHome and openclawLockDir at sibling dirs
// under a temp root. Returns (home, lockDir).
func openclawFixture(t *testing.T) (home, lockDir string) {
	t.Helper()
	root := t.TempDir()
	home = filepath.Join(root, ".openclaw")
	lockDir = filepath.Join(root, "locks")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldHome := openclawHome
	oldLock := openclawLockDir
	openclawHome = func() string { return home }
	openclawLockDir = func() string { return lockDir }
	t.Cleanup(func() {
		openclawHome = oldHome
		openclawLockDir = oldLock
	})
	return home, lockDir
}

func openclawTestCfg(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.StateDir = t.TempDir()
	cfg.ConnectorFreshness = 30 * time.Second
	return cfg
}

// stubOpenClawSessions replaces the sessions CLI with a fixture.
func stubOpenClawSessions(t *testing.T, out string, err error) {
	t.Helper()
	old := openclawSessionsOutput
	openclawSessionsOutput = func(config.Config, int) (string, error) { return out, err }
	t.Cleanup(func() { openclawSessionsOutput = old })
}

func writeLock(t *testing.T, lockDir, name, body string) {
	t.Helper()
	writeFile(t, filepath.Join(lockDir, name), body)
}

func TestOpenClawEnabledGatesOnDataDir(t *testing.T) {
	// Isolate from the real home / tmp lock dir.
	root := t.TempDir()
	oldHome := openclawHome
	oldLock := openclawLockDir
	openclawHome = func() string { return filepath.Join(root, "missing-home") }
	openclawLockDir = func() string { return filepath.Join(root, "missing-locks") }
	t.Cleanup(func() {
		openclawHome = oldHome
		openclawLockDir = oldLock
	})

	cfg := config.Defaults()
	if (OpenClaw{}).Enabled(cfg) {
		t.Error("enabled with no ~/.openclaw and no lock dir")
	}

	home, lockDir := openclawFixture(t)
	if !(OpenClaw{}).Enabled(cfg) {
		t.Error("not enabled with ~/.openclaw present")
	}
	// Home gone but lock dir present still enables.
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	if !(OpenClaw{}).Enabled(cfg) {
		t.Errorf("not enabled with lock dir %s present", lockDir)
	}
}

func TestOpenClawProbeNoLockEmitsNothing(t *testing.T) {
	openclawFixture(t)
	stubOpenClawSessions(t, "", errSessionsMissing)
	recs, err := (OpenClaw{}).Probe(openclawTestCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("records = %+v, want none without a gateway lock", recs)
	}
}

// errSessionsMissing stands in for LookPath failure / CLI missing.
var errSessionsMissing = os.ErrNotExist

func TestOpenClawProbeIdleGateway(t *testing.T) {
	_, lockDir := openclawFixture(t)
	writeLock(t, lockDir, "gateway.deadbeef.lock", `{"pid":4242,"createdAt":"2026-01-01T00:00:00Z","port":18789}`)
	stubOpenClawSessions(t, `{"sessions":[]}`, nil)

	recs, err := (OpenClaw{}).Probe(openclawTestCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	r := recs[0]
	if r.PID != 4242 || r.Label != "OpenClaw" {
		t.Errorf("record = %+v, want PID 4242 label OpenClaw", r)
	}
	if r.Status != agent.StatusIdle || r.Detail != "gateway" {
		t.Errorf("record = %+v, want idle gateway", r)
	}
}

func TestOpenClawProbeWorkingFromRunningStatus(t *testing.T) {
	_, lockDir := openclawFixture(t)
	writeLock(t, lockDir, "gateway.deadbeef.lock", `{"pid":4242,"port":18789}`)
	stubOpenClawSessions(t, `{
		"sessions": [
			{"key":"a","status":"running"},
			{"key":"b","status":"done"},
			{"key":"c","status":"running"}
		]
	}`, nil)

	recs, err := (OpenClaw{}).Probe(openclawTestCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	if recs[0].Status != agent.StatusWorking || recs[0].Detail != "2 active sessions" {
		t.Errorf("record = %+v, want working with 2 active sessions", recs[0])
	}
}

func TestOpenClawProbeWorkingFromActiveRowsWithoutStatus(t *testing.T) {
	// Older CLI: --active filtered the list but rows omit status.
	_, lockDir := openclawFixture(t)
	writeLock(t, lockDir, "gateway.deadbeef.lock", `{"pid":99,"port":18789}`)
	stubOpenClawSessions(t, `{"sessions":[{"key":"a"},{"key":"b"}]}`, nil)

	recs, err := (OpenClaw{}).Probe(openclawTestCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	if recs[0].Status != agent.StatusWorking || recs[0].Detail != "2 active sessions" {
		t.Errorf("record = %+v, want working with 2 active sessions", recs[0])
	}
}

func TestOpenClawProbeMtimeFallback(t *testing.T) {
	home, lockDir := openclawFixture(t)
	writeLock(t, lockDir, "gateway.deadbeef.lock", `{"pid":4242,"port":18789}`)
	// CLI unavailable: fall back to recent sqlite mtime.
	stubOpenClawSessions(t, "", errSessionsMissing)

	db := filepath.Join(home, "agents", "main", "agent", "openclaw-agent.sqlite")
	writeFile(t, db, "sqlite-placeholder")
	// Ensure mtime is fresh (writeFile already just wrote it).

	recs, err := (OpenClaw{}).Probe(openclawTestCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	if recs[0].Status != agent.StatusWorking || recs[0].Detail != "session activity" {
		t.Errorf("record = %+v, want working session activity", recs[0])
	}
}

func TestOpenClawProbeSkipsNonGatewayRoles(t *testing.T) {
	_, lockDir := openclawFixture(t)
	writeLock(t, lockDir, "gateway.aaaa.lock",
		`{"pid":111,"port":18789,"role":"sqlite-maintenance"}`)
	writeLock(t, lockDir, "gateway.bbbb.lock",
		`{"pid":222,"port":18789,"role":"skill-workshop-apply"}`)
	stubOpenClawSessions(t, `{"sessions":[]}`, nil)

	recs, err := (OpenClaw{}).Probe(openclawTestCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("records = %+v, want none for non-gateway lock roles", recs)
	}
}

func TestOpenClawProbePrefersConfigLockOverStateLock(t *testing.T) {
	_, lockDir := openclawFixture(t)
	writeLock(t, lockDir, "gateway.state.deadbeef.lock", `{"pid":111,"port":18789}`)
	writeLock(t, lockDir, "gateway.deadbeef.lock", `{"pid":222,"port":18789}`)
	stubOpenClawSessions(t, `{"sessions":[]}`, nil)

	recs, err := (OpenClaw{}).Probe(openclawTestCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].PID != 222 {
		t.Fatalf("records = %+v, want config lock PID 222", recs)
	}
}

func TestOpenClawProbeStateLockFallback(t *testing.T) {
	_, lockDir := openclawFixture(t)
	writeLock(t, lockDir, "gateway.state.deadbeef.lock", `{"pid":333,"port":18789}`)
	stubOpenClawSessions(t, `{"sessions":[]}`, nil)

	recs, err := (OpenClaw{}).Probe(openclawTestCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].PID != 333 {
		t.Fatalf("records = %+v, want state lock PID 333", recs)
	}
}

func TestParseOpenClawActiveSessions(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
		ok   bool
	}{
		{"empty", `{"sessions":[]}`, 0, true},
		{"running", `{"sessions":[{"status":"running"},{"status":"done"}]}`, 1, true},
		{"no status fields", `{"sessions":[{},{}]}`, 2, true},
		{"malformed", `not-json`, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, ok := parseOpenClawActiveSessions(tt.in)
			if ok != tt.ok || n != tt.want {
				t.Errorf("parse(%q) = %d,%v want %d,%v", tt.in, n, ok, tt.want, tt.ok)
			}
		})
	}
}
