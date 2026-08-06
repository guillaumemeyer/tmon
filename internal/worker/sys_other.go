//go:build !linux && !darwin

package worker

import "errors"

var errAlreadyRunning = errors.New("worker already running")

// acquirePidLock is unsupported on this platform: tmon targets tmux, which
// is linux/darwin only.
func acquirePidLock(stateDir string) (func(), error) {
	return nil, errors.New("tmon: worker is not supported on this OS")
}

// spawnDetached is unsupported on this platform.
func spawnDetached(stateDir string) error {
	return errors.New("tmon: worker is not supported on this OS")
}
