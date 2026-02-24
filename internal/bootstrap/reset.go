package bootstrap

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Resetter struct {
	pool         *pgxpool.Pool
	bootstrapper *Bootstrapper
}

func NewResetter(pool *pgxpool.Pool, bootstrapper *Bootstrapper) *Resetter {
	return &Resetter{
		pool:         pool,
		bootstrapper: bootstrapper,
	}
}

func (r *Resetter) Reset(ctx context.Context) error {
	if r == nil || r.pool == nil {
		return fmt.Errorf("reset database pool is not configured")
	}
	if r.bootstrapper == nil {
		return fmt.Errorf("reset bootstrapper is not configured")
	}

	tableNames, err := r.applicationTables(ctx)
	if err != nil {
		return err
	}
	if len(tableNames) > 0 {
		quoted := make([]string, 0, len(tableNames))
		for _, tableName := range tableNames {
			quoted = append(quoted, pgx.Identifier{tableName}.Sanitize())
		}

		truncateSQL := fmt.Sprintf(
			"TRUNCATE TABLE %s RESTART IDENTITY CASCADE",
			strings.Join(quoted, ", "),
		)
		if _, err := r.pool.Exec(ctx, truncateSQL); err != nil {
			return fmt.Errorf("truncate tables: %w", err)
		}
	}

	if err := r.bootstrapper.Run(ctx); err != nil {
		return fmt.Errorf("run bootstrap after reset: %w", err)
	}
	return nil
}

func (r *Resetter) applicationTables(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT tablename
		FROM pg_tables
		WHERE schemaname = 'public'
		  AND tablename <> 'schema_migrations'
	`)
	if err != nil {
		return nil, fmt.Errorf("query public table names: %w", err)
	}
	defer rows.Close()

	names := make([]string, 0)
	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			return nil, fmt.Errorf("scan table name: %w", scanErr)
		}
		names = append(names, name)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("iterate table names: %w", rows.Err())
	}

	sort.Strings(names)
	return names, nil
}
