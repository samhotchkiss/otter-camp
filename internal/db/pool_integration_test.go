//go:build integration

package db

import (
	"context"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestPoolPingReachable(t *testing.T) {
	pgPool := testdb.New(t)
	pool := &Pool{pool: pgPool}
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("Ping returned error for reachable database: %v", err)
	}
}

func TestPoolPingUnreachableTimesOutQuickly(t *testing.T) {
	pool, err := New(context.Background(), "postgres://localhost:1/does_not_exist", DefaultMaxConns)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer pool.Close()

	started := time.Now()
	err = pool.Ping(context.Background())
	if err == nil {
		t.Fatal("expected Ping error for unreachable database")
	}
	if elapsed := time.Since(started); elapsed > 6*time.Second {
		t.Fatalf("Ping took %v, expected <= 6s", elapsed)
	}
}
