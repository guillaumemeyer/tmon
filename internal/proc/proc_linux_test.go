//go:build linux

package proc

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// writeFixture builds a fake /proc/<pid> tree under t.TempDir() and points
// procRoot at it. Returns the pid dir. procRoot is restored on cleanup.
func writeFixture(t *testing.T, pid int, stat, cmdline, io string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proc")
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if stat != "" {
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if cmdline != "" {
		if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if io != "" {
		if err := os.WriteFile(filepath.Join(dir, "io"), []byte(io), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := procRoot
	procRoot = root
	t.Cleanup(func() { procRoot = old })
	return dir
}

// A realistic stat line. 1-based field 7 (tty_nr) = 34824 = (136<<8)|8,
// which encodes /dev/pts/8. Fields 14-17 (utime..cstime) = 100 200 300 400.
const testStat = "12345 (grok build) S 1 12345 12345 34824 12345 4194304 0 0 0 0 100 200 300 400 500 600 700 800 900 1000 1100 1200"

func TestReadCmdline(t *testing.T) {
	writeFixture(t, 12345, "", "grok\x00build\x00--agent", "")
	got, err := ReadCmdline(12345)
	if err != nil {
		t.Fatal(err)
	}
	if got != "grok build --agent" {
		t.Errorf("ReadCmdline = %q, want %q", got, "grok build --agent")
	}
}

func TestReadCmdlineMissing(t *testing.T) {
	writeFixture(t, 99999, "", "", "")
	if _, err := ReadCmdline(99999); err == nil {
		t.Error("expected error for missing cmdline")
	}
}

func TestReadCPUTicks(t *testing.T) {
	writeFixture(t, 12345, testStat, "", "")
	got, err := ReadCPUTicks(12345)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1000 { // 100+200+300+400
		t.Errorf("ReadCPUTicks = %d, want 1000", got)
	}
}

func TestStatField(t *testing.T) {
	writeFixture(t, 12345, testStat, "", "")
	if v, err := StatField(12345, 1); err != nil || v != 1 {
		t.Errorf("ppid = %d, %v; want 1", v, err)
	}
	if v, err := StatField(12345, 4); err != nil || v != 34824 {
		t.Errorf("tty_nr = %d, %v; want 34824", v, err)
	}
	if _, err := StatField(12345, 99); err == nil {
		t.Error("expected out-of-range error")
	}
}

func TestParentPID(t *testing.T) {
	writeFixture(t, 12345, testStat, "", "")
	got, err := ParentPID(12345)
	if err != nil || got != 1 {
		t.Errorf("ParentPID = %d, %v; want 1", got, err)
	}
}

func TestReadIOBytes(t *testing.T) {
	writeFixture(t, 12345, "", "", "rchar: 1000\nwchar: 2000\nsyscr: 5\nsyscw: 6\n")
	got, err := ReadIOBytes(12345)
	if err != nil {
		t.Fatal(err)
	}
	if got != 3000 {
		t.Errorf("ReadIOBytes = %d, want 3000", got)
	}
}

func TestTTYForPID(t *testing.T) {
	writeFixture(t, 12345, testStat, "", "")
	if got := TTYForPID(12345); got != "/dev/pts/8" {
		t.Errorf("TTYForPID = %q, want /dev/pts/8", got)
	}
}

func TestDevTTYName(t *testing.T) {
	cases := []struct {
		tty  int64
		want string
	}{
		{34818, "/dev/pts/2"}, // (136<<8)|2 — devpts major, minor 2
		{34816, "/dev/pts/0"}, // (136<<8)|0
		{34824, "/dev/pts/8"}, // (136<<8)|8
		{1025, "/dev/tty1"},   // (4<<8)|1 — legacy console
		{0, ""},               // no controlling terminal
	}
	for _, tc := range cases {
		if got := DevTTYName(tc.tty); got != tc.want {
			t.Errorf("DevTTYName(%d) = %q, want %q", tc.tty, got, tc.want)
		}
	}
}

func TestReadCWD(t *testing.T) {
	dir := writeFixture(t, 12345, "", "", "")
	real := filepath.Join(dir, "cwd-target")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, "cwd")); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCWD(12345)
	if err != nil {
		t.Fatal(err)
	}
	if got != real {
		t.Errorf("ReadCWD = %q, want %q", got, real)
	}
}
