package worker

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

func TestClaudeAccessTokenFromConfigDir(t *testing.T) {
	orig := claudeAccessToken
	t.Cleanup(func() { claudeAccessToken = orig })
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-test"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := claudeAccessToken(); got != "sk-ant-oat01-test" {
		t.Errorf("token = %q, want sk-ant-oat01-test", got)
	}
}

func TestClaudeAccessTokenMissing(t *testing.T) {
	orig := claudeAccessToken
	t.Cleanup(func() { claudeAccessToken = orig })
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if got := claudeAccessToken(); got != "" {
		t.Errorf("token = %q, want empty", got)
	}
}

func TestProbeClaudeLegacyWindows(t *testing.T) {
	origURL, origToken := claudeUsageURL, claudeAccessToken
	t.Cleanup(func() { claudeUsageURL, claudeAccessToken = origURL, origToken })
	claudeAccessToken = func() string { return "sk-ant-oat01-test" }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-ant-oat01-test" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("anthropic-beta"); got != "oauth-2025-04-20" {
			t.Errorf("anthropic-beta = %q", got)
		}
		fmt.Fprint(w, `{"five_hour":{"utilization":38,"resets_at":"2026-08-06T14:00:00+00:00"},"seven_day":{"utilization":12,"resets_at":"2026-08-09T00:00:00Z"}}`)
	}))
	defer srv.Close()
	claudeUsageURL = srv.URL

	q, err := probeClaude(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 38 || q.Label != "Current session" || q.ResetAt != "2026-08-06T14:00:00Z" {
		t.Errorf("quota = %+v", q)
	}
	want := []agent.QuotaWindow{
		{Pct: 38, Label: "Current session", ResetAt: "2026-08-06T14:00:00Z"},
		{Pct: 12, Label: "Current week (all models)", ResetAt: "2026-08-09T00:00:00Z"},
	}
	if !reflect.DeepEqual(q.Windows, want) {
		t.Errorf("windows = %+v, want %+v", q.Windows, want)
	}
}

func TestProbeClaudeNoCredentials(t *testing.T) {
	orig := claudeAccessToken
	t.Cleanup(func() { claudeAccessToken = orig })
	claudeAccessToken = func() string { return "" }

	q, err := probeClaude(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" || q.AuthHelpText == "" {
		t.Errorf("quota = %+v, want status+auth help for missing credentials", q)
	}
}

func TestProbeClaudeRateLimited(t *testing.T) {
	origURL, origToken := claudeUsageURL, claudeAccessToken
	t.Cleanup(func() { claudeUsageURL, claudeAccessToken = origURL, origToken })
	claudeAccessToken = func() string { return "sk-ant-oat01-test" }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	claudeUsageURL = srv.URL

	q, err := probeClaude(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" {
		t.Errorf("quota = %+v, want a rate-limit status text", q)
	}
}

func TestProbeClaudeHTTPError(t *testing.T) {
	origURL, origToken := claudeUsageURL, claudeAccessToken
	t.Cleanup(func() { claudeUsageURL, claudeAccessToken = origURL, origToken })
	claudeAccessToken = func() string { return "sk-ant-oat01-test" }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	claudeUsageURL = srv.URL

	q, err := probeClaude(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" {
		t.Errorf("quota = %+v, want an HTTP status text", q)
	}
}

func TestParseClaudeUsageLimits(t *testing.T) {
	// The five_hour legacy block must not double the session window when
	// limits[] already carries it.
	body := []byte(`{
	  "five_hour": {"utilization": 10, "resets_at": "2026-08-06T13:00:00Z"},
	  "limits": [
	    {"kind": "weekly_all", "percent": 55, "resets_at": "2026-08-09T00:00:00Z"},
	    {"kind": "session", "percent": 27.6, "resets_at": "2026-08-06T14:00:00Z"}
	  ]
	}`)
	q, err := parseClaudeUsage(body)
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 28 || q.Label != "Current session" || q.ResetAt != "2026-08-06T14:00:00Z" {
		t.Errorf("quota = %+v, want session window from limits[]", q)
	}
	want := []agent.QuotaWindow{
		{Pct: 28, Label: "Current session", ResetAt: "2026-08-06T14:00:00Z"},
		{Pct: 55, Label: "Current week (all models)", ResetAt: "2026-08-09T00:00:00Z"},
	}
	if !reflect.DeepEqual(q.Windows, want) {
		t.Errorf("windows = %+v, want %+v", q.Windows, want)
	}
}

func TestParseClaudeUsageWeeklyFallback(t *testing.T) {
	body := []byte(`{
	  "limits": [{"kind": "weekly_all", "percent": 55.4, "resets_at": "2026-08-09T00:00:00Z"}]
	}`)
	q, err := parseClaudeUsage(body)
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 55 || q.Label != "Current week (all models)" {
		t.Errorf("quota = %+v, want the weekly fallback", q)
	}
	if len(q.Windows) != 1 || q.Windows[0].Label != "Current week (all models)" {
		t.Errorf("windows = %+v, want the single weekly window", q.Windows)
	}
}

