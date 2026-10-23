// Package testdb provides a throwaway Postgres for tests, via testcontainers.
package testdb

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const image = "postgres:18"

var (
	mu      sync.Mutex
	once    sync.Once
	pool    *pgxpool.Pool
	initErr error
)

// New returns a pool on a migrated Postgres container shared by the whole test
// package (started once, reaped by testcontainers' ryuk). All tables are
// truncated at the start of every test; a mutex held until the test's cleanup
// serialises tests, so t.Parallel() cannot race the truncate.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	mu.Lock() // serialise tests sharing the DB; released on cleanup
	t.Cleanup(mu.Unlock)
	ctx := context.Background()
	once.Do(func() {
		goose.SetLogger(goose.NopLogger())
		pool, initErr = start(ctx)
	})
	if initErr != nil {
		t.Fatalf("testdb: %v", initErr)
	}
	if _, err := pool.Exec(ctx, `truncate songs, requests, bot_comments, bot_state, users, sessions, suggestions, votes, song_history restart identity cascade`); err != nil {
		t.Fatalf("testdb: truncate: %v", err)
	}
	return pool
}

func start(ctx context.Context) (*pgxpool.Pool, error) {
	ctr, err := tcpostgres.Run(ctx, image,
		tcpostgres.WithDatabase("pr0sauce"),
		tcpostgres.WithUsername("pr0sauce"),
		tcpostgres.WithPassword("pr0sauce"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(90*time.Second)),
	)
	if err != nil {
		return nil, err
	}
	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx, url); err != nil {
		return nil, err
	}
	return db.Open(ctx, url)
}
