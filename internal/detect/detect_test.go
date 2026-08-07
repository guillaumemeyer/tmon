//go:build linux

package detect

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/guillaumemeyer/tmon/internal/proc"
)

// fakeProcEntry is one directory under the synthetic /proc tree.
type fakeProcEntry struct {
	cmdline string // NUL bytes become spaces, as in the real table
	cwd     string // "" = no cwd symlink (unreadable)
}

// writeFakeProc builds a synthetic /proc tree at root and points the proc
// package at it. Returns the restore function.
func writeFakeProc(t *testing.T, entries map[int]fakeProcEntry) func() {
	t.Helper()
	root := t.TempDir()
	for pid, e := range entries {
		dir := filepath.Join(root, fmt.Sprint(pid))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(e.cmdline), 0o644); err != nil {
			t.Fatal(err)
		}
		if e.cwd != "" {
			if err := os.Symlink(e.cwd, filepath.Join(dir, "cwd")); err != nil {
				t.Fatal(err)
			}
		}
	}
	return proc.SetProcRoot(root)
}

func TestAllScansProcessTable(t *testing.T) {
	restore := writeFakeProc(t, map[int]fakeProcEntry{
		1:  {cmdline: "init\x00"},
		10: {cmdline: "grok build --model fast\x00", cwd: "/home/u/code"},
		20: {cmdline: "claude code /home/u\x00", cwd: "/home/u"},
		30: {cmdline: "hermes gateway run\x00", cwd: "/opt/hermes"}, // gateway: skipped
		40: {cmdline: "hermes agent\x00", cwd: "/opt/hermes"},
		50: {cmdline: "codex exec\x00", cwd: "/home/u/code"},
		60: {cmdline: "definitely-not-an-agent --flag\x00", cwd: "/tmp"},
	})
	defer restore()

	agents, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 4 {
		t.Fatalf("agents = %+v, want 4 (gateway and non-matches excluded)", agents)
	}

	want := []struct {
		pid   int
		label string
		cwd   string
	}{
		{10, "Grok", "u/code"},
		{20, "Claude", "home/u"},
		{40, "Hermes", "opt/hermes"},
		{50, "Codex", "u/code"},
	}
	for i, w := range want {
		a := agents[i]
		if a.PID != w.pid || a.Label != w.label || a.CWD != w.cwd {
			t.Errorf("agents[%d] = {PID:%d Label:%q CWD:%q}, want {PID:%d Label:%q CWD:%q}",
				i, a.PID, a.Label, a.CWD, w.pid, w.label, w.cwd)
		}
	}
}

func TestAllTruncatesCmdline(t *testing.T) {
	long := "grok build --model fast --prompt " + string(make([]byte, 200))
	restore := writeFakeProc(t, map[int]fakeProcEntry{
		77: {cmdline: long, cwd: "/home/u"},
	})
	defer restore()

	agents, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 {
		t.Fatalf("agents = %+v, want 1", agents)
	}
	if len(agents[0].Cmdline) != CmdlineSnippetLimit {
		t.Errorf("cmdline length = %d, want %d", len(agents[0].Cmdline), CmdlineSnippetLimit)
	}
}

func TestAllUnreadableCwdIsQuestionMark(t *testing.T) {
	restore := writeFakeProc(t, map[int]fakeProcEntry{
		88: {cmdline: "grok build\x00"}, // no cwd symlink
	})
	defer restore()

	agents, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].CWD != "?" {
		t.Fatalf("agents = %+v, want one agent with CWD ?", agents)
	}
}
