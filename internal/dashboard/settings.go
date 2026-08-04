package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Preview width as a percentage of the popup (excluding the separator).
const (
	defaultPreviewPct = 50
	minPreviewPct     = 15
	maxPreviewPct     = 85
	previewResizeStep = 5 // percent points per ←/→
)

// dashSettings is the on-disk dashboard UI preferences.
type dashSettings struct {
	PreviewPct int `json:"preview_pct"`
}

// loadSettings reads persisted UI prefs from settingsPath, if set.
// Missing or corrupt files leave defaults in place.
func (m *Model) loadSettings() {
	if m.settingsPath == "" {
		return
	}
	data, err := os.ReadFile(m.settingsPath)
	if err != nil {
		return
	}
	var s dashSettings
	if err := json.Unmarshal(data, &s); err != nil {
		return
	}
	m.previewPct = clampPreviewPct(s.PreviewPct)
}

// saveSettings writes the current UI prefs. Failures are ignored so a
// read-only state dir never breaks the popup.
func (m *Model) saveSettings() {
	if m.settingsPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(m.settingsPath), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(dashSettings{PreviewPct: clampPreviewPct(m.previewPct)})
	if err != nil {
		return
	}
	_ = os.WriteFile(m.settingsPath, data, 0o644)
}

func clampPreviewPct(pct int) int {
	if pct < minPreviewPct {
		return minPreviewPct
	}
	if pct > maxPreviewPct {
		return maxPreviewPct
	}
	return pct
}
