package connector

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

// fixturePrimeSession mirrors the live daemon-list row shape.
func fixturePrimeSession(sessionFile string) primeSession {
	return primeSession{
		ID:             "abc123",
		SessionFile:    sessionFile,
		Lifecycle:      "live",
		RuntimeKind:    "top-level",
		Activity:       "idle",
		TaskState:      "needs_input",
		LastActivityAt: "2026-08-07T19:36:20.208Z",
		CWD:            "/home/user/code/project",
		WorkerPID:      258362,
		FirstMessage:   "how did you get your deepseek credentials?",
		Model: primeModel{
			ID:            "deepseek-v4-pro",
			Provider:      "deepseek",
			ContextWindow: 1000000,
		},
	}
}

func resetPrimeList() {
	primeListMu.Lock()
	primeListCache = nil
	primeListAt = time.Time{}
	primeListMu.Unlock()
}

func writePrimeJSONL(t *testing.T, path string) {
	t.Helper()
	lines := []string{
		`{"type":"session","id":"s1","cwd":"/home/user/code/project"}`,
		`{"type":"message","id":"m1","message":{"role":"user","content":[]}}`,
		`{"type":"message","id":"m2","message":{"role":"assistant","content":[],"usage":{"input":4453,"output":300,"cacheRead":0,"cacheWrite":0,"totalTokens":4753}}}`,
		`{"type":"message","id":"m3","message":{"role":"assistant","content":[],"usage":{"input":48,"output":197,"cacheRead":4736,"cacheWrite":0,"totalTokens":4981}}}`,
		`{"type":"agent_status","id":"a1","status":{"taskState":"needs_input"}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPrimeEnabled(t *testing.T) {
	oldHome, oldPath := primeHome, primeOnPath
	defer func() { primeHome, primeOnPath = oldHome, oldPath }()

	primeOnPath = func() bool { return false }

	dir := t.TempDir()
	primeHome = func() string { return dir }
	if !(Prime{}).Enabled(config.Config{}) {
		t.Error("Enabled = false with agent dir present, want true")
	}

	primeHome = func() string { return filepath.Join(dir, "missing") }
	if (Prime{}).Enabled(config.Config{}) {
		t.Error("Enabled = true without agent dir or CLI, want false")
	}

	primeOnPath = func() bool { return true }
	if !(Prime{}).Enabled(config.Config{}) {
		t.Error("Enabled = false with CLI on PATH, want true")
	}
}

func TestPrimeListSessionsTTL(t *testing.T) {
	resetPrimeList()
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "daemon.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	oldSock, oldRun := primeSocketPath, primeListJSON
	primeSocketPath = func() string { return sock }
	defer func() { primeSocketPath, primeListJSON = oldSock, oldRun }()

	calls := 0
	primeListJSON = func() ([]primeSession, error) {
		calls++
		return []primeSession{fixturePrimeSession("")}, nil
	}

	if got := primeListSessions(); len(got) != 1 {
		t.Fatalf("first call: got %d sessions, want 1", len(got))
	}
	if got := primeListSessions(); len(got) != 1 {
		t.Fatalf("cached call: got %d sessions, want 1", len(got))
	}
	if calls != 1 {
		t.Fatalf("spawned %d times within TTL, want 1", calls)
	}

	primeListMu.Lock()
	primeListAt = time.Now().Add(-primeListTTL - time.Second)
	primeListMu.Unlock()

	if got := primeListSessions(); len(got) != 1 {
		t.Fatalf("expired call: got %d sessions, want 1", len(got))
	}
	if calls != 2 {
		t.Fatalf("spawned %d times total, want 2", calls)
	}
}

func TestPrimeListSessionsSocketGate(t *testing.T) {
	resetPrimeList()
	oldSock, oldRun := primeSocketPath, primeListJSON
	primeSocketPath = func() string { return filepath.Join(t.TempDir(), "missing.sock") }
	defer func() { primeSocketPath, primeListJSON = oldSock, oldRun }()

	calls := 0
	primeListJSON = func() ([]primeSession, error) {
		calls++
		return nil, nil
	}
	if got := primeListSessions(); got != nil {
		t.Fatalf("got %d sessions with no socket, want nil", len(got))
	}
	if calls != 0 {
		t.Fatalf("spawned %d times with no socket, want 0", calls)
	}
}

func TestPrimeListSessionsFailureGated(t *testing.T) {
	resetPrimeList()
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "daemon.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	oldSock, oldRun := primeSocketPath, primeListJSON
	primeSocketPath = func() string { return sock }
	defer func() { primeSocketPath, primeListJSON = oldSock, oldRun }()

	calls := 0
	primeListJSON = func() ([]primeSession, error) {
		calls++
		return nil, fmt.Errorf("daemon unreachable")
	}
	if got := primeListSessions(); got != nil {
		t.Fatalf("first call: want nil, got %d sessions", len(got))
	}
	if got := primeListSessions(); got != nil {
		t.Fatalf("second call: want nil (failure cached), got %d sessions", len(got))
	}
	if calls != 1 {
		t.Fatalf("spawned %d times after failure, want 1", calls)
	}
}

func TestPrimeRecord(t *testing.T) {
	now := time.Now()

	t.Run("needs_input idle with client pid", func(t *testing.T) {
		rec := primeRecord(fixturePrimeSession(""), 42, primeHookState{}, now)
		if rec.PID != 42 {
			t.Errorf("PID = %d, want 42 (client)", rec.PID)
		}
		if rec.Status != agent.StatusIdle {
			t.Errorf("Status = %q, want idle", rec.Status)
		}
		if rec.Detail != "needs:input · model:deepseek/deepseek-v4-pro" {
			t.Errorf("Detail = %q", rec.Detail)
		}
		if rec.CWD != "code/project" {
			t.Errorf("CWD = %q, want code/project", rec.CWD)
		}
		if rec.Title != "how did you get your deepseek credentials?" {
			t.Errorf("Title = %q", rec.Title)
		}
		want, _ := time.Parse(time.RFC3339Nano, "2026-08-07T19:36:20.208Z")
		if !rec.At.Equal(want) {
			t.Errorf("At = %v, want %v (lastActivityAt)", rec.At, want)
		}
		if rec.Usage.WindowTokens != 1000000 {
			t.Errorf("WindowTokens = %d, want 1000000", rec.Usage.WindowTokens)
		}
	})

	t.Run("detached falls back to worker pid", func(t *testing.T) {
		rec := primeRecord(fixturePrimeSession(""), 0, primeHookState{}, now)
		if rec.PID != 258362 {
			t.Errorf("PID = %d, want worker 258362", rec.PID)
		}
	})

	t.Run("working streaming with parsed At", func(t *testing.T) {
		s := fixturePrimeSession("")
		s.Activity = "working"
		s.IsStreaming = true
		rec := primeRecord(s, 0, primeHookState{}, now)
		if rec.Status != agent.StatusWorking {
			t.Errorf("Status = %q, want working", rec.Status)
		}
		if rec.Detail != "streaming · model:deepseek/deepseek-v4-pro" {
			t.Errorf("Detail = %q", rec.Detail)
		}
		want, _ := time.Parse(time.RFC3339Nano, "2026-08-07T19:36:20.208Z")
		if !rec.At.Equal(want) {
			t.Errorf("At = %v, want %v", rec.At, want)
		}
	})

	t.Run("idle completed", func(t *testing.T) {
		s := fixturePrimeSession("")
		s.TaskState = "completed"
		rec := primeRecord(s, 0, primeHookState{}, now)
		if rec.Status != agent.StatusIdle {
			t.Errorf("Status = %q, want idle", rec.Status)
		}
		if rec.Detail != "turn-complete · model:deepseek/deepseek-v4-pro" {
			t.Errorf("Detail = %q", rec.Detail)
		}
	})

	t.Run("session name wins over first message", func(t *testing.T) {
		s := fixturePrimeSession("")
		s.SessionName = "Refactor the CLI"
		rec := primeRecord(s, 0, primeHookState{}, now)
		if rec.Title != "Refactor the CLI" {
			t.Errorf("Title = %q, want session name", rec.Title)
		}
	})

	t.Run("usage from jsonl tail", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "session.jsonl")
		writePrimeJSONL(t, f)
		rec := primeRecord(fixturePrimeSession(f), 0, primeHookState{}, now)
		if rec.Usage.TokensUsed != 4784 {
			t.Errorf("TokensUsed = %d, want 4784 (48 input + 4736 cacheRead)", rec.Usage.TokensUsed)
		}
		if rec.Usage.WindowTokens != 1000000 {
			t.Errorf("WindowTokens = %d, want 1000000", rec.Usage.WindowTokens)
		}
	})
}

func TestPrimeHookJoin(t *testing.T) {
	now := time.Now()

	workingBase := func() primeSession {
		s := fixturePrimeSession("")
		s.Activity = "working"
		s.IsBashRunning = true
		return s
	}

	t.Run("blocked approval wins over native", func(t *testing.T) {
		rec := primeRecord(workingBase(), 0, primeHookState{status: agent.StatusBlocked, detail: "approval:bash", valid: true}, now)
		if rec.Status != agent.StatusBlocked {
			t.Errorf("Status = %q, want blocked", rec.Status)
		}
		if rec.Detail != "approval:bash · model:deepseek/deepseek-v4-pro" {
			t.Errorf("Detail = %q", rec.Detail)
		}
	})

	t.Run("working tool name wins", func(t *testing.T) {
		rec := primeRecord(workingBase(), 0, primeHookState{status: agent.StatusWorking, detail: "tool:write", valid: true}, now)
		if rec.Status != agent.StatusWorking {
			t.Errorf("Status = %q, want working", rec.Status)
		}
		if rec.Detail != "tool:write · model:deepseek/deepseek-v4-pro" {
			t.Errorf("Detail = %q", rec.Detail)
		}
	})

	t.Run("idle hook corrects stale working", func(t *testing.T) {
		rec := primeRecord(workingBase(), 0, primeHookState{status: agent.StatusIdle, detail: "turn-complete", valid: true}, now)
		if rec.Status != agent.StatusIdle {
			t.Errorf("Status = %q, want idle", rec.Status)
		}
		if rec.Detail != "turn-complete · model:deepseek/deepseek-v4-pro" {
			t.Errorf("Detail = %q", rec.Detail)
		}
	})

	t.Run("idle hook replaces native needs_input detail", func(t *testing.T) {
		rec := primeRecord(fixturePrimeSession(""), 0, primeHookState{status: agent.StatusIdle, detail: "turn-complete", valid: true}, now)
		if rec.Status != agent.StatusIdle {
			t.Errorf("Status = %q, want idle", rec.Status)
		}
		if rec.Detail != "turn-complete · model:deepseek/deepseek-v4-pro" {
			t.Errorf("Detail = %q", rec.Detail)
		}
	})
}

func TestPrimeLastAssistantTokens(t *testing.T) {
	f := filepath.Join(t.TempDir(), "session.jsonl")
	writePrimeJSONL(t, f)
	if got := primeLastAssistantTokens(f); got != 4784 {
		t.Errorf("primeLastAssistantTokens = %d, want 4784", got)
	}
	if got := primeLastAssistantTokens(filepath.Join(t.TempDir(), "missing.jsonl")); got != 0 {
		t.Errorf("missing file: got %d, want 0", got)
	}
}

func TestPrimeClientsByCWD(t *testing.T) {
	oldPIDs, oldCmd, oldCWD, oldTTY := primeListPIDs, primeReadCmdline, primeReadCWD, primeTTYForPID
	defer func() {
		primeListPIDs, primeReadCmdline, primeReadCWD, primeTTYForPID = oldPIDs, oldCmd, oldCWD, oldTTY
	}()

	primeListPIDs = func() ([]int, error) { return []int{11, 22, 33}, nil }
	primeReadCmdline = func(pid int) (string, error) {
		if pid == 22 {
			return "node server.js", nil // not a prime process
		}
		return "prime-agent", nil
	}
	primeReadCWD = func(pid int) (string, error) {
		switch pid {
		case 11:
			return "/home/user/a", nil
		case 33:
			return "/home/user/b", nil
		}
		return "", os.ErrNotExist
	}
	primeTTYForPID = func(pid int) string {
		switch pid {
		case 11:
			return "/dev/pts/4"
		case 22:
			return "/dev/pts/5"
		default:
			return "" // 33: worker without a tty
		}
	}

	got := primeClientsByCWD()
	if len(got) != 1 || got["/home/user/a"] != 11 {
		t.Errorf("primeClientsByCWD = %v, want {/home/user/a: 11}", got)
	}
}

func TestPrimeProbe(t *testing.T) {
	resetPrimeList()
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "daemon.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	oldSock, oldRun := primeSocketPath, primeListJSON
	oldPIDs, oldCmd, oldCWD, oldTTY := primeListPIDs, primeReadCmdline, primeReadCWD, primeTTYForPID
	defer func() {
		primeSocketPath, primeListJSON = oldSock, oldRun
		primeListPIDs, primeReadCmdline, primeReadCWD, primeTTYForPID = oldPIDs, oldCmd, oldCWD, oldTTY
	}()
	primeSocketPath = func() string { return sock }

	s1 := fixturePrimeSession(filepath.Join(tmp, "s1.jsonl"))
	s2 := fixturePrimeSession("")
	s2.TaskState = "completed"
	s2.FirstMessage = ""
	s2.CWD = "/home/user/code/other"

	primeListJSON = func() ([]primeSession, error) { return []primeSession{s1, s2}, nil }
	primeListPIDs = func() ([]int, error) { return []int{11}, nil }
	primeReadCmdline = func(pid int) (string, error) { return "prime-agent", nil }
	primeReadCWD = func(pid int) (string, error) {
		if pid == 11 {
			return "/home/user/code/project", nil
		}
		return "", os.ErrNotExist
	}
	primeTTYForPID = func(pid int) string {
		if pid == 11 {
			return "/dev/pts/4"
		}
		return ""
	}

	hooks := filepath.Join(tmp, "hooks", "prime")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	hookBody := fmt.Sprintf(`{"status":"working","detail":"tool:write","sessionFile":%q}`, s1.SessionFile)
	if err := os.WriteFile(filepath.Join(hooks, "hash.json"), []byte(hookBody), 0o600); err != nil {
		t.Fatal(err)
	}

	recs, err := (Prime{}).Probe(config.Config{HookStateDir: filepath.Join(tmp, "hooks")})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	if recs[0].PID != 11 || recs[1].PID != 258362 {
		t.Errorf("PIDs = [%d %d], want [11 258362]", recs[0].PID, recs[1].PID)
	}
	if recs[0].Status != agent.StatusWorking || recs[0].Detail != "tool:write · model:deepseek/deepseek-v4-pro" {
		t.Errorf("s1 = %q %q, want working tool:write", recs[0].Status, recs[0].Detail)
	}
	if recs[1].Status != agent.StatusIdle || !strings.HasPrefix(recs[1].Detail, "turn-complete") {
		t.Errorf("s2 = %q %q, want idle turn-complete", recs[1].Status, recs[1].Detail)
	}
	if recs[1].Title != "" {
		t.Errorf("s2 Title = %q, want empty (no name, no first message)", recs[1].Title)
	}
}
