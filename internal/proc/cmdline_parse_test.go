package proc

import "testing"

func TestParseProcArgs2(t *testing.T) {
	// exec_path + padding + two args
	raw := []byte(" /usr/bin/claude\x00\x00claude\x00--dangerously-skip-permissions\x00HOME=/tmp\x00")
	// strip leading space that sometimes appears — use clean buffer
	raw = []byte("/usr/bin/claude\x00\x00claude\x00--dangerously-skip-permissions\x00HOME=/tmp\x00")
	got := parseProcArgs2(raw, 2)
	if len(got) != 2 || got[0] != "claude" || got[1] != "--dangerously-skip-permissions" {
		t.Fatalf("parseProcArgs2 = %#v, want [claude --dangerously-skip-permissions]", got)
	}
	if j := joinArgv(got); j != "claude --dangerously-skip-permissions" {
		t.Errorf("joinArgv = %q", j)
	}
}

func TestParseProcArgs2Empty(t *testing.T) {
	if got := parseProcArgs2(nil, 0); got != nil {
		t.Errorf("got %#v", got)
	}
}
