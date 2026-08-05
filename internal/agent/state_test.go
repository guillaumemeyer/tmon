package agent

import (
	"testing"

	"github.com/guillaumemeyer/tmon/internal/config"
)

// testOptions mirrors the plugin defaults: 3s polls, 500 ms/s CPU threshold,
// 102400 bytes IO threshold, 3-poll idle grace, 100 ticks/sec.
func testOptions() Options {
	return NewOptions(config.Config{
		PollIntervalMs:      3000,
		ActivityThresholdMs: 500,
		IOThreshold:         102400,
		IdleDecayPolls:      3,
		CLKTicks:            100,
	})
}

func TestFirstSightingIsIdle(t *testing.T) {
	tr := NewTracker(testOptions())
	tr.BeginPoll()
	if got := tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false); got != StatusIdle {
		t.Errorf("first sighting = %q, want idle", got)
	}
}

func TestSeedPrevEnablesCPUDelta(t *testing.T) {
	// Simulate a prior poll that only wrote CPU baselines to disk.
	tr := NewTracker(testOptions())
	tr.SeedPrev([]AgentState{{
		PID: 1, Label: "Grok", Status: StatusIdle, CPU: 1000, IO: 0,
	}})
	tr.BeginPoll()
	// +200 ticks ≥ threshold (150) → working
	got := tr.Evaluate(1, "Grok", "c", "?", 1200, 0, false)
	if got != StatusWorking {
		t.Errorf("seeded CPU-active poll = %q, want working", got)
	}
}

func TestSeedPrevEmptyIsNoop(t *testing.T) {
	tr := NewTracker(testOptions())
	tr.SeedPrev(nil)
	tr.BeginPoll()
	if got := tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false); got != StatusIdle {
		t.Errorf("empty seed first sighting = %q, want idle", got)
	}
}

func TestSeedPrevCopiesState(t *testing.T) {
	// Caller mutates the slice after SeedPrev; tracker must not see it.
	agents := []AgentState{{PID: 1, Label: "Grok", CPU: 1000, IO: 0}}
	tr := NewTracker(testOptions())
	tr.SeedPrev(agents)
	agents[0].CPU = 999999
	tr.BeginPoll()
	got := tr.Evaluate(1, "Grok", "c", "?", 1200, 0, false)
	if got != StatusWorking {
		t.Errorf("after caller mutation got %q, want working (seed must copy)", got)
	}
}

func TestZeroCPUStaysIdle(t *testing.T) {
	// A just-forked process with no ticks yet must not be mislabeled; the
	// delta math needs a baseline poll anyway.
	tr := NewTracker(testOptions())
	tr.BeginPoll()
	tr.Evaluate(1, "Grok", "c", "?", 0, 0, false)
	tr.EndPoll()

	tr.BeginPoll()
	if got := tr.Evaluate(1, "Grok", "c", "?", 0, 0, false); got != StatusIdle {
		t.Errorf("zero-CPU second poll = %q, want idle", got)
	}
}

func TestIdleDecayGrace(t *testing.T) {
	tr := NewTracker(testOptions())
	tr.BeginPoll()
	tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false) // first sight: idle
	tr.EndPoll()

	tr.BeginPoll()
	tr.Evaluate(1, "Grok", "c", "?", 1200, 0, false) // working
	tr.EndPoll()

	// Two quiet polls stay in grace (streak 1 and 2); the third quiet poll
	// (streak 3, == IdleDecayPolls) flips to idle. README: "no meaningful
	// activity for 3 consecutive polls".
	for i := 1; i <= 2; i++ {
		tr.BeginPoll()
		got := tr.Evaluate(1, "Grok", "c", "?", 1200, 0, false)
		if got != StatusWorking {
			t.Fatalf("quiet poll %d = %q, want working (grace)", i, got)
		}
		tr.EndPoll()
	}

	tr.BeginPoll()
	if got := tr.Evaluate(1, "Grok", "c", "?", 1200, 0, false); got != StatusIdle {
		t.Errorf("3rd quiet poll = %q, want idle", got)
	}
}

func TestWorkingOnCPU(t *testing.T) {
	// Threshold: 500 ms/s * 3 s * 100 ticks/s / 1000 = 150 ticks/poll.
	tr := NewTracker(testOptions())
	tr.BeginPoll()
	tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false)
	tr.EndPoll()

	tr.BeginPoll()
	got := tr.Evaluate(1, "Grok", "c", "?", 1200, 0, false) // +200 ticks
	if got != StatusWorking {
		t.Errorf("CPU-active poll = %q, want working", got)
	}
}

func TestWorkingOnIO(t *testing.T) {
	tr := NewTracker(testOptions())
	tr.BeginPoll()
	tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false)
	tr.EndPoll()

	tr.BeginPoll()
	got := tr.Evaluate(1, "Grok", "c", "?", 1000, 200000, false) // +200KB IO
	if got != StatusWorking {
		t.Errorf("IO-active poll = %q, want working", got)
	}
}

func TestIOBelowThresholdIsNotWorking(t *testing.T) {
	tr := NewTracker(testOptions())
	tr.BeginPoll()
	tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false)
	tr.EndPoll()

	tr.BeginPoll()
	got := tr.Evaluate(1, "Grok", "c", "?", 1000, 50000, false) // +50KB < 102400
	if got == StatusWorking {
		t.Errorf("sub-threshold IO marked working: %q", got)
	}
}

