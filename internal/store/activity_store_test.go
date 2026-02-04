package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActivityStore_Create(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "activity-test-create")
	ctx := ctxWithWorkspace(orgID)

	store := NewActivityStore(db)

	input := CreateActivityInput{
		Action:   "task.created",
		Metadata: json.RawMessage(`{"source":"test"}`),
	}

	activity, err := store.Create(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, activity)

	assert.NotEmpty(t, activity.ID)
	assert.Equal(t, orgID, activity.OrgID)
	assert.Equal(t, "task.created", activity.Action)
	assert.NotZero(t, activity.CreatedAt)
}

func TestActivityStore_Create_WithAllFields(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "activity-test-all-fields")
	ctx := ctxWithWorkspace(orgID)

	// Create a task for the activity
	var taskID string
	err := db.QueryRow(`
		INSERT INTO tasks (org_id, title, status, priority)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, orgID, "Test Task", "queued", "P2").Scan(&taskID)
	require.NoError(t, err)

	// Create an agent for the activity
	var agentID string
	err = db.QueryRow(`
		INSERT INTO agents (org_id, slug, display_name, status)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, orgID, "test-agent", "Test Agent", "active").Scan(&agentID)
	require.NoError(t, err)

	store := NewActivityStore(db)

	input := CreateActivityInput{
		TaskID:   &taskID,
		AgentID:  &agentID,
		Action:   "task.status_changed",
		Metadata: json.RawMessage(`{"old_status":"queued","new_status":"in_progress"}`),
	}

	activity, err := store.Create(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, activity)

	assert.Equal(t, taskID, *activity.TaskID)
	assert.Equal(t, agentID, *activity.AgentID)
	assert.Equal(t, "task.status_changed", activity.Action)
}

func TestActivityStore_Create_NoWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	store := NewActivityStore(db)
	ctx := context.Background()

	input := CreateActivityInput{
		Action:   "test.action",
		Metadata: json.RawMessage(`{}`),
	}

	activity, err := store.Create(ctx, input)
	assert.Error(t, err)
	assert.Nil(t, activity)
	assert.ErrorIs(t, err, ErrNoWorkspace)
}

func TestActivityStore_CreateWithWorkspaceID(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "activity-test-explicit")
	ctx := context.Background() // No workspace in context

	store := NewActivityStore(db)

	input := CreateActivityInput{
		Action:   "webhook.received",
		Metadata: json.RawMessage(`{"event":"task.started"}`),
	}

	activity, err := store.CreateWithWorkspaceID(ctx, orgID, input)
	require.NoError(t, err)
	require.NotNil(t, activity)

	assert.Equal(t, orgID, activity.OrgID)
	assert.Equal(t, "webhook.received", activity.Action)
}

func TestActivityStore_CreateWithWorkspaceID_EmptyWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	store := NewActivityStore(db)
	ctx := context.Background()

	input := CreateActivityInput{
		Action:   "test.action",
		Metadata: json.RawMessage(`{}`),
	}

	activity, err := store.CreateWithWorkspaceID(ctx, "", input)
	assert.Error(t, err)
	assert.Nil(t, activity)
	assert.ErrorIs(t, err, ErrNoWorkspace)
}

func TestActivityStore_List(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "activity-test-list")
	ctx := ctxWithWorkspace(orgID)

	store := NewActivityStore(db)

	// Create multiple activities
	for i := 0; i < 5; i++ {
		_, err := store.Create(ctx, CreateActivityInput{
			Action:   "test.action",
			Metadata: json.RawMessage(`{}`),
		})
		require.NoError(t, err)
	}

	// List all
	activities, err := store.List(ctx, ActivityFilter{})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(activities), 5)
}

func TestActivityStore_List_WithLimit(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "activity-test-limit")
	ctx := ctxWithWorkspace(orgID)

	store := NewActivityStore(db)

	// Create activities
	for i := 0; i < 10; i++ {
		_, err := store.Create(ctx, CreateActivityInput{
			Action:   "test.action",
			Metadata: json.RawMessage(`{}`),
		})
		require.NoError(t, err)
	}

	// List with limit
	activities, err := store.List(ctx, ActivityFilter{Limit: 3})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(activities), 3)
}

