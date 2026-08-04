// hermes.go — Hermes Agent connector.
//
// Hermes is EXACT + HOOKS tier: the gateway writes its own live state, so
// no installation is needed. The connector reads ~/.hermes/gateway_state.json
// (with gateway.pid as a fallback) and emits one record for the gateway
// process — which the /proc signature table does not match, so the poll loop
// injects it as a connector-only agent.
//
// The gateway is a long-running daemon; its state file is only rewritten
// when something changes. The freshness gate therefore treats a stale file
// as "gateway idle": the record decays to the /proc path (the interactive
// TUI keeps its own heuristic row) until Hermes becomes active again.
package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

// hermesHome returns the Hermes config directory (~/.hermes). A var so
// tests can point it at a fixture directory.
var hermesHome = func() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".hermes")
}

// Hermes reads the Hermes gateway's live state.
type Hermes struct{}

// hermesGatewayState is the subset of gateway_state.json the connector uses.
type hermesGatewayState struct {
	PID          int    `json:"pid"`
	GatewayState string `json:"gateway_state"`
	ActiveAgents int    `json:"active_agents"`
	UpdatedAt    string `json:"updated_at"`
}

// hermesGatewayPID is the subset of gateway.pid the connector uses as a
// fallback when gateway_state.json is unreadable.
type hermesGatewayPID struct {
	PID int `json:"pid"`
}

func (Hermes) Name() string { return "hermes" }

// Enabled reports whether the Hermes gateway state surface exists.
func (Hermes) Enabled(cfg config.Config) bool {
	p := filepath.Join(hermesHome(), "gateway_state.json")
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// Probe returns one record for the gateway process, or none when the
// gateway is not installed. Token usage comes from `hermes insights`,
// TTL-cached so the analysis runs at most once a minute.
func (Hermes) Probe(cfg config.Config) ([]Record, error) {
	home := hermesHome()
	statePath := filepath.Join(home, "gateway_state.json")

	rec, ok, err := probeGatewayState(statePath)
	if err == nil && ok {
		rec.Usage = hermesUsage(cfg)
		return []Record{rec}, nil
	}
	// Unreadable or unparseable state file: gateway.pid still carries the
	// PID. If that fails too, surface the original error.
	if recs, perr := probeGatewayPID(filepath.Join(home, "gateway.pid")); perr == nil {
		if u := hermesUsage(cfg); !u.Empty() {
			for i := range recs {
				recs[i].Usage = u
			}
		}
		return recs, nil
	} else if err != nil {
		return nil, err
	} else {
		return nil, perr
	}
}

func probeGatewayState(path string) (Record, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Record{}, false, err
	}
	var gs hermesGatewayState
	if err := json.Unmarshal(b, &gs); err != nil {
		return Record{}, false, err
	}
	if gs.PID <= 0 {
		return Record{}, false, nil
	}

	rec := Record{
		PID:    gs.PID,
		Label:  "Hermes",
		Status: agent.StatusIdle,
		Detail: "gateway",
		At:     fileModTime(path),
	}
	if ts, err := time.Parse(time.RFC3339Nano, gs.UpdatedAt); err == nil {
		rec.At = ts
	}

	switch gs.GatewayState {
	case "running":
		if gs.ActiveAgents > 0 {
			rec.Status = agent.StatusWorking
			rec.Detail = fmt.Sprintf("%d active agents", gs.ActiveAgents)
		}
	default:
		rec.Status = agent.StatusIdle
		rec.Detail = "gateway:" + gs.GatewayState
	}
	return rec, true, nil
}

func probeGatewayPID(path string) ([]Record, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var gp hermesGatewayPID
	if err := json.Unmarshal(b, &gp); err != nil {
		return nil, err
	}
	if gp.PID <= 0 {
		return nil, nil
	}
	return []Record{{
		PID:    gp.PID,
		Label:  "Hermes",
		Status: agent.StatusIdle,
		Detail: "gateway",
		At:     fileModTime(path),
	}}, nil
}

// fileModTime returns a file's mtime, or now when it cannot be stat'ed —
// the freshness gate then keeps the record (missing timestamp is treated
// as "just seen", and liveness still protects against dead PIDs).
func fileModTime(path string) time.Time {
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime()
	}
	return time.Now()
}

// hermesInsightsTTL bounds how often the (≈0.5s) insights analysis runs.
const hermesInsightsTTL = 60 * time.Second

// hermesInsightsOutput runs `hermes insights --days 1` and returns its
// stdout. A seam so tests can inject a fixture instead of spawning hermes.
var hermesInsightsOutput = func(cfg config.Config) (string, error) {
	bin, err := exec.LookPath("hermes")
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "insights", "--days", "1").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

var (
	insightsInputRe  = regexp.MustCompile(`Input tokens:\s+([\d,]+)`)
	insightsOutputRe = regexp.MustCompile(`Output tokens:\s+([\d,]+)`)
)

// hermesUsage returns gateway-level token usage from `hermes insights`
// (input + output tokens), TTL-cached on disk so every fresh `tmon status`
// process reuses the last analysis. Zero usage when insights fails or
// reports nothing.
func hermesUsage(cfg config.Config) agent.Usage {
	out, err := runCachedTTL(cfg.StateDir, "hermes-insights-1d", hermesInsightsTTL, func() (string, error) {
		return hermesInsightsOutput(cfg)
	})
	if err != nil {
		return agent.Usage{}
	}
	in, outT := parseInsightsTokens(out)
	if in+outT <= 0 {
		return agent.Usage{}
	}
	return agent.Usage{TokensUsed: in + outT}
}

// parseInsightsTokens extracts the input/output token counts from the
// insights box table ("Input tokens: 79,757"). Unparseable output yields 0.
func parseInsightsTokens(out string) (inTokens, outTokens int64) {
	if m := insightsInputRe.FindStringSubmatch(out); len(m) == 2 {
		inTokens, _ = strconv.ParseInt(strings.ReplaceAll(m[1], ",", ""), 10, 64)
	}
	if m := insightsOutputRe.FindStringSubmatch(out); len(m) == 2 {
		outTokens, _ = strconv.ParseInt(strings.ReplaceAll(m[1], ",", ""), 10, 64)
	}
	return inTokens, outTokens
}