func TestBlockedOverridesActivity(t *testing.T) {
	tr := NewTracker(testOptions())
	tr.BeginPoll()
	tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false)
	tr.EndPoll()

	tr.BeginPoll()
	got := tr.Evaluate(1, "Grok", "c", "?", 5000, 999999, true) // busy AND blocked
	if got != StatusBlocked {
		t.Errorf("blocked busy poll = %q, want blocked", got)
	}
}

func TestBlockedResetsIdleStreak(t *testing.T) {
	tr := NewTracker(testOptions())
	tr.BeginPoll()
	tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false)
	tr.EndPoll()
	tr.BeginPoll()
	tr.Evaluate(1, "Grok", "c", "?", 1000, 0, true) // blocked
	tr.EndPoll()

	// Back to a quiet unblocked poll: streak restarts, still grace.
	tr.BeginPoll()
	if got := tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false); got != StatusBlocked {
		t.Errorf("poll after blocked = %q, want blocked (grace keeps old status)", got)
	}
}

func TestEndPollPrunesDeadAgents(t *testing.T) {
	tr := NewTracker(testOptions())
	tr.BeginPoll()
	tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false)
	tr.EndPoll()

	tr.BeginPoll() // pid 1 not evaluated this poll (process died)
	tr.EndPoll()

	if len(tr.Snapshot()) != 0 {
		t.Error("dead agent not pruned")
	}
}

func TestSnapshotSortedByPID(t *testing.T) {
	tr := NewTracker(testOptions())
	tr.BeginPoll()
	tr.Evaluate(2, "Grok", "c", "?", 1000, 0, false)
	tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false)
	tr.Evaluate(3, "Grok", "c", "?", 1000, 0, false)
	got := tr.Snapshot()
	for i := 1; i < len(got); i++ {
		if got[i].PID < got[i-1].PID {
			t.Fatalf("snapshot not sorted: %+v", got)
		}
	}
}

// ─── Authoritative (connector) path ──────────────────────────────────────────

func TestEvaluateAuthoritativeRecordsStatusAndDetail(t *testing.T) {
	tr := NewTracker(testOptions())
	tr.BeginPoll()
	if got := tr.EvaluateAuthoritative(1, "Grok", "code/tmon", "?", StatusWorking, "phase:reasoning", "Refactor login"); got != StatusWorking {
		t.Fatalf("status = %q, want working", got)
	}
	snap := tr.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot len = %d, want 1", len(snap))
	}
	if snap[0].Detail != "phase:reasoning" {
		t.Errorf("Detail = %q, want phase:reasoning", snap[0].Detail)
	}
	if snap[0].Title != "Refactor login" {
		t.Errorf("Title = %q, want Refactor login", snap[0].Title)
	}
	if snap[0].CWD != "code/tmon" {
		t.Errorf("CWD = %q, want code/tmon", snap[0].CWD)
	}
	if snap[0].LastTs == 0 {
		t.Error("LastTs not stamped on first authoritative sighting")
	}
}

func TestEvaluateAuthoritativeResetsStreakOnChange(t *testing.T) {
	tr := NewTracker(testOptions())
	tr.BeginPoll()
	tr.EvaluateAuthoritative(1, "Grok", "c", "?", StatusWorking, "tool:Bash", "")
	tr.EndPoll()

	// Same status: streak must not reset (no flicker).
	tr.BeginPoll()
	tr.EvaluateAuthoritative(1, "Grok", "c", "?", StatusWorking, "tool:Read", "")
	snap := tr.Snapshot()
	if snap[0].IdleStreak != 0 {
		t.Errorf("same-status poll: IdleStreak = %d, want 0", snap[0].IdleStreak)
	}
	tr.EndPoll()

	// Blocked: streak resets and LastTs moves on.
	tr.BeginPoll()
	tr.EvaluateAuthoritative(1, "Grok", "c", "?", StatusBlocked, "permission:Bash", "")
	snap = tr.Snapshot()
	if snap[0].IdleStreak != 0 {
		t.Errorf("blocked poll: IdleStreak = %d, want 0", snap[0].IdleStreak)
	}
	if snap[0].LastTs == 0 {
		t.Error("LastTs not stamped on transition")
	}
}

func TestEvaluateAuthoritativePreservesBaseline(t *testing.T) {
	// A connector agent that goes quiet must leave CPU/IO intact so the
	// heuristic path has a baseline when it takes over.
	tr := NewTracker(testOptions())
	tr.BeginPoll()
	tr.EvaluateAuthoritative(1, "Grok", "c", "?", StatusWorking, "tool:Bash", "")
	tr.EndPoll()

	// Heuristic takeover: the authoritative record left CPU at 0, so this
	// poll is a fresh baseline (idle), then quiet polls stay idle (grace of
	// 3 keeps the previous status until the streak runs out).
	tr.BeginPoll()
	tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false)
	tr.EndPoll()

	for i := 0; i < 3; i++ {
		tr.BeginPoll()
		tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false)
		tr.EndPoll()
	}

	tr.BeginPoll()
	if got := tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false); got != StatusIdle {
		t.Errorf("quiet polls after connector = %q, want idle", got)
	}
}
