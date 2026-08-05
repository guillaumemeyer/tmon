# Contributing to tmon

Thanks for helping grow the fleet. The fastest path is a **community
connector** — the interface is tiny, and new agents are how tmon stays useful
as the ecosystem multiplies (same growth model that made TPM the default
tmux plugin story).

## Prerequisites

- **Go** matching `go.mod` (currently 1.26+)
- **tmux ≥ 3.2** if you want to exercise the dashboard end-to-end
- From the repo root: `go test ./...` should pass before you open a PR

## How detection works

1. **Process signatures** — `internal/detect/signatures.go` matches process
   command lines. **First match wins**, so put specific patterns before broad
   ones.
2. **Connectors** — `internal/connector/` supplies authoritative working /
   blocked / idle state when the agent publishes it (state files, hooks, …).
   Without a connector, tmon falls back to CPU/IO and pane-content heuristics.
3. **Dashboard identity** — display name in `internal/dashboard/names.go`
   (`agentFullName`) and brand color (`agentIdentityColor`).

## Connector interface

```go
// internal/connector/connector.go
type Connector interface {
	Name() string
	Enabled(cfg config.Config) bool
	Probe(cfg config.Config) ([]Record, error)
}
```

- **`Name`** — short id used by `@tmon-connectors` / `TMON_CONNECTORS`
- **`Enabled`** — cheap presence check (agent installed / state paths exist)
- **`Probe`** — return fresh `Record`s (PID, Label matching detect, Status,
  Detail, At, optional Title / Usage / Profile). Failures are isolated: one
  bad connector never kills the poll.

Read `aider.go` for a minimal native probe, or `claude.go` / `hermes.go` for
hooks- and DB-backed examples.

## Copy-paste template

Create `internal/connector/<agent>.go`:

```go
package connector

import (
	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

// MyAgent reads authoritative state for <agent>.
type MyAgent struct{}

func (MyAgent) Name() string { return "myagent" }

// Enabled reports whether the agent looks installed (state dirs / configs).
func (MyAgent) Enabled(cfg config.Config) bool {
	// return dirExists(homePath(".myagent"))
	return false
}

// Probe returns working / blocked / idle records for live processes.
func (MyAgent) Probe(cfg config.Config) ([]Record, error) {
	// Prefer the agent's own state files or hooks over heuristics.
	// Each Record needs PID, Label (must match detect), Status, and At.
	_ = agent.StatusWorking
	_ = cfg
	return nil, nil
}
```

Add a `_test.go` next to it. Then register the connector:

```go
// internal/connector/connector.go — Registry
var Registry = []Connector{
	// …
	MyAgent{},
}
```

## Checklist for a new agent

- [ ] Detect signatures in `internal/detect/signatures.go` (+ matrix cases in
      `signatures_test.go`)
- [ ] Connector: `Enabled` + `Probe`, registered in `Registry`
- [ ] Display name in `agentFullName` and identity color in
      `agentIdentityColor` (`internal/dashboard/names.go`)
- [ ] Unit tests for the connector (and dashboard bits if you touch them)
- [ ] README: supported-agents list + feature matrix row
- [ ] Hooks (if applicable): install path via `tmon hooks install <agent>` —
      follow existing agents under `cmd/tmon/hooks.go` / `hooks/`

### Hook-based agents

Some agents only expose lifecycle events through hooks (Claude Code, Codex,
Cursor, …). Install helpers already exist — extend them rather than inventing
a parallel path. Without hooks, those agents often report idle-only via a
native fallback.

## PR expectations

- `go test ./...` (and `go vet ./...`) clean
- Stay focused: no drive-by refactors unrelated to the agent
- Prefer small, reviewable diffs; match existing style and comments

Questions? Open an issue describing the agent’s state surface (files, hooks,
or “heuristic only”) and we can point you at the closest existing connector.
