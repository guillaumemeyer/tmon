// Package git resolves the git context (repository root and current branch)
// of a working directory using filesystem reads only. No git subprocess is
// used, so the status poll and the dashboard refresh stay fast. Pull-request
// lookup is the exception: it shells out to gh once per branch per cache
// window and caches the result.
package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Workspace describes the enclosing git repository of a directory.
type Workspace struct {
	// Root is the absolute path of the repository root (the directory that
	// contains .git, or the worktree root for a linked checkout).
	Root string
	// Branch is the current branch name; for a detached HEAD it is the
	// short commit SHA (first 7 characters).
	Branch string
}

// Find walks up from dir to locate the enclosing git repository. It returns
// ok=false when dir is not inside a repository (or the tree is unreadable).
// Linked worktrees (a .git file pointing at a gitdir) are resolved through
// the gitdir's HEAD.
func Find(dir string) (*Workspace, bool) {
	if dir == "" {
		return nil, false
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, false
	}
	for cur := abs; ; cur = filepath.Dir(cur) {
		if ws, ok := findAt(cur); ok {
			return ws, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return nil, false // reached the filesystem root
		}
	}
}

// findAt resolves the repository anchored at dir, or returns ok=false when
// dir has no .git entry.
func findAt(dir string) (*Workspace, bool) {
	gitPath := filepath.Join(dir, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return nil, false
	}
	head := filepath.Join(gitPath, "HEAD")
	if !info.IsDir() {
		// Linked worktree: .git is a file with a "gitdir: <path>" line.
		gitdir, ok := readGitdir(gitPath)
		if !ok {
			return nil, false
		}
		head = filepath.Join(gitdir, "HEAD")
	}
	branch, ok := readHead(head)
	if !ok {
		return nil, false
	}
	return &Workspace{Root: dir, Branch: branch}, true
}

// readGitdir parses the "gitdir: <path>" line of a linked-worktree .git
// file. Relative paths resolve against the directory that holds the file.
func readGitdir(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(b))
	rest, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return "", false
	}
	p := strings.TrimSpace(rest)
	if p == "" {
		return "", false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(filepath.Dir(path), p)
	}
	return filepath.Clean(p), true
}

// readHead reads and parses a git HEAD file.
func readHead(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return parseHead(string(b)), true
}

// parseHead converts a HEAD file's content into the branch name: a symbolic
// ref "ref: refs/heads/main" becomes "main"; anything else (a detached
// commit SHA) becomes its short form.
func parseHead(content string) string {
	line := strings.TrimSpace(content)
	if rest, ok := strings.CutPrefix(line, "ref:"); ok {
		ref := strings.TrimSpace(rest)
		if branch, ok := strings.CutPrefix(ref, "refs/heads/"); ok && branch != "" {
			return branch
		}
		return ref
	}
	if len(line) >= 7 {
		return line[:7]
	}
	return line
}

// PR describes an open pull request for a branch.
type PR struct {
	// Number is the pull request number, e.g. "42".
	Number string
	// Title is the pull request title.
	Title string
}

// DefaultPRTTL is how long a gh lookup result stays cached. The dashboard
// refreshes every ~1.5s, so the TTL keeps gh from being spawned more than
// once per branch per minute.
const DefaultPRTTL = time.Minute

// runGH is the gh runner seam. Tests replace it with a fake.
var runGH = func(dir string, args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

// ghCheck reports whether gh is on PATH. It is a var so tests can stub it.
var ghCheck = func() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

var (
	ghOnce   sync.Once
	ghCached bool
)

// ghOnPath reports whether gh is available, caching the answer after the
// first call so PRFor does not walk PATH on every refresh.
func ghOnPath() bool {
	ghOnce.Do(func() { ghCached = ghCheck() })
	return ghCached
}

// prEntry is one cached lookup result.
type prEntry struct {
	pr PR
	ok bool
	at time.Time
}

// prCache bounds gh spawns across refreshes and agents. Entries expire by
// TTL; the map is pruned when it grows past prCacheMax.
var prCache = struct {
	sync.Mutex
	entries map[string]prEntry
}{entries: make(map[string]prEntry)}

// prCacheMax bounds the number of cached lookups before pruning.
const prCacheMax = 512

// PRFor returns the open pull request for branch in the repository rooted
// at root. It is best-effort: ok=false when gh is missing, the lookup fails,
// or no open PR exists. Results (hits and misses) are cached for ttl so
// repeated refreshes stay cheap.
func PRFor(root, branch string, ttl time.Duration) (PR, bool) {
	if !ghOnPath() {
		return PR{}, false
	}
	key := root + "\x00" + branch
	now := time.Now()

	prCache.Lock()
	if e, ok := prCache.entries[key]; ok && now.Sub(e.at) < ttl {
		prCache.Unlock()
		return e.pr, e.ok
	}
	prCache.Unlock()

	pr, ok := lookupPR(root, branch)
	prCache.Lock()
	prunePRCache(now)
	prCache.entries[key] = prEntry{pr: pr, ok: ok, at: now}
	prCache.Unlock()
	return pr, ok
}

// prunePRCache drops expired entries when the cache grows past its cap, so
// a long-lived dashboard never accumulates unbounded state.
func prunePRCache(now time.Time) {
	if len(prCache.entries) < prCacheMax {
		return
	}
	for k, e := range prCache.entries {
		if now.Sub(e.at) >= time.Minute {
			delete(prCache.entries, k)
		}
	}
	if len(prCache.entries) >= prCacheMax {
		prCache.entries = make(map[string]prEntry)
	}
}

// lookupPR runs gh once for the branch. A malformed or empty answer is a
// miss. An empty PR list makes jq's .[0] yield null, which stringifies to
// "null\tnull"; the // empty clause turns that into no output at all, and
// the "null" guard covers older gh/jq combinations that still print it.
func lookupPR(root, branch string) (PR, bool) {
	out, err := runGH(root, "pr", "list", "--head", branch,
		"--json", "number,title", `--jq`, `.[0] // empty | "\(.number)\t\(.title)"`)
	if err != nil {
		return PR{}, false
	}
	num, title, ok := strings.Cut(strings.TrimSpace(out), "\t")
	if !ok || num == "" || num == "null" {
		return PR{}, false
	}
	return PR{Number: num, Title: title}, true
}
