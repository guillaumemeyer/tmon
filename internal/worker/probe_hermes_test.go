package worker

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

// hermesFixtureHome points hermesHomeDir at a temp ~/.hermes layout and
// returns the home path.
func hermesFixtureHome(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, ".hermes")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	old := hermesHomeDir
	hermesHomeDir = func() string { return home }
	t.Cleanup(func() { hermesHomeDir = old })
	return home
}

// hermesFixtureEnv writes a .env containing the given key.
func hermesFixtureEnv(t *testing.T, home, key, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, ".env"),
		[]byte(fmt.Sprintf("# comment\n%s=%s\n", key, value)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHermesModelConfig(t *testing.T) {
	home := hermesFixtureHome(t)
	if err := os.WriteFile(filepath.Join(home, "config.yaml"),
		[]byte("model:\n  base_url: https://api.deepseek.com/v1\n  default: deepseek-v4-flash\n  provider: deepseek\nagent:\n  max_turns: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	provider, baseURL := hermesModelConfig(home)
	if provider != "deepseek" || baseURL != "https://api.deepseek.com/v1" {
		t.Errorf("hermesModelConfig = (%q, %q), want (deepseek, https://api.deepseek.com/v1)", provider, baseURL)
	}
}

func TestHermesModelConfigMissing(t *testing.T) {
	home := hermesFixtureHome(t)
	if provider, baseURL := hermesModelConfig(home); provider != "" || baseURL != "" {
		t.Errorf("hermesModelConfig = (%q, %q), want empty without config.yaml", provider, baseURL)
	}
}

func TestHermesEnvKey(t *testing.T) {
	cases := []struct{ provider, want string }{
		{"deepseek", "DEEPSEEK_API_KEY"},
		{"openrouter", "OPENROUTER_API_KEY"},
		{"google", "GOOGLE_API_KEY"},
		{"", ""},
	}
	for _, c := range cases {
		if got := hermesEnvKey(c.provider); got != c.want {
			t.Errorf("hermesEnvKey(%q) = %q, want %q", c.provider, got, c.want)
		}
	}
}

func TestHermesAPIKey(t *testing.T) {
	home := hermesFixtureHome(t)
	hermesFixtureEnv(t, home, "DEEPSEEK_API_KEY", "sk-test-123")
	if got := hermesAPIKey(home, "deepseek"); got != "sk-test-123" {
		t.Errorf("hermesAPIKey = %q, want sk-test-123", got)
	}
	if got := hermesAPIKey(home, "openrouter"); got != "" {
		t.Errorf("hermesAPIKey(openrouter) = %q, want empty", got)
	}
}

func TestHermesProviderName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"deepseek", "DeepSeek"},
		{"DeepSeek", "DeepSeek"},
		{"openrouter", "OpenRouter"},
		{"", "Hermes"},
		{"custom", "custom"},
	}
	for _, c := range cases {
		if got := hermesProviderName(c.in); got != c.want {
			t.Errorf("hermesProviderName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHermesCurrencySymbol(t *testing.T) {
	cases := []struct{ in, want string }{
		{"CNY", "¥"},
		{"USD", "$"},
		{"EUR", "€"},
		{"GBP", "£"},
		{"JPY", "¥"},
		{"KRW", "KRW "},
		{"", ""},
	}
	for _, c := range cases {
		if got := hermesCurrencySymbol(c.in); got != c.want {
			t.Errorf("hermesCurrencySymbol(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHermesBalanceAmount(t *testing.T) {
	if got, err := hermesBalanceAmount([]byte(`110.00`)); err != nil || got != 110.0 {
		t.Errorf("number = %v, %v; want 110", got, err)
	}
	if got, err := hermesBalanceAmount([]byte(`"110.00"`)); err != nil || got != 110.0 {
		t.Errorf("string = %v, %v; want 110", got, err)
	}
	if _, err := hermesBalanceAmount([]byte(``)); err == nil {
		t.Error("empty raw: want an error")
	}
	if _, err := hermesBalanceAmount([]byte(`"abc"`)); err == nil {
		t.Error("non-numeric string: want an error")
	}
}

func TestProbeHermesDeepSeek(t *testing.T) {
	home := hermesFixtureHome(t)
	hermesFixtureEnv(t, home, "DEEPSEEK_API_KEY", "sk-test-123")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/balance" {
			t.Errorf("path = %q, want /user/balance", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test-123" {
			t.Errorf("Authorization = %q", got)
		}
		fmt.Fprint(w, `{"is_available": true, "balance_infos": [{"currency": "CNY", "total_balance": "110.00"}]}`)
	}))
	defer srv.Close()

	if err := os.WriteFile(filepath.Join(home, "config.yaml"),
		[]byte(fmt.Sprintf("model:\n  base_url: %s\n  provider: deepseek\n", srv.URL)), 0o644); err != nil {
		t.Fatal(err)
	}

	q, err := probeHermes(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	wantLabel := "DeepSeek balance ¥110.00"
	if q.Label != wantLabel || q.Pct != 0 {
		t.Errorf("quota = %+v, want label %q at 0%%", q, wantLabel)
	}
	want := []agent.QuotaWindow{{Pct: 0, Label: wantLabel}}
	if !reflect.DeepEqual(q.Windows, want) {
		t.Errorf("windows = %+v, want %+v", q.Windows, want)
	}
}

func TestProbeHermesDeepSeekNumericBalance(t *testing.T) {
	// Some providers emit total_balance as a JSON number, not a string.
	home := hermesFixtureHome(t)
	hermesFixtureEnv(t, home, "DEEPSEEK_API_KEY", "sk-test-123")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"is_available": true, "balance_infos": [{"currency": "USD", "total_balance": 66.09}]}`)
	}))
	defer srv.Close()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"),
		[]byte(fmt.Sprintf("model:\n  base_url: %s\n  provider: deepseek\n", srv.URL)), 0o644); err != nil {
		t.Fatal(err)
	}
	q, err := probeHermes(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if want := "DeepSeek balance $66.09"; q.Label != want {
		t.Errorf("label = %q, want %q", q.Label, want)
	}
}

func TestProbeHermesOpenRouter(t *testing.T) {
	home := hermesFixtureHome(t)
	hermesFixtureEnv(t, home, "OPENROUTER_API_KEY", "sk-or-123")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/credits" {
			t.Errorf("path = %q, want /credits", r.URL.Path)
		}
		fmt.Fprint(w, `{"total_credits": 12.5, "total_usage": 3.25}`)
	}))
	defer srv.Close()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"),
		[]byte(fmt.Sprintf("model:\n  base_url: %s\n  provider: openrouter\n", srv.URL)), 0o644); err != nil {
		t.Fatal(err)
	}
	q, err := probeHermes(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if want := "OpenRouter balance $12.50"; q.Label != want {
		t.Errorf("label = %q, want %q", q.Label, want)
	}
}

func TestProbeHermesNoHome(t *testing.T) {
	old := hermesHomeDir
	hermesHomeDir = func() string { return "" }
	t.Cleanup(func() { hermesHomeDir = old })
	q, err := probeHermes(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" {
		t.Errorf("quota = %+v, want a status text without a home", q)
	}
}

func TestProbeHermesNoProvider(t *testing.T) {
	hermesFixtureHome(t) // empty home: no config.yaml at all
	q, err := probeHermes(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" || q.AuthHelpText == "" {
		t.Errorf("quota = %+v, want status + auth help without a provider", q)
	}
}

func TestProbeHermesNoKey(t *testing.T) {
	home := hermesFixtureHome(t)
	if err := os.WriteFile(filepath.Join(home, "config.yaml"),
		[]byte("model:\n  base_url: https://api.deepseek.com/v1\n  provider: deepseek\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	q, err := probeHermes(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" || q.AuthHelpText == "" {
		t.Errorf("quota = %+v, want status + auth help without a key", q)
	}
	if !strings.Contains(q.StatusText, "DEEPSEEK_API_KEY") {
		t.Errorf("StatusText = %q, want it to name DEEPSEEK_API_KEY", q.StatusText)
	}
}

func TestProbeHermesHTTPError(t *testing.T) {
	home := hermesFixtureHome(t)
	hermesFixtureEnv(t, home, "DEEPSEEK_API_KEY", "sk-test-123")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"),
		[]byte(fmt.Sprintf("model:\n  base_url: %s\n  provider: deepseek\n", srv.URL)), 0o644); err != nil {
		t.Fatal(err)
	}
	q, err := probeHermes(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" {
		t.Errorf("quota = %+v, want an HTTP status text", q)
	}
}

func TestProbeHermesUnauthorized(t *testing.T) {
	home := hermesFixtureHome(t)
	hermesFixtureEnv(t, home, "DEEPSEEK_API_KEY", "sk-test-123")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"),
		[]byte(fmt.Sprintf("model:\n  base_url: %s\n  provider: deepseek\n", srv.URL)), 0o644); err != nil {
		t.Fatal(err)
	}
	q, err := probeHermes(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" {
		t.Errorf("quota = %+v, want a credentials status text", q)
	}
}

func TestProbeHermesRateLimited(t *testing.T) {
	home := hermesFixtureHome(t)
	hermesFixtureEnv(t, home, "DEEPSEEK_API_KEY", "sk-test-123")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"),
		[]byte(fmt.Sprintf("model:\n  base_url: %s\n  provider: deepseek\n", srv.URL)), 0o644); err != nil {
		t.Fatal(err)
	}
	q, err := probeHermes(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" {
		t.Errorf("quota = %+v, want a rate-limit status text", q)
	}
}
