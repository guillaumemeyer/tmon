package worker

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

// grokLiveResponse is the shape of the real /v1/billing?format=credits
// response, captured from a live probe. The per-product breakdown lets the
// dashboard show one window per category, like Claude's per-model windows.
const grokLiveResponse = `{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-08-01T21:49:23.925411+00:00","end":"2026-08-08T21:49:23.925411+00:00"},"creditUsagePercent":51.0,"onDemandCap":{"val":0},"onDemandUsed":{"val":0},"productUsage":[{"product":"GrokBuild","usagePercent":48.0},{"product":"GrokChat","usagePercent":3.0}],"isUnifiedBillingUser":true,"prepaidBalance":{"val":4275},"topUpMethod":"TOP_UP_METHOD_SAVED_PAYMENT_METHOD","billingPeriodStart":"2026-08-01T21:49:23.925411+00:00","billingPeriodEnd":"2026-08-08T21:49:23.925411+00:00"}}`

func TestGrokAccessTokenFromConfigDir(t *testing.T) {
	orig := grokConfigDir
	t.Cleanup(func() { grokConfigDir = orig })
	dir := t.TempDir()
	grokConfigDir = func() string { return dir }
	// Anchor the expiry to the future: a fixed timestamp goes stale the
	// moment the clock passes it, and grokAccessToken treats an expired
	// token as missing.
	expires := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	auth := fmt.Sprintf(`{"https://auth.x.ai::client-id":{"key":"grok-oat01-test","expires_at":%q}}`, expires)
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(auth), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := grokAccessToken(); got != "grok-oat01-test" {
		t.Errorf("token = %q, want grok-oat01-test", got)
	}
}

