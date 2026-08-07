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

	"github.com/guillaumemeyer/tmon/internal/agent"
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

// probeClaude fetches the Claude OAuth usage endpoint and maps every
// reported limit window (session, weekly all-models, weekly per-model) into
// the quota block. No credentials, an HTTP error, or an unparseable body
// produce a Quota with StatusText instead of an error, so the worker keeps
// running and the dashboard shows why.
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
// windows (five_hour/seven_day) or the newer limits array (or both), plus
// the optional extra_usage pay-as-you-go block.
type claudeUsageResp struct {
	FiveHour   *claudeWindow     `json:"five_hour"`
	SevenDay   *claudeWindow     `json:"seven_day"`
	Limits     []claudeLimit     `json:"limits"`
	ExtraUsage *claudeExtraUsage `json:"extra_usage"`
}

type claudeWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

type claudeLimit struct {
	Kind     string  `json:"kind"`
	Percent  float64 `json:"percent"`
	ResetsAt string  `json:"resets_at"`
	Scope    *struct {
		Model *struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
	} `json:"scope"`
}

// claudeExtraUsage is the monthly pay-as-you-go block of the usage
// response. Amounts are in minor currency units (cents), so the probe
// divides by 100 before storing dollars.
type claudeExtraUsage struct {
	IsEnabled    bool     `json:"is_enabled"`
	MonthlyLimit *float64 `json:"monthly_limit"` // cents; null when disabled
	UsedCredits  *float64 `json:"used_credits"`  // cents spent so far this month; null when disabled
	Currency     string   `json:"currency"`      // ISO code, e.g. "USD"
}

// parseClaudeUsage maps every rate-limit window in the response to a quota
// window in a fixed display order: session, weekly all-models, then weekly
// per-model (e.g. "Current week (Fable)"), and finally the monthly
// extra-usage window as a dollar window when pay-as-you-go is enabled.
// Legacy top-level windows (five_hour/seven_day) fill in for their
// limits[] counterparts. The first window becomes the quota's primary
// Pct/Label/ResetAt so older consumers keep a single-window view.
func parseClaudeUsage(body []byte) (Quota, error) {
	var resp claudeUsageResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return Quota{}, err
	}
	var windows []agent.QuotaWindow
	add := func(pct float64, resetAt, label string) {
		windows = append(windows, agent.QuotaWindow{
			Pct:     int(pct + 0.5),
			Label:   label,
			ResetAt: normalizeResetAt(resetAt),
		})
	}
	hasLimit := func(kind string) bool {
		for _, l := range resp.Limits {
			if l.Kind == kind {
				return true
			}
		}
		return false
	}
	for _, l := range resp.Limits {
		if l.Kind == "session" {
			add(l.Percent, l.ResetsAt, "Current session")
		}
	}
	if !hasLimit("session") && resp.FiveHour != nil {
		add(resp.FiveHour.Utilization, resp.FiveHour.ResetsAt, "Current session")
	}
	for _, l := range resp.Limits {
		if l.Kind == "weekly_all" {
			add(l.Percent, l.ResetsAt, "Current week (all models)")
		}
	}
	if !hasLimit("weekly_all") && resp.SevenDay != nil {
		add(resp.SevenDay.Utilization, resp.SevenDay.ResetsAt, "Current week (all models)")
	}
	for _, l := range resp.Limits {
		if l.Kind == "weekly_scoped" && l.Scope != nil && l.Scope.Model != nil && l.Scope.Model.DisplayName != "" {
			add(l.Percent, l.ResetsAt, "Current week ("+l.Scope.Model.DisplayName+")")
		}
	}
	if resp.ExtraUsage != nil && resp.ExtraUsage.IsEnabled {
		if w := claudeExtraWindow(*resp.ExtraUsage); w != nil {
			windows = append(windows, *w)
		}
	}
	if len(windows) == 0 {
		return Quota{StatusText: "no rate-limit window in Claude usage response"}, nil
	}
	return Quota{
		Pct:     windows[0].Pct,
		Label:   windows[0].Label,
		ResetAt: windows[0].ResetAt,
		Windows: windows,
	}, nil
}

// claudeExtraWindow maps the monthly extra-usage block into a dollar quota
// window. The API reports cents; the window stores dollars (Spend consumed,
// Limit cap) with the currency's display symbol. nil when the block carries
// neither a limit nor a spent amount (e.g. everything null at 0).
func claudeExtraWindow(e claudeExtraUsage) *agent.QuotaWindow {
	var limit, spend float64
	if e.MonthlyLimit != nil {
		limit = *e.MonthlyLimit / 100
	}
	if e.UsedCredits != nil {
		spend = *e.UsedCredits / 100
	}
	if limit <= 0 && spend <= 0 {
		return nil
	}
	w := agent.QuotaWindow{
		Label: "Extra usage (monthly)",
		Limit: limit,
		Spend: spend,
	}
	w.Currency = currencySymbol(e.Currency)
	if w.Currency == "" {
		w.Currency = "$"
	}
	if limit > 0 {
		w.Pct = int(spend*100/limit + 0.5)
	}
	return &w
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
