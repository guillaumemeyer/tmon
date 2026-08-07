//go:build linux

package detect

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/guillaumemeyer/tmon/internal/proc"
)

// buildBenchProc creates a synthetic /proc tree with total processes, of
// which matching are agents whose cmdline matches a signature. Returns the
// restore function for proc.SetProcRoot.
func buildBenchProc(b *testing.B, total, matching int) func() {
	b.Helper()
	root := b.TempDir()
	for pid := 1; pid <= total; pid++ {
		dir := filepath.Join(root, fmt.Sprint(pid))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		cmdline := "some-unrelated-process --flag --verbose"
		if pid <= matching {
			cmdline = "grok build --model fast"
		}
		if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline+"\x00"), 0o644); err != nil {
			b.Fatal(err)
		}
		if pid <= matching {
			if err := os.Symlink("/home/u/code", filepath.Join(dir, "cwd")); err != nil {
				b.Fatal(err)
			}
		}
	}
	return proc.SetProcRoot(root)
}

// BenchmarkDetectAll measures the full /proc signature scan: 2000
// processes, 10 of them matching agents. This is the headline number for
// the parallel scan, since the dashboard and the poll both call detect.All
// on every refresh.
func BenchmarkDetectAll(b *testing.B) {
	restore := buildBenchProc(b, 2000, 10)
	defer restore()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		agents, err := All()
		if err != nil {
			b.Fatal(err)
		}
		if len(agents) != 10 {
			b.Fatalf("agents = %d, want 10", len(agents))
		}
	}
}
