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

func TestFirstSightingIsRunning(t *testing.T) {
	tr := NewTracker(testOptions())
	tr.BeginPoll()
	if got := tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false); got != StatusRunning {
		t.Errorf("first sighting = %q, want running", got)
	}
}

func TestZeroCPUStaysRunning(t *testing.T) {
	// The bash plugin treats a zero previous CPU read as a first sighting;
	// a just-forked process with no ticks yet must not be called "idle".
	tr := NewTracker(testOptions())
	tr.BeginPoll()
	tr.Evaluate(1, "Grok", "c", "?", 0, 0, false)
	tr.EndPoll()

	tr.BeginPoll()
	if got := tr.Evaluate(1, "Grok", "c", "?", 0, 0, false); got != StatusRunning {
		t.Errorf("zero-CPU second poll = %q, want running", got)
	}
}

func TestIdleDecayGrace(t *testing.T) {
	tr := NewTracker(testOptions())
	tr.BeginPoll()
	tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false) // first sight: running
	tr.EndPoll()

	// Two quiet polls stay in grace (streak 1 and 2); the third quiet poll
	// (streak 3, == IdleDecayPolls) flips to idle. README: "no meaningful
	// activity for 3 consecutive polls".
	for i := 1; i <= 2; i++ {
		tr.BeginPoll()
		got := tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false)
		if got != StatusRunning {
			t.Fatalf("quiet poll %d = %q, want running (grace)", i, got)
		}
		tr.EndPoll()
	}

	tr.BeginPoll()
	if got := tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false); got != StatusIdle {
		t.Errorf("3rd quiet poll = %q, want idle", got)
	}
}

func TestActiveOnCPU(t *testing.T) {
	// Threshold: 500 ms/s * 3 s * 100 ticks/s / 1000 = 150 ticks/poll.
	tr := NewTracker(testOptions())
	tr.BeginPoll()
	tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false)
	tr.EndPoll()

	tr.BeginPoll()
	got := tr.Evaluate(1, "Grok", "c", "?", 1200, 0, false) // +200 ticks
	if got != StatusActive {
		t.Errorf("CPU-active poll = %q, want active", got)
	}
}

func TestActiveOnIO(t *testing.T) {
	tr := NewTracker(testOptions())
	tr.BeginPoll()
	tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false)
	tr.EndPoll()

	tr.BeginPoll()
	got := tr.Evaluate(1, "Grok", "c", "?", 1000, 200000, false) // +200KB IO
	if got != StatusActive {
		t.Errorf("IO-active poll = %q, want active", got)
	}
}

func TestIOBelowThresholdIsNotActive(t *testing.T) {
	tr := NewTracker(testOptions())
	tr.BeginPoll()
	tr.Evaluate(1, "Grok", "c", "?", 1000, 0, false)
	tr.EndPoll()

	tr.BeginPoll()
	got := tr.Evaluate(1, "Grok", "c", "?", 1000, 50000, false) // +50KB < 102400
	if got == StatusActive {
		t.Errorf("sub-threshold IO marked active: %q", got)
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
