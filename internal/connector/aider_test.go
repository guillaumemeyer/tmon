//go:build linux

package connector

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/proc"
)

func TestAiderEnabledGatesOnHomeHistory(t *testing.T) {
	home := withHome(t)
	cfg := config.Defaults()
	if (Aider{}).Enabled(cfg) {
		t.Error("enabled with no aider home files")
	}
	writeFile(t, filepath.Join(home, ".aider.history.md"), "")
	if !(Aider{}).Enabled(cfg) {
		t.Error("not enabled with ~/.aider.history.md present")
	}
}

// aiderFixture sets up a fake /proc with one aider process (pid 4242) whose
// cwd points at a project dir containing .aider.chat.history.md.
func aiderFixture(t *testing.T, historyContent string) string {
	t.Helper()
	root := t.TempDir()
	restore := proc.SetProcRoot(root)
	t.Cleanup(restore)
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if historyContent != "" {
		writeFile(t, filepath.Join(proj, ".aider.chat.history.md"), historyContent)
	}
	pidDir := filepath.Join(root, "4242")
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(proj, filepath.Join(pidDir, "cwd")); err != nil {
		t.Fatal(err)
	}
	stubPIDs(t, "Aider", []int{4242})
	return proj
}

func TestAiderEmitsActiveWhenHistoryFresh(t *testing.T) {
	withHome(t)
	cfg := config.Defaults()
	proj := aiderFixture(t, "# chat\n")

	recs, err := (Aider{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	r := recs[0]
	if r.PID != 4242 || r.Label != "Aider" || r.Status != agent.StatusWorking || r.Detail != "editing" {
		t.Errorf("record = %+v, want PID 4242 Aider working editing", r)
	}
	if want := proc.CWDShort(proj); r.CWD != want {
		t.Errorf("CWD = %q, want short form %q", r.CWD, want)
	}
	if time.Since(r.At) > 5*time.Second {
		t.Errorf("At = %v, want recent history mtime", r.At)
	}
}

func TestAiderSilentWhenHistoryStale(t *testing.T) {
	withHome(t)
	cfg := config.Defaults()
	proj := aiderFixture(t, "# chat\n")
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(filepath.Join(proj, ".aider.chat.history.md"), old, old); err != nil {
		t.Fatal(err)
	}

	recs, err := (Aider{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("records = %+v, want none (history older than turn window)", recs)
	}
}

func TestAiderSilentWithoutHistoryFile(t *testing.T) {
	withHome(t)
	cfg := config.Defaults()
	aiderFixture(t, "") // no history file written yet

	recs, err := (Aider{}).Probe(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("records = %+v, want none (no history file)", recs)
	}
}
