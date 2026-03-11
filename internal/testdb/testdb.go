package testdb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/migrate"
)

var (
	templateOnce sync.Once
	templateErr  error
)

const templatePrepLockID int64 = 9_226_014_608

const (
	defaultDropCleanupTimeout      = 45 * time.Second
	defaultDropRetryAttemptTimeout = 10 * time.Second
	defaultDropRetryMaxAttempts    = 5
	defaultDropRetryInitialBackoff = 100 * time.Millisecond
	defaultDropRetryMaxBackoff     = 2 * time.Second
	dropRetryObjectInUseSQLState   = "55006"
)

type databaseAdmin interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type dropRetryConfig struct {
	MaxAttempts    int
	AttemptTimeout time.Duration
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

func (c dropRetryConfig) normalize() dropRetryConfig {
	cfg := c
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaultDropRetryMaxAttempts
	}
	if cfg.AttemptTimeout <= 0 {
		cfg.AttemptTimeout = defaultDropRetryAttemptTimeout
	}
	if cfg.InitialBackoff < 0 {
		cfg.InitialBackoff = 0
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = defaultDropRetryMaxBackoff
	}
	if cfg.InitialBackoff > cfg.MaxBackoff {
		cfg.InitialBackoff = cfg.MaxBackoff
	}
	return cfg
}

func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	baseURL := strings.TrimSpace(os.Getenv("OTTERCAMP_TEST_DATABASE_URL"))
	if baseURL == "" {
		baseURL = "postgres://localhost/ottercamp_test_template"
	}

	templateName, err := dbNameFromURL(baseURL)
	if err != nil {
		t.Fatalf("parse OTTERCAMP_TEST_DATABASE_URL: %v", err)
	}

	templateOnce.Do(func() {
		templateErr = prepareTemplateDatabase(ctx, baseURL, templateName)
	})
	if templateErr != nil {
		t.Fatalf("prepare template database: %v", templateErr)
	}

	adminURL, err := withDBName(baseURL, "postgres")
	if err != nil {
		t.Fatalf("admin url: %v", err)
	}
	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	defer adminPool.Close()

	testDBName := fmt.Sprintf("ottercamp_test_%d_%s", time.Now().UnixNano(), strings.ReplaceAll(uuid.NewString(), "-", ""))
	createSQL := fmt.Sprintf(
		"CREATE DATABASE %s TEMPLATE %s",
		pgx.Identifier{testDBName}.Sanitize(),
		pgx.Identifier{templateName}.Sanitize(),
	)
	if _, err := adminPool.Exec(ctx, createSQL); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	testURL, err := withDBName(baseURL, testDBName)
	if err != nil {
		t.Fatalf("test database url: %v", err)
	}
	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatalf("test pool: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), defaultDropCleanupTimeout)
		defer cancel()

		cleanupAdmin, cleanupErr := pgxpool.New(cleanupCtx, adminURL)
		if cleanupErr != nil {
			t.Fatalf("cleanup admin pool: %v", cleanupErr)
		}
		defer cleanupAdmin.Close()

		dropCfg := dropRetryConfig{
			MaxAttempts:    defaultDropRetryMaxAttempts,
			AttemptTimeout: defaultDropRetryAttemptTimeout,
			InitialBackoff: defaultDropRetryInitialBackoff,
			MaxBackoff:     defaultDropRetryMaxBackoff,
		}
		if dropErr := dropDatabaseWithRetry(cleanupCtx, cleanupAdmin, testDBName, dropCfg); dropErr != nil {
			t.Fatalf("drop test database %s: %v", testDBName, dropErr)
		}
	})

	return pool
}

