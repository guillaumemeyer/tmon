//go:build darwin

package proc

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Darwin proc_info call numbers and flavors (sys/proc_info.h, sys/resource.h).
const (
	procInfoCallPIDInfo   = 2
	procInfoCallPIDRusage = 9

	procPIDVNodePathInfo = 9
	rusageInfoV2         = 2

	// maxPathLen is Darwin MAXPATHLEN.
	maxPathLen = 1024
)

// rusageInfoV2Struct mirrors struct rusage_info_v2 from sys/resource.h.
// Field order and sizes match the Darwin ABI (amd64 and arm64).
type rusageInfoV2Struct struct {
	UUID                [16]byte
	UserTime            uint64 // nanoseconds
	SystemTime          uint64 // nanoseconds
	PkgIdleWkups        uint64
	InterruptWkups      uint64
	Pageins             uint64
	WiredSize           uint64
	ResidentSize        uint64
	PhysFootprint       uint64
	ProcStartAbstime    uint64
	ProcExitAbstime     uint64
	ChildUserTime       uint64
	ChildSystemTime     uint64
	ChildPkgIdleWkups   uint64
	ChildInterruptWkups uint64
	ChildPageins        uint64
	ChildElapsedAbstime uint64
	DiskIOBytesRead     uint64
	DiskIOBytesWritten  uint64
}

// vnodeInfo is struct vnode_info (sys/proc_info.h) — size verified against
// sizeof on Darwin; path follows immediately in vnode_info_path.
//
// We only need vip_path after the fixed-size vnode_info header. Using a
// fixed byte layout avoids fragile nested struct packing across Go versions.
const vnodeInfoSize = 152 // sizeof(struct vnode_info) on Darwin

// procVnodePathInfoSize is sizeof(struct proc_vnodepathinfo).
const procVnodePathInfoSize = 2 * (vnodeInfoSize + maxPathLen) // 2352

// ListPIDs returns every process ID visible via kern.proc.all.
func ListPIDs() ([]int, error) {
	kprocs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, len(kprocs))
	for i := range kprocs {
		pids = append(pids, int(kprocs[i].Proc.P_pid))
	}
	return pids, nil
}

// ReadCmdline returns argv joined with spaces (Linux-compatible representation).
func ReadCmdline(pid int) (string, error) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", err
	}
	if len(raw) < 4 {
		return "", fmt.Errorf("procargs2 for pid %d too short", pid)
	}
	nargs := int(binary.LittleEndian.Uint32(raw[:4]))
	argv := parseProcArgs2(raw[4:], nargs)
	return joinArgv(argv), nil
}

// ReadEnv returns the value of key from the process environment.
// Darwin does not expose environ without CGO, so this always returns "".
func ReadEnv(pid int, key string) string { return "" }

// ReadCWD resolves the working directory of pid via proc_info(PROC_PIDVNODEPATHINFO).
func ReadCWD(pid int) (string, error) {
	buf := make([]byte, procVnodePathInfoSize)
	n, err := procInfo(procInfoCallPIDInfo, pid, procPIDVNodePathInfo, 0, buf)
	if err != nil {
		return "", err
	}
	if n < vnodeInfoSize+1 {
		return "", fmt.Errorf("vnode path info for pid %d too short (%d)", pid, n)
	}
	// cdir path starts after the first vnode_info header.
	pathBytes := buf[vnodeInfoSize : vnodeInfoSize+maxPathLen]
	path := cstring(pathBytes)
	if path == "" {
		return "", fmt.Errorf("empty cwd for pid %d", pid)
	}
	return path, nil
}

// ParentPID returns the parent PID of pid.
func ParentPID(pid int) (int, error) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, err
	}
	return int(k.Eproc.Ppid), nil
}

// ReadCPUTicks returns cumulative user+system+child CPU time as 100 Hz ticks
// so the shared activity tracker (CLKTicks=100) works without changes.
func ReadCPUTicks(pid int) (int64, error) {
	var usage rusageInfoV2Struct
	if err := procRusage(pid, &usage); err != nil {
		return 0, err
	}
	// Nanoseconds → 100 Hz ticks: ticks = ns * 100 / 1e9
	totalNS := usage.UserTime + usage.SystemTime + usage.ChildUserTime + usage.ChildSystemTime
	return int64(totalNS * 100 / 1e9), nil
}

// ReadIOBytes returns cumulative disk read+write bytes for pid.
// Darwin only exposes disk I/O (not the broader rchar/wchar of Linux /proc/io);
// deltas still drive the "working" heuristic.
func ReadIOBytes(pid int) (int64, error) {
	var usage rusageInfoV2Struct
	if err := procRusage(pid, &usage); err != nil {
		return 0, err
	}
	return int64(usage.DiskIOBytesRead + usage.DiskIOBytesWritten), nil
}

// TTYForPID returns the controlling terminal device path for pid, or "" if none.
func TTYForPID(pid int) string {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return ""
	}
	return darwinDevTTYName(k.Eproc.Tdev)
}

func procRusage(pid int, usage *rusageInfoV2Struct) error {
	// libproc passes buffersize=0 for PIDRUSAGE; flavor selects the struct size.
	_, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO,
		uintptr(procInfoCallPIDRusage),
		uintptr(pid),
		uintptr(rusageInfoV2),
		0,
		uintptr(unsafe.Pointer(usage)),
		0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}

// procInfo wraps the Darwin SYS_PROC_INFO syscall.
// callnum is PROC_INFO_CALL_*; flavor/arg match the libproc wrappers.
func procInfo(callnum, pid, flavor int, arg uint64, buf []byte) (int, error) {
	var p unsafe.Pointer
	var n int
	if len(buf) > 0 {
		p = unsafe.Pointer(&buf[0])
		n = len(buf)
	}
	r1, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO,
		uintptr(callnum),
		uintptr(pid),
		uintptr(flavor),
		uintptr(arg),
		uintptr(p),
		uintptr(n),
	)
	if errno != 0 {
		return 0, errno
	}
	return int(r1), nil
}

func cstring(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
