package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/models"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

const testDBURLKey = "OTTER_TEST_DATABASE_URL"

func TestQueuePriority(t *testing.T) {
	db := setupTestDatabase(t)
	orgID := createTestOrganization(t, db, "dispatch-priority")
	ctx := ctxWithWorkspace(orgID)

	t0 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	insertTask(t, ctx, db, orgID, "P1 task", models.TaskStatusQueued, models.TaskPriorityP1, t0)
	insertTask(t, ctx, db, orgID, "P0 task", models.TaskStatusQueued, models.TaskPriorityP0, t0.Add(1*time.Minute))

	queue := NewQueue(db)

	task, err := queue.Pickup(ctx)
	require.NoError(t, err)
	require.NotNil(t, task)
	require.Equal(t, models.TaskPriorityP0, task.Priority)

	task, err = queue.Pickup(ctx)
	require.NoError(t, err)
	require.NotNil(t, task)
	require.Equal(t, models.TaskPriorityP1, task.Priority)
}

func TestQueueFIFO(t *testing.T) {
	db := setupTestDatabase(t)
	orgID := createTestOrganization(t, db, "dispatch-fifo")
	ctx := ctxWithWorkspace(orgID)

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	taskA := insertTask(t, ctx, db, orgID, "task-a", models.TaskStatusQueued, models.TaskPriorityP1, base)
	taskB := insertTask(t, ctx, db, orgID, "task-b", models.TaskStatusQueued, models.TaskPriorityP1, base.Add(1*time.Minute))
	taskC := insertTask(t, ctx, db, orgID, "task-c", models.TaskStatusQueued, models.TaskPriorityP1, base.Add(2*time.Minute))

	queue := NewQueue(db)

	first, err := queue.Pickup(ctx)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Equal(t, taskA, first.ID)

	second, err := queue.Pickup(ctx)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.Equal(t, taskB, second.ID)

	third, err := queue.Pickup(ctx)
	require.NoError(t, err)
	require.NotNil(t, third)
	require.Equal(t, taskC, third.ID)
}

func TestQueuePickup(t *testing.T) {
	db := setupTestDatabase(t)
	orgID := createTestOrganization(t, db, "dispatch-pickup")
	ctx := ctxWithWorkspace(orgID)

	taskID := insertTask(t, ctx, db, orgID, "queued", models.TaskStatusQueued, models.TaskPriorityP2, time.Now().UTC())

	queue := NewQueue(db)
	task, err := queue.Pickup(ctx)
	require.NoError(t, err)
	require.NotNil(t, task)
	require.Equal(t, taskID, task.ID)
	require.Equal(t, models.TaskStatusDispatched, task.Status)

	status := fetchTaskStatus(t, ctx, db, taskID)
	require.Equal(t, models.TaskStatusDispatched, status)
}

func TestQueueIdempotent(t *testing.T) {
	db := setupTestDatabase(t)
	orgID := createTestOrganization(t, db, "dispatch-idempotent")
	ctx := ctxWithWorkspace(orgID)

	taskID := insertTask(t, ctx, db, orgID, "queued", models.TaskStatusQueued, models.TaskPriorityP2, time.Now().UTC())

	queue := NewQueue(db)
	first, err := queue.Pickup(ctx)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Equal(t, taskID, first.ID)

	second, err := queue.Pickup(ctx)
	require.NoError(t, err)
	require.Nil(t, second)

	status := fetchTaskStatus(t, ctx, db, taskID)
	require.Equal(t, models.TaskStatusDispatched, status)
}

func insertTask(t *testing.T, ctx context.Context, db *sql.DB, orgID, title, status, priority string, createdAt time.Time) string {
	t.Helper()

	conn, err := store.WithWorkspaceID(ctx, db, orgID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = conn.Close()
	})

	var id string
	err = conn.QueryRowContext(ctx, `
		INSERT INTO tasks (org_id, title, status, priority, created_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, orgID, title, status, priority, createdAt).Scan(&id)
	require.NoError(t, err)
	return id
}

func fetchTaskStatus(t *testing.T, ctx context.Context, db *sql.DB, taskID string) string {
	t.Helper()

	conn, err := store.WithWorkspace(ctx, db)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = conn.Close()
	})

	var status string
	err = conn.QueryRowContext(ctx, "SELECT status FROM tasks WHERE id = $1", taskID).Scan(&status)
	require.NoError(t, err)
	return status
}

func setupTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	connStr := os.Getenv(testDBURLKey)
	if connStr == "" {
		t.Skipf("set %s to a dedicated test database", testDBURLKey)
	}

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)

	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS pgcrypto")
	require.NoError(t, err)

	m, err := migrate.New("file://"+getMigrationsDir(t), connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = m.Close()
	})

	err = m.Down()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	return db
}

func getMigrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	require.NoError(t, err)
	return dir
}

func createTestOrganization(t *testing.T, db *sql.DB, slug string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		"INSERT INTO organizations (name, slug, tier) VALUES ($1, $2, 'free') RETURNING id",
		"Org "+slug,
		slug,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func ctxWithWorkspace(workspaceID string) context.Context {
	return context.WithValue(context.Background(), middleware.WorkspaceIDKey, workspaceID)
}
