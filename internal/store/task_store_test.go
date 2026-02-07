package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskStore_Create(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "task-test-create")
	ctx := ctxWithWorkspace(orgID)

	store := NewTaskStore(db)

	input := CreateTaskInput{
		Title:    "Test Task",
		Status:   "queued",
		Priority: "P2",
		Context:  json.RawMessage(`{"key":"value"}`),
	}

	task, err := store.Create(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, task)

	assert.NotEmpty(t, task.ID)
	assert.Equal(t, orgID, task.OrgID)
	assert.Equal(t, "Test Task", task.Title)
	assert.Equal(t, "queued", task.Status)
	assert.Equal(t, "P2", task.Priority)
	assert.NotZero(t, task.Number)
	assert.NotZero(t, task.CreatedAt)
	assert.NotZero(t, task.UpdatedAt)
}

func TestTaskStore_Create_WithAllFields(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "task-test-all-fields")
	ctx := ctxWithWorkspace(orgID)

	// Create project for the task
	var projectID string
	err := db.QueryRow(
		"INSERT INTO projects (org_id, name, slug, status) VALUES ($1, $2, $3, $4) RETURNING id",
		orgID, "Test Project", "test-project", "active",
	).Scan(&projectID)
	require.NoError(t, err)

	// Create agent for assignment
	var agentID string
	err = db.QueryRow(
		"INSERT INTO agents (org_id, slug, display_name, status) VALUES ($1, $2, $3, $4) RETURNING id",
		orgID, "test-agent", "Test Agent", "active",
	).Scan(&agentID)
	require.NoError(t, err)

	store := NewTaskStore(db)
	desc := "A detailed description"

	input := CreateTaskInput{
		ProjectID:       &projectID,
		Title:           "Full Task",
		Description:     &desc,
		Status:          "in_progress",
		Priority:        "P0",
		Context:         json.RawMessage(`{"urgent":true}`),
		AssignedAgentID: &agentID,
	}

	task, err := store.Create(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, task)

	assert.Equal(t, projectID, *task.ProjectID)
	assert.Equal(t, "A detailed description", *task.Description)
	assert.Equal(t, "in_progress", task.Status)
	assert.Equal(t, "P0", task.Priority)
	assert.Equal(t, agentID, *task.AssignedAgentID)
}

func TestTaskStore_Create_NoWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	store := NewTaskStore(db)
	ctx := context.Background() // No workspace

	input := CreateTaskInput{
		Title:    "Test Task",
		Status:   "queued",
		Priority: "P2",
	}

	task, err := store.Create(ctx, input)
	assert.Error(t, err)
	assert.Nil(t, task)
	assert.ErrorIs(t, err, ErrNoWorkspace)
}

func TestTaskStore_GetByID(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "task-test-getbyid")
	ctx := ctxWithWorkspace(orgID)

	store := NewTaskStore(db)

	// Create a task first
	input := CreateTaskInput{
		Title:    "Findable Task",
		Status:   "queued",
		Priority: "P1",
	}

	created, err := store.Create(ctx, input)
	require.NoError(t, err)

	// Retrieve it
	found, err := store.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, found)

	assert.Equal(t, created.ID, found.ID)
	assert.Equal(t, "Findable Task", found.Title)
	assert.Equal(t, "queued", found.Status)
	assert.Equal(t, "P1", found.Priority)
}

func TestTaskStore_GetByID_NotFound(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "task-test-notfound")
	ctx := ctxWithWorkspace(orgID)

	store := NewTaskStore(db)

	task, err := store.GetByID(ctx, "550e8400-e29b-41d4-a716-446655440000")
	assert.Error(t, err)
	assert.Nil(t, task)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestTaskStore_GetByNumber(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "task-test-getbynumber")
	ctx := ctxWithWorkspace(orgID)

	store := NewTaskStore(db)

	// Create a task
	input := CreateTaskInput{
		Title:    "Numbered Task",
		Status:   "queued",
		Priority: "P2",
	}

	created, err := store.Create(ctx, input)
	require.NoError(t, err)

	// Find by number
	found, err := store.GetByNumber(ctx, created.Number)
	require.NoError(t, err)
	require.NotNil(t, found)

	assert.Equal(t, created.ID, found.ID)
	assert.Equal(t, created.Number, found.Number)
}

func TestTaskStore_GetByNumber_NotFound(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "task-test-number-notfound")
	ctx := ctxWithWorkspace(orgID)

	store := NewTaskStore(db)

	task, err := store.GetByNumber(ctx, 99999)
	assert.Error(t, err)
	assert.Nil(t, task)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestTaskStore_List(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "task-test-list")
	ctx := ctxWithWorkspace(orgID)

	store := NewTaskStore(db)

	// Create multiple tasks
	for i := 0; i < 3; i++ {
		_, err := store.Create(ctx, CreateTaskInput{
			Title:    "List Task",
			Status:   "queued",
			Priority: "P2",
		})
		require.NoError(t, err)
	}

	// List all
	tasks, err := store.List(ctx, TaskFilter{})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(tasks), 3)
}

