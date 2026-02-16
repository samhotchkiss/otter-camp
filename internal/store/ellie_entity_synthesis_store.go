package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const ellieEntitySynthesisDefaultGrowthThreshold = 0.20

type EllieEntitySynthesisCandidate struct {
	EntityKey                 string
	EntityName                string
	MentionCount              int
	ExistingSynthesisMemoryID *string
	ExistingSourceMemoryCount int
	NeedsResynthesis          bool
}

type EllieEntitySynthesisStore struct {
	db *sql.DB
}

func NewEllieEntitySynthesisStore(db *sql.DB) *EllieEntitySynthesisStore {
	return &EllieEntitySynthesisStore{db: db}
}

func (s *EllieEntitySynthesisStore) ListCandidates(
	ctx context.Context,
	orgID string,
	minMentions int,
	limit int,
) ([]EllieEntitySynthesisCandidate, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie entity synthesis store is not configured")
	}

	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}

	if minMentions <= 0 {
		minMentions = 5
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 2000 {
		limit = 2000
	}

	rows, err := s.db.QueryContext(
		ctx,
		`WITH entity_mentions AS (
			SELECT
				m.org_id,
				LOWER(match[1]) AS entity_key,
				match[1] AS entity_name,
				m.id AS memory_id
			FROM memories m
			CROSS JOIN LATERAL regexp_matches(
				m.title || E'\n' || m.content,
				'([A-Z][A-Za-z0-9][A-Za-z0-9_-]{2,})',
				'g'
			) AS match
			WHERE m.org_id = $1
			  AND m.status = 'active'
			  AND COALESCE(m.metadata->>'source_type', '') <> 'synthesis'
		), entity_counts AS (
			SELECT
				org_id,
				entity_key,
				MIN(entity_name) AS entity_name,
				COUNT(DISTINCT memory_id)::int AS mention_count
			FROM entity_mentions
			GROUP BY org_id, entity_key
			HAVING COUNT(DISTINCT memory_id) >= $2
		), latest_synthesis AS (
			SELECT DISTINCT ON (m.org_id, LOWER(m.metadata->>'entity_key'))
				m.org_id,
				LOWER(m.metadata->>'entity_key') AS entity_key,
				m.id,
				COALESCE(NULLIF(m.metadata->>'source_memory_count', '')::int, 0) AS source_memory_count
			FROM memories m
			WHERE m.org_id = $1
			  AND m.status = 'active'
			  AND m.metadata->>'source_type' = 'synthesis'
			  AND COALESCE(m.metadata->>'entity_key', '') <> ''
			ORDER BY m.org_id, LOWER(m.metadata->>'entity_key'), m.occurred_at DESC, m.id DESC
		), scored AS (
			SELECT
				c.entity_key,
				c.entity_name,
				c.mention_count,
				s.id::text AS existing_synthesis_memory_id,
				COALESCE(s.source_memory_count, 0) AS existing_source_memory_count,
				CASE
					WHEN s.id IS NULL THEN false
					WHEN COALESCE(s.source_memory_count, 0) <= 0 THEN true
					ELSE ((c.mention_count - s.source_memory_count)::double precision / s.source_memory_count::double precision) >= $4
				END AS needs_resynthesis
			FROM entity_counts c
			LEFT JOIN latest_synthesis s
			  ON s.org_id = c.org_id
			 AND s.entity_key = c.entity_key
		)
		SELECT
			entity_key,
			entity_name,
			mention_count,
			existing_synthesis_memory_id,
			existing_source_memory_count,
			needs_resynthesis
		FROM scored
		WHERE existing_synthesis_memory_id IS NULL
		   OR needs_resynthesis
		ORDER BY mention_count DESC, entity_key ASC
		LIMIT $3`,
		orgID,
		minMentions,
		limit,
		ellieEntitySynthesisDefaultGrowthThreshold,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list ellie entity synthesis candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]EllieEntitySynthesisCandidate, 0, limit)
	for rows.Next() {
		var (
			row               EllieEntitySynthesisCandidate
			existingSynthesis sql.NullString
		)
		if err := rows.Scan(
			&row.EntityKey,
			&row.EntityName,
			&row.MentionCount,
			&existingSynthesis,
			&row.ExistingSourceMemoryCount,
			&row.NeedsResynthesis,
		); err != nil {
			return nil, fmt.Errorf("failed to scan ellie entity synthesis candidate: %w", err)
		}
		if existingSynthesis.Valid {
			trimmed := strings.TrimSpace(existingSynthesis.String)
			if trimmed != "" {
				row.ExistingSynthesisMemoryID = &trimmed
			}
		}
		candidates = append(candidates, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading ellie entity synthesis candidates: %w", err)
	}

	return candidates, nil
}
