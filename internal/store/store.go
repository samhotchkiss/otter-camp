// Package store provides database access with row-level workspace isolation.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	_ "github.com/lib/pq"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

var (
	// ErrNoWorkspace is returned when a workspace ID is required but not present.
	ErrNoWorkspace = errors.New("workspace ID not found in context")
	// ErrInvalidWorkspace is returned when a workspace ID is invalid.
	ErrInvalidWorkspace = errors.New("invalid workspace ID")
	// ErrValidation is returned when caller input fails validation.
	ErrValidation = errors.New("validation failed")
	// ErrNotFound is returned when a requested entity does not exist.
	ErrNotFound = errors.New("entity not found")
	// ErrForbidden is returned when access to an entity is denied.
	ErrForbidden = errors.New("access denied")
)

var (
	globalDB     *sql.DB
	globalDBErr  error
	globalDBOnce sync.Once
)

var uuidRegex = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

func normalizeWorkspaceID(workspaceID string) (string, error) {
	trimmed := strings.TrimSpace(workspaceID)
	if trimmed == "" {
		return "", ErrNoWorkspace
	}
	if !uuidRegex.MatchString(trimmed) {
		return "", ErrInvalidWorkspace
	}
	return trimmed, nil
}

// DB returns the shared database connection pool.
func DB() (*sql.DB, error) {
	globalDBOnce.Do(func() {
		dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
		if dbURL == "" {
			globalDBErr = errors.New("DATABASE_URL is not set")
			return
		}

		db, err := sql.Open("postgres", dbURL)
		if err != nil {
			globalDBErr = err
			return
		}

		if err := db.Ping(); err != nil {
			_ = db.Close()
			globalDBErr = err
			return
		}

		// Auto-run sync state migrations
		runSyncStateMigrations(db)

		globalDB = db
	})

	return globalDB, globalDBErr
}

// runSyncStateMigrations ensures the sync state tables exist
func runSyncStateMigrations(db *sql.DB) {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS agent_sync_state (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			role TEXT,
			status TEXT NOT NULL DEFAULT 'offline',
			avatar TEXT,
			current_task TEXT,
			last_seen TEXT,
			model TEXT,
			total_tokens INTEGER DEFAULT 0,
			context_tokens INTEGER DEFAULT 0,
			channel TEXT,
			session_key TEXT,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS sync_metadata (
			key TEXT PRIMARY KEY,
			value TEXT,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_sync_state_status ON agent_sync_state(status)`,
	}

	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			// Log but don't fail - tables might already exist
			fmt.Fprintf(os.Stderr, "Migration note: %v\n", err)
		}
	}
}

// WithWorkspace sets the app.org_id session variable for RLS policies.
// This must be called before any query that uses RLS-protected tables.
func WithWorkspace(ctx context.Context, db *sql.DB) (*sql.Conn, error) {
	workspaceID, err := normalizeWorkspaceID(middleware.WorkspaceFromContext(ctx))
	if err != nil {
		return nil, err
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}

	// Note: SET LOCAL doesn't support parameterized queries in PostgreSQL.
	// The workspaceID is validated as a UUID above, so interpolation is safe.
	_, err = conn.ExecContext(ctx, fmt.Sprintf("SET LOCAL app.org_id = '%s'", workspaceID))
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to set workspace: %w", err)
	}

	return conn, nil
}

// WithWorkspaceID sets the app.org_id session variable for RLS policies
// using an explicit workspace ID instead of extracting from context.
// Useful for admin operations or service-to-service calls.
func WithWorkspaceID(ctx context.Context, db *sql.DB, workspaceID string) (*sql.Conn, error) {
	workspaceID, err := normalizeWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}

	// Note: SET LOCAL doesn't support parameterized queries in PostgreSQL.
	// The workspaceID is validated as a UUID above, so interpolation is safe.
	_, err = conn.ExecContext(ctx, fmt.Sprintf("SET LOCAL app.org_id = '%s'", workspaceID))
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to set workspace: %w", err)
	}

	return conn, nil
}

// WithWorkspaceTx starts a transaction with the workspace context set.
// The caller must commit or rollback the transaction.
func WithWorkspaceTx(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	workspaceID, err := normalizeWorkspaceID(middleware.WorkspaceFromContext(ctx))
	if err != nil {
		return nil, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Note: SET LOCAL doesn't support parameterized queries in PostgreSQL.
	// The workspaceID is validated as a UUID above, so interpolation is safe.
	_, err = tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL app.org_id = '%s'", workspaceID))
	if err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("failed to set workspace: %w", err)
	}

	return tx, nil
}

// WithWorkspaceIDTx starts a transaction with an explicit workspace ID set.
func WithWorkspaceIDTx(ctx context.Context, db *sql.DB, workspaceID string) (*sql.Tx, error) {
	workspaceID, err := normalizeWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Note: SET LOCAL doesn't support parameterized queries in PostgreSQL.
	// The workspaceID is validated as a UUID above, so interpolation is safe.
	_, err = tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL app.org_id = '%s'", workspaceID))
	if err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("failed to set workspace: %w", err)
	}

	return tx, nil
}

// Querier is an interface for database query execution.
// Both *sql.DB, *sql.Conn, and *sql.Tx implement this interface.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// nullableString converts a *string to a sql-compatible value.
func nullableString(value *string) interface{} {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}
