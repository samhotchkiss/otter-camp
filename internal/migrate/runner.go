package migrate

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	migrationsfs "github.com/samhotchkiss/otter-camp/migrations"
)

const advisoryLockID int64 = 9_226_014_607
const hnswIndexSignalComment = "-- HNSW_INDEX: must run outside transaction; migration runner handles this automatically"

type Runner struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
	fsys   fs.FS
}

func NewRunner(pool *pgxpool.Pool, logger *slog.Logger) *Runner {
	return NewRunnerWithFS(pool, logger, migrationsfs.Files)
}

func NewRunnerWithFS(pool *pgxpool.Pool, logger *slog.Logger, fsys fs.FS) *Runner {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &Runner{
		pool:   pool,
		logger: logger,
		fsys:   fsys,
	}
}

func (r *Runner) Run(ctx context.Context) error {
	if r == nil || r.pool == nil {
		return fmt.Errorf("migration runner requires a database pool")
	}

	if _, err := r.pool.Exec(ctx, `SELECT pg_advisory_lock($1)`, advisoryLockID); err != nil {
		return fmt.Errorf("acquire advisory lock: %w", err)
	}
	defer func() {
		_, _ = r.pool.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryLockID)
	}()

	if err := r.ensureSchemaMigrations(ctx); err != nil {
		return err
	}

	all, err := LoadMigrations(r.fsys)
	if err != nil {
		return err
	}

	pending, err := PlanPending(ctx, r, all)
	if err != nil {
		return err
	}

	for _, m := range pending {
		if err := r.applyMigration(ctx, m); err != nil {
			r.logger.Error("migration failed", "version", m.Version, "name", m.Name, "error", err)
			return err
		}
		r.logger.Info("migration applied", "version", m.Version, "name", m.Name)
	}

	return nil
}

func (r *Runner) AppliedVersions(ctx context.Context) (map[int]struct{}, error) {
	rows, err := r.pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("load applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int]struct{})
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied[version] = struct{}{}
	}

	if rows.Err() != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", rows.Err())
	}

	return applied, nil
}

func (r *Runner) ensureSchemaMigrations(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version integer PRIMARY KEY,
			applied_at timestamptz NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

func (r *Runner) applyMigration(ctx context.Context, migration Migration) error {
	if requiresNonTransactionalExecution(migration.SQL) {
		return r.applyMigrationOutsideTransaction(ctx, migration)
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin migration %04d: %w", migration.Version, err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, migration.SQL); err != nil {
		return fmt.Errorf("execute migration %04d (%s): %w", migration.Version, migration.File, err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO schema_migrations (version, applied_at)
		VALUES ($1, now())
	`, migration.Version); err != nil {
		return fmt.Errorf("record migration %04d: %w", migration.Version, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %04d: %w", migration.Version, err)
	}

	return nil
}

func (r *Runner) applyMigrationOutsideTransaction(ctx context.Context, migration Migration) error {
	statements := splitSQLStatements(migration.SQL)
	for _, stmt := range statements {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := r.pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("execute migration %04d (%s): %w", migration.Version, migration.File, err)
		}
	}

	if _, err := r.pool.Exec(ctx, `
		INSERT INTO schema_migrations (version, applied_at)
		VALUES ($1, now())
	`, migration.Version); err != nil {
		return fmt.Errorf("record migration %04d: %w", migration.Version, err)
	}

	return nil
}

func requiresNonTransactionalExecution(sql string) bool {
	return strings.Contains(sql, hnswIndexSignalComment)
}

func splitSQLStatements(sql string) []string {
	normalized := strings.ReplaceAll(sql, "\r\n", "\n")
	parts := strings.Split(normalized, ";\n")
	statements := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		statements = append(statements, trimmed)
	}
	return statements
}
