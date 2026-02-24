package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Router interface {
	ConnFor(ctx context.Context, orgID uuid.UUID) (*pgxpool.Pool, error)
}

type SingleRouter struct {
	pool *pgxpool.Pool
}

func NewSingleRouter(pool *Pool) *SingleRouter {
	if pool == nil {
		return &SingleRouter{}
	}
	return &SingleRouter{pool: pool.Raw()}
}

func NewSingleRouterFromRaw(pool *pgxpool.Pool) *SingleRouter {
	return &SingleRouter{pool: pool}
}

func (r *SingleRouter) ConnFor(_ context.Context, _ uuid.UUID) (*pgxpool.Pool, error) {
	if r == nil || r.pool == nil {
		return nil, fmt.Errorf("database pool is not configured")
	}
	return r.pool, nil
}
