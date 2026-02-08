package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createLabelTestIssue(t *testing.T, db *sql.DB, orgID, projectID, title string, number int) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		`INSERT INTO project_issues (org_id, project_id, issue_number, title, state, origin)
		 VALUES ($1, $2, $3, $4, 'open', 'local')
		 RETURNING id`,
		orgID,
		projectID,
		number,
		title,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestLabelStoreCRUD(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "labels-crud-org")
	ctx := ctxWithWorkspace(orgID)

	store := NewLabelStore(db)

	created, err := store.Create(ctx, "bug", "#ef4444")
	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, orgID, created.OrgID)
	assert.Equal(t, "bug", created.Name)
	assert.Equal(t, "#ef4444", created.Color)

	byID, err := store.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, byID)
	assert.Equal(t, created.ID, byID.ID)

	byName, err := store.GetByName(ctx, "bug")
	require.NoError(t, err)
	require.NotNil(t, byName)
	assert.Equal(t, created.ID, byName.ID)

	labels, err := store.List(ctx)
	require.NoError(t, err)
	require.Len(t, labels, 1)
	assert.Equal(t, created.ID, labels[0].ID)

	updatedName := "type:bug"
	updatedColor := "#dc2626"
	updated, err := store.Update(ctx, created.ID, &updatedName, &updatedColor)
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, updatedName, updated.Name)
	assert.Equal(t, updatedColor, updated.Color)

	err = store.Delete(ctx, created.ID)
	require.NoError(t, err)

	afterDelete, err := store.GetByID(ctx, created.ID)
	assert.Error(t, err)
	assert.Nil(t, afterDelete)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestLabelStoreEnsureByName(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "labels-ensure-org")
	ctx := ctxWithWorkspace(orgID)

	store := NewLabelStore(db)

	first, err := store.EnsureByName(ctx, "feature", "#22c55e")
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, "feature", first.Name)
	assert.Equal(t, "#22c55e", first.Color)

	second, err := store.EnsureByName(ctx, "feature", "#000000")
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, "#22c55e", second.Color)
}

func TestLabelStoreProjectLabels(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "labels-project-org")
	ctx := ctxWithWorkspace(orgID)
	projectID := createTestProject(t, db, orgID, "Labels Project")

	store := NewLabelStore(db)
	bug, err := store.Create(ctx, "bug", "#ef4444")
	require.NoError(t, err)
	priority, err := store.Create(ctx, "priority:high", "#f97316")
	require.NoError(t, err)

	require.NoError(t, store.AddToProject(ctx, projectID, bug.ID))
	require.NoError(t, store.AddToProject(ctx, projectID, priority.ID))

	labels, err := store.ListForProject(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, labels, 2)

	require.NoError(t, store.RemoveFromProject(ctx, projectID, bug.ID))
	labels, err = store.ListForProject(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, labels, 1)
	assert.Equal(t, priority.ID, labels[0].ID)

	require.NoError(t, store.Delete(ctx, priority.ID))
	labels, err = store.ListForProject(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, labels, 0)
}

func TestLabelStoreIssueLabels(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "labels-issue-org")
	ctx := ctxWithWorkspace(orgID)
	projectID := createTestProject(t, db, orgID, "Issue Labels Project")
	issueID := createLabelTestIssue(t, db, orgID, projectID, "Label me", 1)

	store := NewLabelStore(db)
	label, err := store.Create(ctx, "needs-review", "#eab308")
	require.NoError(t, err)

	require.NoError(t, store.AddToIssue(ctx, issueID, label.ID))
	labels, err := store.ListForIssue(ctx, issueID)
	require.NoError(t, err)
	require.Len(t, labels, 1)
	assert.Equal(t, "needs-review", labels[0].Name)

	require.NoError(t, store.RemoveFromIssue(ctx, issueID, label.ID))
	labels, err = store.ListForIssue(ctx, issueID)
	require.NoError(t, err)
	require.Len(t, labels, 0)
}

func TestLabelStoreMapLookups(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "labels-map-org")
	ctx := ctxWithWorkspace(orgID)

	projectA := createTestProject(t, db, orgID, "Project A")
	projectB := createTestProject(t, db, orgID, "Project B")
	issueA := createLabelTestIssue(t, db, orgID, projectA, "Issue A", 1)
	issueB := createLabelTestIssue(t, db, orgID, projectB, "Issue B", 1)

	store := NewLabelStore(db)
	bug, err := store.Create(ctx, "bug", "#ef4444")
	require.NoError(t, err)
	feature, err := store.Create(ctx, "feature", "#22c55e")
	require.NoError(t, err)
	blocked, err := store.Create(ctx, "blocked", "#f97316")
	require.NoError(t, err)

	require.NoError(t, store.AddToProject(ctx, projectA, bug.ID))
	require.NoError(t, store.AddToProject(ctx, projectA, feature.ID))
	require.NoError(t, store.AddToProject(ctx, projectB, blocked.ID))
	require.NoError(t, store.AddToIssue(ctx, issueA, bug.ID))
	require.NoError(t, store.AddToIssue(ctx, issueB, feature.ID))

	projectMap, err := store.MapForProjects(ctx, []string{projectA, projectB})
	require.NoError(t, err)
	require.Len(t, projectMap[projectA], 2)
	require.Len(t, projectMap[projectB], 1)

	issueMap, err := store.MapForIssues(ctx, []string{issueA, issueB})
	require.NoError(t, err)
	require.Len(t, issueMap[issueA], 1)
	require.Len(t, issueMap[issueB], 1)
	assert.Equal(t, bug.ID, issueMap[issueA][0].ID)
	assert.Equal(t, feature.ID, issueMap[issueB][0].ID)
}

func TestLabelStoreNoWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	store := NewLabelStore(db)

	label, err := store.Create(context.Background(), "bug", "#ef4444")
	assert.Error(t, err)
	assert.Nil(t, label)
	assert.ErrorIs(t, err, ErrNoWorkspace)
}
