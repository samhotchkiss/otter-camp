package memory

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func seedEllieContextInjectionFixture(
	t *testing.T,
	db *sql.DB,
	label string,
) (orgID, projectID, roomID, agentID, memoryID string) {
	t.Helper()

	err := db.QueryRow(
		`INSERT INTO organizations (name, slug, tier) VALUES ($1, $2, 'free') RETURNING id`,
		fmt.Sprintf("%s org", label),
		fmt.Sprintf("%s-org", label),
	).Scan(&orgID)
	require.NoError(t, err)

	err = db.QueryRow(
		`INSERT INTO projects (org_id, name, status) VALUES ($1, $2, 'active') RETURNING id`,
		orgID,
		fmt.Sprintf("%s project", label),
	).Scan(&projectID)
	require.NoError(t, err)

	err = db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status) VALUES ($1, $2, $3, 'active') RETURNING id`,
		orgID,
		fmt.Sprintf("%s-agent", label),
		fmt.Sprintf("%s Agent", label),
	).Scan(&agentID)
	require.NoError(t, err)

	err = db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id) VALUES ($1, $2, 'project', $3) RETURNING id`,
		orgID,
		fmt.Sprintf("%s room", label),
		projectID,
	).Scan(&roomID)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO room_participants (org_id, room_id, participant_id, participant_type)
		 VALUES ($1, $2, $3, 'agent')`,
		orgID,
		roomID,
		agentID,
	)
	require.NoError(t, err)

	err = db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, status, importance, confidence)
		 VALUES ($1, 'technical_decision', 'Database policy', 'Use Postgres with explicit migrations', 'active', 5, 0.9)
		 RETURNING id`,
		orgID,
	).Scan(&memoryID)
	require.NoError(t, err)

	embeddingStore := store.NewConversationEmbeddingStore(db)
	err = embeddingStore.UpdateMemoryEmbedding(context.Background(), memoryID, testEllieContextEmbeddingVector(0.01))
	require.NoError(t, err)

	return orgID, projectID, roomID, agentID, memoryID
}

func insertContextInjectionUserMessage(t *testing.T, db *sql.DB, orgID, roomID, senderID, body string, createdAt time.Time) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, type, created_at, attachments)
		 VALUES ($1, $2, $3, 'user', $4, 'message', $5, '[]'::jsonb)`,
		orgID,
		roomID,
		senderID,
		body,
		createdAt,
	)
	require.NoError(t, err)
}

func newEllieContextInjectionWorkerForIntegration(db *sql.DB, cooldown int) *EllieContextInjectionWorker {
	queue := store.NewEllieContextInjectionStore(db)
	service := NewEllieProactiveInjectionService(EllieProactiveInjectionConfig{Threshold: 0.40, MaxItems: 3})
	worker := NewEllieContextInjectionWorker(queue, &fakeEllieContextInjectionEmbedder{}, service, EllieContextInjectionWorkerConfig{
		BatchSize:         20,
		PollInterval:      time.Second,
		Threshold:         0.40,
		MaxMemoriesPerMsg: 3,
		CooldownMessages:  cooldown,
	})
	worker.Logf = nil
	return worker
}

func TestEllieContextInjectionReinjectsAfterCompaction(t *testing.T) {
	db := setupEmbeddingWorkerTestDatabase(t)

	orgID, _, roomID, agentID, _ := seedEllieContextInjectionFixture(t, db, "reinject")
	worker := newEllieContextInjectionWorkerForIntegration(db, 1)

	base := time.Date(2026, 2, 12, 15, 40, 0, 0, time.UTC)
	insertContextInjectionUserMessage(t, db, orgID, roomID, agentID, "Should we add a database?", base)

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)

	var firstInjectionAt time.Time
	err = db.QueryRow(
		`SELECT created_at FROM chat_messages
		 WHERE org_id = $1 AND room_id = $2 AND type = 'context_injection'
		 ORDER BY created_at DESC LIMIT 1`,
		orgID,
		roomID,
	).Scan(&firstInjectionAt)
	require.NoError(t, err)

	_, err = db.Exec(`UPDATE rooms SET last_compacted_at = $2 WHERE id = $1`, roomID, firstInjectionAt.Add(1*time.Second))
	require.NoError(t, err)

	insertContextInjectionUserMessage(t, db, orgID, roomID, agentID, "Need DB call updated", base.Add(2*time.Minute))
	insertContextInjectionUserMessage(t, db, orgID, roomID, agentID, "Reminder about database", base.Add(3*time.Minute))

	processed, err = worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)

	var injectionCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM chat_messages
		 WHERE org_id = $1 AND room_id = $2 AND type = 'context_injection'`,
		orgID,
		roomID,
	).Scan(&injectionCount)
	require.NoError(t, err)
	require.Equal(t, 2, injectionCount)
}

