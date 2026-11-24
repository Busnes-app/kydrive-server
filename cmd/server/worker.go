package main

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/store"
)

// electRetry is how often a standby replica tries to become the scheduled worker.
var electRetry = 10 * time.Second

// electGrace is how long a new worker waits before starting jobs: longer than a lost
// worker's session probe (one second, plus its one-second timeout) takes to cancel its own.
const electGrace = 3 * time.Second

// startJobs runs every job until ctx ends and returns one channel per job, closed once that
// job has stopped for good. In multi-tenant PostgreSQL only the replica holding the
// scheduled-worker lock runs them, all together; losing the lock cancels them, and they
// return before this replica tries again. Elsewhere each job simply runs, as before.
func startJobs(ctx context.Context, st store.Store, jobs ...func(context.Context)) []chan struct{} {
	done := make([]chan struct{}, len(jobs))
	for i := range done {
		done[i] = make(chan struct{})
	}
	runAll := func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(electGrace):
		}
		var wg sync.WaitGroup
		for _, job := range jobs {
			wg.Go(func() { job(ctx) })
		}
		wg.Wait()
		return nil
	}
	if st.Driver() != "postgres" || !st.Drive().Multi() {
		for i, job := range jobs {
			go func() { defer close(done[i]); job(ctx) }()
		}
		return done
	}
	go func() {
		defer func() {
			for _, d := range done {
				close(d)
			}
		}()
		for {
			won, err := st.WithReplicaLock(ctx, "scheduled-worker", runAll)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				log.Printf("[WORKER] scheduled-worker lock: %v", err)
			} else if won {
				log.Print("[WORKER] lost the scheduled-worker lock; jobs stopped")
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(electRetry):
			}
		}
	}()
	return done
}