func TestActivityStore_List_WithOffset(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "activity-test-offset")
	ctx := ctxWithWorkspace(orgID)

	store := NewActivityStore(db)

	// Create activities
	for i := 0; i < 10; i++ {
		_, err := store.Create(ctx, CreateActivityInput{
			Action:   "test.action",
			Metadata: json.RawMessage(`{}`),
		})
		require.NoError(t, err)
	}

	// Get first page
	page1, err := store.List(ctx, ActivityFilter{Limit: 5, Offset: 0})
	require.NoError(t, err)

	// Get second page
	page2, err := store.List(ctx, ActivityFilter{Limit: 5, Offset: 5})
	require.NoError(t, err)

	// Should be different IDs
	if len(page1) > 0 && len(page2) > 0 {
		assert.NotEqual(t, page1[0].ID, page2[0].ID)
	}
}

func TestActivityStore_List_WithTaskFilter(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "activity-test-task-filter")
	ctx := ctxWithWorkspace(orgID)

	// Create two tasks
	var taskID1, taskID2 string
	err := db.QueryRow(`
		INSERT INTO tasks (org_id, title, status, priority)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, orgID, "Task 1", "queued", "P2").Scan(&taskID1)
	require.NoError(t, err)

	err = db.QueryRow(`
		INSERT INTO tasks (org_id, title, status, priority)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, orgID, "Task 2", "queued", "P2").Scan(&taskID2)
	require.NoError(t, err)

	store := NewActivityStore(db)

	// Create activities for both tasks
	_, err = store.Create(ctx, CreateActivityInput{TaskID: &taskID1, Action: "task.created", Metadata: json.RawMessage(`{}`)})
	require.NoError(t, err)

	_, err = store.Create(ctx, CreateActivityInput{TaskID: &taskID2, Action: "task.created", Metadata: json.RawMessage(`{}`)})
	require.NoError(t, err)

	// Filter by task1
	activities, err := store.List(ctx, ActivityFilter{TaskID: &taskID1})
	require.NoError(t, err)
	for _, a := range activities {
		assert.Equal(t, taskID1, *a.TaskID)
	}
}

func TestActivityStore_List_WithActionFilter(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "activity-test-action-filter")
	ctx := ctxWithWorkspace(orgID)

	store := NewActivityStore(db)

	// Create activities with different actions
	_, err := store.Create(ctx, CreateActivityInput{Action: "task.created", Metadata: json.RawMessage(`{}`)})
	require.NoError(t, err)

	_, err = store.Create(ctx, CreateActivityInput{Action: "task.updated", Metadata: json.RawMessage(`{}`)})
	require.NoError(t, err)

	_, err = store.Create(ctx, CreateActivityInput{Action: "task.deleted", Metadata: json.RawMessage(`{}`)})
	require.NoError(t, err)

	// Filter by action
	activities, err := store.List(ctx, ActivityFilter{Action: "task.created"})
	require.NoError(t, err)
	for _, a := range activities {
		assert.Equal(t, "task.created", a.Action)
	}
}

func TestActivityStore_List_NoWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	store := NewActivityStore(db)
	ctx := context.Background()

	activities, err := store.List(ctx, ActivityFilter{})
	assert.Error(t, err)
	assert.Nil(t, activities)
	assert.ErrorIs(t, err, ErrNoWorkspace)
}

