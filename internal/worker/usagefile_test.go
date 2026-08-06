package worker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUsageFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := UsageFile{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC),
		DeviceID:      "hostname",
		Quota: map[string]Quota{
			"claude": {Pct: 38, Label: "Session (5-hour)", ResetAt: "2026-08-06T14:00:00Z", Tier: "Max 20x"},
			"codex":  {Pct: 12, Label: "Weekly (7-day)", ResetAt: "2026-08-09T00:00:00Z", Tier: "Pro"},
		},
	}
	if err := SaveUsageFile(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadUsageFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != SchemaVersion {
		t.Errorf("schemaVersion = %d, want %d", got.SchemaVersion, SchemaVersion)
	}
	if got.DeviceID != "hostname" {
		t.Errorf("deviceId = %q, want hostname", got.DeviceID)
	}
	cl := got.Quota["claude"]
	if cl.Pct != 38 || cl.Label != "Session (5-hour)" || cl.ResetAt != "2026-08-06T14:00:00Z" {
		t.Errorf("claude quota = %+v", cl)
	}
	if _, ok := got.Quota["codex"]; !ok {
		t.Errorf("missing codex quota: %+v", got.Quota)
	}
}

func TestLoadUsageFileMissing(t *testing.T) {
	if _, err := LoadUsageFile(t.TempDir()); !os.IsNotExist(err) {
		t.Fatalf("missing file error = %v, want os.ErrNotExist", err)
	}
}

func TestLoadUsageFileCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "usage.json"), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUsageFile(dir); err == nil {
		t.Fatal("corrupt usage.json: want an error")
	}
}

func TestLoadUsageFileWrongVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "usage.json"), []byte(`{"schemaVersion": 99}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUsageFile(dir); err == nil {
		t.Fatal("wrong schema version: want an error")
	}
}

func TestSaveUsageFileStampsVersion(t *testing.T) {
	dir := t.TempDir()
	if err := SaveUsageFile(dir, UsageFile{SchemaVersion: 0}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadUsageFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != SchemaVersion {
		t.Errorf("schemaVersion = %d, want %d (stamped on save)", got.SchemaVersion, SchemaVersion)
	}
}