func TestEllieContextInjectionCooldownSuppressesRepeat(t *testing.T) {
	db := setupEmbeddingWorkerTestDatabase(t)

	orgID, _, roomID, agentID, _ := seedEllieContextInjectionFixture(t, db, "cooldown")
	worker := newEllieContextInjectionWorkerForIntegration(db, 2)

	base := time.Date(2026, 2, 12, 15, 50, 0, 0, time.UTC)
	insertContextInjectionUserMessage(t, db, orgID, roomID, agentID, "Need DB guidance", base)

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)

	insertContextInjectionUserMessage(t, db, orgID, roomID, agentID, "Another DB mention", base.Add(1*time.Minute))

	processed, err = worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, processed)

	var injectionCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM chat_messages
		 WHERE org_id = $1 AND room_id = $2 AND type = 'context_injection'`,
		orgID,
		roomID,
	).Scan(&injectionCount)
	require.NoError(t, err)
	require.Equal(t, 1, injectionCount)
}

func TestEllieContextInjectionWorkerHandlesMultipleOrgs(t *testing.T) {
	db := setupEmbeddingWorkerTestDatabase(t)

	orgA, _, roomA, agentA, _ := seedEllieContextInjectionFixture(t, db, "multi-a")
	orgB, _, roomB, agentB, _ := seedEllieContextInjectionFixture(t, db, "multi-b")
	worker := newEllieContextInjectionWorkerForIntegration(db, 1)

	base := time.Date(2026, 2, 12, 16, 0, 0, 0, time.UTC)
	insertContextInjectionUserMessage(t, db, orgA, roomA, agentA, "Org A asks about DB", base)
	insertContextInjectionUserMessage(t, db, orgB, roomB, agentB, "Org B asks about DB", base.Add(1*time.Minute))

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, processed)

	var orgAInjections int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM chat_messages
		 WHERE org_id = $1 AND room_id = $2 AND type = 'context_injection'`,
		orgA,
		roomA,
	).Scan(&orgAInjections)
	require.NoError(t, err)
	require.Equal(t, 1, orgAInjections)

	var orgBInjections int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM chat_messages
		 WHERE org_id = $1 AND room_id = $2 AND type = 'context_injection'`,
		orgB,
		roomB,
	).Scan(&orgBInjections)
	require.NoError(t, err)
	require.Equal(t, 1, orgBInjections)
}

func TestEllieContextInjectionIncludesSupersessionNote(t *testing.T) {
	db := setupEmbeddingWorkerTestDatabase(t)

	orgID, _, roomID, agentID, oldMemoryID := seedEllieContextInjectionFixture(t, db, "supersession")

	var replacementMemoryID string
	err := db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, status, importance, confidence)
		 VALUES ($1, 'technical_decision', 'Database policy updated', 'Use MySQL with explicit migrations', 'active', 5, 0.9)
		 RETURNING id`,
		orgID,
	).Scan(&replacementMemoryID)
	require.NoError(t, err)

	embeddingStore := store.NewConversationEmbeddingStore(db)
	err = embeddingStore.UpdateMemoryEmbedding(context.Background(), replacementMemoryID, testEllieContextEmbeddingVector(0.01))
	require.NoError(t, err)

	_, err = db.Exec(
		`UPDATE memories
		 SET status = 'deprecated',
		     superseded_by = $2
		 WHERE id = $1`,
		oldMemoryID,
		replacementMemoryID,
	)
	require.NoError(t, err)

	worker := newEllieContextInjectionWorkerForIntegration(db, 1)

	base := time.Date(2026, 2, 12, 16, 10, 0, 0, time.UTC)
	insertContextInjectionUserMessage(t, db, orgID, roomID, agentID, "What is our current database policy?", base)

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)

	var injectedBody string
	err = db.QueryRow(
		`SELECT body FROM chat_messages
		 WHERE org_id = $1
		   AND room_id = $2
		   AND type = 'context_injection'
		 ORDER BY created_at DESC, id DESC
		 LIMIT 1`,
		orgID,
		roomID,
	).Scan(&injectedBody)
	require.NoError(t, err)
	require.Contains(t, injectedBody, "Updated context: previous decision")
	require.Contains(t, injectedBody, oldMemoryID)
}
