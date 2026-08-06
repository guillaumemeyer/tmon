package main

import (
	"fmt"
	"os"

	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/worker"
)

// cmdWorker runs the usage worker loop. `tmon worker` is the auto-spawn
// target for the status command; `tmon worker stop` terminates the running
// worker and disables auto-respawn until it is started manually.
func cmdWorker(args []string) int {
	if len(args) == 1 && args[0] == "stop" {
		if err := worker.Stop(config.FromEnv()); err != nil {
			fmt.Fprintln(os.Stderr, "tmon: worker stop:", err)
			return 1
		}
		fmt.Println("tmon worker stopped (auto-respawn disabled until a manual `tmon worker` or `tmon daemon` run)")
		return 0
	}
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: tmon worker [stop]")
		return 2
	}
	if err := worker.Run(config.FromEnv()); err != nil {
		fmt.Fprintln(os.Stderr, "tmon: worker:", err)
		return 1
	}
	return 0
}

// cmdDaemon runs the same worker loop manually — for headless setups and
// debugging. It shares the pid lock with the auto-spawned worker, so at
// most one instance runs per state dir.
func cmdDaemon(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: tmon daemon")
		return 2
	}
	if err := worker.Run(config.FromEnv()); err != nil {
		fmt.Fprintln(os.Stderr, "tmon: daemon:", err)
		return 1
	}
	return 0
}
