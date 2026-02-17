package store

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenClawMigrationResetStoreDeletesWorkspaceArtifactsAndReturnsCounts(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "openclaw-reset-delete-org-a")
	otherOrgID := createTestOrganization(t, db, "openclaw-reset-delete-org-b")

	seedOpenClawResetArtifacts(t, db, orgID, "a")
	seedOpenClawResetArtifacts(t, db, otherOrgID, "b")
	seedOpenClawResetProgress(t, db, orgID)
	seedOpenClawResetProgress(t, db, otherOrgID)

	resetStore := NewOpenClawMigrationResetStore(db)
	result, err := resetStore.Reset(context.Background(), OpenClawMigrationResetInput{
		OrgID:              orgID,
		OpenClawPhaseTypes: []string{"agent_import", "history_backfill", "memory_extraction", "entity_synthesis"},
	})
	require.NoError(t, err)

	require.Equal(t, 1, result.PausedPhases)
	require.Equal(t, 3, result.ProgressRowsDeleted)
	require.Equal(t, map[string]int{
		"chat_messages":         1,
		"conversations":         1,
		"room_participants":     1,
		"rooms":                 1,
		"memories":              1,
		"ellie_memory_taxonomy": 1,
		"ellie_taxonomy_nodes":  1,
		"ellie_project_docs":    1,
	}, result.Deleted)
	require.Equal(t, 8, result.TotalDeleted)

	require.Equal(t, 0, countOpenClawResetScopedRows(t, db, "chat_messages", orgID))
	require.Equal(t, 0, countOpenClawResetScopedRows(t, db, "conversations", orgID))
	require.Equal(t, 0, countOpenClawResetScopedRows(t, db, "room_participants", orgID))
	require.Equal(t, 0, countOpenClawResetScopedRows(t, db, "rooms", orgID))
	require.Equal(t, 0, countOpenClawResetScopedRows(t, db, "memories", orgID))
	require.Equal(t, 0, countOpenClawResetScopedRows(t, db, "ellie_memory_taxonomy", orgID))
	require.Equal(t, 0, countOpenClawResetScopedRows(t, db, "ellie_taxonomy_nodes", orgID))
	require.Equal(t, 0, countOpenClawResetScopedRows(t, db, "ellie_project_docs", orgID))

	require.Equal(t, 1, countOpenClawResetScopedRows(t, db, "chat_messages", otherOrgID))
	require.Equal(t, 1, countOpenClawResetScopedRows(t, db, "conversations", otherOrgID))
	require.Equal(t, 1, countOpenClawResetScopedRows(t, db, "room_participants", otherOrgID))
	require.Equal(t, 1, countOpenClawResetScopedRows(t, db, "rooms", otherOrgID))
	require.Equal(t, 1, countOpenClawResetScopedRows(t, db, "memories", otherOrgID))
	require.Equal(t, 1, countOpenClawResetScopedRows(t, db, "ellie_memory_taxonomy", otherOrgID))
	require.Equal(t, 1, countOpenClawResetScopedRows(t, db, "ellie_taxonomy_nodes", otherOrgID))
	require.Equal(t, 1, countOpenClawResetScopedRows(t, db, "ellie_project_docs", otherOrgID))
}

func TestOpenClawMigrationResetStoreOnlyDeletesOpenClawProgressRows(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "openclaw-reset-progress-org")
	seedOpenClawResetProgress(t, db, orgID)

	resetStore := NewOpenClawMigrationResetStore(db)
	result, err := resetStore.Reset(context.Background(), OpenClawMigrationResetInput{
		OrgID:              orgID,
		OpenClawPhaseTypes: []string{"agent_import", "history_backfill", "memory_extraction", "entity_synthesis"},
	})
	require.NoError(t, err)
	require.Equal(t, 3, result.ProgressRowsDeleted)

	var remaining int
	err = db.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM migration_progress
		  WHERE org_id = $1
		    AND migration_type = 'legacy_backfill'`,
		orgID,
	).Scan(&remaining)
	require.NoError(t, err)
	require.Equal(t, 1, remaining)
}

func TestOpenClawMigrationResetStoreOrgIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "openclaw-reset-isolation-org-a")
	otherOrgID := createTestOrganization(t, db, "openclaw-reset-isolation-org-b")

	seedOpenClawResetArtifacts(t, db, orgID, "a")
	seedOpenClawResetArtifacts(t, db, otherOrgID, "b")
	seedOpenClawResetProgress(t, db, orgID)
	seedOpenClawResetProgress(t, db, otherOrgID)

	resetStore := NewOpenClawMigrationResetStore(db)
	_, err := resetStore.Reset(context.Background(), OpenClawMigrationResetInput{
		OrgID:              orgID,
		OpenClawPhaseTypes: []string{"agent_import", "history_backfill", "memory_extraction", "entity_synthesis"},
	})
	require.NoError(t, err)

	var otherOrgProgress int
	err = db.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM migration_progress
		  WHERE org_id = $1
		    AND migration_type IN ('agent_import', 'history_backfill', 'memory_extraction')`,
		otherOrgID,
	).Scan(&otherOrgProgress)
	require.NoError(t, err)
	require.Equal(t, 3, otherOrgProgress)

	require.Equal(t, 1, countOpenClawResetScopedRows(t, db, "chat_messages", otherOrgID))
	require.Equal(t, 1, countOpenClawResetScopedRows(t, db, "memories", otherOrgID))
	require.Equal(t, 1, countOpenClawResetScopedRows(t, db, "ellie_project_docs", otherOrgID))
}

