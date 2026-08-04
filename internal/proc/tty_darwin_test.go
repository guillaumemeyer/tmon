//go:build darwin

package proc

import "testing"

func TestDarwinDevTTYName(t *testing.T) {
	// major 16, minor 1 → /dev/ttys001
	tdev := int32((16 << 24) | 1)
	if got := darwinDevTTYName(tdev); got != "/dev/ttys001" {
		t.Errorf("got %q, want /dev/ttys001", got)
	}
	if got := darwinDevTTYName(0); got != "" {
		t.Errorf("zero tdev: got %q", got)
	}
	if got := darwinDevTTYName(-1); got != "" {
		t.Errorf("-1 tdev: got %q", got)
	}
}
