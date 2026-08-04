package connector

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// countUsage is a parseTokens helper: it sums the "tokens" number in a
// JSON-ish line `{"tokens":N}`, mimicking the transcript parsers.
func countUsage(line []byte) int64 {
	s := string(line)
	if i := strings.Index(s, `"tokens":`); i >= 0 {
		rest := s[i+len(`"tokens":`):]
		if j := strings.IndexByte(rest, '}'); j > 0 {
			if n, err := strconv.ParseInt(rest[:j], 10, 64); err == nil {
				return n
			}
		}
	}
	return 0
}

func TestIncrementalTokens(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "session.jsonl")

	// First poll: empty file, no cache.
	os.WriteFile(src, nil, 0o644)
	if n, err := incrementalTokens(dir, src, countUsage); err != nil || n != 0 {
		t.Fatalf("empty file: n=%d err=%v", n, err)
	}

	// Appended usage is counted.
	write := func(s string) {
		f, err := os.OpenFile(src, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(s); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	write(`{"type":"assistant","usage":{"tokens":10}}
`)
	n, err := incrementalTokens(dir, src, countUsage)
	if err != nil || n != 10 {
		t.Fatalf("after first append: n=%d err=%v, want 10", n, err)
	}

	// Unchanged file: no re-read, same total.
	n, err = incrementalTokens(dir, src, countUsage)
	if err != nil || n != 10 {
		t.Fatalf("unchanged: n=%d err=%v, want 10", n, err)
	}

	// More appended bytes are added to the running total, including a
	// partial trailing line that must be skipped until it completes.
	write(`{"type":"assistant","usage":{"tokens":25}}
{"type":"assistant","usage":{"tokens":`)
	n, err = incrementalTokens(dir, src, countUsage)
	if err != nil || n != 35 {
		t.Fatalf("after second append: n=%d err=%v, want 35", n, err)
	}
	write(`5}}
`)
	n, err = incrementalTokens(dir, src, countUsage)
	if err != nil || n != 40 {
		t.Fatalf("after partial line completes: n=%d err=%v, want 40", n, err)
	}
}

func TestIncrementalTokensRotation(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "session.jsonl")
	os.WriteFile(src, []byte(`{"usage":{"tokens":50}}
`), 0o644)
	n, err := incrementalTokens(dir, src, countUsage)
	if err != nil || n != 50 {
		t.Fatalf("initial: n=%d err=%v, want 50", n, err)
	}

	// A new session truncates the file: the count must restart, not
	// continue from 50.
	if err := os.WriteFile(src, []byte(`{"usage":{"tokens":7}}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err = incrementalTokens(dir, src, countUsage)
	if err != nil || n != 7 {
		t.Fatalf("after rotation: n=%d err=%v, want 7", n, err)
	}
}

func TestPruneUsageCache(t *testing.T) {
	dir := t.TempDir()
	usageDir := filepath.Join(dir, "usage")
	os.MkdirAll(usageDir, 0o755)

	live := filepath.Join(dir, "live.jsonl")
	os.WriteFile(live, []byte("x"), 0o644)
	dead := filepath.Join(dir, "dead.jsonl")

	// Write two entries past the (low) cap by lowering it temporarily.
	old := usageCacheMaxEntries
	usageCacheMaxEntries = 1
	defer func() { usageCacheMaxEntries = old }()

	saveUsageEntry(dir, live, usageEntry{Src: live, Size: 1, Tokens: 5})
	saveUsageEntry(dir, dead, usageEntry{Src: dead, Size: 1, Tokens: 9})

	entries, err := os.ReadDir(usageDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("prune kept %d entries, want 1 (dead source removed)", len(entries))
	}
}

func TestRunCachedTTL(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	fn := func() (string, error) {
		calls++
		return "output", nil
	}

	// First call runs and caches.
	out, err := runCachedTTL(dir, "test", time.Hour, fn)
	if err != nil || out != "output" || calls != 1 {
		t.Fatalf("first call: out=%q calls=%d err=%v", out, calls, err)
	}
	// Within the TTL the CLI is not re-run.
	out, err = runCachedTTL(dir, "test", time.Hour, fn)
	if err != nil || out != "output" || calls != 1 {
		t.Fatalf("cached call: out=%q calls=%d err=%v", out, calls, err)
	}

	// A fresh cache dir (poll after expiry) re-runs.
	dir2 := t.TempDir()
	if _, err := runCachedTTL(dir2, "test", time.Hour, fn); err != nil || calls != 2 {
		t.Fatalf("expired call: calls=%d err=%v, want 2", calls, err)
	}
}
