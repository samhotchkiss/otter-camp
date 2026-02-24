package db

import (
	"context"
	"os"
	"testing"
)

func TestNewFromEnvRequiresDatabaseURL(t *testing.T) {
	t.Setenv("OTTERCAMP_DATABASE_URL", "")
	_, err := NewFromEnv(context.Background())
	if err == nil {
		t.Fatal("expected error when OTTERCAMP_DATABASE_URL is missing")
	}
}

func TestNewFromEnvValidatesMaxConns(t *testing.T) {
	t.Setenv("OTTERCAMP_DATABASE_URL", "postgres://localhost/ottercamp")
	t.Setenv("OTTERCAMP_DB_MAX_CONNS", "nope")

	_, err := NewFromEnv(context.Background())
	if err == nil {
		t.Fatal("expected error when OTTERCAMP_DB_MAX_CONNS is invalid")
	}
}

func TestNewRejectsEmptyDatabaseURL(t *testing.T) {
	_, err := New(context.Background(), "", DefaultMaxConns)
	if err == nil {
		t.Fatal("expected error for empty database URL")
	}
}

func TestNewRejectsInvalidMaxConns(t *testing.T) {
	_, err := New(context.Background(), "postgres://localhost/ottercamp", 0)
	if err == nil {
		t.Fatal("expected error for invalid max conns")
	}
}

func TestNewFromEnvUsesDefaultMaxConns(t *testing.T) {
	t.Setenv("OTTERCAMP_DATABASE_URL", "postgres://localhost/ottercamp")
	_ = os.Unsetenv("OTTERCAMP_DB_MAX_CONNS")

	p, err := NewFromEnv(context.Background())
	if err != nil {
		t.Fatalf("NewFromEnv returned error: %v", err)
	}
	p.Close()

	if got := p.Raw().Config().MaxConns; got != DefaultMaxConns {
		t.Fatalf("max conns = %d, want %d", got, DefaultMaxConns)
	}

	if got := p.Raw().Config().MinConns; got != DefaultMinConns {
		t.Fatalf("min conns = %d, want %d", got, DefaultMinConns)
	}
}
