// Package worker implements the background usage worker: a long-lived
// process, auto-spawned by `tmon status`, that probes agent quota APIs and
// (in Phase 2) scans transcripts for the token ledger. It writes
// <state>/usage.json, which the status poll and the dashboard read. The
// status poll itself never touches the network — the worker is the only
// authorized network client, unless the worker is explicitly disabled, in
// which case the poll falls back to TTL-gated lazy probes.
package worker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SchemaVersion is the usage.json schema version. Bump when the shape
// changes so older files are ignored rather than misread.
const SchemaVersion = 1

// UsageFile is the full <state>/usage.json schema (v1). Phase 1 fills the
// quota block; the ledger fields are reserved for Phase 2 (tokens by day,
// by model, all-time).
type UsageFile struct {
	SchemaVersion int                   `json:"schemaVersion"`
	GeneratedAt   time.Time             `json:"generatedAt"`
	DeviceID      string                `json:"deviceId"`
	Quota         map[string]Quota      `json:"quota,omitempty"`
	Today         TodayUsage            `json:"today,omitempty"`
	RecentDays    []DayUsage            `json:"recentDays,omitempty"`
	ModelUsage    map[string]ModelUsage `json:"modelUsage,omitempty"`
	ActiveDays    []string              `json:"activeDays,omitempty"`
}

// Quota is one provider's account quota window. Quota is account-level,
// never per-agent: multiple sessions share one window. Pct is the percent
// of the window used, ResetAt the next reset as RFC3339, Tier the plan
// tier when the API exposes it. StatusText/AuthHelpText carry why a window
// is absent (no credentials, rate limited, …) so the dashboard can show it.
type Quota struct {
	Pct          int    `json:"pct"`
	Label        string `json:"label"`
	ResetAt      string `json:"resetAt"`
	Tier         string `json:"tier,omitempty"`
	StatusText   string `json:"statusText,omitempty"`
	AuthHelpText string `json:"authHelpText,omitempty"`
}

// TodayUsage is the ledger's today block (Phase 2).
type TodayUsage struct {
	Tokens        int64            `json:"tokens"`
	Prompts       int64            `json:"prompts"`
	Sessions      int              `json:"sessions"`
	TokensByModel map[string]int64 `json:"tokensByModel"`
}

// DayUsage is one day bucket in recentDays (Phase 2).
type DayUsage struct {
	Date   string `json:"date"`
	Tokens int64  `json:"tokens"`
}

// ModelUsage is the all-time four-way token split per model (Phase 2).
type ModelUsage struct {
	InputTokens              int64 `json:"inputTokens"`
	OutputTokens             int64 `json:"outputTokens"`
	CacheReadInputTokens     int64 `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64 `json:"cacheCreationInputTokens"`
}

// UsageFilePath is <state>/usage.json.
func UsageFilePath(stateDir string) string {
	return filepath.Join(stateDir, "usage.json")
}

// LoadUsageFile reads usage.json. A missing file returns an error wrapping
// os.ErrNotExist; an unreadable, corrupt, or wrong-version file returns
// that error. Callers that treat "absent" as "no data yet" should check
// os.IsNotExist.
func LoadUsageFile(stateDir string) (UsageFile, error) {
	var uf UsageFile
	b, err := os.ReadFile(UsageFilePath(stateDir))
	if err != nil {
		return uf, err
	}
	if err := json.Unmarshal(b, &uf); err != nil {
		return uf, fmt.Errorf("usage.json corrupt: %w", err)
	}
	if uf.SchemaVersion != SchemaVersion {
		return uf, fmt.Errorf("usage.json schema v%d, want v%d", uf.SchemaVersion, SchemaVersion)
	}
	return uf, nil
}

// SaveUsageFile writes usage.json atomically (tmp + rename) so a reader
// never sees a half-written file.
func SaveUsageFile(stateDir string, uf UsageFile) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	uf.SchemaVersion = SchemaVersion
	b, err := json.MarshalIndent(uf, "", "  ")
	if err != nil {
		return err
	}
	path := UsageFilePath(stateDir)
	tmp, err := os.CreateTemp(stateDir, ".usage.json.tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
