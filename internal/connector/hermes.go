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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
// gateway is not installed.
func (Hermes) Probe(cfg config.Config) ([]Record, error) {
	home := hermesHome()
	statePath := filepath.Join(home, "gateway_state.json")

	rec, ok, err := probeGatewayState(statePath)
	if err == nil && ok {
		return []Record{rec}, nil
	}
	// Unreadable or unparseable state file: gateway.pid still carries the
	// PID. If that fails too, surface the original error.
	if recs, perr := probeGatewayPID(filepath.Join(home, "gateway.pid")); perr == nil {
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
		Status: agent.StatusRunning,
		Detail: "gateway",
		At:     fileModTime(path),
	}
	if ts, err := time.Parse(time.RFC3339Nano, gs.UpdatedAt); err == nil {
		rec.At = ts
	}

	switch gs.GatewayState {
	case "running":
		if gs.ActiveAgents > 0 {
			rec.Status = agent.StatusActive
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
		Status: agent.StatusRunning,
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
