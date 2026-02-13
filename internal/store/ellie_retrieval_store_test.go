package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func ellieRetrievalEmbeddingVector(values ...float64) []float64 {
	vector := make([]float64, 768)
	for i, value := range values {
		if i >= len(vector) {
			break
		}
		vector[i] = value
	}
	return vector
}

func TestEllieRetrievalStoreIncludesMemorySensitivity(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-retrieval-memory-sensitivity-org")

	_, err := db.Exec(
		`INSERT INTO memories (org_id, kind, title, content, sensitivity, status)
		 VALUES
		 ($1, 'fact', 'Normal Memory', 'normal memory content', 'normal', 'active'),
		 ($1, 'lesson', 'Sensitive Memory', 'sensitive memory content', 'sensitive', 'active')`,
		orgID,
	)
	require.NoError(t, err)

	store := NewEllieRetrievalStore(db)
	memories, err := store.ListMemoriesForOrg(context.Background(), orgID, 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(memories), 2)

	sensitivityByTitle := map[string]string{}
	for _, memory := range memories {
		sensitivityByTitle[memory.Title] = memory.Sensitivity
	}
	require.Equal(t, "normal", sensitivityByTitle["Normal Memory"])
	require.Equal(t, "sensitive", sensitivityByTitle["Sensitive Memory"])
}

func TestEllieRetrievalStoreIncludesConversationSensitivityInRoomAndHistory(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-retrieval-conversation-sensitivity-org")
	projectID := createTestProject(t, db, orgID, "Ellie Retrieval Conversation Sensitivity Project")
	agentID := insertSchemaAgent(t, db, orgID, "ellie-retrieval-conversation-agent")

	var roomID string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Ellie Retrieval Sensitivity Room', 'project', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&roomID)
	require.NoError(t, err)

	base := time.Date(2026, 2, 12, 18, 0, 0, 0, time.UTC)

	var normalConversationID string
	err = db.QueryRow(
		`INSERT INTO conversations (org_id, room_id, topic, started_at, ended_at, sensitivity)
		 VALUES ($1, $2, 'Normal conversation', $3, $4, 'normal')
		 RETURNING id`,
		orgID,
		roomID,
		base,
		base.Add(2*time.Minute),
	).Scan(&normalConversationID)
	require.NoError(t, err)

	var sensitiveConversationID string
	err = db.QueryRow(
		`INSERT INTO conversations (org_id, room_id, topic, started_at, ended_at, sensitivity)
		 VALUES ($1, $2, 'Sensitive conversation', $3, $4, 'sensitive')
		 RETURNING id`,
		orgID,
		roomID,
		base.Add(3*time.Minute),
		base.Add(5*time.Minute),
	).Scan(&sensitiveConversationID)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, type, created_at, conversation_id, attachments)
		 VALUES
		 ($1, $2, $3, 'agent', 'normal conversation message', 'message', $4, $5, '[]'::jsonb),
		 ($1, $2, $3, 'agent', 'sensitive conversation message', 'message', $6, $7, '[]'::jsonb)`,
		orgID,
		roomID,
		agentID,
		base.Add(1*time.Minute),
		normalConversationID,
		base.Add(4*time.Minute),
		sensitiveConversationID,
	)
	require.NoError(t, err)

	store := NewEllieRetrievalStore(db)

	conversations, err := store.ListRoomConversations(context.Background(), orgID, roomID, 10)
	require.NoError(t, err)
	require.Len(t, conversations, 2)

	sensitivityByTopic := map[string]string{}
	for _, conversation := range conversations {
		sensitivityByTopic[conversation.Topic] = conversation.Sensitivity
	}
	require.Equal(t, "normal", sensitivityByTopic["Normal conversation"])
	require.Equal(t, "sensitive", sensitivityByTopic["Sensitive conversation"])

	history, err := store.ListRoomConversationHistory(context.Background(), orgID, roomID, 10)
	require.NoError(t, err)
	require.Len(t, history, 2)

	sensitivityByBody := map[string]string{}
	for _, item := range history {
		require.NotNil(t, item.ConversationSensitivity)
		sensitivityByBody[item.Body] = *item.ConversationSensitivity
	}
	require.Equal(t, "normal", sensitivityByBody["normal conversation message"])
	require.Equal(t, "sensitive", sensitivityByBody["sensitive conversation message"])
}

func TestEllieRetrievalStoreSearchProjectAndOrgScopes(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-retrieval-search-scope-org")
	projectID := createTestProject(t, db, orgID, "Ellie Retrieval Scope Project")

	store := NewEllieRetrievalStore(db)

	var projectMemoryID string
	err := db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, status, source_project_id)
		 VALUES ($1, 'technical_decision', 'Project DB choice', 'Project chose Postgres', 'active', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&projectMemoryID)
	require.NoError(t, err)

	var orgMemoryID string
	err = db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, status)
		 VALUES ($1, 'preference', 'Org DB preference', 'Sam prefers explicit SQL migrations', 'active')
		 RETURNING id`,
		orgID,
	).Scan(&orgMemoryID)
	require.NoError(t, err)

	projectResults, err := store.SearchMemoriesByProject(context.Background(), orgID, projectID, "postgres", 10)
	require.NoError(t, err)
	require.Len(t, projectResults, 1)
	require.Equal(t, projectMemoryID, projectResults[0].MemoryID)

	orgResults, err := store.SearchMemoriesOrgWide(context.Background(), orgID, "sql", 10)
	require.NoError(t, err)
	require.Len(t, orgResults, 1)
	require.Equal(t, orgMemoryID, orgResults[0].MemoryID)
}

