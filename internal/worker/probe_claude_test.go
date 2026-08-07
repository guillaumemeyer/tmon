package worker

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

// pinClaudeProbeSeams overrides the credential seams a probe test needs so
// it never touches the real ~/.claude credentials or the network, and
// restores them when the test ends. expiry 0 and an empty refresh token
// keep the refresh path quiet unless a test pins its own values.
func pinClaudeProbeSeams(t *testing.T, token string) {
	t.Helper()
	origAT, origRT, origExp, origScopes := claudeAccessToken, claudeRefreshToken, claudeTokenExpiry, claudeOAuthScopes
	t.Cleanup(func() {
		claudeAccessToken, claudeRefreshToken, claudeTokenExpiry, claudeOAuthScopes = origAT, origRT, origExp, origScopes
	})
	claudeAccessToken = func() string { return token }
	claudeRefreshToken = func() string { return "" }
	claudeTokenExpiry = func() int64 { return 0 }
	claudeOAuthScopes = func() []string { return nil }
}

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
	origURL := claudeUsageURL
	t.Cleanup(func() { claudeUsageURL = origURL })
	pinClaudeProbeSeams(t, "sk-ant-oat01-test")

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
	origURL := claudeUsageURL
	t.Cleanup(func() { claudeUsageURL = origURL })
	pinClaudeProbeSeams(t, "sk-ant-oat01-test")

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
	origURL := claudeUsageURL
	t.Cleanup(func() { claudeUsageURL = origURL })
	pinClaudeProbeSeams(t, "sk-ant-oat01-test")

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

// TestProbeClaudeRefreshesExpiredToken drives the full path: an expired
// access token in the credentials file, a fake token endpoint issuing a new
// pair, and a fake usage endpoint asserting the new bearer token. The
// credentials file must end up with the refreshed pair and every unknown
// field preserved.
func TestProbeClaudeRefreshesExpiredToken(t *testing.T) {
	origTokenURL, origUsageURL := claudeTokenURL, claudeUsageURL
	t.Cleanup(func() { claudeTokenURL, claudeUsageURL = origTokenURL, origUsageURL })
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	credPath := filepath.Join(dir, ".credentials.json")
	past := time.Now().Add(-time.Hour).UnixMilli()
	if err := os.WriteFile(credPath, []byte(fmt.Sprintf(`{
	  "otherTopLevel": {"keep": true},
	  "claudeAiOauth": {
	    "accessToken": "sk-ant-oat01-old",
	    "refreshToken": "sk-ant-ort01-old",
	    "expiresAt": %d,
	    "scopes": ["user:profile", "user:inference"],
	    "futureField": "preserved"
	  }
	}`, past)), 0o600); err != nil {
		t.Fatal(err)
	}

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("token refresh method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("token refresh body: %v", err)
		}
		if body["grant_type"] != "refresh_token" || body["refresh_token"] != "sk-ant-ort01-old" {
			t.Errorf("token refresh body = %+v", body)
		}
		if body["client_id"] != claudeClientID {
			t.Errorf("client_id = %q, want %q", body["client_id"], claudeClientID)
		}
		if body["scope"] != "user:profile user:inference" {
			t.Errorf("scope = %q", body["scope"])
		}
		fmt.Fprint(w, `{"access_token":"sk-ant-oat01-new","refresh_token":"sk-ant-ort01-new","expires_in":28800,"scope":"user:profile user:inference"}`)
	}))
	defer tokenSrv.Close()
	claudeTokenURL = tokenSrv.URL

	usageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-ant-oat01-new" {
			t.Errorf("usage Authorization = %q, want the refreshed token", got)
		}
		fmt.Fprint(w, `{"limits":[{"kind":"session","percent":42,"resets_at":"2026-08-07T14:00:00Z"},{"kind":"weekly_all","percent":9,"resets_at":"2026-08-09T00:00:00Z"}]}`)
	}))
	defer usageSrv.Close()
	claudeUsageURL = usageSrv.URL

	q, err := probeClaude(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 42 || q.Label != "Current session" {
		t.Errorf("quota = %+v, want the session window", q)
	}

	b, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatal(err)
	}
	var cred struct {
		OtherTopLevel map[string]bool `json:"otherTopLevel"`
		ClaudeAiOauth struct {
			AccessToken  string   `json:"accessToken"`
			RefreshToken string   `json:"refreshToken"`
			ExpiresAt    int64    `json:"expiresAt"`
			FutureField  string   `json:"futureField"`
			Scopes       []string `json:"scopes"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(b, &cred); err != nil {
		t.Fatal(err)
	}
	if cred.ClaudeAiOauth.AccessToken != "sk-ant-oat01-new" || cred.ClaudeAiOauth.RefreshToken != "sk-ant-ort01-new" {
		t.Errorf("credentials = %+v, want the refreshed pair", cred.ClaudeAiOauth)
	}
	if cred.ClaudeAiOauth.FutureField != "preserved" || !cred.OtherTopLevel["keep"] {
		t.Errorf("credentials lost unknown fields: %s", b)
	}
	if cred.ClaudeAiOauth.ExpiresAt <= past {
		t.Errorf("expiresAt = %d, want after the refresh", cred.ClaudeAiOauth.ExpiresAt)
	}
	if len(cred.ClaudeAiOauth.Scopes) != 2 {
		t.Errorf("scopes = %+v, want the stored scopes kept", cred.ClaudeAiOauth.Scopes)
	}
}

func TestProbeClaudeValidTokenSkipsRefresh(t *testing.T) {
	origTokenURL, origUsageURL := claudeTokenURL, claudeUsageURL
	t.Cleanup(func() { claudeTokenURL, claudeUsageURL = origTokenURL, origUsageURL })
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	future := time.Now().Add(24 * time.Hour).UnixMilli()
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"),
		[]byte(fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-ok","refreshToken":"sk-ant-ort01-rt","expiresAt":%d,"scopes":["user:profile"]}}`, future)), 0o600); err != nil {
		t.Fatal(err)
	}

	// A token endpoint that fails hard proves the probe never calls it.
	claudeTokenURL = "http://127.0.0.1:1/token"
	usageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-ant-oat01-ok" {
			t.Errorf("usage Authorization = %q", got)
		}
		fmt.Fprint(w, `{"limits":[{"kind":"session","percent":5,"resets_at":"2026-08-07T14:00:00Z"}]}`)
	}))
	defer usageSrv.Close()
	claudeUsageURL = usageSrv.URL

	q, err := probeClaude(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 5 {
		t.Errorf("quota = %+v, want the session window", q)
	}
}

