package store

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func createPipelineRoleTestProject(t *testing.T, db *sql.DB, orgID, name string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		`INSERT INTO projects (org_id, name, status) VALUES ($1, $2, 'active') RETURNING id`,
		orgID,
		name,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func createPipelineRoleTestAgent(t *testing.T, db *sql.DB, orgID, slug string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status) VALUES ($1, $2, $3, 'active') RETURNING id`,
		orgID,
		slug,
		"Agent "+slug,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestNormalizePipelineRoleAssignmentInput(t *testing.T) {
	t.Run("normalizes role and trims agent id", func(t *testing.T) {
		agentID := " 550e8400-e29b-41d4-a716-446655440123 "
		normalized, err := normalizePipelineRoleAssignmentInput(UpsertPipelineRoleAssignmentInput{
			ProjectID: "550e8400-e29b-41d4-a716-446655440000",
			Role:      " Planner ",
			AgentID:   &agentID,
		})
		require.NoError(t, err)
		require.Equal(t, PipelineRolePlanner, normalized.Role)
		require.NotNil(t, normalized.AgentID)
		require.Equal(t, "550e8400-e29b-41d4-a716-446655440123", *normalized.AgentID)
	})

	t.Run("rejects invalid role", func(t *testing.T) {
		_, err := normalizePipelineRoleAssignmentInput(UpsertPipelineRoleAssignmentInput{
			ProjectID: "550e8400-e29b-41d4-a716-446655440000",
			Role:      "observer",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "role")
	})
}

func TestPipelineRoleStore_UpsertListAndClear(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "pipeline-role-org")
	projectID := createPipelineRoleTestProject(t, db, orgID, "Pipeline Role Project")
	plannerAgent := createPipelineRoleTestAgent(t, db, orgID, "planner-agent")
	workerAgent := createPipelineRoleTestAgent(t, db, orgID, "worker-agent")

	store := NewPipelineRoleStore(db)
	ctx := ctxWithWorkspace(orgID)

	initial, err := store.ListByProject(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, initial, 3)
	require.Equal(t, PipelineRolePlanner, initial[0].Role)
	require.Nil(t, initial[0].AgentID)
	require.Equal(t, PipelineRoleWorker, initial[1].Role)
	require.Nil(t, initial[1].AgentID)
	require.Equal(t, PipelineRoleReviewer, initial[2].Role)
	require.Nil(t, initial[2].AgentID)

	_, err = store.Upsert(ctx, UpsertPipelineRoleAssignmentInput{
		ProjectID: projectID,
		Role:      PipelineRolePlanner,
		AgentID:   &plannerAgent,
	})
	require.NoError(t, err)
	_, err = store.Upsert(ctx, UpsertPipelineRoleAssignmentInput{
		ProjectID: projectID,
		Role:      PipelineRoleWorker,
		AgentID:   &workerAgent,
	})
	require.NoError(t, err)

	updated, err := store.ListByProject(ctx, projectID)
	require.NoError(t, err)
	require.NotNil(t, updated[0].AgentID)
	require.Equal(t, plannerAgent, *updated[0].AgentID)
	require.NotNil(t, updated[1].AgentID)
	require.Equal(t, workerAgent, *updated[1].AgentID)
	require.Nil(t, updated[2].AgentID)

	_, err = store.Upsert(ctx, UpsertPipelineRoleAssignmentInput{
		ProjectID: projectID,
		Role:      PipelineRolePlanner,
		AgentID:   nil,
	})
	require.NoError(t, err)

	cleared, err := store.ListByProject(ctx, projectID)
	require.NoError(t, err)
	require.Nil(t, cleared[0].AgentID)
}

func TestPipelineRoleStore_RejectsCrossWorkspaceAgent(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgA := createTestOrganization(t, db, "pipeline-role-org-a")
	orgB := createTestOrganization(t, db, "pipeline-role-org-b")
	projectA := createPipelineRoleTestProject(t, db, orgA, "Pipeline Role Project A")
	otherAgent := createPipelineRoleTestAgent(t, db, orgB, "other-agent")

	store := NewPipelineRoleStore(db)
	ctxA := ctxWithWorkspace(orgA)

	_, err := store.Upsert(ctxA, UpsertPipelineRoleAssignmentInput{
		ProjectID: projectA,
		Role:      PipelineRolePlanner,
		AgentID:   &otherAgent,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNotFound)
}