func TestEllieRetrievalStoreKeywordScaffoldBehavior(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-retrieval-keyword-scaffold-org")
	projectID := createTestProject(t, db, orgID, "Keyword Scaffold Project")

	retrievalStore := NewEllieRetrievalStore(db)

	_, err := db.Exec(
		`INSERT INTO memories (org_id, kind, title, content, status, source_project_id)
		 VALUES ($1, 'technical_decision', 'Storage', 'We chose Postgres as the persistence layer', 'active', $2)`,
		orgID,
		projectID,
	)
	require.NoError(t, err)

	results, err := retrievalStore.SearchMemoriesByProject(context.Background(), orgID, projectID, "database choice", 10)
	require.NoError(t, err)
	require.Empty(t, results)
}

func TestEllieRetrievalStoreSemanticQueryFindsNonLiteralMatches(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-retrieval-semantic-org")
	projectID := createTestProject(t, db, orgID, "Ellie Semantic Retrieval Project")
	agentID := insertSchemaAgent(t, db, orgID, "ellie-retrieval-semantic-agent")

	var roomID string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Semantic Room', 'project', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&roomID)
	require.NoError(t, err)

	var semanticMemoryID string
	err = db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, status, source_project_id)
		 VALUES ($1, 'technical_decision', 'Storage direction', 'The team chose Postgres for production persistence.', 'active', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&semanticMemoryID)
	require.NoError(t, err)

	var irrelevantMemoryID string
	err = db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, status, source_project_id)
		 VALUES ($1, 'fact', 'Unrelated memory', 'The office lunch schedule was updated.', 'active', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&irrelevantMemoryID)
	require.NoError(t, err)

	var semanticMessageID string
	err = db.QueryRow(
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, type, attachments)
		 VALUES ($1, $2, $3, 'agent', 'We selected Postgres after comparing storage options.', 'message', '[]'::jsonb)
		 RETURNING id`,
		orgID,
		roomID,
		agentID,
	).Scan(&semanticMessageID)
	require.NoError(t, err)

	queryVector := ellieRetrievalEmbeddingVector(1, 0)
	nearVector := ellieRetrievalEmbeddingVector(1, 0)
	farVector := ellieRetrievalEmbeddingVector(-1, 0)
	nearLiteral, err := formatVectorLiteral(nearVector)
	require.NoError(t, err)
	farLiteral, err := formatVectorLiteral(farVector)
	require.NoError(t, err)

	_, err = db.Exec(`UPDATE memories SET embedding = $2::vector WHERE id = $1`, semanticMemoryID, nearLiteral)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE memories SET embedding = $2::vector WHERE id = $1`, irrelevantMemoryID, farLiteral)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE chat_messages SET embedding = $2::vector WHERE id = $1`, semanticMessageID, nearLiteral)
	require.NoError(t, err)

	store := NewEllieRetrievalStore(db)

	memoryResults, err := store.SearchMemoriesByProjectWithEmbedding(
		context.Background(),
		orgID,
		projectID,
		"database choice",
		queryVector,
		10,
	)
	require.NoError(t, err)
	require.NotEmpty(t, memoryResults)
	require.Equal(t, semanticMemoryID, memoryResults[0].MemoryID)

	chatResults, err := store.SearchChatHistoryWithEmbedding(context.Background(), orgID, "database choice", queryVector, 10)
	require.NoError(t, err)
	require.NotEmpty(t, chatResults)
	require.Equal(t, semanticMessageID, chatResults[0].MessageID)
}

func TestEllieRetrievalStoreSemanticQueryBlendsKeywordAndVectorRanking(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-retrieval-semantic-blend-org")
	projectID := createTestProject(t, db, orgID, "Ellie Semantic Blend Project")

	var keywordBoostedID string
	err := db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, status, source_project_id)
		 VALUES ($1, 'technical_decision', 'Database choice documented', 'The explicit database choice remains Postgres.', 'active', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&keywordBoostedID)
	require.NoError(t, err)

	var semanticOnlyID string
	err = db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, status, source_project_id)
		 VALUES ($1, 'technical_decision', 'Storage strategy', 'The team settled on a relational engine after comparing tradeoffs.', 'active', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&semanticOnlyID)
	require.NoError(t, err)

	queryVector := ellieRetrievalEmbeddingVector(1, 0)
	keywordVector := ellieRetrievalEmbeddingVector(0.6, 0.8)
	semanticOnlyVector := ellieRetrievalEmbeddingVector(0.95, 0.3122499)
	keywordLiteral, err := formatVectorLiteral(keywordVector)
	require.NoError(t, err)
	semanticOnlyLiteral, err := formatVectorLiteral(semanticOnlyVector)
	require.NoError(t, err)

	_, err = db.Exec(`UPDATE memories SET embedding = $2::vector WHERE id = $1`, keywordBoostedID, keywordLiteral)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE memories SET embedding = $2::vector WHERE id = $1`, semanticOnlyID, semanticOnlyLiteral)
	require.NoError(t, err)

	store := NewEllieRetrievalStore(db)
	results, err := store.SearchMemoriesOrgWideWithEmbedding(context.Background(), orgID, "database choice", queryVector, 10)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, keywordBoostedID, results[0].MemoryID)
	require.Equal(t, semanticOnlyID, results[1].MemoryID)
}

