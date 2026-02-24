package testutil

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func PublishEvent(t testing.TB, db *pgxpool.Pool, orgID uuid.UUID, eventType string, payload any) int64 {
	t.Helper()

	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal event payload: %v", err)
	}

	var seq int64
	if err := db.QueryRow(context.Background(), `
		INSERT INTO domain_event (organization_id, event_type, actor_type, payload)
		VALUES ($1, $2, 'system', $3::jsonb)
		RETURNING seq
	`, orgID, eventType, encoded).Scan(&seq); err != nil {
		t.Fatalf("insert domain_event: %v", err)
	}
	return seq
}

func AdvanceCursor(t testing.TB, db *pgxpool.Pool, consumerName string, seq int64) {
	t.Helper()

	if _, err := db.Exec(context.Background(), `
		INSERT INTO consumer_cursor (consumer_name, organization_id, last_seq)
		VALUES ($1, NULL, $2)
		ON CONFLICT (consumer_name) WHERE organization_id IS NULL
		DO UPDATE SET last_seq = EXCLUDED.last_seq
	`, consumerName, seq); err != nil {
		t.Fatalf("advance consumer cursor: %v", err)
	}
}

func EnqueueJob(t testing.TB, db *pgxpool.Pool, jobType string, priority int, payload any) uuid.UUID {
	t.Helper()

	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal job payload: %v", err)
	}

	var id uuid.UUID
	if err := db.QueryRow(context.Background(), `
		INSERT INTO job_queue (job_type, priority, payload)
		VALUES ($1, $2, $3::jsonb)
		RETURNING id
	`, jobType, priority, encoded).Scan(&id); err != nil {
		t.Fatalf("insert job_queue: %v", err)
	}
	return id
}
