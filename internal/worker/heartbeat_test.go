package worker

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestHeartbeatRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := WriteHeartbeat(dir); err != nil {
		t.Fatal(err)
	}
	hb, err := ReadHeartbeat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(hb) > time.Minute {
		t.Errorf("heartbeat %v too old, want ~now", hb)
	}
}

func TestReadHeartbeatMissing(t *testing.T) {
	if _, err := ReadHeartbeat(t.TempDir()); err == nil {
		t.Fatal("missing heartbeat: want an error")
	}
}

func TestHeartbeatFresh(t *testing.T) {
	dir := t.TempDir()
	if HeartbeatFresh(dir) {
		t.Fatal("no heartbeat file: must not be fresh")
	}
	if err := WriteHeartbeat(dir); err != nil {
		t.Fatal(err)
	}
	if !HeartbeatFresh(dir) {
		t.Fatal("fresh heartbeat: must be fresh")
	}
	// Stamp a stale heartbeat and shorten the threshold.
	if err := os.WriteFile(HeartbeatPath(dir), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if HeartbeatFresh(dir) {
		t.Fatal("1970 heartbeat: must be stale")
	}
	// A heartbeat at the very edge of the threshold is still fresh, and one
	// just past it is stale. (Timestamp 0 would be ~56 years old — past any
	// threshold.)
	old := HeartbeatStaleAfter
	HeartbeatStaleAfter = time.Hour
	t.Cleanup(func() { HeartbeatStaleAfter = old })
	edge := time.Now().Add(-30 * time.Minute).Unix()
	if err := os.WriteFile(filepath.Join(dir, "usage", "heartbeat"), []byte(strconv.FormatInt(edge, 10)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !HeartbeatFresh(dir) {
		t.Fatal("heartbeat within threshold: must be fresh")
	}
	past := time.Now().Add(-2 * time.Hour).Unix()
	if err := os.WriteFile(filepath.Join(dir, "usage", "heartbeat"), []byte(strconv.FormatInt(past, 10)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if HeartbeatFresh(dir) {
		t.Fatal("heartbeat past the threshold: must be stale")
	}
}

func TestHeartbeatCorrupt(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "usage")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "heartbeat"), []byte("not-a-time"), 0o644); err != nil {
		t.Fatal(err)
	}
	if HeartbeatFresh(t.TempDir()) {
		t.Fatal("corrupt heartbeat: must not be fresh")
	}
}