func TestTaskStore_List_WithStatusFilter(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "task-test-list-status")
	ctx := ctxWithWorkspace(orgID)

	store := NewTaskStore(db)

	// Create tasks with different statuses
	_, err := store.Create(ctx, CreateTaskInput{Title: "Queued", Status: "queued", Priority: "P2"})
	require.NoError(t, err)

	_, err = store.Create(ctx, CreateTaskInput{Title: "In Progress", Status: "in_progress", Priority: "P2"})
	require.NoError(t, err)

	_, err = store.Create(ctx, CreateTaskInput{Title: "Done", Status: "done", Priority: "P2"})
	require.NoError(t, err)

	// Filter by queued
	tasks, err := store.List(ctx, TaskFilter{Status: "queued"})
	require.NoError(t, err)
	for _, task := range tasks {
		assert.Equal(t, "queued", task.Status)
	}

	// Filter by in_progress
	tasks, err = store.List(ctx, TaskFilter{Status: "in_progress"})
	require.NoError(t, err)
	for _, task := range tasks {
		assert.Equal(t, "in_progress", task.Status)
	}
}

func TestTaskStore_List_WithProjectFilter(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "task-test-list-project")
	ctx := ctxWithWorkspace(orgID)

	// Create two projects
	var projectID1, projectID2 string
	err := db.QueryRow(
		"INSERT INTO projects (org_id, name, slug, status) VALUES ($1, $2, $3, $4) RETURNING id",
		orgID, "Project 1", "project-1", "active",
	).Scan(&projectID1)
	require.NoError(t, err)

	err = db.QueryRow(
		"INSERT INTO projects (org_id, name, slug, status) VALUES ($1, $2, $3, $4) RETURNING id",
		orgID, "Project 2", "project-2", "active",
	).Scan(&projectID2)
	require.NoError(t, err)

	store := NewTaskStore(db)

	// Create tasks in different projects
	_, err = store.Create(ctx, CreateTaskInput{Title: "P1 Task", ProjectID: &projectID1, Status: "queued", Priority: "P2"})
	require.NoError(t, err)

	_, err = store.Create(ctx, CreateTaskInput{Title: "P2 Task", ProjectID: &projectID2, Status: "queued", Priority: "P2"})
	require.NoError(t, err)

	// Filter by project 1
	tasks, err := store.List(ctx, TaskFilter{ProjectID: &projectID1})
	require.NoError(t, err)
	for _, task := range tasks {
		assert.Equal(t, projectID1, *task.ProjectID)
	}
}

func TestTaskStore_Update(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "task-test-update")
	ctx := ctxWithWorkspace(orgID)

	store := NewTaskStore(db)

	// Create a task
	created, err := store.Create(ctx, CreateTaskInput{
		Title:    "Original Title",
		Status:   "queued",
		Priority: "P3",
	})
	require.NoError(t, err)

	// Update it
	newDesc := "Updated description"
	updated, err := store.Update(ctx, created.ID, UpdateTaskInput{
		Title:       "Updated Title",
		Description: &newDesc,
		Status:      "in_progress",
		Priority:    "P1",
		Context:     json.RawMessage(`{"updated":true}`),
	})
	require.NoError(t, err)
	require.NotNil(t, updated)

	assert.Equal(t, created.ID, updated.ID)
	assert.Equal(t, "Updated Title", updated.Title)
	assert.Equal(t, "Updated description", *updated.Description)
	assert.Equal(t, "in_progress", updated.Status)
	assert.Equal(t, "P1", updated.Priority)
}

func TestTaskStore_Update_NotFound(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "task-test-update-notfound")
	ctx := ctxWithWorkspace(orgID)

	store := NewTaskStore(db)

	task, err := store.Update(ctx, "550e8400-e29b-41d4-a716-446655440000", UpdateTaskInput{
		Title:    "Doesn't matter",
		Status:   "queued",
		Priority: "P2",
	})
	assert.Error(t, err)
	assert.Nil(t, task)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestTaskStore_UpdateStatus(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "task-test-updatestatus")
	ctx := ctxWithWorkspace(orgID)

	store := NewTaskStore(db)

	// Create a task
	created, err := store.Create(ctx, CreateTaskInput{
		Title:    "Status Test",
		Status:   "queued",
		Priority: "P2",
	})
	require.NoError(t, err)
	assert.Equal(t, "queued", created.Status)

	// Update status
	updated, err := store.UpdateStatus(ctx, created.ID, "in_progress")
	require.NoError(t, err)
	require.NotNil(t, updated)

	assert.Equal(t, created.ID, updated.ID)
	assert.Equal(t, "in_progress", updated.Status)

	// Update again
	updated, err = store.UpdateStatus(ctx, created.ID, "done")
	require.NoError(t, err)
	assert.Equal(t, "done", updated.Status)
}

