//go:build darwin

package proc

import "fmt"

// darwinDevTTYName converts a Darwin device number (e_tdev) to a path matching
// tmux's #{pane_tty}, typically /dev/ttysNNN.
//
// Darwin major/minor macros (sys/types.h):
//
//	major(x) = (x >> 24) & 0xff
//	minor(x) = x & 0xffffff
func darwinDevTTYName(tdev int32) string {
	if tdev == -1 || tdev == 0 {
		return ""
	}
	// #define major(x) ((int32_t)(((u_int32_t)(x) >> 24) & 0xff))
	// #define minor(x) ((int32_t)((x) & 0xffffff))
	u := uint32(tdev)
	major := (u >> 24) & 0xff
	minor := u & 0xffffff
	// Pseudo-ttys use major 16 on modern macOS (CHARACTER_MAJOR_PTS).
	// Also accept the historical range used by some versions.
	switch major {
	case 16, 4:
		return fmt.Sprintf("/dev/ttys%03d", minor)
	default:
		// Fall back: still produce ttys path if minor looks like a pts index.
		if minor < 1000 {
			return fmt.Sprintf("/dev/ttys%03d", minor)
		}
		return ""
	}
}
