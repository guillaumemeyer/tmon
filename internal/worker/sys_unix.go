//go:build linux || darwin

package worker

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// errAlreadyRunning is returned when the worker pid lock is held by another
// process.
var errAlreadyRunning = errors.New("worker already running")

// acquirePidLock flocks <state>/usage/worker.pid and writes the current pid
// into it. The returned func releases the lock and closes the file; the
// lock is also released automatically when the process exits.
func acquirePidLock(stateDir string) (func(), error) {
	dir := filepath.Join(stateDir, "usage")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "worker.pid"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errAlreadyRunning
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, 0); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// spawnDetached starts `tmon worker` in a new session with stdin closed and
// stdout/stderr redirected to <state>/usage/worker.log, so the child
// neither inherits the tmux status-bar pipe (which would block the bar or
// die with it) nor holds it open. Returns once the child is started. A var
// so tests can inject it.
var spawnDetached = func(stateDir string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := filepath.Join(stateDir, "usage")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(dir, "worker.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "worker")
	cmd.Stdin = nil
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = logf.Close()
		return err
	}
	if err := cmd.Process.Release(); err != nil {
		_ = logf.Close()
		return err
	}
	return nil
}
