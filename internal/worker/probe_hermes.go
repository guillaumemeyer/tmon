// probe_hermes.go — Hermes Agent quota probe.
//
// Hermes is bring-your-own-key: the provider and base URL come from the
// model: block of ~/.hermes/config.yaml and the API key from
// ~/.hermes/.env. There is no central Hermes account to bill against, so
// the probe reports the configured provider's account balance instead.
// OpenAI-compatible providers (DeepSeek, …) expose GET {base}/user/balance;
// OpenRouter exposes GET {base}/credits.
package worker

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

// hermesProbeTimeout bounds the balance HTTPS GET. Provider balance
// endpoints can be slow, so a hard deadline is mandatory like the other
// probes.
const hermesProbeTimeout = 8 * time.Second

// hermesHomeDir returns the Hermes config directory (~/.hermes). A var so
// tests can point it at a fixture directory.
var hermesHomeDir = func() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".hermes")
}

// hermesDefaultBaseURL fills in a known provider's base URL when
// config.yaml omits model.base_url.
var hermesDefaultBaseURL = map[string]string{
	"deepseek":   "https://api.deepseek.com/v1",
	"openrouter": "https://openrouter.ai/api/v1",
}

// hermesProviderName maps provider ids to display names; unknown ids keep
// their raw spelling.
func hermesProviderName(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "deepseek":
		return "DeepSeek"
	case "openrouter":
		return "OpenRouter"
	case "google", "gemini":
		return "Google"
	case "openai":
		return "OpenAI"
	case "anthropic":
		return "Anthropic"
	case "ollama":
		return "Ollama"
	default:
		s := strings.TrimSpace(provider)
		if s == "" {
			return "Hermes"
		}
		return s
	}
}

// hermesEnvKey returns the .env variable that holds the provider's API key:
// the provider id uppercased with non-alphanumerics folded to underscores,
// plus _API_KEY (deepseek → DEEPSEEK_API_KEY, openrouter →
// OPENROUTER_API_KEY).
func hermesEnvKey(provider string) string {
	if provider == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(provider) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String() + "_API_KEY"
}

// hermesModelConfig reads the provider and base URL from the top-level
// model: block of config.yaml without a full YAML dependency.
func hermesModelConfig(home string) (provider, baseURL string) {
	b, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil {
		return "", ""
	}
	inModel := false
	for _, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent == 0 {
			inModel = strings.HasPrefix(trimmed, "model:")
			continue
		}
		if !inModel {
			continue
		}
		if strings.HasPrefix(trimmed, "provider:") {
			provider = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "provider:")), `"'`)
		} else if strings.HasPrefix(trimmed, "base_url:") {
			baseURL = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "base_url:")), `"'`)
		}
	}
	return provider, baseURL
}

// hermesAPIKey reads the provider's key from ~/.hermes/.env (KEY=VALUE
// lines). "" when the variable is unset or the file is missing.
func hermesAPIKey(home, provider string) string {
	key := hermesEnvKey(provider)
	if key == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(home, ".env"))
	if err != nil {
		return ""
	}
	prefix := key + "="
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

// probeHermes reports the balance of the provider Hermes is configured with,
// so a Hermes row gets an account-level usage line instead of "📊 Usage: ?".
// A balance is not a usage percent, so the quota is a single 0% window whose
// Balance field carries the amount and whose label names the account
// ("DeepSeek balance"). No home, no key, an HTTP error, or an unparseable
// body produce a Quota with StatusText instead of an error, so the worker
// keeps running and the dashboard shows why.
func probeHermes(cfg config.Config) (Quota, error) {
	q := Quota{}
	home := hermesHomeDir()
	if home == "" {
		q.StatusText = "no Hermes home (~/.hermes)"
		return q, nil
	}
	provider, baseURL := hermesModelConfig(home)
	if provider == "" {
		q.StatusText = "no Hermes provider configured (model.provider in ~/.hermes/config.yaml)"
		q.AuthHelpText = "run: hermes setup"
		return q, nil
	}
	if baseURL == "" {
		baseURL = hermesDefaultBaseURL[strings.ToLower(strings.TrimSpace(provider))]
		if baseURL == "" {
			q.StatusText = fmt.Sprintf("no base URL for Hermes provider %q (set model.base_url in ~/.hermes/config.yaml)", provider)
			q.AuthHelpText = "run: hermes setup"
			return q, nil
		}
	}
	key := hermesAPIKey(home, provider)
	if key == "" {
		q.StatusText = fmt.Sprintf("no %s in ~/.hermes/.env", hermesEnvKey(provider))
		q.AuthHelpText = "run: hermes setup"
		return q, nil
	}
	return fetchBalanceQuota(baseURL, key, provider)
}

