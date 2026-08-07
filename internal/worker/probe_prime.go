// probe_prime.go — Prime Agent quota probe.
//
// Prime Agent (PrimeIntellect) is bring-your-own-key like Hermes: this
// machine's prime-agent runs DeepSeek models, so the probe reports the
// DeepSeek account balance — the same number Hermes shows when Hermes is
// on DeepSeek too, which is correct: they share one account. The key comes
// from DEEPSEEK_API_KEY in the worker's environment (the same variable the
// prime-agent process itself inherits). No key, an HTTP error, or an
// unparseable body produce a Quota with StatusText instead of an error, so
// the worker keeps running and the dashboard shows why.
package worker

import (
	"os"

	"github.com/guillaumemeyer/tmon/internal/config"
)

// primeBalanceURL is the DeepSeek OpenAI-compatible balance endpoint. A var
// so tests can point it at a fake server.
var primeBalanceURL = "https://api.deepseek.com/v1"

// probePrime reports the DeepSeek account balance for prime-agent sessions,
// so a Prime row gets an account-level usage line instead of "📊 Usage: ?".
func probePrime(cfg config.Config) (Quota, error) {
	key := os.Getenv("DEEPSEEK_API_KEY")
	if key == "" {
		q := Quota{
			StatusText:   "no DEEPSEEK_API_KEY in the worker environment",
			AuthHelpText: "export DEEPSEEK_API_KEY (prime-agent uses DeepSeek)",
		}
		return q, nil
	}
	return fetchBalanceQuota(primeBalanceURL, key, "deepseek")
}