func TestParseClaudeUsageScopedWindow(t *testing.T) {
	// A weekly_scoped limit names its model; the window keeps the API's
	// null resets_at (Claude Code shows no reset for it either).
	body := []byte(`{
	  "limits": [
	    {"kind": "session", "percent": 0, "resets_at": "2026-08-07T02:39:59Z"},
	    {"kind": "weekly_all", "percent": 0, "resets_at": "2026-08-09T07:59:59Z"},
	    {"kind": "weekly_scoped", "percent": 2.5, "resets_at": null, "scope": {"model": {"display_name": "Fable"}}}
	  ]
	}`)
	q, err := parseClaudeUsage(body)
	if err != nil {
		t.Fatal(err)
	}
	want := []agent.QuotaWindow{
		{Pct: 0, Label: "Current session", ResetAt: "2026-08-07T02:39:59Z"},
		{Pct: 0, Label: "Current week (all models)", ResetAt: "2026-08-09T07:59:59Z"},
		{Pct: 3, Label: "Current week (Fable)", ResetAt: ""},
	}
	if !reflect.DeepEqual(q.Windows, want) {
		t.Errorf("windows = %+v, want %+v", q.Windows, want)
	}
	if q.Pct != 0 || q.Label != "Current session" {
		t.Errorf("primary = %+v, want the session window", q)
	}
}

func TestParseClaudeUsageScopedWithoutName(t *testing.T) {
	// A scoped window with no model name carries no label, so it is
	// skipped rather than rendered as "Current week ()".
	body := []byte(`{
	  "limits": [{"kind": "weekly_scoped", "percent": 10, "resets_at": null, "scope": {"model": {"display_name": ""}}}]
	}`)
	q, err := parseClaudeUsage(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Windows) != 0 || q.StatusText == "" {
		t.Errorf("quota = %+v, want no window and a status text", q)
	}
}

func TestParseClaudeUsageEmpty(t *testing.T) {
	q, err := parseClaudeUsage([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" {
		t.Errorf("quota = %+v, want a status text for an empty response", q)
	}
}

func TestParseClaudeUsageExtraUsage(t *testing.T) {
	// The extra_usage block is monthly pay-as-you-go in cents: the probe
	// divides by 100 and appends a dollar window after the percent
	// windows, so the dashboard shows "consumed and remaining" in $.
	body := []byte(`{
	  "limits": [
	    {"kind": "session", "percent": 10, "resets_at": "2026-08-06T14:00:00Z"},
	    {"kind": "weekly_all", "percent": 55, "resets_at": "2026-08-09T00:00:00Z"}
	  ],
	  "extra_usage": {
	    "is_enabled": true,
	    "monthly_limit": 10000,
	    "used_credits": 1856.0,
	    "utilization": 18.56,
	    "currency": "USD"
	  }
	}`)
	q, err := parseClaudeUsage(body)
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 10 || q.Label != "Current session" {
		t.Errorf("primary = %+v, want the session window", q)
	}
	want := []agent.QuotaWindow{
		{Pct: 10, Label: "Current session", ResetAt: "2026-08-06T14:00:00Z"},
		{Pct: 55, Label: "Current week (all models)", ResetAt: "2026-08-09T00:00:00Z"},
		{Pct: 19, Label: "Extra usage (monthly)", Limit: 100, Spend: 18.56, Currency: "$"},
	}
	if !reflect.DeepEqual(q.Windows, want) {
		t.Errorf("windows = %+v, want %+v", q.Windows, want)
	}
}

func TestParseClaudeUsageExtraUsageDisabled(t *testing.T) {
	// A disabled extra_usage block (or one with no numbers) adds no dollar
	// window; the percent windows stand alone.
	body := []byte(`{
	  "limits": [{"kind": "session", "percent": 3, "resets_at": "2026-08-06T14:00:00Z"}],
	  "extra_usage": {"is_enabled": false, "monthly_limit": null, "used_credits": null}
	}`)
	q, err := parseClaudeUsage(body)
	if err != nil {
		t.Fatal(err)
	}
	want := []agent.QuotaWindow{
		{Pct: 3, Label: "Current session", ResetAt: "2026-08-06T14:00:00Z"},
	}
	if !reflect.DeepEqual(q.Windows, want) {
		t.Errorf("windows = %+v, want %+v", q.Windows, want)
	}
}

func TestParseClaudeUsageExtraUsageOnly(t *testing.T) {
	// A response with no rate-limit windows but a live extra_usage block
	// still yields a quota — the dollar window alone.
	body := []byte(`{
	  "extra_usage": {
	    "is_enabled": true,
	    "monthly_limit": 5000,
	    "used_credits": 1250.0,
	    "currency": "USD"
	  }
	}`)
	q, err := parseClaudeUsage(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Windows) != 1 {
		t.Fatalf("windows = %+v, want the single extra-usage window", q.Windows)
	}
	w := q.Windows[0]
	if w.Label != "Extra usage (monthly)" || w.Limit != 50 || w.Spend != 12.5 || w.Currency != "$" {
		t.Errorf("window = %+v, want $12.50 used of $50.00", w)
	}
}

func TestParseClaudeUsageGarbage(t *testing.T) {
	if _, err := parseClaudeUsage([]byte(`{nope`)); err == nil {
		t.Fatal("garbage body: want an error")
	}
}

func TestNormalizeResetAt(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"2026-08-06T14:00:00Z", "2026-08-06T14:00:00Z"},
		{"2026-08-06T16:00:00+02:00", "2026-08-06T14:00:00Z"},
		{"not-a-time", "not-a-time"},
	}
	for _, c := range cases {
		if got := normalizeResetAt(c.in); got != c.want {
			t.Errorf("normalizeResetAt(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
