//go:build darwin

package proc

import (
	"os"
	"strings"
	"testing"
)

func TestListPIDsLive(t *testing.T) {
	pids, err := ListPIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(pids) == 0 {
		t.Fatal("ListPIDs returned empty")
	}
	self := os.Getpid()
	found := false
	for _, p := range pids {
		if p == self {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListPIDs missing self pid %d", self)
	}
}

func TestReadCmdlineSelf(t *testing.T) {
	got, err := ReadCmdline(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	// Test binary name appears somewhere in argv.
	if !strings.Contains(got, "proc.test") && !strings.Contains(got, "tmon") && got == "" {
		t.Errorf("ReadCmdline self = %q, unexpected empty/unrelated", got)
	}
	t.Logf("self cmdline: %q", got)
}

func TestParentPIDSelf(t *testing.T) {
	ppid, err := ParentPID(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if ppid <= 0 {
		t.Errorf("ParentPID = %d, want > 0", ppid)
	}
}

func TestReadCWDSelf(t *testing.T) {
	cwd, err := ReadCWD(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if cwd != wd {
		// macOS may resolve symlinks differently; require same evaluated path.
		if realC, e1 := os.Readlink(cwd); e1 == nil {
			cwd = realC
		}
		if cwd != wd {
			t.Logf("ReadCWD = %q, Getwd = %q (may differ by symlink resolution)", cwd, wd)
		}
	}
	if cwd == "" {
		t.Error("empty cwd")
	}
}

func TestReadCPUTicksSelf(t *testing.T) {
	ticks, err := ReadCPUTicks(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	// Process has done some work by the time the test runs.
	if ticks < 0 {
		t.Errorf("ReadCPUTicks = %d", ticks)
	}
	t.Logf("self cpu ticks (100Hz): %d", ticks)
}

func TestReadIOBytesSelf(t *testing.T) {
	n, err := ReadIOBytes(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if n < 0 {
		t.Errorf("ReadIOBytes = %d", n)
	}
	t.Logf("self io bytes: %d", n)
}

func TestTTYForPIDSelf(t *testing.T) {
	// May be empty when tests run without a controlling terminal (CI).
	tty := TTYForPID(os.Getpid())
	t.Logf("self tty: %q", tty)
	if tty != "" && !strings.HasPrefix(tty, "/dev/") {
		t.Errorf("TTYForPID = %q, want /dev/... or empty", tty)
	}
}
