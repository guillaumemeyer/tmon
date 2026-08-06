package worker

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/guillaumemeyer/tmon/internal/config"
)

// claudeProbeTimeout bounds the HTTPS GET. The endpoint is undocumented and
// can hang, so a hard deadline is mandatory.
const claudeProbeTimeout = 8 * time.Second

// claudeUsageURL is the Claude Code OAuth usage endpoint (undocumented,
// used by Claude Code's own /usage display). A var so tests can point it at
// a fake server.
var claudeUsageURL = "https://api.anthropic.com/api/oauth/usage"

// claudeConfigDir returns $CLAUDE_CONFIG_DIR when set, else ~/.claude.
func claudeConfigDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".claude")
}

// claudeCredentials mirrors the OAuth fields of ~/.claude/.credentials.json.
type claudeCredentials struct {
	ClaudeAiOauth struct {
		AccessToken string `json:"accessToken"`
	} `json:"claudeAiOauth"`
}

// claudeAccessToken reads the OAuth access token from
// ~/.claude/.credentials.json (or $CLAUDE_CONFIG_DIR). "" when missing. A
// var so tests can inject a token without touching the home directory.
var claudeAccessToken = func() string {
	dir := claudeConfigDir()
	if dir == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if err != nil {
		return ""
	}
	var c claudeCredentials
	if json.Unmarshal(b, &c) != nil {
		return ""
	}
	return strings.TrimSpace(c.ClaudeAiOauth.AccessToken)
}

// probeClaude fetches the Claude OAuth usage endpoint and maps the session
// (5-hour) window into the quota block. No credentials, an HTTP error, or
// an unparseable body produce a Quota with StatusText instead of an error,
// so the worker keeps running and the dashboard shows why.
func probeClaude(cfg config.Config) (Quota, error) {
	q := Quota{}
	token := claudeAccessToken()
	if token == "" {
		q.StatusText = "no Claude credentials (sign in to Claude Code first)"
		q.AuthHelpText = "run: claude login"
		return q, nil
	}
	req, err := http.NewRequest(http.MethodGet, claudeUsageURL, nil)
	if err != nil {
		return q, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	client := &http.Client{Timeout: claudeProbeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		q.StatusText = "Claude usage API unreachable: " + err.Error()
		return q, nil
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		q.StatusText = "Claude usage API rate limited"
		return q, nil
	case resp.StatusCode != http.StatusOK:
		q.StatusText = fmt.Sprintf("Claude usage API HTTP %d", resp.StatusCode)
		return q, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return q, err
	}
	u, err := parseClaudeUsage(body)
	if err != nil {
		q.StatusText = "unexpected Claude usage response: " + err.Error()
		return q, nil
	}
	return u, nil
}

// claudeUsageResp is the OAuth usage response: either the legacy top-level
// windows (five_hour/seven_day) or the newer limits array (or both).
type claudeUsageResp struct {
	FiveHour *claudeWindow `json:"five_hour"`
	SevenDay *claudeWindow `json:"seven_day"`
	Limits   []claudeLimit `json:"limits"`
}

type claudeWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

type claudeLimit struct {
	Kind     string  `json:"kind"`
	Percent  float64 `json:"percent"`
	ResetsAt string  `json:"resets_at"`
}

// parseClaudeUsage selects the session (5-hour) window — from the limits
// array when present, else the legacy five_hour object — and maps it to a
// quota block. It falls back to the weekly window when no session window
// exists (some plans only expose weekly).
func parseClaudeUsage(body []byte) (Quota, error) {
	var resp claudeUsageResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return Quota{}, err
	}
	for _, l := range resp.Limits {
		if l.Kind == "session" {
			return quotaFromWindow(l.Percent, l.ResetsAt, "Session (5-hour)"), nil
		}
	}
	if resp.FiveHour != nil {
		return quotaFromWindow(resp.FiveHour.Utilization, resp.FiveHour.ResetsAt, "Session (5-hour)"), nil
	}
	for _, l := range resp.Limits {
		if l.Kind == "weekly_all" {
			return quotaFromWindow(l.Percent, l.ResetsAt, "Weekly (7-day)"), nil
		}
	}
	if resp.SevenDay != nil {
		return quotaFromWindow(resp.SevenDay.Utilization, resp.SevenDay.ResetsAt, "Weekly (7-day)"), nil
	}
	return Quota{StatusText: "no rate-limit window in Claude usage response"}, nil
}

// quotaFromWindow maps one window to a quota block, rounding the percent
// half-up (matching the dashboard's own rounding convention).
func quotaFromWindow(pct float64, resetAt, label string) Quota {
	return Quota{Pct: int(pct + 0.5), Label: label, ResetAt: normalizeResetAt(resetAt)}
}

// normalizeResetAt parses an API reset timestamp into UTC RFC3339, keeping
// the raw string when it does not parse.
func normalizeResetAt(s string) string {
	if s == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return s
}
