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

// grokProbeTimeout bounds the HTTPS GET. The billing endpoint is internal
// to Grok Build (undocumented) and can hang, so a hard deadline is
// mandatory, like the Claude probe.
const grokProbeTimeout = 8 * time.Second

// grokBillingURL is Grok Build's credits/billing endpoint (undocumented,
// used by Grok Build's own /usage display). A var so tests can point it at
// a fake server.
var grokBillingURL = "https://cli-chat-proxy.grok.com/v1/billing?format=credits"

// grokConfigDir returns the Grok Build config directory (~/.grok). A var
// so tests can point it at a fixture directory.
var grokConfigDir = func() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".grok")
}

// grokAuthJSON mirrors the OIDC credential entry of ~/.grok/auth.json.
// Grok Build keeps the file fresh while it runs: the key is a short-lived
// access token and expires_at its expiry, refreshed by the CLI itself.
type grokAuthJSON map[string]struct {
	Key       string `json:"key"`
	ExpiresAt string `json:"expires_at"`
}

// grokAccessToken reads the current access token from ~/.grok/auth.json
// (the entry keyed by the auth.x.ai OIDC client id). "" when missing or
// expired — the CLI refreshes it on its next run, so an expired token
// means Grok Build has not been running. A var so tests can inject a token
// without touching the home directory.
var grokAccessToken = func() string {
	dir := grokConfigDir()
	if dir == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		return ""
	}
	var m grokAuthJSON
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	for k, v := range m {
		if !strings.HasPrefix(k, "https://auth.x.ai::") {
			continue
		}
		if v.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339Nano, v.ExpiresAt); err == nil && time.Now().After(t) {
				return "" // expired; Grok Build refreshes it when it runs again
			}
		}
		return strings.TrimSpace(v.Key)
	}
	return ""
}

// probeGrok fetches Grok Build's billing endpoint and maps the account
// credit usage into the quota block: one overall window for the billing
// period plus one window per product the API reports (e.g. Grok Build,
// Grok Chat). No credentials, an HTTP error, or an unparseable body
// produce a Quota with StatusText instead of an error, so the worker keeps
// running and the dashboard shows why.
func probeGrok(cfg config.Config) (Quota, error) {
	q := Quota{}
	token := grokAccessToken()
	if token == "" {
		q.StatusText = "no Grok credentials (sign in to Grok Build first)"
		q.AuthHelpText = "run: grok login"
		return q, nil
	}
	req, err := http.NewRequest(http.MethodGet, grokBillingURL, nil)
	if err != nil {
		return q, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("x-grok-client-mode", "grok-build")
	client := &http.Client{Timeout: grokProbeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		q.StatusText = "Grok usage API unreachable: " + err.Error()
		return q, nil
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		q.StatusText = "Grok usage API rate limited"
		return q, nil
	case resp.StatusCode == http.StatusUnauthorized:
		q.StatusText = "Grok credentials expired or invalid (run grok login)"
		return q, nil
	case resp.StatusCode != http.StatusOK:
		q.StatusText = fmt.Sprintf("Grok usage API HTTP %d", resp.StatusCode)
		return q, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return q, err
	}
	u, err := parseGrokUsage(body)
	if err != nil {
		q.StatusText = "unexpected Grok usage response: " + err.Error()
		return q, nil
	}
	return u, nil
}

// grokBillingResp is the billing response. The config block carries the
// overall credit usage percent, the billing period, and a per-product
// breakdown of the same period.
type grokBillingResp struct {
	Config struct {
		CurrentPeriod struct {
			Type  string `json:"type"`
			Start string `json:"start"`
			End   string `json:"end"`
		} `json:"currentPeriod"`
		CreditUsagePercent float64 `json:"creditUsagePercent"`
		ProductUsage       []struct {
			Product      string  `json:"product"`
			UsagePercent float64 `json:"usagePercent"`
		} `json:"productUsage"`
		BillingPeriodStart string `json:"billingPeriodStart"`
		BillingPeriodEnd   string `json:"billingPeriodEnd"`
	} `json:"config"`
}

// parseGrokUsage maps the billing response to quota windows in a fixed
// order: the overall credit window first, then one window per product.
// The overall window becomes the quota's primary Pct/Label/ResetAt so
// older consumers keep a single-window view.
func parseGrokUsage(body []byte) (Quota, error) {
	var resp grokBillingResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return Quota{}, err
	}
	reset := normalizeResetAt(resp.Config.BillingPeriodEnd)
	if reset == "" {
		reset = normalizeResetAt(resp.Config.CurrentPeriod.End)
	}
	var windows []agent.QuotaWindow
	add := func(pct float64, label string) {
		windows = append(windows, agent.QuotaWindow{
			Pct:     int(pct + 0.5),
			Label:   label,
			ResetAt: reset,
		})
	}
	overall := resp.Config.CreditUsagePercent
	if overall > 0 || reset != "" {
		add(overall, grokPeriodLabel(resp.Config.CurrentPeriod.Type))
	}
	for _, pu := range resp.Config.ProductUsage {
		name := grokProductName(pu.Product)
		if name == "" {
			continue
		}
		add(pu.UsagePercent, name)
	}
	if len(windows) == 0 {
		return Quota{StatusText: "no usage window in Grok billing response"}, nil
	}
	return Quota{
		Pct:     windows[0].Pct,
		Label:   windows[0].Label,
		ResetAt: windows[0].ResetAt,
		Windows: windows,
	}, nil
}

// grokProductName maps the API's product identifiers to display names;
// unknown products keep their raw identifier.
func grokProductName(product string) string {
	switch strings.TrimSpace(product) {
	case "GrokBuild":
		return "Grok Build"
	case "GrokChat":
		return "Grok Chat"
	default:
		return strings.TrimSpace(product)
	}
}

// grokPeriodLabel names the overall window from the billing period type.
func grokPeriodLabel(periodType string) string {
	switch periodType {
	case "USAGE_PERIOD_TYPE_WEEKLY":
		return "Weekly credits"
	case "USAGE_PERIOD_TYPE_MONTHLY":
		return "Monthly credits"
	default:
		return "Credits"
	}
}