func seedOpenClawResetProgress(t *testing.T, db *sql.DB, orgID string) {
	t.Helper()
	_, err := db.ExecContext(
		context.Background(),
		`INSERT INTO migration_progress (org_id, migration_type, status)
		 VALUES
		 ($1, 'agent_import', 'completed'),
		 ($1, 'history_backfill', 'running'),
		 ($1, 'memory_extraction', 'failed'),
		 ($1, 'legacy_backfill', 'running')`,
		orgID,
	)
	require.NoError(t, err)
}

func seedOpenClawResetArtifacts(t *testing.T, db *sql.DB, orgID, seed string) {
	t.Helper()

	userID := insertOpenClawResetTestUser(t, db, orgID, seed)

	var projectID string
	err := db.QueryRowContext(
		context.Background(),
		`INSERT INTO projects (org_id, name, status)
		 VALUES ($1, $2, 'active')
		 RETURNING id`,
		orgID,
		fmt.Sprintf("OpenClaw Reset Project %s", seed),
	).Scan(&projectID)
	require.NoError(t, err)

	var roomID string
	err = db.QueryRowContext(
		context.Background(),
		`INSERT INTO rooms (org_id, name, type)
		 VALUES ($1, $2, 'ad_hoc')
		 RETURNING id`,
		orgID,
		fmt.Sprintf("OpenClaw Reset Room %s", seed),
	).Scan(&roomID)
	require.NoError(t, err)

	_, err = db.ExecContext(
		context.Background(),
		`INSERT INTO room_participants (org_id, room_id, participant_id, participant_type)
		 VALUES ($1, $2, $3, 'user')`,
		orgID,
		roomID,
		userID,
	)
	require.NoError(t, err)

	var conversationID string
	err = db.QueryRowContext(
		context.Background(),
		`INSERT INTO conversations (org_id, room_id, topic, started_at)
		 VALUES ($1, $2, $3, NOW())
		 RETURNING id`,
		orgID,
		roomID,
		fmt.Sprintf("OpenClaw Conversation %s", seed),
	).Scan(&conversationID)
	require.NoError(t, err)

	_, err = db.ExecContext(
		context.Background(),
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, conversation_id)
		 VALUES ($1, $2, $3, 'user', $4, $5)`,
		orgID,
		roomID,
		userID,
		fmt.Sprintf("OpenClaw Message %s", seed),
		conversationID,
	)
	require.NoError(t, err)

	var memoryID string
	err = db.QueryRowContext(
		context.Background(),
		`INSERT INTO memories (org_id, kind, title, content, status)
		 VALUES ($1, 'fact', $2, $3, 'active')
		 RETURNING id`,
		orgID,
		fmt.Sprintf("OpenClaw Memory %s", seed),
		fmt.Sprintf("OpenClaw Memory Content %s", seed),
	).Scan(&memoryID)
	require.NoError(t, err)

	var nodeID string
	err = db.QueryRowContext(
		context.Background(),
		`INSERT INTO ellie_taxonomy_nodes (org_id, parent_id, slug, display_name)
		 VALUES ($1, NULL, $2, $3)
		 RETURNING id`,
		orgID,
		fmt.Sprintf("openclaw-%s", seed),
		fmt.Sprintf("OpenClaw %s", seed),
	).Scan(&nodeID)
	require.NoError(t, err)

	_, err = db.ExecContext(
		context.Background(),
		`INSERT INTO ellie_memory_taxonomy (memory_id, node_id, confidence)
		 VALUES ($1, $2, 0.9)`,
		memoryID,
		nodeID,
	)
	require.NoError(t, err)

	_, err = db.ExecContext(
		context.Background(),
		`INSERT INTO ellie_project_docs (org_id, project_id, file_path, content_hash, is_active)
		 VALUES ($1, $2, $3, $4, true)`,
		orgID,
		projectID,
		fmt.Sprintf("/docs/%s.md", seed),
		fmt.Sprintf("hash-%s", seed),
	)
	require.NoError(t, err)
}

func insertOpenClawResetTestUser(t *testing.T, db *sql.DB, orgID, seed string) string {
	t.Helper()
	var userID string
	err := db.QueryRowContext(
		context.Background(),
		`INSERT INTO users (org_id, subject, issuer, display_name)
		 VALUES ($1, $2, 'otter.test', $3)
		 RETURNING id`,
		orgID,
		fmt.Sprintf("openclaw-reset-user-%s", seed),
		fmt.Sprintf("OpenClaw Reset User %s", seed),
	).Scan(&userID)
	require.NoError(t, err)
	return userID
}

func countOpenClawResetScopedRows(t *testing.T, db *sql.DB, tableName, orgID string) int {
	t.Helper()

	var query string
	switch tableName {
	case "ellie_memory_taxonomy":
		query = `SELECT COUNT(*)
		           FROM ellie_memory_taxonomy emt
		           JOIN memories m ON m.id = emt.memory_id
		          WHERE m.org_id = $1`
	default:
		query = fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE org_id = $1", tableName)
	}

	var count int
	err := db.QueryRowContext(context.Background(), query, orgID).Scan(&count)
	require.NoError(t, err)
	return count
}
