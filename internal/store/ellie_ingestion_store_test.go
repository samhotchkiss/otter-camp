package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEllieIngestionStoreDefaultsMemorySensitivityToNormal(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-ingestion-default-org")
	projectID := createTestProject(t, db, orgID, "Ellie Ingestion Default Project")

	var roomID string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Ellie Ingestion Default Room', 'project', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&roomID)
	require.NoError(t, err)

	var conversationID string
	err = db.QueryRow(
		`INSERT INTO conversations (org_id, room_id, topic, started_at, ended_at)
		 VALUES ($1, $2, 'Ellie Ingestion Default Conversation', $3, $4)
		 RETURNING id`,
		orgID,
		roomID,
		time.Date(2026, 2, 12, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 12, 10, 30, 0, 0, time.UTC),
	).Scan(&conversationID)
	require.NoError(t, err)

	store := NewEllieIngestionStore(db)
	memoryID, err := store.CreateEllieExtractedMemory(context.Background(), CreateEllieExtractedMemoryInput{
		OrgID:                orgID,
		Kind:                 "fact",
		Title:                "Default sensitivity memory",
		Content:              "Default sensitivity content",
		SourceConversationID: &conversationID,
		SourceProjectID:      &projectID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, memoryID)

	var sensitivity string
	err = db.QueryRow(`SELECT sensitivity FROM memories WHERE id = $1`, memoryID).Scan(&sensitivity)
	require.NoError(t, err)
	require.Equal(t, "normal", sensitivity)
}

func TestEllieIngestionStorePersistsSensitiveMemory(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-ingestion-sensitive-org")
	projectID := createTestProject(t, db, orgID, "Ellie Ingestion Sensitive Project")

	var roomID string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Ellie Ingestion Sensitive Room', 'project', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&roomID)
	require.NoError(t, err)

	var conversationID string
	err = db.QueryRow(
		`INSERT INTO conversations (org_id, room_id, topic, started_at, ended_at)
		 VALUES ($1, $2, 'Ellie Ingestion Sensitive Conversation', $3, $4)
		 RETURNING id`,
		orgID,
		roomID,
		time.Date(2026, 2, 12, 11, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 12, 11, 30, 0, 0, time.UTC),
	).Scan(&conversationID)
	require.NoError(t, err)

	store := NewEllieIngestionStore(db)
	memoryID, err := store.CreateEllieExtractedMemory(context.Background(), CreateEllieExtractedMemoryInput{
		OrgID:                orgID,
		Kind:                 "lesson",
		Title:                "Sensitive memory",
		Content:              "Sensitive content",
		Sensitivity:          "sensitive",
		SourceConversationID: &conversationID,
		SourceProjectID:      &projectID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, memoryID)

	var sensitivity string
	err = db.QueryRow(`SELECT sensitivity FROM memories WHERE id = $1`, memoryID).Scan(&sensitivity)
	require.NoError(t, err)
	require.Equal(t, "sensitive", sensitivity)
}

func TestEllieIngestionStoreRejectsInvalidMemorySensitivity(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-ingestion-invalid-sensitivity-org")
	projectID := createTestProject(t, db, orgID, "Ellie Ingestion Invalid Sensitivity Project")

	var roomID string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Ellie Ingestion Invalid Sensitivity Room', 'project', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&roomID)
	require.NoError(t, err)

	var conversationID string
	err = db.QueryRow(
		`INSERT INTO conversations (org_id, room_id, topic, started_at, ended_at)
		 VALUES ($1, $2, 'Ellie Ingestion Invalid Sensitivity Conversation', $3, $4)
		 RETURNING id`,
		orgID,
		roomID,
		time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 12, 12, 30, 0, 0, time.UTC),
	).Scan(&conversationID)
	require.NoError(t, err)

	store := NewEllieIngestionStore(db)
	_, err = store.CreateEllieExtractedMemory(context.Background(), CreateEllieExtractedMemoryInput{
		OrgID:                orgID,
		Kind:                 "fact",
		Title:                "Invalid sensitivity memory",
		Content:              "Invalid sensitivity content",
		Sensitivity:          "top-secret",
		SourceConversationID: &conversationID,
		SourceProjectID:      &projectID,
	})
	require.ErrorContains(t, err, "invalid sensitivity")
}

func TestEllieIngestionStoreHasComplianceFingerprint(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-ingestion-fingerprint-org")
	projectID := createTestProject(t, db, orgID, "Ellie Ingestion Fingerprint Project")
	store := NewEllieIngestionStore(db)

	metadata := json.RawMessage(`{"source":"compliance_review","compliance_fingerprint":"fingerprint-123"}`)
	_, err := store.CreateEllieExtractedMemory(context.Background(), CreateEllieExtractedMemoryInput{
		OrgID:           orgID,
		Kind:            "lesson",
		Title:           "Compliance lesson",
		Content:         "Repeated finding",
		Metadata:        metadata,
		SourceProjectID: &projectID,
	})
	require.NoError(t, err)

	exists, err := store.HasComplianceFingerprint(context.Background(), orgID, "fingerprint-123")
	require.NoError(t, err)
	require.True(t, exists)

	exists, err = store.HasComplianceFingerprint(context.Background(), orgID, "fingerprint-999")
	require.NoError(t, err)
	require.False(t, exists)
}

func TestEllieIngestionStoreInsertExtractedMemoryNullsMissingForeignRefs(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-ingestion-missing-refs-org")
	projectID := createTestProject(t, db, orgID, "Ellie Ingestion Missing Refs Project")
	store := NewEllieIngestionStore(db)

	// Valid room so memory row has workspace context.
	var roomID string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Missing Refs Room', 'project', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&roomID)
	require.NoError(t, err)
	require.NotEmpty(t, roomID)

	// UUID-shaped IDs that do not exist in this org should not fail insertion.
	missingConversationID := "11111111-1111-1111-1111-111111111111"
	missingProjectID := "22222222-2222-2222-2222-222222222222"

	inserted, err := store.InsertExtractedMemory(context.Background(), CreateEllieExtractedMemoryInput{
		OrgID:                orgID,
		Kind:                 "fact",
		Title:                "Missing refs should not fail",
		Content:              "This should insert and null invalid foreign references.",
		SourceConversationID: &missingConversationID,
		SourceProjectID:      &missingProjectID,
	})
	require.NoError(t, err)
	require.True(t, inserted)

	var conversationID sql.NullString
	var storedProjectID sql.NullString
	err = db.QueryRow(
		`SELECT source_conversation_id::text, source_project_id::text
		   FROM memories
		  WHERE org_id = $1
		    AND title = 'Missing refs should not fail'
		  ORDER BY created_at DESC
		  LIMIT 1`,
		orgID,
	).Scan(&conversationID, &storedProjectID)
	require.NoError(t, err)
	require.False(t, conversationID.Valid)
	require.False(t, storedProjectID.Valid)
}
