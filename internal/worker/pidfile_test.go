package worker

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestAcquirePidLockExclusive(t *testing.T) {
	dir := t.TempDir()
	release, err := acquirePidLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	// A second acquire in the same process must fail: flock is per open
	// file description, so the duplicate open cannot take the lock.
	if _, err := acquirePidLock(dir); err != errAlreadyRunning {
		t.Fatalf("second acquire error = %v, want errAlreadyRunning", err)
	}
}

func TestAcquirePidLockWritesPid(t *testing.T) {
	dir := t.TempDir()
	release, err := acquirePidLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	b, err := os.ReadFile(PidPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		t.Fatalf("pid file = %q, want a positive integer", b)
	}
}

func TestAcquirePidLockRelease(t *testing.T) {
	dir := t.TempDir()
	release, err := acquirePidLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	release()
	// After release the lock is free again.
	release2, err := acquirePidLock(dir)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	release2()
}
