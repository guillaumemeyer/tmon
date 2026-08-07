package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/parallel"
)

// QuotaTTL is how often the quota probes may hit the network. The usage
// endpoints are undocumented and aggressively rate-limited, so 15 minutes
// per window is both kind to the APIs and fresh enough for the dashboard.
const QuotaTTL = 15 * time.Minute

// Probe is one provider quota source. Key is the usage.json quota key
// ("claude", "codex") and Label the tmon agent label the quota attaches to;
// Run performs the network call. Run must tolerate its own timeouts and
// return (Quota, error) where an error means "no usable window at all".
type Probe struct {
	Key   string
	Label string
	Run   func(cfg config.Config) (Quota, error)
}

// probes is the ordered set of quota sources. A var so tests can inject
// fake probes into the loop.
var probes = []Probe{
	{Key: "claude", Label: "Claude", Run: probeClaude},
	{Key: "grok", Label: "Grok", Run: probeGrok},
	{Key: "codex", Label: "Codex", Run: probeCodex},
	{Key: "hermes", Label: "Hermes", Run: probeHermes},
	{Key: "prime", Label: "Prime", Run: probePrime},
}

// runQuotaProbes probes every source and returns the quota map. A failing
// probe yields a Quota with StatusText so the failure is visible in the
// dashboard instead of silently absent. Probes are network calls and run
// in parallel, so the worker cycle waits for the slowest source rather
// than the sum of all of them. Each worker writes its own slot; the map
// build below is single-threaded.
func runQuotaProbes(cfg config.Config) map[string]Quota {
	type probeResult struct {
		key string
		q   Quota
	}
	results := make([]probeResult, len(probes))
	parallel.ForEach(len(probes), parallel.DefaultWorkers, func(i int) {
		p := probes[i]
		q, err := p.Run(cfg)
		if err != nil {
			q.StatusText = err.Error()
		}
		results[i] = probeResult{key: p.Key, q: q}
	})
	out := make(map[string]Quota, len(results))
	for _, r := range results {
		out[r.key] = r.q
	}
	return out
}

// lazyQuotaPath is the on-disk TTL cache for the worker-off fallback.
func lazyQuotaPath(stateDir string) string {
	return filepath.Join(stateDir, "usage", "quota-lazy.json")
}

// LazyQuota returns the quota map for the worker-off fallback: it probes at
// most once per QuotaTTL and reuses the cached result in between. It runs
// network probes inside the caller (the status poll) — call it only when
// the worker is disabled, which is the explicit opt-out that authorizes
// network use in the poll.
func LazyQuota(cfg config.Config) map[string]Quota {
	type cache struct {
		ProbedAt time.Time        `json:"probedAt"`
		Quota    map[string]Quota `json:"quota"`
	}
	path := lazyQuotaPath(cfg.StateDir)
	if b, err := os.ReadFile(path); err == nil {
		var c cache
		if json.Unmarshal(b, &c) == nil && time.Since(c.ProbedAt) < QuotaTTL && len(c.Quota) > 0 {
			return c.Quota
		}
	}
	quota := runQuotaProbes(cfg)
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	if b, err := json.Marshal(cache{ProbedAt: time.Now(), Quota: quota}); err == nil {
		_ = os.WriteFile(path, b, 0o644)
	}
	return quota
}
