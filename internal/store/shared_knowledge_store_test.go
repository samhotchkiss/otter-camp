package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSharedKnowledgeStore(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "shared-knowledge-store-org")

	var sourceAgentID string
	err := db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'shared-knowledge-source', 'Shared Knowledge Source', 'active')
		 RETURNING id`,
		orgID,
	).Scan(&sourceAgentID)
	require.NoError(t, err)

	store := NewSharedKnowledgeStore(db)
	ctx := ctxWithWorkspace(orgID)

	created, err := store.Create(ctx, CreateSharedKnowledgeInput{
		SourceAgentID: sourceAgentID,
		Kind:          SharedKnowledgeKindLesson,
		Title:         "Always include org scoping",
		Content:       "Cross-org reads should never leak records.",
		Scope:         SharedKnowledgeScopeOrg,
		QualityScore:  0.6,
	})
	require.NoError(t, err)
	require.Equal(t, SharedKnowledgeKindLesson, created.Kind)
	require.Equal(t, SharedKnowledgeStatusActive, created.Status)

	confirmed, err := store.Confirm(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, 1, confirmed.Confirmations)
	require.Greater(t, confirmed.QualityScore, created.QualityScore)

	contradicted, err := store.Contradict(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, 1, contradicted.Contradictions)
	require.Less(t, contradicted.QualityScore, confirmed.QualityScore)

	searchResults, err := store.Search(ctx, SharedKnowledgeSearchParams{
		Query: "org scoping",
		Limit: 10,
	})
	require.NoError(t, err)
	require.NotEmpty(t, searchResults)
	require.Equal(t, created.ID, searchResults[0].ID)
	require.NotNil(t, searchResults[0].Relevance)
}

func TestSharedKnowledgeStoreScopeFiltering(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "shared-knowledge-scope-org")

	var sourceAgentID string
	err := db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'shared-knowledge-source-scope', 'Shared Knowledge Source Scope', 'active')
		 RETURNING id`,
		orgID,
	).Scan(&sourceAgentID)
	require.NoError(t, err)

	var engineeringAgentID string
	err = db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'shared-knowledge-engineering', 'Engineering Agent', 'active')
		 RETURNING id`,
		orgID,
	).Scan(&engineeringAgentID)
	require.NoError(t, err)

	var contentAgentID string
	err = db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'shared-knowledge-content', 'Content Agent', 'active')
		 RETURNING id`,
		orgID,
	).Scan(&contentAgentID)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO agent_teams (org_id, agent_id, team_name) VALUES ($1, $2, 'engineering')`,
		orgID,
		engineeringAgentID,
	)
	require.NoError(t, err)
	_, err = db.Exec(
		`INSERT INTO agent_teams (org_id, agent_id, team_name) VALUES ($1, $2, 'content')`,
		orgID,
		contentAgentID,
	)
	require.NoError(t, err)

	store := NewSharedKnowledgeStore(db)
	ctx := ctxWithWorkspace(orgID)

	_, err = store.Create(ctx, CreateSharedKnowledgeInput{
		SourceAgentID: sourceAgentID,
		Kind:          SharedKnowledgeKindPattern,
		Title:         "Engineering-only rule",
		Content:       "Applies only to engineering team.",
		Scope:         SharedKnowledgeScopeTeam,
		ScopeTeams:    []string{"engineering"},
		QualityScore:  0.7,
	})
	require.NoError(t, err)

	_, err = store.Create(ctx, CreateSharedKnowledgeInput{
		SourceAgentID: sourceAgentID,
		Kind:          SharedKnowledgeKindFact,
		Title:         "Org-wide fact",
		Content:       "This applies to every team.",
		Scope:         SharedKnowledgeScopeOrg,
		QualityScore:  0.8,
	})
	require.NoError(t, err)

	engineerVisible, err := store.ListForAgent(ctx, engineeringAgentID, 10)
	require.NoError(t, err)
	require.Len(t, engineerVisible, 2)

	contentVisible, err := store.ListForAgent(ctx, contentAgentID, 10)
	require.NoError(t, err)
	require.Len(t, contentVisible, 1)
	require.Equal(t, "Org-wide fact", contentVisible[0].Title)
}