func TestProbeClaudeRefreshRateLimited(t *testing.T) {
	origTokenURL := claudeTokenURL
	t.Cleanup(func() { claudeTokenURL = origTokenURL })
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	past := time.Now().Add(-time.Hour).UnixMilli()
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"),
		[]byte(fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-old","refreshToken":"sk-ant-ort01-old","expiresAt":%d,"scopes":["user:profile"]}}`, past)), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	claudeTokenURL = srv.URL

	q, err := probeClaude(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.StatusText, "rate limited") {
		t.Errorf("StatusText = %q, want a rate-limit message", q.StatusText)
	}
	if q.AuthHelpText != "" {
		t.Errorf("AuthHelpText = %q, want empty for a transient rate limit", q.AuthHelpText)
	}
}

func TestProbeClaudeRefreshInvalid(t *testing.T) {
	origTokenURL := claudeTokenURL
	t.Cleanup(func() { claudeTokenURL = origTokenURL })
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	past := time.Now().Add(-time.Hour).UnixMilli()
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"),
		[]byte(fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-old","refreshToken":"sk-ant-ort01-old","expiresAt":%d,"scopes":["user:profile"]}}`, past)), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	claudeTokenURL = srv.URL

	q, err := probeClaude(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.StatusText == "" {
		t.Error("StatusText = empty, want an invalid-grant message")
	}
	if q.AuthHelpText != "run: claude login" {
		t.Errorf("AuthHelpText = %q, want login guidance", q.AuthHelpText)
	}
}

func TestProbeClaudeExpiredNoRefreshToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	past := time.Now().Add(-time.Hour).UnixMilli()
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"),
		[]byte(fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-old","expiresAt":%d}}`, past)), 0o600); err != nil {
		t.Fatal(err)
	}

	q, err := probeClaude(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.StatusText == "" || q.AuthHelpText != "run: claude login" {
		t.Errorf("quota = %+v, want an expired-token status with login guidance", q)
	}
}

func TestClaudeRefreshOAuthTokenSuccess(t *testing.T) {
	origURL := claudeTokenURL
	t.Cleanup(func() { claudeTokenURL = origURL })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"access_token":"sk-ant-oat01-new","refresh_token":"sk-ant-ort01-new","expires_in":7200,"token_type":"Bearer","scope":"user:profile"}`)
	}))
	defer srv.Close()
	claudeTokenURL = srv.URL

	access, refresh, expiresAt, err := claudeRefreshOAuthToken("sk-ant-ort01-old", []string{"user:profile"})
	if err != nil {
		t.Fatal(err)
	}
	if access != "sk-ant-oat01-new" || refresh != "sk-ant-ort01-new" {
		t.Errorf("tokens = %q / %q, want the refreshed pair", access, refresh)
	}
	if d := time.Until(time.UnixMilli(expiresAt)); d < 2*time.Hour-time.Minute || d > 3*time.Hour {
		t.Errorf("expiresAt %d not ~2h from now", expiresAt)
	}
}

func TestClaudeRefreshOAuthTokenKeepsRefreshToken(t *testing.T) {
	// A refresh response without refresh_token means the server kept the
	// old one; the caller must not store an empty refresh token.
	origURL := claudeTokenURL
	t.Cleanup(func() { claudeTokenURL = origURL })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"access_token":"sk-ant-oat01-new","expires_in":3600}`)
	}))
	defer srv.Close()
	claudeTokenURL = srv.URL

	_, refresh, _, err := claudeRefreshOAuthToken("sk-ant-ort01-old", nil)
	if err != nil {
		t.Fatal(err)
	}
	if refresh != "sk-ant-ort01-old" {
		t.Errorf("refresh = %q, want the old token kept", refresh)
	}
}

