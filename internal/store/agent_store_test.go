package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentStore_Create(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "agent-test-create")
	ctx := ctxWithWorkspace(orgID)

	store := NewAgentStore(db)

	input := CreateAgentInput{
		Slug:        "test-agent",
		DisplayName: "Test Agent",
		Status:      "active",
	}

	agent, err := store.Create(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, agent)

	assert.NotEmpty(t, agent.ID)
	assert.Equal(t, orgID, agent.OrgID)
	assert.Equal(t, "test-agent", agent.Slug)
	assert.Equal(t, "Test Agent", agent.DisplayName)
	assert.Equal(t, "active", agent.Status)
	assert.NotZero(t, agent.CreatedAt)
	assert.NotZero(t, agent.UpdatedAt)
}

func TestAgentStore_Create_WithAllFields(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "agent-test-all-fields")
	ctx := ctxWithWorkspace(orgID)

	store := NewAgentStore(db)

	avatarURL := "https://example.com/avatar.png"
	webhookURL := "https://example.com/webhook"
	sessionPattern := "agent:main:%"
	projectID := createTestProject(t, db, orgID, "Agent Lifecycle Project")

	input := CreateAgentInput{
		Slug:           "full-agent",
		DisplayName:    "Full Agent",
		AvatarURL:      &avatarURL,
		WebhookURL:     &webhookURL,
		Status:         "busy",
		SessionPattern: &sessionPattern,
		IsEphemeral:    true,
		ProjectID:      &projectID,
	}

	agent, err := store.Create(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, agent)

	assert.Equal(t, "full-agent", agent.Slug)
	assert.Equal(t, "Full Agent", agent.DisplayName)
	assert.Equal(t, avatarURL, *agent.AvatarURL)
	assert.Equal(t, webhookURL, *agent.WebhookURL)
	assert.Equal(t, "busy", agent.Status)
	assert.Equal(t, sessionPattern, *agent.SessionPattern)
	assert.True(t, agent.IsEphemeral)
	require.NotNil(t, agent.ProjectID)
	assert.Equal(t, projectID, *agent.ProjectID)
}

func TestAgentStore_Create_DefaultLifecycleMetadata(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "agent-test-default-lifecycle")
	ctx := ctxWithWorkspace(orgID)
	store := NewAgentStore(db)

	agent, err := store.Create(ctx, CreateAgentInput{
		Slug:        "default-lifecycle-agent",
		DisplayName: "Default Lifecycle Agent",
		Status:      "active",
	})
	require.NoError(t, err)
	require.NotNil(t, agent)
	assert.False(t, agent.IsEphemeral)
	assert.Nil(t, agent.ProjectID)
}

func TestAgentStore_Create_NoWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	store := NewAgentStore(db)
	ctx := context.Background()

	input := CreateAgentInput{
		Slug:        "test-agent",
		DisplayName: "Test Agent",
		Status:      "active",
	}

	agent, err := store.Create(ctx, input)
	assert.Error(t, err)
	assert.Nil(t, agent)
	assert.ErrorIs(t, err, ErrNoWorkspace)
}

func TestAgentStore_GetByID(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "agent-test-getbyid")
	ctx := ctxWithWorkspace(orgID)

	store := NewAgentStore(db)

	// Create an agent
	created, err := store.Create(ctx, CreateAgentInput{
		Slug:        "findable-agent",
		DisplayName: "Findable Agent",
		Status:      "active",
	})
	require.NoError(t, err)

	// Retrieve it
	found, err := store.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, found)

	assert.Equal(t, created.ID, found.ID)
	assert.Equal(t, "findable-agent", found.Slug)
	assert.Equal(t, "Findable Agent", found.DisplayName)
}

func TestAgentStore_GetByID_NotFound(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "agent-test-notfound")
	ctx := ctxWithWorkspace(orgID)

	store := NewAgentStore(db)

	agent, err := store.GetByID(ctx, "550e8400-e29b-41d4-a716-446655440000")
	assert.Error(t, err)
	assert.Nil(t, agent)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestAgentStore_GetBySlug(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "agent-test-getbyslug")
	ctx := ctxWithWorkspace(orgID)

	store := NewAgentStore(db)

	// Create an agent
	created, err := store.Create(ctx, CreateAgentInput{
		Slug:        "unique-slug",
		DisplayName: "Unique Agent",
		Status:      "active",
	})
	require.NoError(t, err)

	// Find by slug
	found, err := store.GetBySlug(ctx, "unique-slug")
	require.NoError(t, err)
	require.NotNil(t, found)

	assert.Equal(t, created.ID, found.ID)
	assert.Equal(t, "unique-slug", found.Slug)
}

func TestAgentStore_GetBySlug_NotFound(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "agent-test-slug-notfound")
	ctx := ctxWithWorkspace(orgID)

	store := NewAgentStore(db)

	agent, err := store.GetBySlug(ctx, "nonexistent-slug")
	assert.Error(t, err)
	assert.Nil(t, agent)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestAgentStore_List(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "agent-test-list")
	ctx := ctxWithWorkspace(orgID)

	store := NewAgentStore(db)

	// Create multiple agents
	for i := 0; i < 3; i++ {
		_, err := store.Create(ctx, CreateAgentInput{
			Slug:        "agent-" + string(rune('a'+i)),
			DisplayName: "Agent " + string(rune('A'+i)),
			Status:      "active",
		})
		require.NoError(t, err)
	}

	// List all
	agents, err := store.List(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(agents), 3)
}

func TestAgentStore_List_NoWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	store := NewAgentStore(db)
	ctx := context.Background()

	agents, err := store.List(ctx)
	assert.Error(t, err)
	assert.Nil(t, agents)
	assert.ErrorIs(t, err, ErrNoWorkspace)
}