func TestEllieRetrievalStoreTreatsWildcardQueryCharsAsLiterals(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-retrieval-wildcard-org")
	projectID := createTestProject(t, db, orgID, "Ellie Retrieval Wildcard Project")
	agentID := insertSchemaAgent(t, db, orgID, "ellie-retrieval-wildcard-agent")

	var roomID string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Wildcard Room', 'project', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&roomID)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO memories (org_id, kind, title, content, status, source_project_id)
		 VALUES
		 ($1, 'fact', 'literal memory', 'needle %_ marker memory', 'active', $2),
		 ($1, 'fact', 'wildcard memory', 'needle zz marker memory', 'active', $2)`,
		orgID,
		projectID,
	)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, type, attachments)
		 VALUES
		 ($1, $2, $3, 'agent', 'needle %_ marker message', 'message', '[]'::jsonb),
		 ($1, $2, $3, 'agent', 'needle zz marker message', 'message', '[]'::jsonb)`,
		orgID,
		roomID,
		agentID,
	)
	require.NoError(t, err)

	store := NewEllieRetrievalStore(db)

	roomResults, err := store.SearchRoomContext(context.Background(), orgID, roomID, "%_", 10)
	require.NoError(t, err)
	require.Len(t, roomResults, 1)
	require.Contains(t, roomResults[0].Body, "%_")

	projectMemoryResults, err := store.SearchMemoriesByProject(context.Background(), orgID, projectID, "%_", 10)
	require.NoError(t, err)
	require.Len(t, projectMemoryResults, 1)
	require.Contains(t, projectMemoryResults[0].Content, "%_")

	chatResults, err := store.SearchChatHistory(context.Background(), orgID, "%_", 10)
	require.NoError(t, err)
	require.Len(t, chatResults, 1)
	require.Contains(t, chatResults[0].Body, "%_")
}

