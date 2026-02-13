package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestOpenClawAgentImportUpsertsRosterAndIdentityFiles(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-agent-import-roster")
	openClawRoot := t.TempDir()

	writeOpenClawAgentWorkspaceFixture(t, openClawRoot, "main", "Chief of Staff", "Frank Identity", "tool_a\ntool_b")
	writeOpenClawAgentWorkspaceFixture(t, openClawRoot, "lori", "Agent Resources Director", "Lori Identity", "calendar")
	writeOpenClawAgentWorkspaceFixture(t, openClawRoot, "nova", "", "Nova Identity", "")

	writeOpenClawAgentConfigFixture(t, openClawRoot, []map[string]any{
		{"id": "main", "name": "Frank"},
		{"id": "lori", "name": "Lori"},
		{"id": "nova", "name": "Nova"},
	})

	install, err := DetectOpenClawInstallation(DetectOpenClawOptions{HomeDir: openClawRoot})
	require.NoError(t, err)

	result, err := ImportOpenClawAgents(context.Background(), db, OpenClawAgentImportOptions{
		OrgID:        orgID,
		Installation: install,
	})
	require.NoError(t, err)
	require.Equal(t, 3, result.ImportedAgents)
	require.Equal(t, 2, result.ActiveAgents)
	require.Equal(t, 1, result.InactiveAgents)

	rows, err := db.Query(
		`SELECT slug, display_name, status, is_ephemeral, soul_md, identity_md, instructions_md
		   FROM agents
		  WHERE org_id = $1
		  ORDER BY slug`,
		orgID,
	)
	require.NoError(t, err)
	defer rows.Close()

	type importedRow struct {
		Slug         string
		DisplayName  string
		Status       string
		IsEphemeral  bool
		Soul         sql.NullString
		Identity     sql.NullString
		Instructions sql.NullString
	}
	got := make([]importedRow, 0, 3)
	for rows.Next() {
		var row importedRow
		require.NoError(t, rows.Scan(
			&row.Slug,
			&row.DisplayName,
			&row.Status,
			&row.IsEphemeral,
			&row.Soul,
			&row.Identity,
			&row.Instructions,
		))
		got = append(got, row)
	}
	require.NoError(t, rows.Err())
	require.Len(t, got, 3)

	require.Equal(t, "lori", got[0].Slug)
	require.Equal(t, "active", got[0].Status)
	require.False(t, got[0].IsEphemeral)
	require.Equal(t, "Agent Resources Director", got[0].Soul.String)
	require.Equal(t, "Lori Identity", got[0].Identity.String)

	require.Equal(t, "main", got[1].Slug)
	require.Equal(t, "active", got[1].Status)
	require.False(t, got[1].IsEphemeral)
	require.Equal(t, "Chief of Staff", got[1].Soul.String)
	require.Equal(t, "Frank Identity", got[1].Identity.String)
	require.Equal(t, "tool_a\ntool_b", got[1].Instructions.String)

	require.Equal(t, "nova", got[2].Slug)
	require.Equal(t, "inactive", got[2].Status)
	require.False(t, got[2].IsEphemeral)
	require.Equal(t, "Nova Identity", got[2].Identity.String)
}

