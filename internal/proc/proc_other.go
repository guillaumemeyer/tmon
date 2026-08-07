//go:build !linux && !darwin

package proc

import "fmt"

func unsupported(op string) error {
	return fmt.Errorf("tmon: %s is not supported on this OS", op)
}

// ListPIDs is not supported on this platform.
func ListPIDs() ([]int, error) { return nil, unsupported("ListPIDs") }

// ReadCmdline is not supported on this platform.
func ReadCmdline(pid int) (string, error) { return "", unsupported("ReadCmdline") }

// ReadCWD is not supported on this platform.
func ReadCWD(pid int) (string, error) { return "", unsupported("ReadCWD") }

// ReadEnv is not supported on this platform.
func ReadEnv(pid int, key string) string { return "" }

// ParentPID is not supported on this platform.
func ParentPID(pid int) (int, error) { return 0, unsupported("ParentPID") }

// StartTimeUnix is not supported on this platform.
func StartTimeUnix(pid int) (int64, error) { return 0, unsupported("StartTimeUnix") }

// ReadCPUTicks is not supported on this platform.
func ReadCPUTicks(pid int) (int64, error) { return 0, unsupported("ReadCPUTicks") }

// ReadIOBytes is not supported on this platform.
func ReadIOBytes(pid int) (int64, error) { return 0, unsupported("ReadIOBytes") }

// TTYForPID is not supported on this platform.
func TTYForPID(pid int) string { return "" }
