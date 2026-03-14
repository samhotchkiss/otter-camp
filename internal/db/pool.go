package db

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// Each runtime process holds several long-lived LISTEN/event consumers.
	// Keep enough headroom for foreground job handlers and HTTP requests.
	DefaultMaxConns int32 = 40
	DefaultMinConns int32 = 2
	connectTimeout        = 5 * time.Second
	acquireTimeout        = 10 * time.Second
)

type Pool struct {
	pool *pgxpool.Pool
}

func NewFromEnv(ctx context.Context) (*Pool, error) {
	databaseURL := strings.TrimSpace(os.Getenv("OTTERCAMP_DATABASE_URL"))
	if databaseURL == "" {
		return nil, fmt.Errorf("OTTERCAMP_DATABASE_URL is required")
	}

	maxConns := DefaultMaxConns
	if raw := strings.TrimSpace(os.Getenv("OTTERCAMP_DB_MAX_CONNS")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("invalid OTTERCAMP_DB_MAX_CONNS %q", raw)
		}
		maxConns = int32(v)
	}

	return New(ctx, databaseURL, maxConns)
}

func New(ctx context.Context, databaseURL string, maxConns int32) (*Pool, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, fmt.Errorf("database URL is required")
	}
	if maxConns <= 0 {
		return nil, fmt.Errorf("maxConns must be > 0")
	}

	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}

	cfg.MinConns = DefaultMinConns
	cfg.MaxConns = maxConns
	cfg.ConnConfig.ConnectTimeout = connectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	return &Pool{pool: pool}, nil
}

func (p *Pool) Raw() *pgxpool.Pool {
	if p == nil {
		return nil
	}
	return p.pool
}

func (p *Pool) Close() {
	if p == nil || p.pool == nil {
		return
	}
	p.pool.Close()
}

func (p *Pool) Ping(ctx context.Context) error {
	if p == nil || p.pool == nil {
		return fmt.Errorf("pool is nil")
	}

	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	return p.pool.Ping(pingCtx)
}

func (p *Pool) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	if p == nil || p.pool == nil {
		return nil, fmt.Errorf("pool is nil")
	}

	acqCtx, cancel := context.WithTimeout(ctx, acquireTimeout)
	defer cancel()
	return p.pool.Acquire(acqCtx)
}