func TestTaskStore_UpdateStatus_NotFound(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "task-test-status-notfound")
	ctx := ctxWithWorkspace(orgID)

	store := NewTaskStore(db)

	task, err := store.UpdateStatus(ctx, "550e8400-e29b-41d4-a716-446655440000", "done")
	assert.Error(t, err)
	assert.Nil(t, task)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestTaskStore_Delete(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "task-test-delete")
	ctx := ctxWithWorkspace(orgID)

	store := NewTaskStore(db)

	// Create a task
	created, err := store.Create(ctx, CreateTaskInput{
		Title:    "To Delete",
		Status:   "queued",
		Priority: "P2",
	})
	require.NoError(t, err)

	// Delete it
	err = store.Delete(ctx, created.ID)
	require.NoError(t, err)

	// Verify it's gone
	task, err := store.GetByID(ctx, created.ID)
	assert.Error(t, err)
	assert.Nil(t, task)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestTaskStore_Delete_NotFound(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "task-test-delete-notfound")
	ctx := ctxWithWorkspace(orgID)

	store := NewTaskStore(db)

	err := store.Delete(ctx, "550e8400-e29b-41d4-a716-446655440000")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestTaskStore_WorkspaceIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	// Create two organizations
	orgID1 := createTestOrganization(t, db, "task-isolation-1")
	orgID2 := createTestOrganization(t, db, "task-isolation-2")

	ctx1 := ctxWithWorkspace(orgID1)
	ctx2 := ctxWithWorkspace(orgID2)

	store := NewTaskStore(db)

	// Create task in org1
	task1, err := store.Create(ctx1, CreateTaskInput{
		Title:    "Org1 Task",
		Status:   "queued",
		Priority: "P2",
	})
	require.NoError(t, err)

	// Create task in org2
	task2, err := store.Create(ctx2, CreateTaskInput{
		Title:    "Org2 Task",
		Status:   "queued",
		Priority: "P2",
	})
	require.NoError(t, err)

	// Org1 cannot see org2's task
	_, err = store.GetByID(ctx1, task2.ID)
	assert.ErrorIs(t, err, ErrForbidden)

	// Org2 cannot see org1's task
	_, err = store.GetByID(ctx2, task1.ID)
	assert.ErrorIs(t, err, ErrForbidden)

	// Each org's list only contains their tasks
	tasks1, err := store.List(ctx1, TaskFilter{})
	require.NoError(t, err)
	for _, task := range tasks1 {
		assert.Equal(t, orgID1, task.OrgID)
	}

	tasks2, err := store.List(ctx2, TaskFilter{})
	require.NoError(t, err)
	for _, task := range tasks2 {
		assert.Equal(t, orgID2, task.OrgID)
	}
}

func TestBuildTaskListQuery(t *testing.T) {
	t.Parallel()

	workspaceID := "ws-123"

	// Basic query
	query, args := buildTaskListQuery(workspaceID, TaskFilter{})
	assert.Contains(t, query, "org_id = $1")
	assert.Len(t, args, 1)
	assert.Equal(t, workspaceID, args[0])

	// With status
	query, args = buildTaskListQuery(workspaceID, TaskFilter{Status: "queued"})
	assert.Contains(t, query, "status = $2")
	assert.Len(t, args, 2)

	// With all filters
	projectID := "proj-123"
	agentID := "agent-456"
	query, args = buildTaskListQuery(workspaceID, TaskFilter{
		Status:    "in_progress",
		ProjectID: &projectID,
		AgentID:   &agentID,
	})
	assert.Contains(t, query, "status = $2")
	assert.Contains(t, query, "project_id = $3")
	assert.Contains(t, query, "assigned_agent_id = $4")
	assert.Len(t, args, 4)

	// Query contains ORDER BY
	assert.Contains(t, query, "ORDER BY created_at DESC")
}

func TestNormalizeContext_TaskStore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    json.RawMessage
		expected string
	}{
		{"nil", nil, "{}"},
		{"empty", json.RawMessage{}, "{}"},
		{"null string", json.RawMessage("null"), "{}"},
		{"valid object", json.RawMessage(`{"a":1}`), `{"a":1}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeContext(tt.input)
			assert.Equal(t, tt.expected, string(result))
		})
	}
}
