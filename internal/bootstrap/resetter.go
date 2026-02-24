package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Runner interface {
	Run(ctx context.Context) error
}

type Resetter struct {
	pool         *pgxpool.Pool
	bootstrapper Runner
	logger       *slog.Logger
}

func NewResetter(pool *pgxpool.Pool, bootstrapper Runner, logger *slog.Logger) *Resetter {
	if logger == nil {
		logger = slog.Default()
	}
	return &Resetter{
		pool:         pool,
		bootstrapper: bootstrapper,
		logger:       logger,
	}
}

func (r *Resetter) Reset(ctx context.Context) error {
	if r == nil {
		return fmt.Errorf("resetter is nil")
	}
	if r.pool == nil {
		return fmt.Errorf("resetter pool is not configured")
	}
	if r.bootstrapper == nil {
		return fmt.Errorf("bootstrapper is not configured")
	}

	if err := r.truncateAllTables(ctx); err != nil {
		return err
	}
	return r.bootstrapper.Run(ctx)
}

func (r *Resetter) truncateAllTables(ctx context.Context) error {
	tables, err := r.truncatableTables(ctx)
	if err != nil {
		return err
	}
	if len(tables) == 0 {
		return nil
	}

	quoted := make([]string, 0, len(tables))
	for _, table := range tables {
		quoted = append(quoted, pgx.Identifier{table}.Sanitize())
	}

	statement := fmt.Sprintf("TRUNCATE TABLE %s RESTART IDENTITY CASCADE", strings.Join(quoted, ", "))
	if _, err := r.pool.Exec(ctx, statement); err != nil {
		return fmt.Errorf("truncate tables: %w", err)
	}
	return nil
}

func (r *Resetter) truncatableTables(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT tablename
		FROM pg_tables
		WHERE schemaname = 'public'
		  AND tablename <> 'schema_migrations'
		ORDER BY tablename
	`)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()

	tables := make([]string, 0)
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, fmt.Errorf("scan table: %w", err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tables: %w", err)
	}
	return tables, nil
}
