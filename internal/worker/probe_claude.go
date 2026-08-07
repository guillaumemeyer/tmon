package worker

import (
	"bytes"
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

// claudeCredentialFile mirrors the OAuth fields of
// ~/.claude/.credentials.json.
type claudeCredentialFile struct {
	ClaudeAiOauth struct {
		AccessToken  string   `json:"accessToken"`
		RefreshToken string   `json:"refreshToken"`
		ExpiresAt    int64    `json:"expiresAt"` // Unix ms; 0 when absent
		Scopes       []string `json:"scopes"`
	} `json:"claudeAiOauth"`
}

// claudeCredentials reads the OAuth fields of ~/.claude/.credentials.json
// (or $CLAUDE_CONFIG_DIR). The zero value when the file is missing or
// unparseable. A var so tests can inject a fixture without touching the
// home directory.
var claudeCredentials = func() claudeCredentialFile {
	dir := claudeConfigDir()
	if dir == "" {
		return claudeCredentialFile{}
	}
	b, err := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if err != nil {
		return claudeCredentialFile{}
	}
	var c claudeCredentialFile
	if json.Unmarshal(b, &c) != nil {
		return claudeCredentialFile{}
	}
	return c
}

// claudeAccessToken reads the OAuth access token from
// ~/.claude/.credentials.json (or $CLAUDE_CONFIG_DIR). "" when missing. A
// var so tests can inject a token without touching the home directory.
var claudeAccessToken = func() string {
	return claudeCredentials().ClaudeAiOauth.AccessToken
}

// claudeRefreshToken reads the OAuth refresh token, used to obtain a new
// access token when the current one expires. "" when missing. A var so
// tests can inject it without touching the home directory.
var claudeRefreshToken = func() string {
	return claudeCredentials().ClaudeAiOauth.RefreshToken
}

// claudeTokenExpiry returns the access token's expiry as Unix ms, 0 when
// the credentials carry none (unknown — treated as valid). A var so tests
// can pin it.
var claudeTokenExpiry = func() int64 {
	return claudeCredentials().ClaudeAiOauth.ExpiresAt
}

// claudeOAuthScopes returns the scopes stored with the credentials; the
// refresh request asks for the same set. A var so tests can pin it.
var claudeOAuthScopes = func() []string {
	return claudeCredentials().ClaudeAiOauth.Scopes
}

// claudeClientID is the fixed Claude Code OAuth client id the token
// endpoint requires (same value Claude Code sends).
const claudeClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

// claudeTokenURL is the OAuth token endpoint Claude Code refreshes through.
// A var so tests can point it at a fake server.
var claudeTokenURL = "https://platform.claude.com/v1/oauth/token"

// claudeRefreshBuffer mirrors Claude Code's own expiry check: the access
// token is refreshed when it expires within this window.
const claudeRefreshBuffer = 5 * time.Minute

// claudeTokenResp is the refresh response: a new access token, a rotated
// refresh token (absent when the server keeps the old one), and the access
// token lifetime in seconds.
type claudeTokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// claudeRefreshOAuthToken POSTs the refresh token to the OAuth token
// endpoint and returns the new access token, the (possibly rotated) refresh
// token, and the access token's expiry as Unix ms. Errors carry a status
// text safe for the dashboard: rate limits, invalid grants, and HTTP
// failures are all distinguished.
func claudeRefreshOAuthToken(refreshToken string, scopes []string) (access, refresh string, expiresAt int64, err error) {
	body, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     claudeClientID,
		"scope":         strings.Join(scopes, " "),
	})
	if err != nil {
		return "", "", 0, err
	}
	req, err := http.NewRequest(http.MethodPost, claudeTokenURL, bytes.NewReader(body))
	if err != nil {
		return "", "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: claudeProbeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", 0, fmt.Errorf("Claude token refresh unreachable: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", "", 0, fmt.Errorf("Claude token refresh rate limited")
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest:
		return "", "", 0, fmt.Errorf("Claude OAuth refresh token invalid or expired")
	case resp.StatusCode != http.StatusOK:
		return "", "", 0, fmt.Errorf("Claude token refresh HTTP %d", resp.StatusCode)
	}
	var tr claudeTokenResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tr); err != nil {
		return "", "", 0, fmt.Errorf("unexpected Claude token refresh response: %w", err)
	}
	if strings.TrimSpace(tr.AccessToken) == "" {
		return "", "", 0, fmt.Errorf("Claude token refresh returned no access token")
	}
	expiresAt = time.Now().UnixMilli()
	if tr.ExpiresIn > 0 {
		expiresAt += tr.ExpiresIn * 1000
	}
	if strings.TrimSpace(tr.RefreshToken) == "" {
		tr.RefreshToken = refreshToken // the server kept the same refresh token
	}
	return tr.AccessToken, tr.RefreshToken, expiresAt, nil
}

