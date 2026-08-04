// Package detect scans the process table for running AI coding agents and labels them.
package detect

import (
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
func All() ([]Agent, error) {
	pids, err := proc.ListPIDs()
	if err != nil {
		return nil, err
	}

	agents := make([]Agent, 0)
	for _, pid := range pids {
		cmdline, err := proc.ReadCmdline(pid)
		if err != nil || cmdline == "" {
			continue
		}
		if !Matches(cmdline) {
			continue
		}
		label := MatchLabel(cmdline)
		if label == "" {
			continue
		}
		if len(cmdline) > CmdlineSnippetLimit {
			cmdline = cmdline[:CmdlineSnippetLimit]
		}
		cwd := "?"
		if c, err := proc.ReadCWD(pid); err == nil {
			cwd = proc.CWDShort(c)
		}
		agents = append(agents, Agent{PID: pid, Label: label, Cmdline: cmdline, CWD: cwd})
	}
	return agents, nil
}