func TestGrokAccessTokenExpired(t *testing.T) {
	orig := grokConfigDir
	t.Cleanup(func() { grokConfigDir = orig })
	dir := t.TempDir()
	grokConfigDir = func() string { return dir }
	if err := os.WriteFile(filepath.Join(dir, "auth.json"),
		[]byte(`{"https://auth.x.ai::client-id":{"key":"grok-oat01-test","expires_at":"2020-01-01T00:00:00Z"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := grokAccessToken(); got != "" {
		t.Errorf("token = %q, want empty for an expired token", got)
	}
}

func TestGrokAccessTokenMissing(t *testing.T) {
	orig := grokConfigDir
	t.Cleanup(func() { grokConfigDir = orig })
	grokConfigDir = func() string { return t.TempDir() }
	if got := grokAccessToken(); got != "" {
		t.Errorf("token = %q, want empty", got)
	}
}

func TestProbeGrok(t *testing.T) {
	origURL, origToken, origDir := grokBillingURL, grokAccessToken, grokConfigDir
	t.Cleanup(func() { grokBillingURL, grokAccessToken, grokConfigDir = origURL, origToken, origDir })
	grokAccessToken = func() string { return "grok-oat01-test" }
	grokConfigDir = func() string { return t.TempDir() }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer grok-oat01-test" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("x-grok-client-mode"); got != "grok-build" {
			t.Errorf("x-grok-client-mode = %q", got)
		}
		fmt.Fprint(w, grokLiveResponse)
	}))
	defer srv.Close()
	grokBillingURL = srv.URL

	q, err := probeGrok(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 51 || q.Label != "Weekly credits" || q.ResetAt != "2026-08-08T21:49:23Z" {
		t.Errorf("quota = %+v", q)
	}
	want := []agent.QuotaWindow{
		{Pct: 51, Label: "Weekly credits", ResetAt: "2026-08-08T21:49:23Z"},
		{Pct: 48, Label: "Grok Build", ResetAt: "2026-08-08T21:49:23Z"},
		{Pct: 3, Label: "Grok Chat", ResetAt: "2026-08-08T21:49:23Z"},
	}
	if !reflect.DeepEqual(q.Windows, want) {
		t.Errorf("windows = %+v, want %+v", q.Windows, want)
	}
}

func TestProbeGrokNoCredentials(t *testing.T) {
	orig := grokAccessToken
	t.Cleanup(func() { grokAccessToken = orig })
	grokAccessToken = func() string { return "" }

	q, err := probeGrok(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" || q.AuthHelpText == "" {
		t.Errorf("quota = %+v, want status+auth help for missing credentials", q)
	}
}

func TestProbeGrokRateLimited(t *testing.T) {
	origURL, origToken := grokBillingURL, grokAccessToken
	t.Cleanup(func() { grokBillingURL, grokAccessToken = origURL, origToken })
	grokAccessToken = func() string { return "grok-oat01-test" }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	grokBillingURL = srv.URL

	q, err := probeGrok(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" {
		t.Errorf("quota = %+v, want a rate-limit status text", q)
	}
}

func TestProbeGrokUnauthorized(t *testing.T) {
	origURL, origToken := grokBillingURL, grokAccessToken
	t.Cleanup(func() { grokBillingURL, grokAccessToken = origURL, origToken })
	grokAccessToken = func() string { return "grok-oat01-test" }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	grokBillingURL = srv.URL

	q, err := probeGrok(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" {
		t.Errorf("quota = %+v, want a credentials status text", q)
	}
}

func TestProbeGrokHTTPError(t *testing.T) {
	origURL, origToken := grokBillingURL, grokAccessToken
	t.Cleanup(func() { grokBillingURL, grokAccessToken = origURL, origToken })
	grokAccessToken = func() string { return "grok-oat01-test" }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	grokBillingURL = srv.URL

	q, err := probeGrok(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" {
		t.Errorf("quota = %+v, want an HTTP status text", q)
	}
}

func TestParseGrokUsage(t *testing.T) {
	q, err := parseGrokUsage([]byte(grokLiveResponse))
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 51 || q.Label != "Weekly credits" || q.ResetAt != "2026-08-08T21:49:23Z" {
		t.Errorf("quota = %+v, want the overall weekly window as primary", q)
	}
	want := []agent.QuotaWindow{
		{Pct: 51, Label: "Weekly credits", ResetAt: "2026-08-08T21:49:23Z"},
		{Pct: 48, Label: "Grok Build", ResetAt: "2026-08-08T21:49:23Z"},
		{Pct: 3, Label: "Grok Chat", ResetAt: "2026-08-08T21:49:23Z"},
	}
	if !reflect.DeepEqual(q.Windows, want) {
		t.Errorf("windows = %+v, want %+v", q.Windows, want)
	}
}

func TestParseGrokUsageZeroUsed(t *testing.T) {
	// A 0% window with a reset time still renders, so the reset stays
	// visible even when nothing has been used yet.
	body := []byte(`{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY"},"creditUsagePercent":0,"productUsage":[],"billingPeriodEnd":"2026-08-08T21:49:23.925411+00:00"}}`)
	q, err := parseGrokUsage(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Windows) != 1 || q.Windows[0].Pct != 0 || q.Windows[0].Label != "Weekly credits" {
		t.Errorf("windows = %+v, want the single 0%% weekly window", q.Windows)
	}
}

func TestParseGrokUsageEmpty(t *testing.T) {
	q, err := parseGrokUsage([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" {
		t.Errorf("quota = %+v, want a status text for an empty response", q)
	}
}

func TestParseGrokUsageGarbage(t *testing.T) {
	if _, err := parseGrokUsage([]byte(`{nope`)); err == nil {
		t.Fatal("garbage body: want an error")
	}
}

func TestGrokPeriodLabel(t *testing.T) {
	cases := []struct{ in, want string }{
		{"USAGE_PERIOD_TYPE_WEEKLY", "Weekly credits"},
		{"USAGE_PERIOD_TYPE_MONTHLY", "Monthly credits"},
		{"USAGE_PERIOD_TYPE_DAILY", "Credits"},
		{"", "Credits"},
	}
	for _, c := range cases {
		if got := grokPeriodLabel(c.in); got != c.want {
			t.Errorf("grokPeriodLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
