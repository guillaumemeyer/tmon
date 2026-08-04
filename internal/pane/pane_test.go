//go:build linux

package pane

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/guillaumemeyer/tmon/internal/proc"
)

const fakePanes = `/dev/pts/1|main:0.0|1001|$1|main|0|bash|0
/dev/pts/2|main:0.1|1002|$1|main|0|vim|1
/dev/pts/3|work:1.0|2001|$2|work|1|api|0
`

// writeProcStat writes /proc/<pid>/stat into a temp proc root and returns a
// cleanup func that restores the real /proc mount.
func writeProcStat(t *testing.T, pid int, stat string) func() {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proc")
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
	return proc.SetProcRoot(root)
}

func TestParse(t *testing.T) {
	m, err := Parse(fakePanes)
	if err != nil {
		t.Fatal(err)
	}

	e, ok := m.byPID[2001]
	if !ok {
		t.Fatal("pid 2001 not indexed")
	}
	if e.Target != "work:1.0" || e.SessionName != "work" || e.WindowName != "api" || e.PaneIndex != "0" {
		t.Errorf("entry mismatch: %+v", e)
	}

	e, ok = m.byTTY["/dev/pts/2"]
	if !ok {
		t.Fatal("tty /dev/pts/2 not indexed")
	}
	if e.Target != "main:0.1" || e.SessionID != "$1" {
		t.Errorf("tty entry mismatch: %+v", e)
	}
}

func TestResolveByPID(t *testing.T) {
	m, err := Parse(fakePanes)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := m.Resolve(1001)
	if !ok || e.Target != "main:0.0" {
		t.Errorf("Resolve(1001) = %+v, %v; want main:0.0", e, ok)
	}
}

func TestResolveByTTY(t *testing.T) {
	// tty_nr 34818 = (136<<8)|2 → /dev/pts/2, which is in the fake pane map.
	defer writeProcStat(t, 3333, "3333 (agent) S 1 3333 3333 34818 3333 0 0 0 0 0 0 0 0 0 0")()
	m, err := Parse(fakePanes)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := m.Resolve(3333)
	if !ok || e.Target != "main:0.1" {
		t.Errorf("Resolve(3333) = %+v, %v; want main:0.1", e, ok)
	}
}

func TestResolveByAncestor(t *testing.T) {
	// pid 9999 is a child of 1001 (a pane_pid in the map) with no TTY.
	defer writeProcStat(t, 9999, "9999 (child) S 1001 9999 9999 0 9999 0 0 0 0 0 0 0 0 0 0")()
	m, err := Parse(fakePanes)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := m.Resolve(9999)
	if !ok || e.Target != "main:0.0" {
		t.Errorf("Resolve(9999) = %+v, %v; want main:0.0", e, ok)
	}
}

func TestResolveNotFound(t *testing.T) {
	defer writeProcStat(t, 4242, "4242 (lonely) S 1 4242 4242 0 4242 0 0 0 0 0 0 0 0 0 0")()
	m, err := Parse(fakePanes)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Resolve(4242); ok {
		t.Error("Resolve(4242) should not be found")
	}
}

func TestParseIgnoresMalformedLines(t *testing.T) {
	output := fakePanes + "garbage-line\n" + "/dev/pts/9|only:9.9|notanumber|x\n"
	m, err := Parse(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.byPID) != 3 {
		t.Errorf("expected 3 valid panes, got %d", len(m.byPID))
	}
}