func TestClaudeRefreshOAuthTokenErrors(t *testing.T) {
	origURL := claudeTokenURL
	t.Cleanup(func() { claudeTokenURL = origURL })
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"rate limited", http.StatusTooManyRequests, `{}`, "rate limited"},
		{"invalid grant", http.StatusBadRequest, `{}`, "invalid or expired"},
		{"unauthorized", http.StatusUnauthorized, `{}`, "invalid or expired"},
		{"server error", http.StatusInternalServerError, `{}`, "HTTP 500"},
		{"no access token", http.StatusOK, `{"expires_in":3600}`, "no access token"},
		{"garbage", http.StatusOK, `{nope`, "unexpected Claude token refresh response"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				fmt.Fprint(w, c.body)
			}))
			defer srv.Close()
			claudeTokenURL = srv.URL

			_, _, _, err := claudeRefreshOAuthToken("sk-ant-ort01-old", nil)
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), c.want)
			}
		})
	}
}

func TestClaudeWriteCredentialsPreservesFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, ".credentials.json")
	if err := os.WriteFile(path, []byte(`{
	  "topLevelExtra": {"a": 1},
	  "claudeAiOauth": {
	    "accessToken": "sk-ant-oat01-old",
	    "refreshToken": "sk-ant-ort01-old",
	    "expiresAt": 123,
	    "scopes": ["user:profile"],
	    "futureField": "keep-me"
	  }
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := claudeWriteCredentials("sk-ant-oat01-new", "sk-ant-ort01-new", 456); err != nil {
		t.Fatal(err)
	}

	var cred struct {
		TopLevelExtra map[string]int `json:"topLevelExtra"`
		ClaudeAiOauth struct {
			AccessToken  string   `json:"accessToken"`
			RefreshToken string   `json:"refreshToken"`
			ExpiresAt    int64    `json:"expiresAt"`
			Scopes       []string `json:"scopes"`
			FutureField  string   `json:"futureField"`
		} `json:"claudeAiOauth"`
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &cred); err != nil {
		t.Fatal(err)
	}
	if cred.ClaudeAiOauth.AccessToken != "sk-ant-oat01-new" || cred.ClaudeAiOauth.RefreshToken != "sk-ant-ort01-new" || cred.ClaudeAiOauth.ExpiresAt != 456 {
		t.Errorf("oauth = %+v, want the refreshed values", cred.ClaudeAiOauth)
	}
	if cred.ClaudeAiOauth.FutureField != "keep-me" || cred.TopLevelExtra["a"] != 1 {
		t.Errorf("unknown fields lost: %s", b)
	}
	if len(cred.ClaudeAiOauth.Scopes) != 1 || cred.ClaudeAiOauth.Scopes[0] != "user:profile" {
		t.Errorf("scopes = %+v, want preserved", cred.ClaudeAiOauth.Scopes)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestClaudeWriteCredentialsMissingDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	// A config dir with no credentials file: the write creates it.
	if err := claudeWriteCredentials("a", "b", 1); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"accessToken": "a"`) {
		t.Errorf("credentials = %s", b)
	}
}
