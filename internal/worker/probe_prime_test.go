package worker

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

func TestProbePrimeBalance(t *testing.T) {
	oldURL := primeBalanceURL
	t.Cleanup(func() { primeBalanceURL = oldURL })
	t.Setenv("DEEPSEEK_API_KEY", "sk-test-456")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/balance" {
			t.Errorf("path = %q, want /user/balance", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test-456" {
			t.Errorf("Authorization = %q", got)
		}
		fmt.Fprint(w, `{"is_available": true, "balance_infos": [{"currency": "USD", "total_balance": "65.85"}]}`)
	}))
	defer srv.Close()
	primeBalanceURL = srv.URL

	q, err := probePrime(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.Label != "DeepSeek balance" || q.Balance != 65.85 || q.Currency != "$" {
		t.Errorf("quota = %+v, want DeepSeek balance $65.85", q)
	}
	want := []agent.QuotaWindow{{Pct: 0, Label: "DeepSeek balance", Balance: 65.85, Currency: "$"}}
	if !reflect.DeepEqual(q.Windows, want) {
		t.Errorf("windows = %+v, want %+v", q.Windows, want)
	}
}

func TestProbePrimeNoKey(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	q, err := probePrime(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" || q.AuthHelpText == "" {
		t.Errorf("quota = %+v, want status + auth help without a key", q)
	}
}

func TestProbePrimeHTTPError(t *testing.T) {
	oldURL := primeBalanceURL
	t.Cleanup(func() { primeBalanceURL = oldURL })
	t.Setenv("DEEPSEEK_API_KEY", "sk-test-456")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	primeBalanceURL = srv.URL

	q, err := probePrime(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" {
		t.Errorf("quota = %+v, want a status text on HTTP error", q)
	}
}
