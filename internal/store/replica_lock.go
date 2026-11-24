package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"time"
)

// ErrNotReplicated is WithReplicaLock's refusal outside multi-tenant PostgreSQL.
var ErrNotReplicated = errors.New("replica locks require multi-tenant PostgreSQL")

// OnOneReplica runs work under the named replica lock in multi-tenant PostgreSQL, where a
// contender gets (false, nil), and directly with ctx everywhere else.
func OnOneReplica(ctx context.Context, st Store, name string, work func(context.Context) error) (bool, error) {
	won, err := st.WithReplicaLock(ctx, name, work)
	if errors.Is(err, ErrNotReplicated) {
		return true, work(ctx)
	}
	return won, err
}

// WithReplicaLock reserves one PostgreSQL session for the whole callback. A contender
// returns (false, nil) without running work. Callbacks use the ordinary pool (at least
// one further connection), honor cancellation and tolerate interrupted external effects.
// Direct connections or session pooling are required; transaction pooling is unsafe.
func (s *SQLStore) WithReplicaLock(ctx context.Context, name string, work func(context.Context) error) (bool, error) {
	if s.driver != "postgres" || !s.drive.Multi() {
		return false, ErrNotReplicated
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return false, err
	}
	// Discard the physical session even after acquisition errors or cancellation: a lock
	// must never reach the pool. Closing it releases ownership without an unlock race.
	defer conn.Close()
	defer conn.Raw(func(any) error { return driver.ErrBadConn })
	var won bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(3, hashtext(current_schema() || ':' || $1))`, name).Scan(&won); err != nil || !won {
		return false, err
	}
	owned, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-owned.Done():
				return
			case <-tick.C:
				probe, stop := context.WithTimeout(owned, time.Second)
				err := conn.PingContext(probe)
				stop()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	// Work must return before voluntary release; cleanup also runs if it panics.
	defer func() { cancel(); <-done }()
	return true, work(owned)
}
