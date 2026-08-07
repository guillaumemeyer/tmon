// Package parallel provides a small bounded worker pool for independent
// I/O and subprocess work. The dashboard refresh, the status poll, and the
// worker use it to overlap slow per-agent operations: /proc reads, tmux
// subprocess spawns, and network probes.
package parallel

import "sync"

// DefaultWorkers is the concurrency bound used when a caller passes
// workers <= 0. The hot paths are I/O-bound (file reads, subprocess
// spawns, network calls), so the bound is tuned for I/O concurrency, not
// CPU count. It keeps a fork storm of tmux clients from slowing the
// machine.
const DefaultWorkers = 8

// ForEach runs fn(i) for each i in [0, n) with at most workers concurrent
// calls. When n <= 0 it does nothing. When workers <= 0 it uses
// DefaultWorkers; when n < workers the bound is clamped to n. It returns
// after every call has completed.
//
// Callers that write per-index results should write to pre-sized slots:
// each index is written by exactly one goroutine, so no locking is needed.
func ForEach(n, workers int, fn func(i int)) {
	if n <= 0 {
		return
	}
	if workers <= 0 {
		workers = DefaultWorkers
	}
	if n < workers {
		workers = n
	}
	var wg sync.WaitGroup
	jobs := make(chan int)
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := range jobs {
				fn(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		jobs <- i // block while all workers are busy
	}
	close(jobs)
	wg.Wait()
}
