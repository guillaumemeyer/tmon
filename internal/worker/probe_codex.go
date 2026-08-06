package worker

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/guillaumemeyer/tmon/internal/config"
)

// Codex probe timeouts, per the plan: 8 s for initialize, 4 s for each
// read. The overall conversation is bounded by the sum. Vars so tests can
// shorten them.
var (
	codexInitTimeout  = 8 * time.Second
	codexReadTimeout  = 4 * time.Second
	codexTotalTimeout = 16 * time.Second
)

// codexAppServerArgs matches the plan: sandbox read-only, approval mode
// untrusted, app-server mode.
var codexAppServerArgs = []string{"-s", "read-only", "-a", "untrusted", "app-server"}

// probeCodex spawns `codex app-server` and reads the account plan tier and
// rate-limit windows over JSON-RPC. The weekly (secondary) window is mapped
// to the quota block, falling back to the primary window. The process is
// always terminated after the probe.
func probeCodex(cfg config.Config) (Quota, error) {
	q := Quota{}
	path, err := exec.LookPath("codex")
	if err != nil {
		q.StatusText = "codex not found on PATH"
		return q, nil
	}
	cmd := exec.Command(path, codexAppServerArgs...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return q, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return q, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return q, err
	}
	if err := cmd.Start(); err != nil {
		q.StatusText = "codex app-server failed to start: " + err.Error()
		return q, nil
	}
	var stderrBuf strings.Builder
	go func() { _, _ = io.Copy(&stderrBuf, stderr) }()
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	client := &codexRPC{in: stdin, lines: codexLines(stdout)}
	quota, err := codexQuota(client)
	if err != nil && quota.StatusText == "" {
		quota.StatusText = "codex probe: " + err.Error()
	}
	return quota, nil
}

// codexQuota performs the JSON-RPC conversation against an already-started
// app-server: initialize, initialized, account/read, account/rateLimits/read.
// Split from probeCodex so tests can drive it against a fake server over a
// pipe.
func codexQuota(client *codexRPC) (Quota, error) {
	q := Quota{}
	if _, err := client.call(1, "initialize", map[string]any{
		"clientInfo":   map[string]string{"name": "tmon", "version": "0"},
		"capabilities": map[string]any{},
	}, codexInitTimeout); err != nil {
		q.StatusText = "codex initialize: " + err.Error()
		return q, nil
	}
	client.notify("initialized", nil)

	// account/read → plan tier (best-effort; a failure is not fatal).
	tier := ""
	if res, err := client.call(2, "account/read", map[string]any{"refreshToken": false}, codexReadTimeout); err == nil {
		var ar struct {
			Account *struct {
				PlanType string `json:"planType"`
			} `json:"account"`
		}
		if json.Unmarshal(res, &ar) == nil && ar.Account != nil {
			tier = ar.Account.PlanType
		}
	}

	res, err := client.call(3, "account/rateLimits/read", map[string]any{}, codexReadTimeout)
	if err != nil {
		q.StatusText = "codex rate limits: " + err.Error()
		return q, nil
	}
	var rl struct {
		RateLimits *struct {
			Primary   *codexWindow `json:"primary"`
			Secondary *codexWindow `json:"secondary"`
		} `json:"rateLimits"`
	}
	if json.Unmarshal(res, &rl) != nil || rl.RateLimits == nil {
		q.StatusText = "unexpected codex rate-limits response"
		return q, nil
	}

	// Prefer the weekly (secondary) window, matching the plan; fall back to
	// the primary short window.
	w := rl.RateLimits.Secondary
	label := "Weekly (7-day)"
	if w == nil {
		w = rl.RateLimits.Primary
		label = codexWindowLabel(w)
	}
	if w == nil || w.UsedPercent <= 0 {
		q.StatusText = "no active codex rate-limit window"
		return q, nil
	}
	q = Quota{Pct: w.UsedPercent, Label: label, Tier: tier}
	if w.ResetsAt > 0 {
		q.ResetAt = normalizeResetAt(time.Unix(w.ResetsAt, 0).UTC().Format(time.RFC3339))
	}
	return q, nil
}

type codexWindow struct {
	UsedPercent        int   `json:"usedPercent"`
	WindowDurationMins int   `json:"windowDurationMins"`
	ResetsAt           int64 `json:"resetsAt"`
}

// codexWindowLabel names a window from its duration in minutes.
func codexWindowLabel(w *codexWindow) string {
	if w == nil {
		return "Rate limit"
	}
	switch mins := w.WindowDurationMins; {
	case mins >= 6*24*60:
		return "Weekly (7-day)"
	case mins >= 60:
		return fmt.Sprintf("%d-hour window", mins/60)
	case mins >= 1:
		return fmt.Sprintf("%d-min window", mins)
	}
	return "Rate limit"
}

// codexRPC is a minimal JSON-RPC 2.0 client over a stdio pair with
// per-call deadlines.
type codexRPC struct {
	in    io.Writer
	lines <-chan string
}

type rpcMessage struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// call sends a request with the given id and waits for the matching
// response within timeout. Notifications and unrelated messages are
// skipped.
func (c *codexRPC) call(id int, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	if err := c.send(id, method, params); err != nil {
		return nil, err
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			return nil, fmt.Errorf("%s timed out", method)
		case line, ok := <-c.lines:
			if !ok {
				return nil, fmt.Errorf("codex app-server closed the stream")
			}
			var m rpcMessage
			if json.Unmarshal([]byte(line), &m) != nil || m.ID != id {
				continue
			}
			if m.Error != nil {
				return nil, fmt.Errorf("%s: %s", method, m.Error.Message)
			}
			return m.Result, nil
		}
	}
}

// notify sends a JSON-RPC notification (no id), e.g. "initialized".
func (c *codexRPC) notify(method string, params any) {
	msg := rpcMessage{Method: method}
	if params != nil {
		if b, err := json.Marshal(params); err == nil {
			msg.Params = b
		}
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	_, _ = c.in.Write(append(b, '\n'))
}

func (c *codexRPC) send(id int, method string, params any) error {
	msg := rpcMessage{ID: id, Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		msg.Params = b
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = c.in.Write(append(b, '\n'))
	return err
}

// codexLines streams stdout lines from the app-server. The channel closes
// when the stream ends (process killed or exited).
func codexLines(r io.Reader) <-chan string {
	out := make(chan string)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			out <- sc.Text()
		}
	}()
	return out
}