func TestOpenClawAgentImportStatusMappingAndIdempotency(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-agent-import-idempotent")
	openClawRoot := t.TempDir()

	writeOpenClawAgentWorkspaceFixture(t, openClawRoot, "main", "Chief of Staff", "Frank Identity", "plan")
	writeOpenClawAgentWorkspaceFixture(t, openClawRoot, "elephant", "Chief Context Officer", "Ellie Identity", "memory")
	writeOpenClawAgentWorkspaceFixture(t, openClawRoot, "lori", "Agent Resources Director", "Lori Identity", "ops")
	writeOpenClawAgentWorkspaceFixture(t, openClawRoot, "max", "", "Max Identity", "")

	writeOpenClawAgentConfigFixture(t, openClawRoot, []map[string]any{
		{"id": "main", "name": "Frank"},
		{"id": "elephant", "name": "Ellie"},
		{"id": "lori", "name": "Lori"},
		{"id": "max", "name": "Max"},
	})

	_, err := db.Exec(
		`INSERT INTO agents (org_id, slug, display_name, status, is_ephemeral)
		 VALUES ($1, 'max', 'Legacy Max', 'active', true)`,
		orgID,
	)
	require.NoError(t, err)

	install, err := DetectOpenClawInstallation(DetectOpenClawOptions{HomeDir: openClawRoot})
	require.NoError(t, err)

	first, err := ImportOpenClawAgents(context.Background(), db, OpenClawAgentImportOptions{
		OrgID:        orgID,
		Installation: install,
	})
	require.NoError(t, err)
	require.Equal(t, 4, first.ImportedAgents)
	require.Equal(t, 3, first.ActiveAgents)
	require.Equal(t, 1, first.InactiveAgents)

	second, err := ImportOpenClawAgents(context.Background(), db, OpenClawAgentImportOptions{
		OrgID:        orgID,
		Installation: install,
	})
	require.NoError(t, err)
	require.Equal(t, 4, second.ImportedAgents)
	require.Equal(t, 3, second.ActiveAgents)
	require.Equal(t, 1, second.InactiveAgents)

	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM agents WHERE org_id = $1`, orgID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 4, count)

	rows, err := db.Query(
		`SELECT slug, status, is_ephemeral, display_name
		   FROM agents
		  WHERE org_id = $1
		  ORDER BY slug`,
		orgID,
	)
	require.NoError(t, err)
	defer rows.Close()

	statuses := map[string]string{}
	ephemeral := map[string]bool{}
	displayNames := map[string]string{}
	for rows.Next() {
		var slug string
		var status string
		var isEphemeral bool
		var displayName string
		require.NoError(t, rows.Scan(&slug, &status, &isEphemeral, &displayName))
		statuses[slug] = status
		ephemeral[slug] = isEphemeral
		displayNames[slug] = displayName
	}
	require.NoError(t, rows.Err())

	require.Equal(t, "active", statuses["main"])
	require.Equal(t, "active", statuses["elephant"])
	require.Equal(t, "active", statuses["lori"])
	require.Equal(t, "inactive", statuses["max"])

	require.False(t, ephemeral["main"])
	require.False(t, ephemeral["elephant"])
	require.False(t, ephemeral["lori"])
	require.False(t, ephemeral["max"])

	require.Equal(t, "Max", displayNames["max"])
}

const openClawImportTestDBURLKey = "OTTER_TEST_DATABASE_URL"

func getOpenClawImportTestDatabaseURL(t *testing.T) string {
	t.Helper()
	connStr := os.Getenv(openClawImportTestDBURLKey)
	if connStr == "" {
		t.Skipf("set %s to a dedicated test database", openClawImportTestDBURLKey)
	}
	return connStr
}

func setupOpenClawImportTestDatabase(t *testing.T, connStr string) *sql.DB {
	t.Helper()

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})

	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS pgcrypto")
	require.NoError(t, err)

	migrationsDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	require.NoError(t, err)
	m, err := migrate.New("file://"+migrationsDir, connStr)
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

	return db
}

func createOpenClawImportTestOrganization(t *testing.T, db *sql.DB, slug string) string {
	t.Helper()
	var orgID string
	err := db.QueryRow(
		`INSERT INTO organizations (name, slug, tier)
		 VALUES ($1, $2, 'free')
		 RETURNING id`,
		"Org "+slug,
		slug,
	).Scan(&orgID)
	require.NoError(t, err)
	return orgID
}

func writeOpenClawAgentWorkspaceFixture(t *testing.T, root, slug, soul, identity, tools string) {
	t.Helper()
	workspace := filepath.Join(root, "workspaces", slug)
	require.NoError(t, os.MkdirAll(workspace, 0o755))

	if soul != "" {
		require.NoError(t, os.WriteFile(filepath.Join(workspace, "SOUL.md"), []byte(soul+"\n"), 0o644))
	}
	if identity != "" {
		require.NoError(t, os.WriteFile(filepath.Join(workspace, "IDENTITY.md"), []byte(identity+"\n"), 0o644))
	}
	if tools != "" {
		require.NoError(t, os.WriteFile(filepath.Join(workspace, "TOOLS.md"), []byte(tools+"\n"), 0o644))
	}
}

func writeOpenClawAgentConfigFixture(t *testing.T, root string, agents []map[string]any) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"workspaces_dir": "./workspaces",
		"agents": map[string]any{
			"list": agents,
		},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "openclaw.json"), raw, 0o644))
}
