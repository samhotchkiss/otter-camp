package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOrgIsolation verifies that Org A can't see Org B data
func TestOrgIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	// Create two organizations
	orgA := createTestOrganization(t, db, "org-a")
	orgB := createTestOrganization(t, db, "org-b")

	// Create tasks in each org
	taskStore := NewTaskStore(db)

	ctxA := ctxWithWorkspace(orgA)
	ctxB := ctxWithWorkspace(orgB)

	taskA, err := taskStore.Create(ctxA, CreateTaskInput{
		Title:    "Task in Org A",
		Status:   "queued",
		Priority: "P2",
	})
	require.NoError(t, err)

	taskB, err := taskStore.Create(ctxB, CreateTaskInput{
		Title:    "Task in Org B",
		Status:   "queued",
		Priority: "P2",
	})
	require.NoError(t, err)

	// Org A should only see its own task
	tasksA, err := taskStore.List(ctxA, TaskFilter{})
	require.NoError(t, err)
	assert.Len(t, tasksA, 1)
	assert.Equal(t, taskA.ID, tasksA[0].ID)
	assert.Equal(t, "Task in Org A", tasksA[0].Title)

	// Org B should only see its own task
	tasksB, err := taskStore.List(ctxB, TaskFilter{})
	require.NoError(t, err)
	assert.Len(t, tasksB, 1)
	assert.Equal(t, taskB.ID, tasksB[0].ID)
	assert.Equal(t, "Task in Org B", tasksB[0].Title)

	// Org A should not be able to get Org B's task by ID
	_, err = taskStore.GetByID(ctxA, taskB.ID)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden))

	// Org B should not be able to get Org A's task by ID
	_, err = taskStore.GetByID(ctxB, taskA.ID)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden))
}

// TestOrgIsolationTasks verifies task isolation by org
func TestOrgIsolationTasks(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "tasks-org-a")
	orgB := createTestOrganization(t, db, "tasks-org-b")

	taskStore := NewTaskStore(db)

	ctxA := ctxWithWorkspace(orgA)
	ctxB := ctxWithWorkspace(orgB)

	// Create multiple tasks in Org A
	for i := 0; i < 5; i++ {
		_, err := taskStore.Create(ctxA, CreateTaskInput{
			Title:    "Task A",
			Status:   "queued",
			Priority: "P2",
		})
		require.NoError(t, err)
	}

	// Create tasks in Org B
	for i := 0; i < 3; i++ {
		_, err := taskStore.Create(ctxB, CreateTaskInput{
			Title:    "Task B",
			Status:   "queued",
			Priority: "P2",
		})
		require.NoError(t, err)
	}

	// Verify counts
	tasksA, err := taskStore.List(ctxA, TaskFilter{})
	require.NoError(t, err)
	assert.Len(t, tasksA, 5)

	tasksB, err := taskStore.List(ctxB, TaskFilter{})
	require.NoError(t, err)
	assert.Len(t, tasksB, 3)
}

// TestOrgIsolationAgents verifies agent isolation by org
func TestOrgIsolationAgents(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "agents-org-a")
	orgB := createTestOrganization(t, db, "agents-org-b")

	agentStore := NewAgentStore(db)

	ctxA := ctxWithWorkspace(orgA)
	ctxB := ctxWithWorkspace(orgB)

	// Create agent in Org A
	agentA, err := agentStore.Create(ctxA, CreateAgentInput{
		Slug:        "agent-a",
		DisplayName: "Agent A",
		Status:      "active",
	})
	require.NoError(t, err)

	// Create agent in Org B
	agentB, err := agentStore.Create(ctxB, CreateAgentInput{
		Slug:        "agent-b",
		DisplayName: "Agent B",
		Status:      "active",
	})
	require.NoError(t, err)

	// Org A should only see its agent
	agentsA, err := agentStore.List(ctxA)
	require.NoError(t, err)
	assert.Len(t, agentsA, 1)
	assert.Equal(t, agentA.ID, agentsA[0].ID)

	// Org B should only see its agent
	agentsB, err := agentStore.List(ctxB)
	require.NoError(t, err)
	assert.Len(t, agentsB, 1)
	assert.Equal(t, agentB.ID, agentsB[0].ID)

	// Cross-org access should fail
	_, err = agentStore.GetByID(ctxA, agentB.ID)
	assert.Error(t, err)

	_, err = agentStore.GetByID(ctxB, agentA.ID)
	assert.Error(t, err)
}

