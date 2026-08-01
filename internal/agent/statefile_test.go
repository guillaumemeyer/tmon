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
	sf.Frame = 7
	sf.Agents = []AgentState{
		{PID: 100, Label: "Grok", Status: StatusActive, CPU: 1234, IO: 5678, IdleStreak: 0, Pane: "main:0.0", CWD: "code/tmon"},
	}
	if err := sf.Save(path); err != nil {
		t.Fatal(err)
	}

	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != StateFileVersion || got.Frame != 7 || len(got.Agents) != 1 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	a := got.Agents[0]
	if a.PID != 100 || a.Label != "Grok" || a.Status != StatusActive || a.CPU != 1234 || a.IO != 5678 || a.Pane != "main:0.0" || a.CWD != "code/tmon" {
		t.Errorf("agent mismatch: %+v", a)
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

func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	sf := NewState()
	sf.Agents = []AgentState{{PID: 1, Label: "Grok", Status: StatusRunning}}
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
