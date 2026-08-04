// native.go — shared machinery for connectors that read an agent's own
// session/transcript files directly, as a fallback when hooks are not
// installed (Cursor, Copilot) or as the primary surface (Cline, Aider).
//
// The agents' native files are unversioned and drift between releases, so
// these connectors are deliberately conservative: a bounded walk over known
// candidate directories, and a record is only emitted when a matching
// process is running and the newest file was touched recently. If the paths
// move, the connector simply emits nothing and the agent keeps the
// CPU/IO heuristic path — never worse than before.
package connector

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

// dirExists reports whether path is an existing directory.
func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// fileExists reports whether path is an existing regular file.
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// sessionWalkSkip is the set of directory names never descended into when
// hunting for session files. Editor/cache internals update constantly and
// would otherwise drown out real session activity.
var sessionWalkSkip = map[string]bool{
	"Cache":            true,
	"cache":            true,
	"logs":             true,
	"Logs":             true,
	"extensions":       true,
	"workspaceStorage": true,
	"blob_storage":     true,
	"indexdb":          true,
	"Local Storage":    true,
	"Session Storage":  true,
	"GPUCache":         true,
	"Code Cache":       true,
	"node_modules":     true,
	".git":             true,
}

// newestSessionFile returns the mtime and full path of the newest file under
// dir whose name ends with one of suffixes. The walk is bounded (maxDepth
// levels, maxFiles inspected) so a huge tree — e.g. ~/.cursor with a full
// IDE's worth of data — stays cheap. ok is false when nothing matched.
func newestSessionFile(dir string, suffixes []string, maxDepth, maxFiles int) (t time.Time, path string, ok bool) {
	entries := 0
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip
		}
		entries++
		if entries > maxFiles {
			return fs.SkipAll
		}
		if d.IsDir() {
			if p == dir {
				return nil
			}
			if sessionWalkSkip[d.Name()] {
				return fs.SkipDir
			}
			rel, rerr := filepath.Rel(dir, p)
			if rerr == nil && strings.Count(rel, string(filepath.Separator)) >= maxDepth {
				return fs.SkipDir
			}
			return nil
		}
		if !matchesSuffix(d.Name(), suffixes) {
			return nil
		}
		if fi, ferr := d.Info(); ferr == nil && fi.ModTime().After(t) {
			t, path, ok = fi.ModTime(), p, true
		}
		return nil
	})
	return t, path, ok
}

// matchesSuffix reports whether name ends with any of the suffixes (each
// may omit the leading dot: ".json" or "json" both work).
func matchesSuffix(name string, suffixes []string) bool {
	for _, s := range suffixes {
		s = strings.TrimPrefix(s, ".")
		if strings.HasSuffix(name, "."+s) {
			return true
		}
	}
	return false
}

// nativeSessionRecord pairs the newest session file under one of dirs (the
// first that exists wins) with the first running process of label. The
// record's timestamp is the file mtime, so Collect drops it once the agent
// stops writing for longer than the freshness window and the agent decays
// back to the heuristic path.
func nativeSessionRecord(cfg config.Config, label string, dirs []string, detail string) ([]Record, error) {
	pid := firstRunningPID(label)
	if pid == 0 {
		return nil, nil // agent not running: nothing to report
	}
	for _, dir := range dirs {
		if !dirExists(dir) {
			continue
		}
		at, _, ok := newestSessionFile(dir, []string{".json", ".jsonl"}, 3, 2000)
		if !ok {
			return nil, nil
		}
		return []Record{{
			PID:    pid,
			Label:  label,
			Status: agent.StatusIdle,
			Detail: detail,
			At:     at,
		}}, nil
	}
	return nil, nil
}