func prepareTemplateDatabase(ctx context.Context, baseURL, templateName string) error {
	adminURL, err := withDBName(baseURL, "postgres")
	if err != nil {
		return err
	}
	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		return err
	}
	defer adminPool.Close()

	if _, err := adminPool.Exec(ctx, `SELECT pg_advisory_lock($1)`, templatePrepLockID); err != nil {
		return err
	}
	defer func() {
		_, _ = adminPool.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, templatePrepLockID)
	}()

	var exists bool
	if err := adminPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, templateName).Scan(&exists); err != nil {
		return err
	}

	if !exists {
		createSQL := fmt.Sprintf("CREATE DATABASE %s", pgx.Identifier{templateName}.Sanitize())
		if _, err := adminPool.Exec(ctx, createSQL); err != nil {
			return err
		}
	}

	templateURL, err := withDBName(baseURL, templateName)
	if err != nil {
		return err
	}
	templatePool, err := pgxpool.New(ctx, templateURL)
	if err != nil {
		return err
	}
	defer templatePool.Close()

	runner := migrate.NewRunner(templatePool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return runner.Run(ctx)
}

func dbNameFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	name := strings.TrimPrefix(u.Path, "/")
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("database name missing in %q", rawURL)
	}
	return name, nil
}

func withDBName(rawURL, dbName string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	u.Path = "/" + dbName
	return u.String(), nil
}

func dropDatabaseWithRetry(ctx context.Context, admin databaseAdmin, dbName string, cfg dropRetryConfig) error {
	cfg = cfg.normalize()
	dropSQL := fmt.Sprintf("DROP DATABASE IF EXISTS %s", pgx.Identifier{dbName}.Sanitize())

	backoff := cfg.InitialBackoff
	lastPIDs := make([]int32, 0)
	var lastErr error

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return fmt.Errorf("drop database %s aborted before attempt %d: %w (active_pids=%v)", dbName, attempt, ctx.Err(), lastPIDs)
		}

		if err := terminateDatabaseConnections(ctx, admin, dbName); err != nil {
			return fmt.Errorf("terminate database connections for %s: %w", dbName, err)
		}

		attemptCtx, cancel := context.WithTimeout(ctx, cfg.AttemptTimeout)
		_, err := admin.Exec(attemptCtx, dropSQL)
		cancel()
		if err == nil {
			return nil
		}

		lastErr = err
		if pids, pidErr := activeDatabaseSessionPIDs(ctx, admin, dbName); pidErr == nil {
			lastPIDs = pids
		}

		if !isRetryableDropError(err) || attempt == cfg.MaxAttempts {
			break
		}

		if backoff > 0 {
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return fmt.Errorf("drop database %s cancelled while waiting to retry attempt %d: %w (active_pids=%v)", dbName, attempt+1, ctx.Err(), lastPIDs)
			}
			backoff *= 2
			if backoff > cfg.MaxBackoff {
				backoff = cfg.MaxBackoff
			}
		}
	}

	if lastErr == nil {
		lastErr = errors.New("drop attempt failed for unknown reason")
	}
	return fmt.Errorf("drop database %s failed after %d attempt(s): %w (active_pids=%v)", dbName, cfg.MaxAttempts, lastErr, lastPIDs)
}

func terminateDatabaseConnections(ctx context.Context, admin databaseAdmin, dbName string) error {
	_, err := admin.Exec(ctx, `
		SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity
		WHERE datname = $1 AND pid <> pg_backend_pid()
	`, dbName)
	return err
}

func activeDatabaseSessionPIDs(ctx context.Context, admin databaseAdmin, dbName string) ([]int32, error) {
	var pids []int32
	if err := admin.QueryRow(ctx, `
		SELECT COALESCE(array_agg(pid ORDER BY pid), ARRAY[]::integer[])
		FROM pg_stat_activity
		WHERE datname = $1 AND pid <> pg_backend_pid()
	`, dbName).Scan(&pids); err != nil {
		return nil, err
	}
	return pids, nil
}

func isRetryableDropError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == dropRetryObjectInUseSQLState {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "being accessed by other users") ||
		strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "timeout")
}
