package api

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/stretchr/testify/require"
)

func tableExists(t *testing.T, db *sql.DB, tableName string) bool {
	t.Helper()
	var regclass sql.NullString
	err := db.QueryRow("SELECT to_regclass('public.' || $1)::text", tableName).Scan(&regclass)
	require.NoError(t, err)
	return regclass.Valid && regclass.String != ""
}

func columnExists(t *testing.T, db *sql.DB, tableName, columnName string) bool {
	t.Helper()
	var exists bool
	err := db.QueryRow(
		`SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public'
			  AND table_name = $1
			  AND column_name = $2
		)`,
		tableName,
		columnName,
	).Scan(&exists)
	require.NoError(t, err)
	return exists
}

func currentSchemaVersion(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var version int64
	var dirty bool
	err := db.QueryRow(`SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty)
	require.NoError(t, err)
	require.False(t, dirty)
	return version
}

func checkConstraintDefinition(t *testing.T, db *sql.DB, tableName, constraintName string) string {
	t.Helper()
	var definition string
	err := db.QueryRow(
		`SELECT pg_get_constraintdef(c.oid)
		FROM pg_constraint c
		INNER JOIN pg_class t ON t.oid = c.conrelid
		INNER JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE n.nspname = 'public' AND t.relname = $1 AND c.conname = $2`,
		tableName,
		constraintName,
	).Scan(&definition)
	require.NoError(t, err)
	return definition
}

func TestProjectSettingsMigrationsCreateDeployConfigAndHumanReviewColumn(t *testing.T) {
	db := setupMessageTestDB(t)

	require.True(t, tableExists(t, db, "project_deploy_config"))
	require.True(t, columnExists(t, db, "projects", "require_human_review"))
}

func TestProjectSettingsMigrationFilesExist(t *testing.T) {
	migrationsDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	require.NoError(t, err)

	required := []string{
		"047_create_project_deploy_config.up.sql",
		"047_create_project_deploy_config.down.sql",
		"048_add_require_human_review.up.sql",
		"048_add_require_human_review.down.sql",
		"049_allow_reviewer_gate_approval_state.up.sql",
		"049_allow_reviewer_gate_approval_state.down.sql",
	}

	for _, filename := range required {
		_, err := os.Stat(filepath.Join(migrationsDir, filename))
		require.NoError(t, err, filename)
	}
}

func TestProjectSettingsMigrationFilesContainExpectedDDL(t *testing.T) {
	migrationsDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	require.NoError(t, err)

	checkContains := func(filename string, snippets ...string) {
		t.Helper()
		raw, readErr := os.ReadFile(filepath.Join(migrationsDir, filename))
		require.NoError(t, readErr)
		body := strings.ToLower(string(raw))
		for _, snippet := range snippets {
			require.Contains(t, body, strings.ToLower(snippet), filename)
		}
	}

	checkContains(
		"047_create_project_deploy_config.up.sql",
		"create table project_deploy_config",
		"deploy_method",
		"github_push",
		"cli_command",
	)
	checkContains(
		"048_add_require_human_review.up.sql",
		"alter table projects",
		"add column require_human_review boolean not null default false",
	)
	checkContains(
		"049_allow_reviewer_gate_approval_state.up.sql",
		"alter table project_issues",
		"project_issues_approval_state_check",
		"approved_by_reviewer",
	)
	checkContains(
		"049_allow_reviewer_gate_approval_state.down.sql",
		"update project_issues",
		"where approval_state = 'approved_by_reviewer'",
		"approval_state in ('draft', 'ready_for_review', 'needs_changes', 'approved')",
	)
}

func TestProjectSettingsMigrationsRollbackDeployConfigAndHumanReviewColumn(t *testing.T) {
	connStr := os.Getenv(testDBURLKey)
	if connStr == "" {
		t.Skipf("set %s to a dedicated test database", testDBURLKey)
	}

	db := setupMessageTestDB(t)
	version := currentSchemaVersion(t, db)
	require.GreaterOrEqual(t, version, int64(49))

	migrationsDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	require.NoError(t, err)

	m, err := migrate.New("file://"+migrationsDir, connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = m.Close()
	})

	stepsToRollback := int(version - 46)
	if stepsToRollback > 0 {
		err = m.Steps(-stepsToRollback)
		if err != nil && !errors.Is(err, migrate.ErrNoChange) {
			require.NoError(t, err)
		}
	}

	require.False(t, tableExists(t, db, "project_deploy_config"))
	require.False(t, columnExists(t, db, "projects", "require_human_review"))
	constraint := strings.ToLower(checkConstraintDefinition(t, db, "project_issues", "project_issues_approval_state_check"))
	require.NotContains(t, constraint, "approved_by_reviewer")
	require.Contains(t, constraint, "ready_for_review")
	require.Contains(t, constraint, "approved")
}
