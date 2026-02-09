package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectStore_Create(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "project-test-create")
	ctx := ctxWithWorkspace(orgID)

	store := NewProjectStore(db)

	input := CreateProjectInput{
		Name:   "Test Project",
		Status: "active",
	}

	project, err := store.Create(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, project)

	assert.NotEmpty(t, project.ID)
	assert.Equal(t, orgID, project.OrgID)
	assert.Equal(t, "Test Project", project.Name)
	assert.Equal(t, "active", project.Status)
	assert.NotZero(t, project.CreatedAt)
	assert.NotZero(t, project.UpdatedAt)
}

func TestProjectStore_Create_WithDescription(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "project-test-desc")
	ctx := ctxWithWorkspace(orgID)

	store := NewProjectStore(db)

	desc := "A test project description"
	input := CreateProjectInput{
		Name:        "Project with Desc",
		Status:      "active",
		Description: &desc,
	}

	project, err := store.Create(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, project)

	assert.Equal(t, desc, *project.Description)
}

func TestProjectStore_Create_WithRepoURL(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "project-test-repo")
	ctx := ctxWithWorkspace(orgID)

	store := NewProjectStore(db)

	repoURL := "https://github.com/example/repo"
	input := CreateProjectInput{
		Name:    "Project with Repo",
		Status:  "active",
		RepoURL: &repoURL,
	}

	project, err := store.Create(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, project)

	assert.Equal(t, repoURL, *project.RepoURL)
}

func TestProjectStore_Create_NoWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	store := NewProjectStore(db)
	ctx := context.Background()

	input := CreateProjectInput{
		Name:   "Test Project",
		Status: "active",
	}

	project, err := store.Create(ctx, input)
	assert.Error(t, err)
	assert.Nil(t, project)
	assert.ErrorIs(t, err, ErrNoWorkspace)
}

func TestProjectStore_GetByID(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "project-test-getbyid")
	ctx := ctxWithWorkspace(orgID)

	store := NewProjectStore(db)

	// Create a project
	created, err := store.Create(ctx, CreateProjectInput{
		Name:   "Findable Project",
		Status: "active",
	})
	require.NoError(t, err)

	// Retrieve it
	found, err := store.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, found)

	assert.Equal(t, created.ID, found.ID)
	assert.Equal(t, "Findable Project", found.Name)
}

func TestProjectStore_GetByID_NotFound(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "project-test-notfound")
	ctx := ctxWithWorkspace(orgID)

	store := NewProjectStore(db)

	project, err := store.GetByID(ctx, "550e8400-e29b-41d4-a716-446655440000")
	assert.Error(t, err)
	assert.Nil(t, project)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestProjectStore_GetByName(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "project-test-getbyname")
	ctx := ctxWithWorkspace(orgID)

	store := NewProjectStore(db)

	// Create a project
	created, err := store.Create(ctx, CreateProjectInput{
		Name:   "Unique Name",
		Status: "active",
	})
	require.NoError(t, err)

	// Find by name
	found, err := store.GetByName(ctx, "Unique Name")
	require.NoError(t, err)
	require.NotNil(t, found)

	assert.Equal(t, created.ID, found.ID)
	assert.Equal(t, "Unique Name", found.Name)
}

func TestProjectStore_GetByName_NotFound(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "project-test-name-notfound")
	ctx := ctxWithWorkspace(orgID)

	store := NewProjectStore(db)

	project, err := store.GetByName(ctx, "Nonexistent Name")
	assert.Error(t, err)
	assert.Nil(t, project)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestProjectStore_List(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "project-test-list")
	ctx := ctxWithWorkspace(orgID)

	store := NewProjectStore(db)

	// Create multiple projects
	for i := 0; i < 3; i++ {
		_, err := store.Create(ctx, CreateProjectInput{
			Name:   "Project " + string(rune('A'+i)),
			Status: "active",
		})
		require.NoError(t, err)
	}

	// List all
	projects, err := store.List(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(projects), 3)
}

func TestProjectStore_List_NoWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	store := NewProjectStore(db)
	ctx := context.Background()

	projects, err := store.List(ctx)
	assert.Error(t, err)
	assert.Nil(t, projects)
	assert.ErrorIs(t, err, ErrNoWorkspace)
}

func TestProjectStore_Update(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "project-test-update")
	ctx := ctxWithWorkspace(orgID)

	store := NewProjectStore(db)

	// Create a project
	created, err := store.Create(ctx, CreateProjectInput{
		Name:   "Original Name",
		Status: "active",
	})
	require.NoError(t, err)

	// Update it
	newDesc := "Updated description"
	updated, err := store.Update(ctx, created.ID, UpdateProjectInput{
		Name:        "Updated Name",
		Status:      "archived",
		Description: &newDesc,
	})
	require.NoError(t, err)
	require.NotNil(t, updated)

	assert.Equal(t, created.ID, updated.ID)
	assert.Equal(t, "Updated Name", updated.Name)
	assert.Equal(t, "archived", updated.Status)
	assert.Equal(t, newDesc, *updated.Description)
}

