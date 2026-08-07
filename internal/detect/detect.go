// Package detect scans the process table for running AI coding agents and labels them.
package detect

import (
	"sort"
	"strings"

	"github.com/guillaumemeyer/tmon/internal/parallel"
	"github.com/guillaumemeyer/tmon/internal/proc"
)

// Agent is one running process that matched the signature table.
type Agent struct {
	PID     int
	Label   string
	Cmdline string // truncated to CmdlineSnippetLimit chars
	CWD     string // last two path components, "?" if unreadable
}

// CmdlineSnippetLimit mirrors the bash plugin's 80-char cmdline truncation.
const CmdlineSnippetLimit = 80

// Matches reports whether any signature matches the cmdline.
func Matches(cmdline string) bool {
	return combined.MatchString(cmdline)
}

// MatchLabel returns the first signature label matching cmdline, or "" if
// none does.
func MatchLabel(cmdline string) string {
	for _, s := range Signatures {
		if s.Re.MatchString(cmdline) {
			return s.Label
		}
	}
	return ""
}

// All returns every running process that matches the signature table.
// The process table is scanned in parallel: cmdline reads dominate the scan
// and are independent per PID. Each worker writes its own slot, so no
// locking is needed; output is sorted by PID for a deterministic order.
func All() ([]Agent, error) {
	pids, err := proc.ListPIDs()
	if err != nil {
		return nil, err
	}

	slots := make([]*Agent, len(pids))
	parallel.ForEach(len(pids), parallel.DefaultWorkers, func(i int) {
		pid := pids[i]
		cmdline, err := proc.ReadCmdline(pid)
		if err != nil || cmdline == "" {
			return
		}
		if !Matches(cmdline) {
			return
		}
		label := MatchLabel(cmdline)
		if label == "" {
			return
		}
		// Hermes messaging gateway is not a local CLI/TUI session; the
		// Hermes connector also ignores it. Skip so it never appears as an
		// agent row in status/dashboard.
		if label == "Hermes" && isHermesGatewayCmd(cmdline) {
			return
		}
		if len(cmdline) > CmdlineSnippetLimit {
			cmdline = cmdline[:CmdlineSnippetLimit]
		}
		cwd := "?"
		if c, err := proc.ReadCWD(pid); err == nil {
			cwd = proc.CWDShort(c)
		}
		slots[i] = &Agent{PID: pid, Label: label, Cmdline: cmdline, CWD: cwd}
	})

	agents := make([]Agent, 0, len(slots)/8)
	for _, a := range slots {
		if a != nil {
			agents = append(agents, *a)
		}
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].PID < agents[j].PID })
	return agents, nil
}

// isHermesGatewayCmd reports Hermes messaging-gateway processes.
func isHermesGatewayCmd(cmdline string) bool {
	c := strings.ToLower(cmdline)
	if strings.Contains(c, "gateway run") {
		return true
	}
	if strings.Contains(c, "hermes_cli.main") && strings.Contains(c, "gateway") {
		return true
	}
	if strings.Contains(c, "hermes-gateway") {
		return true
	}
	return false
}