// claudeWriteCredentials persists a refreshed token pair into
// ~/.claude/.credentials.json (or $CLAUDE_CONFIG_DIR), preserving every
// other field — unknown top-level keys and unknown claudeAiOauth keys
// survive — so a concurrent Claude Code refresh is not clobbered wholesale.
// The write is atomic (tmp + rename) with 0600 permissions, matching
// Claude Code's own credential file.
func claudeWriteCredentials(access, refresh string, expiresAt int64) error {
	dir := claudeConfigDir()
	if dir == "" {
		return fmt.Errorf("no Claude config dir")
	}
	path := filepath.Join(dir, ".credentials.json")
	top := map[string]json.RawMessage{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &top)
	}
	oauth := map[string]json.RawMessage{}
	if b, ok := top["claudeAiOauth"]; ok {
		_ = json.Unmarshal(b, &oauth)
	}
	oauth["accessToken"] = jsonRaw(access)
	oauth["refreshToken"] = jsonRaw(refresh)
	oauth["expiresAt"] = jsonRaw(expiresAt)
	top["claudeAiOauth"] = jsonRaw(oauth)
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".credentials.json.tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// jsonRaw marshals v into a RawMessage for merge-preserving credential
// writes (marshal of these scalar/map values cannot fail).
func jsonRaw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// probeClaude fetches the Claude OAuth usage endpoint and maps every
// reported limit window (session, weekly all-models, weekly per-model) into
// the quota block. No credentials, an HTTP error, or an unparseable body
// produce a Quota with StatusText instead of an error, so the worker keeps
// running and the dashboard shows why.
func probeClaude(cfg config.Config) (Quota, error) {
	q := Quota{}
	token := strings.TrimSpace(claudeAccessToken())
	if token == "" {
		q.StatusText = "no Claude credentials (sign in to Claude Code first)"
		q.AuthHelpText = "run: claude login"
		return q, nil
	}
	// Access tokens are short-lived (~8h) and expire while Claude Code
	// runs, after which the usage endpoint 401s. Refresh through the same
	// OAuth token endpoint Claude Code uses when the stored token is
	// expired (5-minute buffer, like Claude Code's own check), and write
	// the rotated pair back so tmon and Claude Code share one file.
	if expiresAt := claudeTokenExpiry(); expiresAt > 0 && time.Now().Add(claudeRefreshBuffer).UnixMilli() >= expiresAt {
		refresh := strings.TrimSpace(claudeRefreshToken())
		if refresh == "" {
			q.StatusText = "Claude OAuth token expired and no refresh token is stored"
			q.AuthHelpText = "run: claude login"
			return q, nil
		}
		access, refresh, expiresAt, err := claudeRefreshOAuthToken(refresh, claudeOAuthScopes())
		if err != nil {
			q.StatusText = err.Error()
			if strings.Contains(q.StatusText, "invalid or expired") {
				q.AuthHelpText = "run: claude login"
			}
			return q, nil
		}
		// A write failure is not fatal: the fresh token still works for
		// this probe, and the next cycle re-reads and refreshes again.
		_ = claudeWriteCredentials(access, refresh, expiresAt)
		token = access
	}
	return claudeFetchUsage(token)
}

// claudeFetchUsage GETs the OAuth usage endpoint with the given access
// token and maps the body into a Quota. An HTTP error or an unparseable
// body produce a Quota with StatusText instead of an error, so the worker
// keeps running and the dashboard shows why.
func claudeFetchUsage(token string) (Quota, error) {
	q := Quota{}
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
	case resp.StatusCode == http.StatusUnauthorized:
		q.StatusText = "Claude OAuth token invalid or expired"
		q.AuthHelpText = "run: claude login"
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