// TestOrgIsolationProjects verifies project isolation by org
func TestOrgIsolationProjects(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "projects-org-a")
	orgB := createTestOrganization(t, db, "projects-org-b")

	projectStore := NewProjectStore(db)

	ctxA := ctxWithWorkspace(orgA)
	ctxB := ctxWithWorkspace(orgB)

	// Create project in Org A
	projectA, err := projectStore.Create(ctxA, CreateProjectInput{
		Name:   "Project A",
		Status: "active",
	})
	require.NoError(t, err)

	// Create project in Org B
	projectB, err := projectStore.Create(ctxB, CreateProjectInput{
		Name:   "Project B",
		Status: "active",
	})
	require.NoError(t, err)

	// Org A should only see its project
	projectsA, err := projectStore.List(ctxA)
	require.NoError(t, err)
	assert.Len(t, projectsA, 1)
	assert.Equal(t, projectA.ID, projectsA[0].ID)

	// Org B should only see its project
	projectsB, err := projectStore.List(ctxB)
	require.NoError(t, err)
	assert.Len(t, projectsB, 1)
	assert.Equal(t, projectB.ID, projectsB[0].ID)

	// Cross-org access should fail
	_, err = projectStore.GetByID(ctxA, projectB.ID)
	assert.Error(t, err)

	_, err = projectStore.GetByID(ctxB, projectA.ID)
	assert.Error(t, err)
}

// TestOrgIsolationFeed verifies activity feed isolation by org
func TestOrgIsolationFeed(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "feed-org-a")
	orgB := createTestOrganization(t, db, "feed-org-b")

	activityStore := NewActivityStore(db)

	ctxA := ctxWithWorkspace(orgA)
	ctxB := ctxWithWorkspace(orgB)

	// Create activity in Org A
	_, err := activityStore.Create(ctxA, CreateActivityInput{
		Action:   "task.created",
		Metadata: json.RawMessage(`{"source": "org-a"}`),
	})
	require.NoError(t, err)

	// Create activity in Org B
	_, err = activityStore.Create(ctxB, CreateActivityInput{
		Action:   "task.created",
		Metadata: json.RawMessage(`{"source": "org-b"}`),
	})
	require.NoError(t, err)

	// Org A should only see its activity
	feedA, err := activityStore.List(ctxA, ActivityFilter{Limit: 100})
	require.NoError(t, err)
	assert.Len(t, feedA, 1)
	assert.Contains(t, string(feedA[0].Metadata), "org-a")

	// Org B should only see its activity
	feedB, err := activityStore.List(ctxB, ActivityFilter{Limit: 100})
	require.NoError(t, err)
	assert.Len(t, feedB, 1)
	assert.Contains(t, string(feedB[0].Metadata), "org-b")
}

// TestRLSEnabled verifies that RLS policies are active
func TestRLSEnabled(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	// Check that RLS is enabled on key tables
	tables := []string{
		"tasks",
		"agents",
		"projects",
		"agent_memories",
		"activity_log",
		"tags",
		"project_chat_messages",
		"project_issues",
		"project_issue_github_links",
		"project_issue_sync_checkpoints",
		"project_issue_participants",
		"project_issue_comments",
	}

	for _, table := range tables {
		var rlsEnabled bool
		err := db.QueryRow(`
			SELECT relrowsecurity 
			FROM pg_class 
			WHERE relname = $1
		`, table).Scan(&rlsEnabled)

		if err == sql.ErrNoRows {
			// Table might not exist in test, skip
			continue
		}
		require.NoError(t, err, "failed to check RLS for table %s", table)
		assert.True(t, rlsEnabled, "RLS should be enabled on table %s", table)
	}
}

func TestAgentMemoriesRLSBlocksCrossOrgReadAndWrite(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "agent-mem-rls-a")
	orgB := createTestOrganization(t, db, "agent-mem-rls-b")

	var agentAID string
	err := db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'agent-mem-rls-a', 'Agent Memory A', 'active')
		 RETURNING id`,
		orgA,
	).Scan(&agentAID)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO agent_memories (org_id, agent_id, kind, date, content)
		 VALUES ($1, $2, 'daily', CURRENT_DATE, 'Org A memory')`,
		orgA,
		agentAID,
	)
	require.NoError(t, err)

	ctxB := ctxWithWorkspace(orgB)
	connB, err := WithWorkspace(ctxB, db)
	require.NoError(t, err)
	defer connB.Close()

	var count int
	err = connB.QueryRowContext(
		ctxB,
		`SELECT COUNT(*) FROM agent_memories WHERE agent_id = $1`,
		agentAID,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "org B must not be able to read org A memories")

	_, err = connB.ExecContext(
		ctxB,
		`INSERT INTO agent_memories (org_id, agent_id, kind, date, content)
		 VALUES ($1, $2, 'daily', CURRENT_DATE, 'cross-org write attempt')`,
		orgA,
		agentAID,
	)
	require.Error(t, err, "cross-org write should be blocked by RLS")
}

