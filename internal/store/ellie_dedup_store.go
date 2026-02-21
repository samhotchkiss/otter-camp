package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
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

type EllieDedupCandidatePair struct {
	MemoryID1  string
	MemoryID2  string
	Similarity float64
}

type EllieDedupReviewMemoryRecord struct {
	MemoryID string
	Title    string
	Content  string
}

func (s *EllieDedupStore) ListCandidatePairs(
	ctx context.Context,
	orgID string,
	threshold float64,
	limit int,
) ([]EllieDedupCandidatePair, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie dedup store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}
	if limit <= 0 {
		limit = 2000
	}
	if limit > 20000 {
		limit = 20000
	}
	if threshold <= 0 {
		threshold = 0.88
	}
	if threshold > 1 {
		threshold = 1
	}

	embeddingCol := embeddingColumnForDimension(1536)

	// Best-effort candidate selection:
	// - For each memory, find a small nearest-neighbor set using pgvector ordering.
	// - De-dupe and keep the strongest similarity per pair.
	//
	// This is intentionally bounded; the LLM reviewer is the expensive step.
	rows, err := s.db.QueryContext(
		ctx,
		fmt.Sprintf(
			`WITH candidates AS (
				SELECT
					m1.id AS memory_id_1,
					m2.id AS memory_id_2,
					(1 - (m1.%s <=> m2.%s))::double precision AS similarity
				FROM memories m1
				JOIN LATERAL (
					SELECT id, %s
					  FROM memories
					 WHERE org_id = m1.org_id
					   AND status = 'active'
					   AND id <> m1.id
					   AND %s IS NOT NULL
					 ORDER BY m1.%s <=> %s
					 LIMIT 12
				) AS m2 ON TRUE
				WHERE m1.org_id = $1
				  AND m1.status = 'active'
				  AND m1.%s IS NOT NULL
			),
			deduped AS (
				SELECT
					LEAST(memory_id_1, memory_id_2) AS memory_id_a,
					GREATEST(memory_id_1, memory_id_2) AS memory_id_b,
					MAX(similarity) AS similarity
				FROM candidates
				WHERE similarity >= $2
				GROUP BY 1, 2
			)
			SELECT memory_id_a::text, memory_id_b::text, similarity
			FROM deduped
			ORDER BY similarity DESC, memory_id_a ASC, memory_id_b ASC
			LIMIT $3`,
			embeddingCol,
			embeddingCol,
			embeddingCol,
			embeddingCol,
			embeddingCol,
			embeddingCol,
			embeddingCol,
		),
		orgID,
		threshold,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list dedup candidate pairs: %w", err)
	}
	defer rows.Close()

	pairs := make([]EllieDedupCandidatePair, 0, limit)
	for rows.Next() {
		var row EllieDedupCandidatePair
		if err := rows.Scan(&row.MemoryID1, &row.MemoryID2, &row.Similarity); err != nil {
			return nil, fmt.Errorf("failed to scan dedup candidate pair: %w", err)
		}
		pairs = append(pairs, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading dedup candidate pairs: %w", err)
	}
	return pairs, nil
}

func (s *EllieDedupStore) ListMemoriesByIDs(
	ctx context.Context,
	orgID string,
	memoryIDs []string,
) ([]EllieDedupReviewMemoryRecord, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie dedup store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}
	if len(memoryIDs) == 0 {
		return []EllieDedupReviewMemoryRecord{}, nil
	}

	normalized := make([]string, 0, len(memoryIDs))
	seen := map[string]struct{}{}
	for _, id := range memoryIDs {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	if len(normalized) == 0 {
		return []EllieDedupReviewMemoryRecord{}, nil
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id::text, title, content
		 FROM memories
		 WHERE org_id = $1
		   AND id = ANY($2::uuid[])
		   AND status = 'active'
		 ORDER BY occurred_at ASC, id ASC`,
		orgID,
		pq.Array(normalized),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list dedup memories: %w", err)
	}
	defer rows.Close()

	memories := make([]EllieDedupReviewMemoryRecord, 0, len(normalized))
	for rows.Next() {
		var row EllieDedupReviewMemoryRecord
		if err := rows.Scan(&row.MemoryID, &row.Title, &row.Content); err != nil {
			return nil, fmt.Errorf("failed to scan dedup memory: %w", err)
		}
		memories = append(memories, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading dedup memories: %w", err)
	}
	return memories, nil
}

func (s *EllieDedupStore) DeprecateMemories(
	ctx context.Context,
	orgID string,
	memoryIDs []string,
	supersededBy *string,
) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("ellie dedup store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return fmt.Errorf("invalid org_id")
	}
	if len(memoryIDs) == 0 {
		return nil
	}

	normalized := make([]string, 0, len(memoryIDs))
	seen := map[string]struct{}{}
	for _, id := range memoryIDs {
		trimmed := strings.TrimSpace(id)
		if !uuidRegex.MatchString(trimmed) {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	if len(normalized) == 0 {
		return nil
	}

	var superseded any
	if supersededBy != nil {
		trimmed := strings.TrimSpace(*supersededBy)
		if uuidRegex.MatchString(trimmed) {
			superseded = trimmed
		}
	}

	_, err := s.db.ExecContext(
		ctx,
		`UPDATE memories
		 SET status = 'deprecated',
		     superseded_by = $3
		 WHERE org_id = $1
		   AND id = ANY($2::uuid[])
		   AND status = 'active'`,
		orgID,
		pq.Array(normalized),
		superseded,
	)
	if err != nil {
		return fmt.Errorf("failed to deprecate memories: %w", err)
	}
	return nil
}

func (s *EllieDedupStore) CreateMergedMemory(
	ctx context.Context,
	orgID string,
	title string,
	content string,
	sourceMemoryIDs []string,
) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("ellie dedup store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return "", fmt.Errorf("invalid org_id")
	}
	title = strings.TrimSpace(title)
	content = strings.TrimSpace(content)
	if title == "" || content == "" {
		return "", fmt.Errorf("title and content are required")
	}

	sourceIDs := make([]string, 0, len(sourceMemoryIDs))
	for _, id := range sourceMemoryIDs {
		trimmed := strings.TrimSpace(id)
		if uuidRegex.MatchString(trimmed) {
			sourceIDs = append(sourceIDs, trimmed)
		}
	}

	metadata, _ := json.Marshal(map[string]any{
		"source_type":       "dedup_merge",
		"source_memory_ids": sourceIDs,
		"source_count":      len(sourceIDs),
	})

	var memoryID string
	if err := s.db.QueryRowContext(
		ctx,
		`INSERT INTO memories (
			org_id,
			kind,
			title,
			content,
			metadata,
			importance,
			confidence,
			status,
			occurred_at
		) VALUES (
			$1,
			'fact',
			$2,
			$3,
			$4::jsonb,
			5,
			0.95,
			'active',
			NOW()
		)
		RETURNING id::text`,
		orgID,
		title,
		content,
		normalizeJSONMap(metadata),
	).Scan(&memoryID); err != nil {
		return "", fmt.Errorf("failed to create merged memory: %w", err)
	}
	return strings.TrimSpace(memoryID), nil
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
