// Package proc reads process metadata for agent detection and activity tracking.
//
// Implementations are OS-specific (build tags):
//   - Linux: /proc
//   - Darwin: sysctl + the proc_info syscall (no CGO)
//
// CPU unit contract: ReadCPUTicks returns a cumulative counter where
// delta/CLK_TCK ≈ seconds of CPU time. Both Linux and Darwin normalize to
// 100 Hz ticks so the tracker’s ActivityThresholdMs math works unchanged
// with the default CLKTicks=100.
//
// API surface (all platforms):
//
//	ListPIDs, ReadCmdline, ReadCWD, ParentPID,
//	ReadCPUTicks, ReadIOBytes, TTYForPID, CWDShort
package proc
