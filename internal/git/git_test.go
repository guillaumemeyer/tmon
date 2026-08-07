package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makeRepo creates a repository dir with a .git/HEAD for the given branch.
func makeRepo(t *testing.T, root, branch string) {
	t.Helper()
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(filepath.Join(gitDir, "refs", "heads"), 0o755); err != nil {
		t.Fatal(err)
	}
	head := "ref: refs/heads/" + branch + "\n"
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindBranchFromSubdir(t *testing.T) {
	root := t.TempDir()
	makeRepo(t, root, "feat/x")
	sub := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	ws, ok := Find(sub)
	if !ok {
		t.Fatal("Find = not ok, want ok")
	}
	if ws.Root != root {
		t.Errorf("Root = %q, want %q", ws.Root, root)
	}
	if ws.Branch != "feat/x" {
		t.Errorf("Branch = %q, want feat/x", ws.Branch)
	}
}

func TestFindAtRoot(t *testing.T) {
	root := t.TempDir()
	makeRepo(t, root, "main")
	ws, ok := Find(root)
	if !ok {
		t.Fatal("Find = not ok, want ok")
	}
	if ws.Branch != "main" {
		t.Errorf("Branch = %q, want main", ws.Branch)
	}
}

func TestFindLinkedWorktree(t *testing.T) {
	base := t.TempDir()
	main := filepath.Join(base, "main")
	makeRepo(t, main, "main")

	// A linked worktree: .git is a file whose "gitdir:" points at a gitdir
	// that carries its own HEAD (mirrors git's worktrees layout).
	wt := filepath.Join(base, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	gitdir := filepath.Join(main, ".git", "worktrees", "wt")
	if err := os.MkdirAll(gitdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitdir, "HEAD"), []byte("ref: refs/heads/wt-branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, ok := Find(wt)
	if !ok {
		t.Fatal("Find(worktree) = not ok, want ok")
	}
	if ws.Root != wt {
		t.Errorf("Root = %q, want worktree dir %q", ws.Root, wt)
	}
	if ws.Branch != "wt-branch" {
		t.Errorf("Branch = %q, want wt-branch", ws.Branch)
	}
}

func TestFindLinkedWorktreeRelativeGitdir(t *testing.T) {
	base := t.TempDir()
	wt := filepath.Join(base, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	gitdir := filepath.Join(base, "gitdirs", "wt")
	if err := os.MkdirAll(gitdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitdir, "HEAD"), []byte("ref: refs/heads/rel\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// gitdir: is relative to the directory holding the .git file.
	rel := filepath.Join("..", "gitdirs", "wt")
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+rel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, ok := Find(wt)
	if !ok {
		t.Fatal("Find = not ok, want ok")
	}
	if ws.Branch != "rel" {
		t.Errorf("Branch = %q, want rel", ws.Branch)
	}
}

func TestFindDetachedHead(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sha := "0123456789abcdef0123456789abcdef01234567"
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(sha+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, ok := Find(root)
	if !ok {
		t.Fatal("Find = not ok, want ok")
	}
	if ws.Branch != "0123456" {
		t.Errorf("Branch = %q, want short SHA 0123456", ws.Branch)
	}
}

func TestFindNotARepo(t *testing.T) {
	dir := t.TempDir()
	if ws, ok := Find(dir); ok {
		t.Fatalf("Find = %+v, want not ok outside a repository", ws)
	}
}

func TestFindEmptyDir(t *testing.T) {
	if ws, ok := Find(""); ok {
		t.Fatalf("Find(\"\") = %+v, want not ok", ws)
	}
}

func TestParseHead(t *testing.T) {
	cases := map[string]string{
		"ref: refs/heads/main\n":                     "main",
		"ref: refs/heads/feat/x\n":                   "feat/x",
		"ref: refs/heads/feature branch\n":           "feature branch",
		"0123456789abcdef0123456789abcdef01234567\n": "0123456",
		"abc\n": "abc",
	}
	for in, want := range cases {
		if got := parseHead(in); got != want {
			t.Errorf("parseHead(%q) = %q, want %q", in, got, want)
		}
	}
}

// resetGHCache forces the next gh availability check to re-run, so tests
// can flip the seam between cases.
func resetGHCache() {
	ghMu.Lock()
	ghCached = false
	ghAt = time.Time{}
	ghMu.Unlock()
}

// resetPRCache clears cached lookups between tests.
func resetPRCache() {
	prCache.Lock()
	prCache.entries = make(map[string]prEntry)
	prCache.Unlock()
}

func TestPRForHit(t *testing.T) {
	resetGHCache()
	resetPRCache()
	ghCheck = func() bool { return true }
	oldRun := runGH
	calls := 0
	runGH = func(ctx context.Context, dir string, args ...string) (string, error) {
		calls++
		if dir != "/repo" {
			t.Errorf("gh dir = %q, want /repo", dir)
		}
		return "42\tFix the thing\n", nil
	}
	t.Cleanup(func() { runGH = oldRun })

	pr, ok := PRFor("/repo", "feat", time.Minute)
	if !ok {
		t.Fatal("PRFor = miss, want hit")
	}
	if pr.Number != "42" || pr.Title != "Fix the thing" {
		t.Errorf("PR = %+v, want #42 Fix the thing", pr)
	}

	// Cache hit: no second gh call.
	if _, ok := PRFor("/repo", "feat", time.Minute); !ok {
		t.Fatal("cached PRFor = miss, want hit")
	}
	if calls != 1 {
		t.Fatalf("gh calls = %d, want 1 (cache hit)", calls)
	}
}

func TestPRForCacheMiss(t *testing.T) {
	resetGHCache()
	resetPRCache()
	ghCheck = func() bool { return true }
	oldRun := runGH
	calls := 0
	runGH = func(ctx context.Context, dir string, args ...string) (string, error) {
		calls++
		return "", nil // gh succeeded but no open PR
	}
	t.Cleanup(func() { runGH = oldRun })

	if _, ok := PRFor("/repo", "feat", time.Minute); ok {
		t.Fatal("PRFor = hit, want miss (no open PR)")
	}
	// Misses are cached too.
	PRFor("/repo", "feat", time.Minute)
	if calls != 1 {
		t.Fatalf("gh calls = %d, want 1 (miss cached)", calls)
	}
}

func TestPRForNoOpenPRIsNull(t *testing.T) {
	// gh + jq emit "null\tnull" when the branch has no open PRs; that is
	// a miss, not a PR numbered "null".
	resetGHCache()
	resetPRCache()
	ghCheck = func() bool { return true }
	oldRun := runGH
	runGH = func(ctx context.Context, dir string, args ...string) (string, error) {
		return "null\tnull\n", nil
	}
	t.Cleanup(func() { runGH = oldRun })

	if _, ok := PRFor("/repo", "feat", time.Minute); ok {
		t.Fatal("PRFor = hit, want miss (no open PR, jq null)")
	}
}

func TestPRForGHError(t *testing.T) {
	resetGHCache()
	resetPRCache()
	ghCheck = func() bool { return true }
	oldRun := runGH
	runGH = func(ctx context.Context, dir string, args ...string) (string, error) {
		return "", os.ErrPermission
	}
	t.Cleanup(func() { runGH = oldRun })

	if _, ok := PRFor("/repo", "feat", time.Minute); ok {
		t.Fatal("PRFor = hit, want miss (gh errored)")
	}
}

func TestGHOnPathRechecks(t *testing.T) {
	resetGHCache()
	oldCheck := ghCheck
	ghCheck = func() bool { return true }
	t.Cleanup(func() { ghCheck = oldCheck })

	if !ghOnPath() {
		t.Fatal("ghOnPath = false, want true (gh on PATH)")
	}
	// Before the TTL elapses the answer stays cached: flipping the seam
	// must not re-run the PATH walk.
	ghCheck = func() bool { return false }
	if !ghOnPath() {
		t.Fatal("ghOnPath re-ran before the TTL, want cached answer")
	}
	// Once the TTL expires the answer self-heals, so a gh installed (or
	// removed) mid-session is noticed without a restart.
	ghMu.Lock()
	ghAt = time.Time{}
	ghMu.Unlock()
	if ghOnPath() {
		t.Fatal("ghOnPath = true after TTL re-check, want false (gh gone)")
	}
}

func TestPRForNoGH(t *testing.T) {
	resetGHCache()
	resetPRCache()
	ghCheck = func() bool { return false }
	oldRun := runGH
	runGH = func(ctx context.Context, dir string, args ...string) (string, error) {
		t.Error("gh must not run when unavailable")
		return "", nil
	}
	t.Cleanup(func() { runGH = oldRun })

	if _, ok := PRFor("/repo", "feat", time.Minute); ok {
		t.Fatal("PRFor = hit, want miss (gh absent)")
	}
}

func TestPRForTTLExpiry(t *testing.T) {
	resetGHCache()
	resetPRCache()
	ghCheck = func() bool { return true }
	oldRun := runGH
	calls := 0
	runGH = func(ctx context.Context, dir string, args ...string) (string, error) {
		calls++
		return "7\told\n", nil
	}
	t.Cleanup(func() { runGH = oldRun })

	if _, ok := PRFor("/repo", "feat", time.Millisecond); !ok {
		t.Fatal("first PRFor = miss, want hit")
	}
	time.Sleep(2 * time.Millisecond)
	if _, ok := PRFor("/repo", "feat", time.Millisecond); !ok {
		t.Fatal("second PRFor = miss, want hit")
	}
	if calls != 2 {
		t.Fatalf("gh calls = %d, want 2 (TTL expired)", calls)
	}
}

// TestPRForHangingGHBounded runs the real gh runner against a fake gh that
// never returns. The lookup must come back (as a miss) once ghTimeout
// elapses — a wedged gh must not hang PRFor, because the dashboard runs the
// loader on its event loop and an unbounded subprocess freezes the popup.
func TestPRForHangingGHBounded(t *testing.T) {
	oldTimeout := ghTimeout
	ghTimeout = 200 * time.Millisecond
	t.Cleanup(func() { ghTimeout = oldTimeout })

	// The fake gh hangs; the real runner must find it on PATH and kill it
	// when the context deadline fires. Earlier tests leave ghCheck stubbed,
	// so pin it to true here to force the lookup through the real runner.
	bin := t.TempDir()
	gh := filepath.Join(bin, "gh")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	resetGHCache()
	resetPRCache()
	oldCheck := ghCheck
	ghCheck = func() bool { return true }
	t.Cleanup(func() { ghCheck = oldCheck })

	start := time.Now()
	if _, ok := PRFor("/repo", "feat", time.Minute); ok {
		t.Fatal("PRFor = hit, want miss (gh hung and was killed)")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("PRFor took %v, want it bounded by ghTimeout", d)
	}
}
