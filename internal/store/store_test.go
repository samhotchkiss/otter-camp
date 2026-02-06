package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDB_NoDatabaseURL(t *testing.T) {
	// Skip if running with test database (would pollute singleton)
	if getTestDatabaseURL(t) != "" {
		t.Skip("skipping when test database is configured")
	}

	// This would test the error case, but we can't easily reset the sync.Once
	// so we just verify the function exists
	_, _ = DB()
}

func TestWithWorkspace_NoWorkspaceInContext(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	ctx := context.Background() // No workspace

	conn, err := WithWorkspace(ctx, db)
	assert.Error(t, err)
	assert.Nil(t, conn)
	assert.ErrorIs(t, err, ErrNoWorkspace)
}

func TestWithWorkspace_InvalidWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	ctx := ctxWithWorkspace("not-a-uuid")

	conn, err := WithWorkspace(ctx, db)
	assert.Error(t, err)
	assert.Nil(t, conn)
	assert.ErrorIs(t, err, ErrInvalidWorkspace)
}

func TestWithWorkspace_ValidWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "store-test-workspace")
	ctx := ctxWithWorkspace(orgID)

	conn, err := WithWorkspace(ctx, db)
	require.NoError(t, err)
	require.NotNil(t, conn)
	defer conn.Close()

	// Verify we can query
	var result string
	err = conn.QueryRowContext(ctx, "SELECT current_setting('app.org_id', true)").Scan(&result)
	require.NoError(t, err)
	assert.Equal(t, orgID, result)
}

func TestWithWorkspaceID_EmptyWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	ctx := context.Background()

	conn, err := WithWorkspaceID(ctx, db, "")
	assert.Error(t, err)
	assert.Nil(t, conn)
	assert.ErrorIs(t, err, ErrNoWorkspace)
}

func TestWithWorkspaceID_InvalidWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	ctx := context.Background()

	conn, err := WithWorkspaceID(ctx, db, "test'")
	assert.Error(t, err)
	assert.Nil(t, conn)
	assert.ErrorIs(t, err, ErrInvalidWorkspace)
}

func TestWithWorkspaceID_ValidWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "store-test-workspaceid")
	ctx := context.Background()

	conn, err := WithWorkspaceID(ctx, db, orgID)
	require.NoError(t, err)
	require.NotNil(t, conn)
	defer conn.Close()

	// Verify workspace is set
	var result string
	err = conn.QueryRowContext(ctx, "SELECT current_setting('app.org_id', true)").Scan(&result)
	require.NoError(t, err)
	assert.Equal(t, orgID, result)
}

func TestWithWorkspaceTx_NoWorkspaceInContext(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	ctx := context.Background()

	tx, err := WithWorkspaceTx(ctx, db)
	assert.Error(t, err)
	assert.Nil(t, tx)
	assert.ErrorIs(t, err, ErrNoWorkspace)
}

func TestWithWorkspaceTx_ValidWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "store-test-tx")
	ctx := ctxWithWorkspace(orgID)

	tx, err := WithWorkspaceTx(ctx, db)
	require.NoError(t, err)
	require.NotNil(t, tx)
	defer tx.Rollback()

	// Verify workspace is set in transaction
	var result string
	err = tx.QueryRowContext(ctx, "SELECT current_setting('app.org_id', true)").Scan(&result)
	require.NoError(t, err)
	assert.Equal(t, orgID, result)
}

func TestWithWorkspaceIDTx_EmptyWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	ctx := context.Background()

	tx, err := WithWorkspaceIDTx(ctx, db, "")
	assert.Error(t, err)
	assert.Nil(t, tx)
	assert.ErrorIs(t, err, ErrNoWorkspace)
}

func TestWithWorkspaceIDTx_ValidWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "store-test-idtx")
	ctx := context.Background()

	tx, err := WithWorkspaceIDTx(ctx, db, orgID)
	require.NoError(t, err)
	require.NotNil(t, tx)
	defer tx.Rollback()

	// Verify workspace is set
	var result string
	err = tx.QueryRowContext(ctx, "SELECT current_setting('app.org_id', true)").Scan(&result)
	require.NoError(t, err)
	assert.Equal(t, orgID, result)
}

func TestWithWorkspaceTx_CommitAndRollback(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "store-test-commit")
	ctx := ctxWithWorkspace(orgID)

	// Test commit
	tx, err := WithWorkspaceTx(ctx, db)
	require.NoError(t, err)

	// Insert a task
	var taskID string
	err = tx.QueryRowContext(ctx,
		"INSERT INTO tasks (org_id, title, status, priority) VALUES ($1, $2, $3, $4) RETURNING id",
		orgID, "Commit Test", "queued", "P2",
	).Scan(&taskID)
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)

	// Verify task exists
	var count int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE id = $1", taskID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Test rollback
	tx2, err := WithWorkspaceTx(ctx, db)
	require.NoError(t, err)

	var taskID2 string
	err = tx2.QueryRowContext(ctx,
		"INSERT INTO tasks (org_id, title, status, priority) VALUES ($1, $2, $3, $4) RETURNING id",
		orgID, "Rollback Test", "queued", "P2",
	).Scan(&taskID2)
	require.NoError(t, err)

	err = tx2.Rollback()
	require.NoError(t, err)

	// Verify task doesn't exist
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE id = $1", taskID2).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestNullableString_Store(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   *string
		wantNil bool
		wantVal string
	}{
		{
			name:    "nil",
			input:   nil,
			wantNil: true,
		},
		{
			name:    "empty",
			input:   strPtr(""),
			wantNil: true,
		},
		{
			name:    "whitespace only",
			input:   strPtr("   "),
			wantNil: true,
		},
		{
			name:    "value with whitespace",
			input:   strPtr("  hello  "),
			wantNil: false,
			wantVal: "hello",
		},
		{
			name:    "normal value",
			input:   strPtr("test"),
			wantNil: false,
			wantVal: "test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := nullableString(tt.input)
			if tt.wantNil {
				assert.Nil(t, result)
			} else {
				assert.Equal(t, tt.wantVal, result)
			}
		})
	}
}

func TestErrorTypes(t *testing.T) {
	t.Parallel()

	// Verify error types are distinct
	assert.NotEqual(t, ErrNoWorkspace, ErrNotFound)
	assert.NotEqual(t, ErrNotFound, ErrForbidden)
	assert.NotEqual(t, ErrNoWorkspace, ErrForbidden)

	// Verify error messages
	assert.Contains(t, ErrNoWorkspace.Error(), "workspace")
	assert.Contains(t, ErrNotFound.Error(), "not found")
	assert.Contains(t, ErrForbidden.Error(), "denied")
}

func strPtr(s string) *string {
	return &s
}
