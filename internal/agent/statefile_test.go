package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	sf := NewState()
	sf.Agents = []AgentState{
		{PID: 100, Label: "Grok", Status: StatusWorking, CPU: 1234, IO: 5678, IdleStreak: 0, Pane: "main:0.0", CWD: "code/tmon", Usage: &Usage{TokensUsed: 13025, WindowTokens: 262144}},
	}
	if err := sf.Save(path); err != nil {
		t.Fatal(err)
	}

	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != StateFileVersion || len(got.Agents) != 1 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	a := got.Agents[0]
	if a.PID != 100 || a.Label != "Grok" || a.Status != StatusWorking || a.CPU != 1234 || a.IO != 5678 || a.Pane != "main:0.0" || a.CWD != "code/tmon" {
		t.Errorf("agent mismatch: %+v", a)
	}
	if a.Usage == nil || a.Usage.TokensUsed != 13025 || a.Usage.WindowTokens != 262144 {
		t.Errorf("usage mismatch: %+v", a.Usage)
	}
}

func TestLoadMissingFile(t *testing.T) {
	sf, err := LoadState(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sf.Agents) != 0 || sf.Version != StateFileVersion {
		t.Errorf("expected empty state, got %+v", sf)
	}
}

func TestLoadCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(path); err == nil {
		t.Error("expected error for corrupt state file")
	}
}

// A version-2 state file (no usage field) must load unchanged: the schema
// bump is backward compatible, and missing usage means "unknown".
func TestLoadV2File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	v2 := `{
  "version": 2,
  "agents": [
    {"pid": 101, "label": "Claude", "status": "working", "detail": "phase:responding", "title": "Fix build"}
  ]
}`
	if err := os.WriteFile(path, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	sf, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sf.Agents) != 1 || sf.Agents[0].PID != 101 || sf.Agents[0].Title != "Fix build" {
		t.Fatalf("v2 load mismatch: %+v", sf.Agents)
	}
	if sf.Agents[0].Usage != nil {
		t.Errorf("expected nil usage for v2 file, got %+v", sf.Agents[0].Usage)
	}
}

func TestUsageEmptyAndContextPct(t *testing.T) {
	if zero := (Usage{}); !zero.Empty() {
		t.Error("zero Usage should be Empty")
	}
	u := Usage{TokensUsed: 13025, WindowTokens: 262144}
	if u.Empty() {
		t.Error("Usage with tokens should not be Empty")
	}
	// 13025/262144 ≈ 4.97% → rounds to 5 (not truncated to 4).
	if got, want := u.ContextPct(), 5; got != want {
		t.Errorf("ContextPct = %d, want %d", got, want)
	}
	if got := (Usage{TokensUsed: 100}).ContextPct(); got != 0 {
		t.Errorf("ContextPct with unknown window = %d, want 0", got)
	}
	if got := (Usage{WindowTokens: 200}).ContextPct(); got != 0 {
		t.Errorf("ContextPct with zero tokens = %d, want 0", got)
	}
	// Sub-1% of a large window: truncate would show 0%; round matches CLI 1%.
	if got := (Usage{TokensUsed: 5336, WindowTokens: 1_000_000}).ContextPct(); got != 1 {
		t.Errorf("ContextPct(5336/1M) = %d, want 1 (rounded)", got)
	}
	if got := (Usage{TokensUsed: 4504, WindowTokens: 1_000_000}).ContextPct(); got != 0 {
		t.Errorf("ContextPct(4504/1M) = %d, want 0 (0.45%% rounds down)", got)
	}
}

func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	sf := NewState()
	sf.Agents = []AgentState{{PID: 1, Label: "Grok", Status: StatusIdle}}
	if err := sf.Save(path); err != nil {
		t.Fatal(err)
	}

	// No temp files may be left behind after a successful save.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "state.json" {
			t.Errorf("unexpected file left behind: %s", e.Name())
		}
	}
}
