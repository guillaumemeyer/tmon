package worker

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// HeartbeatStaleAfter is how old a heartbeat may be before the worker is
// considered dead and respawnable. The worker writes one every Cycle, so a
// heartbeat younger than this means a live worker. A var so tests can
// shorten it.
var HeartbeatStaleAfter = 2 * Cycle

// HeartbeatPath is <state>/usage/heartbeat, a small file holding the unix
// timestamp of the worker's last cycle.
func HeartbeatPath(stateDir string) string {
	return filepath.Join(stateDir, "usage", "heartbeat")
}

// ReadHeartbeat returns the last heartbeat time. A missing or corrupt file
// returns an error.
func ReadHeartbeat(stateDir string) (time.Time, error) {
	b, err := os.ReadFile(HeartbeatPath(stateDir))
	if err != nil {
		return time.Time{}, err
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(secs, 0), nil
}

// WriteHeartbeat stamps the heartbeat file with the current time.
func WriteHeartbeat(stateDir string) error {
	dir := filepath.Join(stateDir, "usage")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(HeartbeatPath(stateDir), []byte(strconv.FormatInt(time.Now().Unix(), 10)+"\n"), 0o644)
}

// HeartbeatFresh reports whether a heartbeat exists and is younger than
// HeartbeatStaleAfter. A fresh heartbeat means a worker is alive.
func HeartbeatFresh(stateDir string) bool {
	hb, err := ReadHeartbeat(stateDir)
	if err != nil {
		return false
	}
	return time.Since(hb) < HeartbeatStaleAfter
}
