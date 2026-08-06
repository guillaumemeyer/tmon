package worker

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
	if q.Pct != 38 || q.Label != "Session (5-hour)" || q.ResetAt != "2026-08-06T14:00:00Z" {
		t.Errorf("quota = %+v", q)
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
	if q.Pct != 28 || q.Label != "Session (5-hour)" || q.ResetAt != "2026-08-06T14:00:00Z" {
		t.Errorf("quota = %+v, want session window from limits[]", q)
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
	if q.Pct != 55 || q.Label != "Weekly (7-day)" {
		t.Errorf("quota = %+v, want the weekly fallback", q)
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