func TestActivityStore_ListByTask(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "activity-test-listbytask")
	ctx := ctxWithWorkspace(orgID)

	// Create a task
	var taskID string
	err := db.QueryRow(`
		INSERT INTO tasks (org_id, title, status, priority)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, orgID, "Task", "queued", "P2").Scan(&taskID)
	require.NoError(t, err)

	store := NewActivityStore(db)

	// Create activities for the task
	for i := 0; i < 3; i++ {
		_, err = store.Create(ctx, CreateActivityInput{
			TaskID:   &taskID,
			Action:   "task.updated",
			Metadata: json.RawMessage(`{}`),
		})
		require.NoError(t, err)
	}

	// List by task
	activities, err := store.ListByTask(ctx, taskID, 10, 0)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(activities), 3)
	for _, a := range activities {
		assert.Equal(t, taskID, *a.TaskID)
	}
}

func TestActivityStore_WorkspaceIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	// Create two organizations
	orgID1 := createTestOrganization(t, db, "activity-isolation-1")
	orgID2 := createTestOrganization(t, db, "activity-isolation-2")

	ctx1 := ctxWithWorkspace(orgID1)
	ctx2 := ctxWithWorkspace(orgID2)

	store := NewActivityStore(db)

	// Create activity in org1
	_, err := store.Create(ctx1, CreateActivityInput{
		Action:   "org1.action",
		Metadata: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	// Create activity in org2
	_, err = store.Create(ctx2, CreateActivityInput{
		Action:   "org2.action",
		Metadata: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	// Each org's list only contains their activities
	activities1, err := store.List(ctx1, ActivityFilter{})
	require.NoError(t, err)
	for _, a := range activities1 {
		assert.Equal(t, orgID1, a.OrgID)
	}

	activities2, err := store.List(ctx2, ActivityFilter{})
	require.NoError(t, err)
	for _, a := range activities2 {
		assert.Equal(t, orgID2, a.OrgID)
	}
}

func TestBuildActivityListQuery(t *testing.T) {
	t.Parallel()

	workspaceID := "ws-123"

	// Basic query with defaults
	query, args := buildActivityListQuery(workspaceID, nil, "", 50, 0)
	assert.Contains(t, query, "org_id = $1")
	assert.Contains(t, query, "LIMIT")
	assert.Contains(t, query, "OFFSET")
	assert.Len(t, args, 3) // workspaceID, limit, offset

	// With task filter
	taskID := "task-123"
	query, args = buildActivityListQuery(workspaceID, &taskID, "", 50, 0)
	assert.Contains(t, query, "task_id = $2")
	assert.Len(t, args, 4)

	// With action filter
	query, args = buildActivityListQuery(workspaceID, nil, "task.created", 50, 0)
	assert.Contains(t, query, "action = $2")
	assert.Len(t, args, 4)

	// With both filters
	query, args = buildActivityListQuery(workspaceID, &taskID, "task.created", 50, 0)
	assert.Contains(t, query, "task_id = $2")
	assert.Contains(t, query, "action = $3")
	assert.Len(t, args, 5)

	// Query contains ORDER BY
	assert.Contains(t, query, "ORDER BY created_at DESC")
}

func TestNormalizeMetadata(t *testing.T) {
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
		{"valid array", json.RawMessage(`[1,2,3]`), `[1,2,3]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeMetadata(tt.input)
			assert.Equal(t, tt.expected, string(result))
		})
	}
}

func TestActivityStore_List_DefaultLimit(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "activity-test-default-limit")
	ctx := ctxWithWorkspace(orgID)

	store := NewActivityStore(db)

	// Create many activities
	for i := 0; i < 60; i++ {
		_, err := store.Create(ctx, CreateActivityInput{
			Action:   "test.action",
			Metadata: json.RawMessage(`{}`),
		})
		require.NoError(t, err)
	}

	// Default limit should be applied
	activities, err := store.List(ctx, ActivityFilter{})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(activities), defaultActivityLimit)
}

func TestActivityStore_List_MaxLimit(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "activity-test-max-limit")
	ctx := ctxWithWorkspace(orgID)

	store := NewActivityStore(db)

	// Request limit higher than max
	activities, err := store.List(ctx, ActivityFilter{Limit: 500})
	require.NoError(t, err)
	// Should be capped at maxActivityLimit
	assert.LessOrEqual(t, len(activities), maxActivityLimit)
}