// TestRLSBypass verifies that superusers can bypass RLS (admin operations)
func TestRLSBypass(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "bypass-org-a")
	orgB := createTestOrganization(t, db, "bypass-org-b")

	// Insert tasks directly (simulating admin/superuser)
	var taskAID, taskBID string
	err := db.QueryRow(
		"INSERT INTO tasks (org_id, title, status, priority) VALUES ($1, $2, $3, $4) RETURNING id",
		orgA, "Admin Task A", "queued", "P2",
	).Scan(&taskAID)
	require.NoError(t, err)

	err = db.QueryRow(
		"INSERT INTO tasks (org_id, title, status, priority) VALUES ($1, $2, $3, $4) RETURNING id",
		orgB, "Admin Task B", "queued", "P2",
	).Scan(&taskBID)
	require.NoError(t, err)

	// Direct query without setting app.org_id should see all tasks (superuser bypass)
	// This works because the test DB connection typically has superuser privileges
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 2, count, "superuser should see all tasks")
}

// TestCrossOrgQuery verifies that cross-org joins return nothing
func TestCrossOrgQuery(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "cross-org-a")
	orgB := createTestOrganization(t, db, "cross-org-b")

	// Create project in Org A
	var projectAID string
	err := db.QueryRow(
		"INSERT INTO projects (org_id, name, status) VALUES ($1, $2, $3) RETURNING id",
		orgA, "Project A", "active",
	).Scan(&projectAID)
	require.NoError(t, err)

	// Create task in Org B referencing Org A's project (should violate FK or be filtered by RLS)
	taskStore := NewTaskStore(db)
	ctxB := ctxWithWorkspace(orgB)

	// This should either fail or not be able to see the project
	_, err = taskStore.Create(ctxB, CreateTaskInput{
		Title:     "Cross-org task",
		ProjectID: &projectAID,
		Status:    "queued",
		Priority:  "P2",
	})
	// The creation might succeed (FK check passes since project exists)
	// but when we query, the cross-org join should return filtered results

	// Verify that from Org B's perspective, we can't see the task with Org A's project
	// by listing tasks and checking the project relationship
	tasks, err := taskStore.List(ctxB, TaskFilter{ProjectID: &projectAID})
	require.NoError(t, err)
	// With RLS properly enforced, this should return no tasks
	// because the project doesn't belong to Org B
	assert.Empty(t, tasks, "cross-org project filter should return no results")
}

// TestNoWorkspaceError verifies that operations fail without workspace context
func TestNoWorkspaceError(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	taskStore := NewTaskStore(db)
	agentStore := NewAgentStore(db)
	projectStore := NewProjectStore(db)
	activityStore := NewActivityStore(db)

	ctx := context.Background() // No workspace in context

	// All operations should fail with ErrNoWorkspace
	_, err := taskStore.List(ctx, TaskFilter{})
	assert.ErrorIs(t, err, ErrNoWorkspace)

	_, err = taskStore.GetByID(ctx, "some-id")
	assert.ErrorIs(t, err, ErrNoWorkspace)

	_, err = agentStore.List(ctx)
	assert.ErrorIs(t, err, ErrNoWorkspace)

	_, err = projectStore.List(ctx)
	assert.ErrorIs(t, err, ErrNoWorkspace)

	_, err = activityStore.List(ctx, ActivityFilter{})
	assert.ErrorIs(t, err, ErrNoWorkspace)
}

// TestUpdateIsolation verifies that updates respect workspace boundaries
func TestUpdateIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "update-org-a")
	orgB := createTestOrganization(t, db, "update-org-b")

	taskStore := NewTaskStore(db)

	ctxA := ctxWithWorkspace(orgA)
	ctxB := ctxWithWorkspace(orgB)

	// Create task in Org A
	taskA, err := taskStore.Create(ctxA, CreateTaskInput{
		Title:    "Original Title",
		Status:   "queued",
		Priority: "P2",
	})
	require.NoError(t, err)

	// Org B should not be able to update Org A's task
	_, err = taskStore.Update(ctxB, taskA.ID, UpdateTaskInput{
		Title:    "Hacked Title",
		Status:   "queued",
		Priority: "P2",
	})
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound), "update from wrong org should fail")

	// Verify the task was not modified
	taskA, err = taskStore.GetByID(ctxA, taskA.ID)
	require.NoError(t, err)
	assert.Equal(t, "Original Title", taskA.Title)
}

// TestDeleteIsolation verifies that deletes respect workspace boundaries
func TestDeleteIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "delete-org-a")
	orgB := createTestOrganization(t, db, "delete-org-b")

	taskStore := NewTaskStore(db)

	ctxA := ctxWithWorkspace(orgA)
	ctxB := ctxWithWorkspace(orgB)

	// Create task in Org A
	taskA, err := taskStore.Create(ctxA, CreateTaskInput{
		Title:    "Task to protect",
		Status:   "queued",
		Priority: "P2",
	})
	require.NoError(t, err)

	// Org B should not be able to delete Org A's task
	err = taskStore.Delete(ctxB, taskA.ID)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound), "delete from wrong org should fail")

	// Verify the task still exists
	taskA, err = taskStore.GetByID(ctxA, taskA.ID)
	require.NoError(t, err)
	assert.Equal(t, "Task to protect", taskA.Title)
}
