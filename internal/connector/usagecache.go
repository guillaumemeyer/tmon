// usagecache.go — on-disk caches that keep per-poll usage computation cheap.
//
// The tmux status bar spawns a fresh `tmon status` process on every status
// refresh, so nothing can be cached in memory between polls. Usage sources
// that would be expensive to re-read every poll — multi-megabyte agent
// transcripts, or external CLIs that take seconds (hermes insights) — are
// therefore cached on disk under <state>/usage/:
//
//   - incrementalTokens: parse only the bytes appended since the last poll,
//     so a growing session costs O(delta) instead of O(file). A shrunken
//     file (new session, rotated transcript) restarts from zero.
//   - runCachedTTL: run a CLI at most once per TTL window and reuse its
//     captured output, amortizing a multi-second analysis across polls.
//
// Both prune stale entries so the directory cannot grow without bound.
package connector

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// usageCacheMaxEntries is the soft cap on cache files; past it, entries
// whose source file is gone are pruned. A var so tests can lower it.
var usageCacheMaxEntries = 64

// usageCacheVersion bumps whenever the parsing logic changes such that
// previously cached token counts are no longer valid (e.g. a parser fix).
// Entries written by an older version are ignored and the source is
// recounted from scratch — without this, a stale count would be served
// forever while the transcript file stays the same size.
//
// v3: Claude context usage switched from sum-of-all-turns to latest-turn
// (each usage block is a full context snapshot, not a delta).
const usageCacheVersion = 3

// usageEntry is one incremental-transcript cache record.
type usageEntry struct {
	Version int    `json:"version"` // usageCacheVersion that produced this count
	Src     string `json:"src"`     // source file path this entry mirrors
	Size    int64  `json:"size"`    // source size at last parse
	Tokens  int64  `json:"tokens"`  // cumulative sum, or latest value (see mode)
}

func usageCachePath(stateDir, src string) string {
	sum := sha1.Sum([]byte(src))
	return filepath.Join(stateDir, "usage", hex.EncodeToString(sum[:])+".json")
}

// tokenAccumSum adds every positive parse result (session totals, e.g. Codex).
// tokenAccumLatest keeps the most recent positive value (context snapshots,
// e.g. Claude — each usage block re-reports the full window fill).
const (
	tokenAccumSum    = false
	tokenAccumLatest = true
)

// incrementalTokens returns the cumulative token count for a growing
// transcript file, parsing only the bytes appended since the last call.
// parseTokens reports the tokens found in one complete line (0 for lines
// that carry no usage). Cache misses and truncated files start from zero.
func incrementalTokens(stateDir, src string, parseTokens func([]byte) int64) (int64, error) {
	return scanTranscriptTokens(stateDir, src, parseTokens, tokenAccumSum)
}

// latestTokens returns the most recent non-zero token count from a growing
// transcript, parsing only the bytes appended since the last call. Use when
// each usage line is a full context snapshot rather than a delta (Claude).
func latestTokens(stateDir, src string, parseTokens func([]byte) int64) (int64, error) {
	return scanTranscriptTokens(stateDir, src, parseTokens, tokenAccumLatest)
}

// scanTranscriptTokens walks the appended portion of a transcript and either
// sums positive parse results or keeps the latest one. Cache misses and
// truncated files start from zero.
func scanTranscriptTokens(stateDir, src string, parseTokens func([]byte) int64, latest bool) (int64, error) {
	fi, err := os.Stat(src)
	if err != nil {
		return 0, err
	}

	// Separate cache keys so sum and latest modes never share a stale count.
	cacheKey := src
	if latest {
		cacheKey = "latest:" + src
	}
	entry := usageEntry{Src: src}
	if b, err := os.ReadFile(usageCachePath(stateDir, cacheKey)); err == nil {
		_ = json.Unmarshal(b, &entry)
	}
	if entry.Version != usageCacheVersion {
		entry = usageEntry{Src: src} // stale parser output: recount from zero
	}
	if entry.Size > fi.Size() {
		entry = usageEntry{Src: src} // rotated/truncated: restart the count
	}
	if entry.Size == fi.Size() {
		return entry.Tokens, nil // unchanged since the last parse
	}

	// Read the appended window. If the previous poll stopped mid-line (the
	// file did not end with a newline), back up to the start of that line:
	// it was never counted while partial, so parsing it once it completes
	// cannot double count.
	f, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	start := entry.Size
	if entry.Size > 0 {
		var b [1]byte
		if _, err := f.ReadAt(b[:], entry.Size-1); err == nil && b[0] != '\n' {
			start = lineStart(f, entry.Size)
		}
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return 0, err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return 0, err
	}

	tokens := entry.Tokens
	for _, line := range strings.Split(string(b), "\n") {
		if t := parseTokens([]byte(line)); t > 0 {
			if latest {
				tokens = t
			} else {
				tokens += t
			}
		}
	}

	entry.Size = fi.Size()
	entry.Tokens = tokens
	saveUsageEntry(stateDir, cacheKey, entry)
	return tokens, nil
}

// lineStart returns the byte offset of the start of the line containing
// pos, scanning backward in chunks; 0 when no newline precedes it.
func lineStart(f *os.File, pos int64) int64 {
	const chunk = 4096
	for pos > 0 {
		n := pos
		if n > chunk {
			n = chunk
		}
		b := make([]byte, n)
		if _, err := f.ReadAt(b, pos-n); err != nil {
			return 0
		}
		if i := strings.LastIndexByte(string(b), '\n'); i >= 0 {
			return pos - n + int64(i) + 1
		}
		pos -= n
	}
	return 0
}

// saveUsageEntry writes the entry atomically, stamping the current cache
// version so older entries are recognized and ignored, then
// opportunistically prunes.
func saveUsageEntry(stateDir, src string, e usageEntry) {
	e.Version = usageCacheVersion
	dir := filepath.Join(stateDir, "usage")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	path := usageCachePath(stateDir, src)
	tmp, err := os.CreateTemp(dir, ".usage.tmp*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return
	}
	pruneUsageCache(dir)
}

// pruneUsageCache keeps the cache directory bounded: once it outgrows
// usageCacheMaxEntries, entries whose source file no longer exists (session
// ended) are removed.
func pruneUsageCache(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= usageCacheMaxEntries {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var ent usageEntry
		if json.Unmarshal(b, &ent) != nil || ent.Src == "" {
			continue
		}
		if _, err := os.Stat(ent.Src); err != nil {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// ttlEntry is one TTL-gated CLI cache record.
type ttlEntry struct {
	RanAt  time.Time `json:"ranAt"`
	Result string    `json:"result"`
}

// runCachedTTL runs fn and caches its output for ttl, so an expensive CLI
// (e.g. hermes insights) runs at most once per window even though every
// poll is a fresh process. Within the window the stale output is reused
// as-is. key must be a safe filename fragment (no path separators).
func runCachedTTL(stateDir, key string, ttl time.Duration, fn func() (string, error)) (string, error) {
	path := filepath.Join(stateDir, "usage", "ttl-"+key+".json")
	if b, err := os.ReadFile(path); err == nil {
		var e ttlEntry
		if json.Unmarshal(b, &e) == nil && time.Since(e.RanAt) < ttl {
			return e.Result, nil
		}
	}
	out, err := fn()
	if err != nil {
		return "", err
	}
	if err := saveTTLEntry(path, ttlEntry{RanAt: time.Now(), Result: out}); err != nil {
		return out, err // cache write failure is non-fatal
	}
	return out, nil
}

func saveTTLEntry(path string, e ttlEntry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ttl.tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
