// Package store provides database access with row-level workspace isolation.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	_ "github.com/lib/pq"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

var (
	// ErrNoWorkspace is returned when a workspace ID is required but not present.
	ErrNoWorkspace = errors.New("workspace ID not found in context")
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

		globalDB = db
	})

	return globalDB, globalDBErr
}

// WithWorkspace sets the app.org_id session variable for RLS policies.
// This must be called before any query that uses RLS-protected tables.
func WithWorkspace(ctx context.Context, db *sql.DB) (*sql.Conn, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}

	_, err = conn.ExecContext(ctx, "SET LOCAL app.org_id = $1", workspaceID)
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
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}

	_, err = conn.ExecContext(ctx, "SET LOCAL app.org_id = $1", workspaceID)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to set workspace: %w", err)
	}

	return conn, nil
}

// WithWorkspaceTx starts a transaction with the workspace context set.
// The caller must commit or rollback the transaction.
func WithWorkspaceTx(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	_, err = tx.ExecContext(ctx, "SET LOCAL app.org_id = $1", workspaceID)
	if err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("failed to set workspace: %w", err)
	}

	return tx, nil
}

// WithWorkspaceIDTx starts a transaction with an explicit workspace ID set.
func WithWorkspaceIDTx(ctx context.Context, db *sql.DB, workspaceID string) (*sql.Tx, error) {
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	_, err = tx.ExecContext(ctx, "SET LOCAL app.org_id = $1", workspaceID)
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