func TestAgentStore_Update(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "agent-test-update")
	ctx := ctxWithWorkspace(orgID)

	store := NewAgentStore(db)

	// Create an agent
	created, err := store.Create(ctx, CreateAgentInput{
		Slug:        "original-slug",
		DisplayName: "Original Name",
		Status:      "active",
	})
	require.NoError(t, err)

	// Update it
	avatarURL := "https://example.com/new-avatar.png"
	updated, err := store.Update(ctx, created.ID, UpdateAgentInput{
		Slug:        "updated-slug",
		DisplayName: "Updated Name",
		AvatarURL:   &avatarURL,
		Status:      "busy",
	})
	require.NoError(t, err)
	require.NotNil(t, updated)

	assert.Equal(t, created.ID, updated.ID)
	assert.Equal(t, "updated-slug", updated.Slug)
	assert.Equal(t, "Updated Name", updated.DisplayName)
	assert.Equal(t, avatarURL, *updated.AvatarURL)
	assert.Equal(t, "busy", updated.Status)
}

func TestAgentStore_Update_NotFound(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "agent-test-update-notfound")
	ctx := ctxWithWorkspace(orgID)

	store := NewAgentStore(db)

	agent, err := store.Update(ctx, "550e8400-e29b-41d4-a716-446655440000", UpdateAgentInput{
		Slug:        "whatever",
		DisplayName: "Whatever",
		Status:      "active",
	})
	assert.Error(t, err)
	assert.Nil(t, agent)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestAgentStore_Delete(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "agent-test-delete")
	ctx := ctxWithWorkspace(orgID)

	store := NewAgentStore(db)

	// Create an agent
	created, err := store.Create(ctx, CreateAgentInput{
		Slug:        "to-delete",
		DisplayName: "To Delete",
		Status:      "active",
	})
	require.NoError(t, err)

	// Delete it
	err = store.Delete(ctx, created.ID)
	require.NoError(t, err)

	// Verify it's gone
	agent, err := store.GetByID(ctx, created.ID)
	assert.Error(t, err)
	assert.Nil(t, agent)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestAgentStore_Delete_NotFound(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "agent-test-delete-notfound")
	ctx := ctxWithWorkspace(orgID)

	store := NewAgentStore(db)

	err := store.Delete(ctx, "550e8400-e29b-41d4-a716-446655440000")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestAgentStore_WorkspaceIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	// Create two organizations
	orgID1 := createTestOrganization(t, db, "agent-isolation-1")
	orgID2 := createTestOrganization(t, db, "agent-isolation-2")

	ctx1 := ctxWithWorkspace(orgID1)
	ctx2 := ctxWithWorkspace(orgID2)

	store := NewAgentStore(db)

	// Create agent in org1
	agent1, err := store.Create(ctx1, CreateAgentInput{
		Slug:        "org1-agent",
		DisplayName: "Org1 Agent",
		Status:      "active",
	})
	require.NoError(t, err)

	// Create agent in org2
	agent2, err := store.Create(ctx2, CreateAgentInput{
		Slug:        "org2-agent",
		DisplayName: "Org2 Agent",
		Status:      "active",
	})
	require.NoError(t, err)

	// Org1 cannot see org2's agent
	_, err = store.GetByID(ctx1, agent2.ID)
	assert.ErrorIs(t, err, ErrForbidden)

	// Org2 cannot see org1's agent
	_, err = store.GetByID(ctx2, agent1.ID)
	assert.ErrorIs(t, err, ErrForbidden)

	// Slugs are scoped to org
	foundInOrg1, err := store.GetBySlug(ctx1, "org1-agent")
	require.NoError(t, err)
	assert.Equal(t, agent1.ID, foundInOrg1.ID)

	_, err = store.GetBySlug(ctx1, "org2-agent")
	assert.ErrorIs(t, err, ErrNotFound)

	// Each org's list only contains their agents
	agents1, err := store.List(ctx1)
	require.NoError(t, err)
	for _, agent := range agents1 {
		assert.Equal(t, orgID1, agent.OrgID)
	}

	agents2, err := store.List(ctx2)
	require.NoError(t, err)
	for _, agent := range agents2 {
		assert.Equal(t, orgID2, agent.OrgID)
	}
}

func TestAgentStore_GetBySessionPattern(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "agent-test-session")
	ctx := ctxWithWorkspace(orgID)

	store := NewAgentStore(db)

	// Create agent with session pattern
	pattern := "agent:main:%"
	_, err := store.Create(ctx, CreateAgentInput{
		Slug:           "main-agent",
		DisplayName:    "Main Agent",
		Status:         "active",
		SessionPattern: &pattern,
	})
	require.NoError(t, err)

	// Should match various sessions
	agent, err := store.GetBySessionPattern(ctx, "agent:main:subagent")
	require.NoError(t, err)
	require.NotNil(t, agent)
	assert.Equal(t, "main-agent", agent.Slug)
}

func TestAgentStore_GetBySessionPattern_NotFound(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "agent-test-session-notfound")
	ctx := ctxWithWorkspace(orgID)

	store := NewAgentStore(db)

	agent, err := store.GetBySessionPattern(ctx, "nonexistent:session")
	assert.Error(t, err)
	assert.Nil(t, agent)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestAgentStore_GetBySessionPattern_NoWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	store := NewAgentStore(db)
	ctx := context.Background()

	agent, err := store.GetBySessionPattern(ctx, "agent:main:test")
	assert.Error(t, err)
	assert.Nil(t, agent)
	assert.ErrorIs(t, err, ErrNoWorkspace)
}