func TestProjectStore_Update_NotFound(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "project-test-update-notfound")
	ctx := ctxWithWorkspace(orgID)

	store := NewProjectStore(db)

	project, err := store.Update(ctx, "550e8400-e29b-41d4-a716-446655440000", UpdateProjectInput{
		Name:   "Whatever",
		Status: "active",
	})
	assert.Error(t, err)
	assert.Nil(t, project)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestProjectStore_Delete(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "project-test-delete")
	ctx := ctxWithWorkspace(orgID)

	store := NewProjectStore(db)

	// Create a project
	created, err := store.Create(ctx, CreateProjectInput{
		Name:   "To Delete",
		Status: "active",
	})
	require.NoError(t, err)

	// Delete it
	err = store.Delete(ctx, created.ID)
	require.NoError(t, err)

	// Verify it's gone
	project, err := store.GetByID(ctx, created.ID)
	assert.Error(t, err)
	assert.Nil(t, project)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestProjectStore_Delete_NotFound(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "project-test-delete-notfound")
	ctx := ctxWithWorkspace(orgID)

	store := NewProjectStore(db)

	err := store.Delete(ctx, "550e8400-e29b-41d4-a716-446655440000")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestProjectStore_WorkspaceIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	// Create two organizations
	orgID1 := createTestOrganization(t, db, "project-isolation-1")
	orgID2 := createTestOrganization(t, db, "project-isolation-2")

	ctx1 := ctxWithWorkspace(orgID1)
	ctx2 := ctxWithWorkspace(orgID2)

	store := NewProjectStore(db)

	// Create project in org1
	project1, err := store.Create(ctx1, CreateProjectInput{
		Name:   "Org1 Project",
		Status: "active",
	})
	require.NoError(t, err)

	// Create project in org2
	project2, err := store.Create(ctx2, CreateProjectInput{
		Name:   "Org2 Project",
		Status: "active",
	})
	require.NoError(t, err)

	// Org1 cannot see org2's project
	_, err = store.GetByID(ctx1, project2.ID)
	assert.ErrorIs(t, err, ErrForbidden)

	// Org2 cannot see org1's project
	_, err = store.GetByID(ctx2, project1.ID)
	assert.ErrorIs(t, err, ErrForbidden)

	// Each org's list only contains their projects
	projects1, err := store.List(ctx1)
	require.NoError(t, err)
	for _, p := range projects1 {
		assert.Equal(t, orgID1, p.OrgID)
	}

	projects2, err := store.List(ctx2)
	require.NoError(t, err)
	for _, p := range projects2 {
		assert.Equal(t, orgID2, p.OrgID)
	}
}

func TestProjectStoreListWithLabels(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "project-label-filter-org")
	otherOrgID := createTestOrganization(t, db, "project-label-filter-other-org")
	ctx := ctxWithWorkspace(orgID)
	otherCtx := ctxWithWorkspace(otherOrgID)

	projectStore := NewProjectStore(db)
	labelStore := NewLabelStore(db)

	projectA, err := projectStore.Create(ctx, CreateProjectInput{Name: "Project A", Status: "active"})
	require.NoError(t, err)
	projectB, err := projectStore.Create(ctx, CreateProjectInput{Name: "Project B", Status: "active"})
	require.NoError(t, err)
	projectC, err := projectStore.Create(ctx, CreateProjectInput{Name: "Project C", Status: "active"})
	require.NoError(t, err)

	labelBug, err := labelStore.Create(ctx, "bug", "#ef4444")
	require.NoError(t, err)
	labelBackend, err := labelStore.Create(ctx, "backend", "#22c55e")
	require.NoError(t, err)
	labelOps, err := labelStore.Create(ctx, "ops", "#3b82f6")
	require.NoError(t, err)

	require.NoError(t, labelStore.AddToProject(ctx, projectA.ID, labelBug.ID))
	require.NoError(t, labelStore.AddToProject(ctx, projectA.ID, labelBackend.ID))
	require.NoError(t, labelStore.AddToProject(ctx, projectB.ID, labelBug.ID))
	require.NoError(t, labelStore.AddToProject(ctx, projectC.ID, labelOps.ID))

	otherProject, err := projectStore.Create(otherCtx, CreateProjectInput{Name: "Other Project", Status: "active"})
	require.NoError(t, err)
	otherLabel, err := labelStore.Create(otherCtx, "other", "#a855f7")
	require.NoError(t, err)
	require.NoError(t, labelStore.AddToProject(otherCtx, otherProject.ID, otherLabel.ID))

	allProjects, err := projectStore.ListWithLabels(ctx, nil)
	require.NoError(t, err)
	labelsByProject := make(map[string][]Label, len(allProjects))
	for _, project := range allProjects {
		labelsByProject[project.ID] = project.Labels
		assert.Equal(t, orgID, project.OrgID)
	}
	require.Len(t, labelsByProject[projectA.ID], 2)
	require.Len(t, labelsByProject[projectB.ID], 1)
	require.Len(t, labelsByProject[projectC.ID], 1)
	_, foundOtherOrgProject := labelsByProject[otherProject.ID]
	require.False(t, foundOtherOrgProject)

	filteredBug, err := projectStore.ListWithLabels(ctx, []string{labelBug.ID})
	require.NoError(t, err)
	require.Len(t, filteredBug, 2)

	filteredBugAndBackend, err := projectStore.ListWithLabels(ctx, []string{labelBug.ID, labelBackend.ID})
	require.NoError(t, err)
	require.Len(t, filteredBugAndBackend, 1)
	require.Equal(t, projectA.ID, filteredBugAndBackend[0].ID)
	require.Len(t, filteredBugAndBackend[0].Labels, 2)

	filteredNone, err := projectStore.ListWithLabels(ctx, []string{labelBug.ID, labelOps.ID})
	require.NoError(t, err)
	require.Len(t, filteredNone, 0)

	_, err = projectStore.ListWithLabels(ctx, []string{"not-a-uuid"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid label filter")

	foundProject, err := projectStore.GetByID(ctx, projectA.ID)
	require.NoError(t, err)
	require.Len(t, foundProject.Labels, 2)
}