func TestEscapeILIKEPattern(t *testing.T) {
	escaped := escapeILIKEPattern(`abc%_\path`)
	require.Equal(t, `abc\%\_\\path`, escaped)
}

func TestNormalizeEllieSearchLimitClampsUpperBound(t *testing.T) {
	require.Equal(t, 10, normalizeEllieSearchLimit(0, 10))
	require.Equal(t, 25, normalizeEllieSearchLimit(25, 10))
	require.Equal(t, maxEllieSearchQueryLimit, normalizeEllieSearchLimit(500, 10))
}

func TestEllieRetrievalStoreProjectAndOrgScopes(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "ellie-retrieval-scope-org-a")
	orgB := createTestOrganization(t, db, "ellie-retrieval-scope-org-b")

	projectA := createTestProject(t, db, orgA, "Ellie Retrieval Scope Project A")
	projectB := createTestProject(t, db, orgB, "Ellie Retrieval Scope Project B")

	var roomA string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Scope Room A', 'project', $2)
		 RETURNING id`,
		orgA,
		projectA,
	).Scan(&roomA)
	require.NoError(t, err)

	var roomB string
	err = db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Scope Room B', 'project', $2)
		 RETURNING id`,
		orgB,
		projectB,
	).Scan(&roomB)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO memories (org_id, kind, title, content, sensitivity, status, source_project_id)
		 VALUES
		 ($1, 'fact', 'Org A project memory', 'org a memory', 'sensitive', 'active', $2),
		 ($3, 'fact', 'Org B project memory', 'org b memory', 'normal', 'active', $4)`,
		orgA,
		projectA,
		orgB,
		projectB,
	)
	require.NoError(t, err)

	base := time.Date(2026, 2, 12, 19, 0, 0, 0, time.UTC)
	_, err = db.Exec(
		`INSERT INTO conversations (org_id, room_id, topic, started_at, ended_at, sensitivity)
		 VALUES
		 ($1, $2, 'Org A project conversation', $3, $4, 'sensitive'),
		 ($5, $6, 'Org B project conversation', $7, $8, 'normal')`,
		orgA,
		roomA,
		base,
		base.Add(3*time.Minute),
		orgB,
		roomB,
		base.Add(5*time.Minute),
		base.Add(8*time.Minute),
	)
	require.NoError(t, err)

	store := NewEllieRetrievalStore(db)

	memoriesA, err := store.ListProjectMemories(context.Background(), orgA, projectA, 10)
	require.NoError(t, err)
	require.Len(t, memoriesA, 1)
	require.Equal(t, "Org A project memory", memoriesA[0].Title)
	require.Equal(t, "sensitive", memoriesA[0].Sensitivity)

	conversationsA, err := store.ListProjectConversations(context.Background(), orgA, projectA, 10)
	require.NoError(t, err)
	require.Len(t, conversationsA, 1)
	require.Equal(t, "Org A project conversation", conversationsA[0].Topic)
	require.Equal(t, "sensitive", conversationsA[0].Sensitivity)

	memoriesCrossOrg, err := store.ListProjectMemories(context.Background(), orgA, projectB, 10)
	require.NoError(t, err)
	require.Empty(t, memoriesCrossOrg)

	conversationsCrossOrg, err := store.ListProjectConversations(context.Background(), orgA, projectB, 10)
	require.NoError(t, err)
	require.Empty(t, conversationsCrossOrg)
}