// fetchBalanceQuota GETs a provider's balance endpoint and parses the body
// into a single balance window: Pct 0, the label naming the account
// ("DeepSeek balance"), and Balance/Currency carrying the amount. A request
// that cannot be built is an error; HTTP-level failures and unparseable
// bodies return a Quota with StatusText so the worker keeps running.
// OpenAI-compatible providers expose GET {base}/user/balance; OpenRouter
// exposes GET {base}/credits.
func fetchBalanceQuota(baseURL, key, provider string) (Quota, error) {
	q := Quota{}
	endpoint := "user/balance"
	if strings.EqualFold(provider, "openrouter") {
		endpoint = "credits"
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/"+endpoint, nil)
	if err != nil {
		return q, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: hermesProbeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		q.StatusText = "provider balance API unreachable: " + err.Error()
		return q, nil
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		q.StatusText = "provider balance API rate limited"
		return q, nil
	case resp.StatusCode == http.StatusUnauthorized:
		q.StatusText = "provider API key invalid or expired"
		return q, nil
	case resp.StatusCode != http.StatusOK:
		q.StatusText = fmt.Sprintf("provider balance API HTTP %d", resp.StatusCode)
		return q, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return q, err
	}

	var amount float64
	var currency string
	if strings.EqualFold(provider, "openrouter") {
		amount, err = hermesOpenRouterBalance(body)
		currency = "$"
	} else {
		amount, currency, err = hermesOpenAIBalance(body)
	}
	if err != nil {
		q.StatusText = err.Error()
		return q, nil
	}
	if amount <= 0 {
		q.StatusText = "no usable balance in provider response"
		return q, nil
	}
	label := hermesProviderName(provider) + " balance"
	return Quota{
		Pct:      0,
		Label:    label,
		Balance:  amount,
		Currency: currency,
		Windows: []agent.QuotaWindow{{
			Pct:      0,
			Label:    label,
			Balance:  amount,
			Currency: currency,
		}},
	}, nil
}

// hermesOpenRouterBalance parses GET {base}/credits into the dollar amount.
// total_credits is a pointer so a missing field (error shape) is reported
// instead of silently reading as a $0.00 balance.
func hermesOpenRouterBalance(body []byte) (float64, error) {
	var resp struct {
		TotalCredits *float64 `json:"total_credits"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || resp.TotalCredits == nil {
		return 0, fmt.Errorf("unexpected OpenRouter credits response")
	}
	return *resp.TotalCredits, nil
}

// hermesOpenAIBalance parses an OpenAI-compatible GET /user/balance
// response into the available amount and its currency display symbol.
func hermesOpenAIBalance(body []byte) (float64, string, error) {
	var resp struct {
		IsAvailable  bool `json:"is_available"`
		BalanceInfos []struct {
			Currency     string          `json:"currency"`
			TotalBalance json.RawMessage `json:"total_balance"`
		} `json:"balance_infos"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, "", fmt.Errorf("unexpected Hermes provider balance response: %w", err)
	}
	if !resp.IsAvailable {
		return 0, "", fmt.Errorf("hermes provider account unavailable")
	}
	if len(resp.BalanceInfos) == 0 {
		return 0, "", fmt.Errorf("no balance info in Hermes provider response")
	}
	amount, err := hermesBalanceAmount(resp.BalanceInfos[0].TotalBalance)
	if err != nil {
		return 0, "", fmt.Errorf("unexpected Hermes provider balance value: %w", err)
	}
	return amount, currencySymbol(resp.BalanceInfos[0].Currency), nil
}

// hermesBalanceAmount parses total_balance, which providers emit as either a
// JSON number or a numeric string.
func hermesBalanceAmount(raw json.RawMessage) (float64, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("empty total_balance")
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, fmt.Errorf("total_balance is neither a number nor a string")
	}
	return strconv.ParseFloat(strings.TrimSpace(s), 64)
}

// currencySymbol maps ISO currency codes to display symbols; unknown codes
// fall back to the bare code so the amount stays readable.
func currencySymbol(currency string) string {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "CNY", "JPY":
		return "¥"
	case "USD":
		return "$"
	case "EUR":
		return "€"
	case "GBP":
		return "£"
	default:
		code := strings.ToUpper(strings.TrimSpace(currency))
		if code == "" {
			return ""
		}
		return code + " "
	}
}
