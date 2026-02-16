package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type RecordEllieDedupReviewedPairInput struct {
	OrgID     string
	MemoryID1 string
	MemoryID2 string
	Decision  string
	Metadata  json.RawMessage
}

type UpsertEllieDedupCursorInput struct {
	OrgID             string
	LastClusterKey    *string
	ProcessedClusters int
	TotalClusters     int
}

type EllieDedupCursor struct {
	OrgID             string
	LastClusterKey    *string
	ProcessedClusters int
	TotalClusters     int
	UpdatedAt         time.Time
}

type EllieDedupStore struct {
	db *sql.DB
}

func NewEllieDedupStore(db *sql.DB) *EllieDedupStore {
	return &EllieDedupStore{db: db}
}

func (s *EllieDedupStore) RecordReviewedPair(ctx context.Context, input RecordEllieDedupReviewedPairInput) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("ellie dedup store is not configured")
	}

	orgID := strings.TrimSpace(input.OrgID)
	if !uuidRegex.MatchString(orgID) {
		return fmt.Errorf("invalid org_id")
	}
	memoryID1, memoryID2, err := canonicalizeEllieDedupPairIDs(input.MemoryID1, input.MemoryID2)
	if err != nil {
		return err
	}
	decision := strings.TrimSpace(input.Decision)
	if decision == "" {
		return fmt.Errorf("decision is required")
	}
	metadata := normalizeJSONMap(input.Metadata)

	_, err = s.db.ExecContext(
		ctx,
		`INSERT INTO ellie_dedup_reviewed (
			org_id,
			memory_id_a,
			memory_id_b,
			decision,
			metadata,
			reviewed_at
		) VALUES (
			$1, $2, $3, $4, $5::jsonb, NOW()
		)
		ON CONFLICT (org_id, memory_id_a, memory_id_b) DO UPDATE
		SET
			decision = EXCLUDED.decision,
			metadata = EXCLUDED.metadata,
			reviewed_at = NOW()`,
		orgID,
		memoryID1,
		memoryID2,
		decision,
		metadata,
	)
	if err != nil {
		return fmt.Errorf("failed to record reviewed dedup pair: %w", err)
	}
	return nil
}

func (s *EllieDedupStore) IsPairReviewed(ctx context.Context, orgID, memoryID1, memoryID2 string) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("ellie dedup store is not configured")
	}

	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return false, fmt.Errorf("invalid org_id")
	}
	canonicalA, canonicalB, err := canonicalizeEllieDedupPairIDs(memoryID1, memoryID2)
	if err != nil {
		return false, err
	}

	var exists bool
	if err := s.db.QueryRowContext(
		ctx,
		`SELECT EXISTS(
			SELECT 1
			FROM ellie_dedup_reviewed
			WHERE org_id = $1
			  AND memory_id_a = $2
			  AND memory_id_b = $3
		)`,
		orgID,
		canonicalA,
		canonicalB,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("failed to check reviewed dedup pair: %w", err)
	}
	return exists, nil
}

func (s *EllieDedupStore) UpsertCursor(ctx context.Context, input UpsertEllieDedupCursorInput) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("ellie dedup store is not configured")
	}

	orgID := strings.TrimSpace(input.OrgID)
	if !uuidRegex.MatchString(orgID) {
		return fmt.Errorf("invalid org_id")
	}
	if input.ProcessedClusters < 0 {
		return fmt.Errorf("processed_clusters must be >= 0")
	}
	if input.TotalClusters < 0 {
		return fmt.Errorf("total_clusters must be >= 0")
	}

	var lastClusterKey any
	if input.LastClusterKey != nil {
		trimmed := strings.TrimSpace(*input.LastClusterKey)
		if trimmed != "" {
			lastClusterKey = trimmed
		}
	}

	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO ellie_dedup_cursors (
			org_id,
			last_cluster_key,
			processed_clusters,
			total_clusters
		) VALUES (
			$1, $2, $3, $4
		)
		ON CONFLICT (org_id) DO UPDATE
		SET
			last_cluster_key = EXCLUDED.last_cluster_key,
			processed_clusters = EXCLUDED.processed_clusters,
			total_clusters = EXCLUDED.total_clusters,
			updated_at = NOW()`,
		orgID,
		lastClusterKey,
		input.ProcessedClusters,
		input.TotalClusters,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert ellie dedup cursor: %w", err)
	}
	return nil
}

func (s *EllieDedupStore) GetCursor(ctx context.Context, orgID string) (*EllieDedupCursor, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie dedup store is not configured")
	}

	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}

	var (
		cursor         EllieDedupCursor
		lastClusterKey sql.NullString
	)
	err := s.db.QueryRowContext(
		ctx,
		`SELECT org_id, last_cluster_key, processed_clusters, total_clusters, updated_at
		 FROM ellie_dedup_cursors
		 WHERE org_id = $1`,
		orgID,
	).Scan(&cursor.OrgID, &lastClusterKey, &cursor.ProcessedClusters, &cursor.TotalClusters, &cursor.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to load ellie dedup cursor: %w", err)
	}
	if lastClusterKey.Valid {
		trimmed := strings.TrimSpace(lastClusterKey.String)
		if trimmed != "" {
			cursor.LastClusterKey = &trimmed
		}
	}
	return &cursor, nil
}

func canonicalizeEllieDedupPairIDs(memoryID1, memoryID2 string) (string, string, error) {
	memoryID1 = strings.TrimSpace(memoryID1)
	memoryID2 = strings.TrimSpace(memoryID2)
	if !uuidRegex.MatchString(memoryID1) {
		return "", "", fmt.Errorf("invalid memory_id_1")
	}
	if !uuidRegex.MatchString(memoryID2) {
		return "", "", fmt.Errorf("invalid memory_id_2")
	}
	if memoryID1 == memoryID2 {
		return "", "", fmt.Errorf("memory ids must be distinct")
	}
	if memoryID2 < memoryID1 {
		return memoryID2, memoryID1, nil
	}
	return memoryID1, memoryID2, nil
}
