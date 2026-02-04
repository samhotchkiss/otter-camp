package store

import (
	"database/sql"
	"errors"

	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func requirePQCode(t *testing.T, err error, code string) {
	t.Helper()
	require.Error(t, err)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		require.Equal(t, code, string(pqErr.Code))
		return
	}
	require.Fail(t, "expected pq.Error", "got %T: %v", err, err)
}

func insertSchemaAgent(t *testing.T, db *sql.DB, orgID, slug string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		"INSERT INTO agents (org_id, slug, display_name, status) VALUES ($1, $2, $3, 'active') RETURNING id",
		orgID,
		slug,
		"Agent "+slug,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func insertSchemaProject(t *testing.T, db *sql.DB, orgID, name string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		"INSERT INTO projects (org_id, name, status) VALUES ($1, $2, 'active') RETURNING id",
		orgID,
		name,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func insertSchemaTask(t *testing.T, db *sql.DB, orgID string, projectID, agentID *string, title string) string {
	t.Helper()
	var projectValue interface{}
	var agentValue interface{}
	if projectID != nil {
		projectValue = *projectID
	}
	if agentID != nil {
		agentValue = *agentID
	}
	var id string
	err := db.QueryRow(
		"INSERT INTO tasks (org_id, project_id, assigned_agent_id, title, status, priority) VALUES ($1, $2, $3, $4, 'queued', 'P2') RETURNING id",
		orgID,
		projectValue,
		agentValue,
		title,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestSchemaMigrationsUpDown(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer func() {
		_ = db.Close()
	}()

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

	err = m.Down()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}
}

func TestSchemaForeignKeys(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "fk-org")
	projectID := insertSchemaProject(t, db, orgID, "FK Project")
	agentID := insertSchemaAgent(t, db, orgID, "fk-agent")
	_ = insertSchemaTask(t, db, orgID, &projectID, &agentID, "FK Task")

	var missingID string
	err := db.QueryRow("SELECT gen_random_uuid()::text").Scan(&missingID)
	require.NoError(t, err)

	_, err = db.Exec(
		"INSERT INTO agents (org_id, slug, display_name, status) VALUES ($1, $2, $3, 'active')",
		missingID,
		"bad-agent",
		"Bad Agent",
	)
	requirePQCode(t, err, "23503")

	_, err = db.Exec(
		"INSERT INTO tasks (org_id, project_id, title, status, priority) VALUES ($1, $2, $3, 'queued', 'P2')",
		orgID,
		missingID,
		"Bad Task",
	)
	requirePQCode(t, err, "23503")
}

func TestSchemaUniqueConstraints(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "uniq-org")
	otherOrgID := createTestOrganization(t, db, "uniq-org-2")

	_, err := db.Exec(
		"INSERT INTO tags (org_id, name, color) VALUES ($1, $2, $3)",
		orgID,
		"backend",
		"#fff",
	)
	require.NoError(t, err)

	_, err = db.Exec(
		"INSERT INTO tags (org_id, name, color) VALUES ($1, $2, $3)",
		orgID,
		"backend",
		"#000",
	)
	requirePQCode(t, err, "23505")

	_, err = db.Exec(
		"INSERT INTO tags (org_id, name, color) VALUES ($1, $2, $3)",
		otherOrgID,
		"backend",
		"#123",
	)
	require.NoError(t, err)

	_ = insertSchemaTask(t, db, orgID, nil, nil, "First")
	secondTaskID := insertSchemaTask(t, db, orgID, nil, nil, "Second")

	_, err = db.Exec("UPDATE tasks SET number = 1 WHERE id = $1", secondTaskID)
	requirePQCode(t, err, "23505")
}

func TestSchemaCascadeDeletes(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "cascade-org")
	agentID := insertSchemaAgent(t, db, orgID, "cascade-agent")
	projectID := insertSchemaProject(t, db, orgID, "Cascade Project")
	_ = insertSchemaTask(t, db, orgID, &projectID, &agentID, "Cascade Task")

	_, err := db.Exec("DELETE FROM organizations WHERE id = $1", orgID)
	require.NoError(t, err)

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM agents").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)

	err = db.QueryRow("SELECT COUNT(*) FROM projects").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)

	err = db.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestSchemaCheckConstraints(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "check-org")

	_, err := db.Exec(
		"INSERT INTO tasks (org_id, title, status, priority) VALUES ($1, $2, $3, $4)",
		orgID,
		"Bad Status",
		"not_a_status",
		"P2",
	)
	requirePQCode(t, err, "23514")

	_, err = db.Exec(
		"INSERT INTO tasks (org_id, title, status, priority) VALUES ($1, $2, $3, $4)",
		orgID,
		"Bad Priority",
		"queued",
		"P9",
	)
	requirePQCode(t, err, "23514")
}
