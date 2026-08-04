package connector

import (
	"testing"

	"github.com/guillaumemeyer/tmon/internal/config"
)

func TestOpenClawEnabledGatesOnDataDir(t *testing.T) {
	home := withHome(t)
	cfg := config.Defaults()
	if (OpenClaw{}).Enabled(cfg) {
		t.Error("enabled with no ~/.openclaw")
	}
	writeFile(t, home+"/.openclaw/keep", "")
	if !(OpenClaw{}).Enabled(cfg) {
		t.Error("not enabled with ~/.openclaw present")
	}
}

func TestOpenClawProbeEmitsNothing(t *testing.T) {
	withHome(t)
	recs, err := (OpenClaw{}).Probe(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("records = %+v, want none (stub connector)", recs)
	}
}
