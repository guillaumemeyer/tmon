//go:build linux

// Package proc Linux implementation — process metadata from /proc.
//
// Field indices below refer to /proc/<pid>/stat after stripping the
// "pid (comm) " prefix, which is the only robust way to parse it: the comm
// field may itself contain spaces and parentheses. After the strip the
// 0-based fields are: 0=state, 1=ppid, 2=pgrp, 3=session, 4=tty_nr,
// 5=tpgid, 6=flags, 7=minflt, 8=cminflt, 9=majflt, 10=cmajflt, 11=utime,
// 12=stime, 13=cutime, 14=cstime.
package proc

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// procRoot is overridable in tests to point at fixture directories.
var procRoot = "/proc"

// SetProcRoot temporarily points the /proc mount at a fixture directory and
// returns a restore function. Test seam only (Linux).
func SetProcRoot(root string) func() {
	old := procRoot
	procRoot = root
	return func() { procRoot = old }
}

func pidPath(pid int, file string) string {
	return fmt.Sprintf("%s/%d/%s", procRoot, pid, file)
}

// ListPIDs returns the numeric PIDs currently present under /proc.
func ListPIDs() ([]int, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

// ReadCmdline returns the argv of pid with NUL bytes replaced by spaces
// (the representation the bash plugin used), or an error.
func ReadCmdline(pid int) (string, error) {
	b, err := os.ReadFile(pidPath(pid, "cmdline"))
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(b), "\x00", " "), nil
}

// ReadCWD resolves the working directory of pid.
func ReadCWD(pid int) (string, error) {
	return os.Readlink(pidPath(pid, "cwd"))
}

// ReadEnv returns the value of key from /proc/<pid>/environ, or "" when the
// variable is unset or the environ file is unreadable.
func ReadEnv(pid int, key string) string {
	if key == "" {
		return ""
	}
	b, err := os.ReadFile(pidPath(pid, "environ"))
	if err != nil {
		return ""
	}
	prefix := key + "="
	for _, entry := range strings.Split(string(b), "\x00") {
		if strings.HasPrefix(entry, prefix) {
			return entry[len(prefix):]
		}
	}
	return ""
}

// statFields parses /proc/<pid>/stat, returning the fields that follow the
// "pid (comm) " prefix.
func statFields(pid int) ([]string, error) {
	b, err := os.ReadFile(pidPath(pid, "stat"))
	if err != nil {
		return nil, err
	}
	line := string(b)
	idx := strings.LastIndex(line, ")")
	if idx < 0 || idx+1 >= len(line) {
		return nil, fmt.Errorf("malformed stat for pid %d", pid)
	}
	return strings.Fields(line[idx+1:]), nil
}

// StatField returns the Nth field (0-based, after the "pid (comm) " prefix)
// of /proc/<pid>/stat.
func StatField(pid int, idx int) (int64, error) {
	fields, err := statFields(pid)
	if err != nil {
		return 0, err
	}
	if idx >= len(fields) {
		return 0, fmt.Errorf("stat field %d out of range for pid %d", idx, pid)
	}
	return strconv.ParseInt(fields[idx], 10, 64)
}

// ParentPID returns the parent PID of pid.
func ParentPID(pid int) (int, error) {
	v, err := StatField(pid, 1)
	return int(v), err
}

// ReadCPUTicks returns utime+stime+cutime+cstime (fields 11-14) for pid.
// Units are kernel jiffies (typically 100 Hz); see package docs.
func ReadCPUTicks(pid int) (int64, error) {
	fields, err := statFields(pid)
	if err != nil {
		return 0, err
	}
	if len(fields) < 15 {
		return 0, fmt.Errorf("stat for pid %d has too few fields", pid)
	}
	var sum int64
	for _, i := range []int{11, 12, 13, 14} {
		v, err := strconv.ParseInt(fields[i], 10, 64)
		if err != nil {
			return 0, err
		}
		sum += v
	}
	return sum, nil
}

// ReadIOBytes returns rchar+wchar for pid.
func ReadIOBytes(pid int) (int64, error) {
	f, err := os.Open(pidPath(pid, "io"))
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var total int64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		if key == "rchar" || key == "wchar" {
			n, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
			if err != nil {
				return 0, err
			}
			total += n
		}
	}
	return total, sc.Err()
}

// TTYForPID converts pid's tty_nr to a device path, or returns "" if the
// process has no controlling terminal.
func TTYForPID(pid int) string {
	n, err := StatField(pid, 4)
	if err != nil {
		return ""
	}
	return DevTTYName(n)
}

// DevTTYName converts a tty_nr value to a device path.
//
// /proc/<pid>/stat field 7 is new_encode_dev(tty_devnum): the low 8 bits of
// the minor in bits 0-7 and the major in bits 8+. devpts uses majors 136-143;
// legacy consoles use major 4.
//
// Note: the original bash plugin tried to also extract an "extended minor"
// from bits 12-31, but that range overlaps the major field for majors >= 16,
// so every devpts TTY decoded to /dev/pts/8 — a bug that silently disabled
// TTY-based pane mapping. We decode the low minor byte, which is correct for
// pts numbers < 256, the universal case for local terminals.
func DevTTYName(ttyNr int64) string {
	major := (ttyNr >> 8) & 0xFFF
	minor := ttyNr & 0xFF
	switch {
	case major >= 136 && major <= 143:
		return fmt.Sprintf("/dev/pts/%d", minor)
	case major == 4:
		return fmt.Sprintf("/dev/tty%d", minor)
	default:
		return ""
	}
}
