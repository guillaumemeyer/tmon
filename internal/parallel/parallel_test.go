package parallel

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestForEachRunsEveryIndex(t *testing.T) {
	const n = 100
	called := make([]int32, n)
	ForEach(n, 8, func(i int) { atomic.AddInt32(&called[i], 1) })
	for i := 0; i < n; i++ {
		if got := atomic.LoadInt32(&called[i]); got != 1 {
			t.Fatalf("index %d called %d times, want 1", i, got)
		}
	}
}

func TestForEachBoundsConcurrency(t *testing.T) {
	const workers = 4
	var active, maxActive int64
	ForEach(32, workers, func(int) {
		cur := atomic.AddInt64(&active, 1)
		for {
			m := atomic.LoadInt64(&maxActive)
			if cur <= m || atomic.CompareAndSwapInt64(&maxActive, m, cur) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond) // hold the slot so overlap is visible
		atomic.AddInt64(&active, -1)
	})
	if got := atomic.LoadInt64(&maxActive); got > workers {
		t.Fatalf("max concurrent calls = %d, want <= %d", got, workers)
	}
	if got := atomic.LoadInt64(&maxActive); got != workers {
		t.Fatalf("max concurrent calls = %d, want %d (pool fully used)", got, workers)
	}
}

func TestForEachNoOpWhenEmpty(t *testing.T) {
	ForEach(0, 8, func(int) { t.Fatal("fn must not run for n = 0") })
	ForEach(-1, 8, func(int) { t.Fatal("fn must not run for n < 0") })
}

func TestForEachClampsWorkersToN(t *testing.T) {
	// n = 1 with workers = 8 must not deadlock and must run exactly once.
	var called int32
	ForEach(1, 8, func(int) { atomic.AddInt32(&called, 1) })
	if got := atomic.LoadInt32(&called); got != 1 {
		t.Fatalf("called = %d, want 1", got)
	}
}

func TestForEachZeroWorkersUsesDefault(t *testing.T) {
	var called int32
	ForEach(5, 0, func(int) { atomic.AddInt32(&called, 1) })
	if got := atomic.LoadInt32(&called); got != 5 {
		t.Fatalf("called = %d, want 5", got)
	}
}
