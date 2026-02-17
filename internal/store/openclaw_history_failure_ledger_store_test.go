package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenClawHistoryFailureLedgerUpsertAndListByOrg(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "openclaw-history-failure-ledger-org-a")
	orgB := createTestOrganization(t, db, "openclaw-history-failure-ledger-org-b")

	ledger := NewOpenClawHistoryFailureLedgerStore(db)

	first, err := ledger.Upsert(context.Background(), UpsertOpenClawHistoryFailureInput{
		OrgID:              orgA,
		MigrationType:      "history_backfill",
		BatchID:            "batch-1",
		AgentSlug:          "main",
		SessionID:          "session-1",
		EventID:            "event-1",
		SessionPath:        "/tmp/main/sessions/a.jsonl",
		Line:               10,
		MessageIDCandidate: "00000000-0000-0000-0000-000000000777",
		ErrorReason:        "insert_chat_message",
		ErrorMessage:       "pq: invalid byte sequence",
	})
	require.NoError(t, err)
	require.Equal(t, 1, first.AttemptCount)
	require.Equal(t, "batch-1", first.BatchID)
	require.Equal(t, "insert_chat_message", first.ErrorReason)

	// Ensure timestamps differ so last_seen_at ordering can be asserted.
	time.Sleep(10 * time.Millisecond)

	second, err := ledger.Upsert(context.Background(), UpsertOpenClawHistoryFailureInput{
		OrgID:              orgA,
		MigrationType:      "history_backfill",
		BatchID:            "batch-2",
		AgentSlug:          "main",
		SessionID:          "session-1",
		EventID:            "event-1",
		SessionPath:        "/tmp/main/sessions/a.jsonl",
		Line:               10,
		MessageIDCandidate: "00000000-0000-0000-0000-000000000777",
		ErrorReason:        "insert_chat_message",
		ErrorMessage:       "pq: invalid byte sequence for encoding",
	})
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, 2, second.AttemptCount)
	require.Equal(t, "batch-2", second.BatchID)
	require.True(t, second.LastSeenAt.After(first.LastSeenAt) || second.LastSeenAt.Equal(first.LastSeenAt))
	require.True(t, second.FirstSeenAt.Equal(first.FirstSeenAt))

	_, err = ledger.Upsert(context.Background(), UpsertOpenClawHistoryFailureInput{
		OrgID:              orgA,
		MigrationType:      "history_backfill",
		BatchID:            "batch-2",
		AgentSlug:          "lori",
		SessionID:          "session-2",
		EventID:            "event-2",
		SessionPath:        "/tmp/lori/sessions/b.jsonl",
		Line:               22,
		MessageIDCandidate: "00000000-0000-0000-0000-000000000888",
		ErrorReason:        "insert_chat_message",
		ErrorMessage:       "pq: value too long",
	})
	require.NoError(t, err)

	_, err = ledger.Upsert(context.Background(), UpsertOpenClawHistoryFailureInput{
		OrgID:              orgB,
		MigrationType:      "history_backfill",
		BatchID:            "batch-9",
		AgentSlug:          "other",
		SessionID:          "session-9",
		EventID:            "event-9",
		SessionPath:        "/tmp/other/sessions/c.jsonl",
		Line:               99,
		MessageIDCandidate: "00000000-0000-0000-0000-000000000999",
		ErrorReason:        "insert_chat_message",
		ErrorMessage:       "pq: invalid input",
	})
	require.NoError(t, err)

	rows, err := ledger.ListByOrg(context.Background(), orgA, ListOpenClawHistoryFailureOptions{Limit: 10})
	require.NoError(t, err)
	require.Len(t, rows, 2)

	require.Equal(t, orgA, rows[0].OrgID)
	require.Equal(t, orgA, rows[1].OrgID)
	require.Equal(t, "main", rows[0].AgentSlug)
	require.Equal(t, 2, rows[0].AttemptCount)
	require.Equal(t, "lori", rows[1].AgentSlug)
	require.Equal(t, 1, rows[1].AttemptCount)
}
