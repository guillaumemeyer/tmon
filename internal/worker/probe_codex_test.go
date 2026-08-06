package worker

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// codexTestClient returns a codexRPC client wired to an in-process fake
// app-server: requests arrive over an io.Pipe and each is answered by
// handle, which returns the raw JSON response line(s) (nil = no reply, for
// timeout tests). The handle must not use t.Fatal — it runs on a goroutine.
func codexTestClient(t *testing.T, handle func(m rpcMessage) []string) *codexRPC {
	t.Helper()
	pr, pw := io.Pipe()
	out := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			var m rpcMessage
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				continue
			}
			for _, line := range handle(m) {
				out <- line
			}
		}
	}()
	t.Cleanup(func() { _ = pw.Close() })
	return &codexRPC{in: pw, lines: out}
}

// shortenCodexTimeouts shrinks the JSON-RPC deadlines so a misbehaving fake
// server fails the test quickly instead of after the production 8 s/4 s.
func shortenCodexTimeouts(t *testing.T) {
	t.Helper()
	origInit, origRead := codexInitTimeout, codexReadTimeout
	codexInitTimeout, codexReadTimeout = 2*time.Second, 2*time.Second
	t.Cleanup(func() { codexInitTimeout, codexReadTimeout = origInit, origRead })
}

func TestCodexQuotaWeeklyPreferred(t *testing.T) {
	shortenCodexTimeouts(t)
	client := codexTestClient(t, func(m rpcMessage) []string {
		switch m.Method {
		case "initialize":
			return []string{`{"id":1,"result":{"protocolVersion":"1.0"}}`}
		case "account/read":
			return []string{`{"id":2,"result":{"account":{"planType":"PRO"}}}`}
		case "account/rateLimits/read":
			return []string{`{"id":3,"result":{"rateLimits":{"primary":{"usedPercent":10,"windowDurationMins":60,"resetsAt":0},"secondary":{"usedPercent":42,"windowDurationMins":10080,"resetsAt":1750000000}}}}`}
		}
		return nil
	})
	q, err := codexQuota(client)
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 42 || q.Label != "Weekly (7-day)" || q.Tier != "PRO" {
		t.Errorf("quota = %+v, want 42%% Weekly (7-day) with tier PRO", q)
	}
	if want := time.Unix(1750000000, 0).UTC().Format(time.RFC3339); q.ResetAt != want {
		t.Errorf("ResetAt = %q, want %q", q.ResetAt, want)
	}
}

func TestCodexQuotaPrimaryFallback(t *testing.T) {
	// No secondary window: the primary short window is used instead.
	shortenCodexTimeouts(t)
	client := codexTestClient(t, func(m rpcMessage) []string {
		switch m.Method {
		case "initialize":
			return []string{`{"id":1,"result":{}}`}
		case "account/read":
			return []string{`{"id":2,"result":{"account":{"planType":"FREE"}}}`}
		case "account/rateLimits/read":
			return []string{`{"id":3,"result":{"rateLimits":{"primary":{"usedPercent":33,"windowDurationMins":60,"resetsAt":0}}}}`}
		}
		return nil
	})
	q, err := codexQuota(client)
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 33 || q.Label != "1-hour window" {
		t.Errorf("quota = %+v, want the primary 1-hour window", q)
	}
}

func TestCodexQuotaTierFailureNonFatal(t *testing.T) {
	// account/read failing must not lose the rate-limit data: the tier is
	// best-effort only.
	shortenCodexTimeouts(t)
	client := codexTestClient(t, func(m rpcMessage) []string {
		switch m.Method {
		case "initialize":
			return []string{`{"id":1,"result":{}}`}
		case "account/read":
			return []string{`{"id":2,"error":{"code":-32000,"message":"boom"}}`}
		case "account/rateLimits/read":
			return []string{`{"id":3,"result":{"rateLimits":{"secondary":{"usedPercent":55,"windowDurationMins":10080,"resetsAt":0}}}}`}
		}
		return nil
	})
	q, err := codexQuota(client)
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 55 || q.Tier != "" {
		t.Errorf("quota = %+v, want 55%% with no tier", q)
	}
}

func TestCodexQuotaErrorResponse(t *testing.T) {
	shortenCodexTimeouts(t)
	client := codexTestClient(t, func(m rpcMessage) []string {
		switch m.Method {
		case "initialize":
			return []string{`{"id":1,"result":{}}`}
		case "account/read":
			return []string{`{"id":2,"result":{"account":{"planType":"FREE"}}}`}
		case "account/rateLimits/read":
			return []string{`{"id":3,"error":{"code":-32601,"message":"method not found"}}`}
		}
		return nil
	})
	q, err := codexQuota(client)
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" {
		t.Errorf("quota = %+v, want a status text for an RPC error", q)
	}
}

func TestCodexQuotaTimeout(t *testing.T) {
	origInit, origRead := codexInitTimeout, codexReadTimeout
	codexInitTimeout, codexReadTimeout = 100*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { codexInitTimeout, codexReadTimeout = origInit, origRead })
	client := codexTestClient(t, func(m rpcMessage) []string { return nil }) // never answers
	q, err := codexQuota(client)
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" || !strings.Contains(q.StatusText, "initialize") {
		t.Errorf("quota = %+v, want an initialize timeout status", q)
	}
}

func TestCodexQuotaNoActiveWindow(t *testing.T) {
	shortenCodexTimeouts(t)
	client := codexTestClient(t, func(m rpcMessage) []string {
		switch m.Method {
		case "initialize":
			return []string{`{"id":1,"result":{}}`}
		case "account/read":
			return []string{`{"id":2,"result":{"account":{"planType":"FREE"}}}`}
		case "account/rateLimits/read":
			return []string{`{"id":3,"result":{"rateLimits":{"primary":{"usedPercent":0,"windowDurationMins":60}}}}`}
		}
		return nil
	})
	q, err := codexQuota(client)
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" {
		t.Errorf("quota = %+v, want a 'no active window' status", q)
	}
}

func TestCodexQuotaUnexpectedResponse(t *testing.T) {
	shortenCodexTimeouts(t)
	client := codexTestClient(t, func(m rpcMessage) []string {
		switch m.Method {
		case "initialize":
			return []string{`{"id":1,"result":{}}`}
		case "account/read":
			return []string{`{"id":2,"result":{"account":{"planType":"FREE"}}}`}
		case "account/rateLimits/read":
			return []string{`{"id":3,"result":{}}`} // no rateLimits key
		}
		return nil
	})
	q, err := codexQuota(client)
	if err != nil {
		t.Fatal(err)
	}
	if q.Pct != 0 || q.StatusText == "" {
		t.Errorf("quota = %+v, want an 'unexpected response' status", q)
	}
}

func TestCodexWindowLabel(t *testing.T) {
	cases := []struct {
		name string
		win  *codexWindow
		want string
	}{
		{"nil", nil, "Rate limit"},
		{"weekly", &codexWindow{WindowDurationMins: 10080}, "Weekly (7-day)"},
		{"hourly", &codexWindow{WindowDurationMins: 60}, "1-hour window"},
		{"minutes", &codexWindow{WindowDurationMins: 5}, "5-min window"},
		{"zero", &codexWindow{WindowDurationMins: 0}, "Rate limit"},
	}
	for _, c := range cases {
		if got := codexWindowLabel(c.win); got != c.want {
			t.Errorf("%s: codexWindowLabel = %q, want %q", c.name, got, c.want)
		}
	}
}
