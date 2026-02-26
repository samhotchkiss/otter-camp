package repo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Memory struct {
	ID                uuid.UUID
	OrganizationID    uuid.UUID
	ProjectID         *uuid.UUID
	ProjectTaskID     *uuid.UUID
	AgentID           *uuid.UUID
	MemoryType        string
	Scope             string
	Content           string
	ContentHash       string
	Embedding         []float32
	Confidence        float64
	UtilityScore      float64
	ExtractionScore   *int
	Status            string
	IsHardened        bool
	Sensitivity       string
	TrustTier         float64
	FileBacked        bool
	FilePath          *string
	FileLastScannedAt *time.Time
	SupersededBy      *uuid.UUID
	SupersededAt      *time.Time
	ArchivedAt        *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type NearDuplicateMatch struct {
	MemoryID         uuid.UUID
	CosineSimilarity float64
}

type RetrievalFilter struct {
	OrganizationID uuid.UUID
	Scopes         []string
	Statuses       []string
	Sensitivity    *string
	ProjectID      *uuid.UUID
	ProjectTaskID  *uuid.UUID
	AgentID        *uuid.UUID
	Limit          int
}

const CandidatePromotionHoldDuration = 24 * time.Hour

func CandidatePromotionCutoff(now time.Time) time.Time {
	return now.UTC().Add(-CandidatePromotionHoldDuration)
}

type MemoryRepo struct {
	pool *pgxpool.Pool
}

func NewMemoryRepo(pool *pgxpool.Pool) *MemoryRepo {
	return &MemoryRepo{pool: pool}
}

func (r *MemoryRepo) Create(ctx context.Context, memory Memory) (Memory, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO memory (
			organization_id,
			project_id,
			project_task_id,
			agent_id,
			memory_type,
			scope,
			content,
			content_hash,
			embedding,
			confidence,
			utility_score,
			extraction_score,
			status,
			is_hardened,
			sensitivity,
			trust_tier,
			file_backed,
			file_path,
			file_last_scanned_at,
			superseded_by,
			superseded_at,
			archived_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			CASE WHEN $9 = '' THEN NULL ELSE $9::vector END,
			$10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22
		)
		RETURNING id, organization_id, project_id, project_task_id, agent_id, memory_type, scope, content, content_hash,
		          embedding::text, confidence, utility_score, extraction_score, status, is_hardened, sensitivity, trust_tier,
		          file_backed, file_path, file_last_scanned_at, superseded_by, superseded_at, archived_at, created_at, updated_at
	`, memory.OrganizationID, memory.ProjectID, memory.ProjectTaskID, memory.AgentID, memory.MemoryType, memory.Scope, memory.Content, memory.ContentHash,
		embeddingToLiteral(memory.Embedding), normalizeScore(memory.Confidence, 0.5), normalizeScore(memory.UtilityScore, 0.5), memory.ExtractionScore,
		defaultMemoryStatus(memory.Status), memory.IsHardened, defaultSensitivity(memory.Sensitivity), normalizeScore(memory.TrustTier, 0.8),
		memory.FileBacked, memory.FilePath, memory.FileLastScannedAt, memory.SupersededBy, memory.SupersededAt, memory.ArchivedAt,
	)

	created, err := scanMemory(row)
	if err != nil {
		return Memory{}, mapDBError(err)
	}
	return created, nil
}

func (r *MemoryRepo) GetByID(ctx context.Context, id uuid.UUID) (Memory, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, project_id, project_task_id, agent_id, memory_type, scope, content, content_hash,
		       embedding::text, confidence, utility_score, extraction_score, status, is_hardened, sensitivity, trust_tier,
		       file_backed, file_path, file_last_scanned_at, superseded_by, superseded_at, archived_at, created_at, updated_at
		FROM memory
		WHERE id = $1
	`, id)

	memory, err := scanMemory(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Memory{}, ErrNotFound
	}
	if err != nil {
		return Memory{}, mapDBError(err)
	}
	return memory, nil
}

func (r *MemoryRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string) (Memory, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE memory
		SET status = $2
		WHERE id = $1
		RETURNING id, organization_id, project_id, project_task_id, agent_id, memory_type, scope, content, content_hash,
		          embedding::text, confidence, utility_score, extraction_score, status, is_hardened, sensitivity, trust_tier,
		          file_backed, file_path, file_last_scanned_at, superseded_by, superseded_at, archived_at, created_at, updated_at
	`, id, status)

	updated, err := scanMemory(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Memory{}, ErrNotFound
	}
	if err != nil {
		return Memory{}, mapDBError(err)
	}
	return updated, nil
}

func (r *MemoryRepo) UpdateEmbedding(ctx context.Context, id uuid.UUID, embedding []float32) error {
	command, err := r.pool.Exec(ctx, `
		UPDATE memory
		SET embedding = CASE WHEN $2 = '' THEN NULL ELSE $2::vector END
		WHERE id = $1
	`, id, embeddingToLiteral(embedding))
	if err != nil {
		return mapDBError(err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *MemoryRepo) UpdateConfidence(ctx context.Context, id uuid.UUID, confidence float64) (Memory, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE memory
		SET confidence = $2
		WHERE id = $1
		RETURNING id, organization_id, project_id, project_task_id, agent_id, memory_type, scope, content, content_hash,
		          embedding::text, confidence, utility_score, extraction_score, status, is_hardened, sensitivity, trust_tier,
		          file_backed, file_path, file_last_scanned_at, superseded_by, superseded_at, archived_at, created_at, updated_at
	`, id, normalizeScore(confidence, 0.5))

	updated, err := scanMemory(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Memory{}, ErrNotFound
	}
	if err != nil {
		return Memory{}, mapDBError(err)
	}
	return updated, nil
}

func (r *MemoryRepo) ListForRetrieval(ctx context.Context, filter RetrievalFilter) ([]Memory, error) {
	if filter.OrganizationID == uuid.Nil {
		return nil, fmt.Errorf("organization id is required")
	}

	statuses := filter.Statuses
	if len(statuses) == 0 {
		statuses = []string{"active"}
	}
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	builder := strings.Builder{}
	builder.WriteString(`
		SELECT id, organization_id, project_id, project_task_id, agent_id, memory_type, scope, content, content_hash,
		       embedding::text, confidence, utility_score, extraction_score, status, is_hardened, sensitivity, trust_tier,
		       file_backed, file_path, file_last_scanned_at, superseded_by, superseded_at, archived_at, created_at, updated_at
		FROM memory
		WHERE organization_id = $1
		  AND status = ANY($2::text[])
	`)

	args := []any{filter.OrganizationID, statuses}
	nextArg := 3
	if len(filter.Scopes) > 0 {
		builder.WriteString(fmt.Sprintf(" AND scope = ANY($%d::text[])", nextArg))
		args = append(args, filter.Scopes)
		nextArg++
	}
	if filter.Sensitivity != nil {
		builder.WriteString(fmt.Sprintf(" AND sensitivity = $%d", nextArg))
		args = append(args, strings.TrimSpace(*filter.Sensitivity))
		nextArg++
	}
	if filter.ProjectID != nil {
		builder.WriteString(fmt.Sprintf(" AND project_id = $%d", nextArg))
		args = append(args, *filter.ProjectID)
		nextArg++
	}
	if filter.ProjectTaskID != nil {
		builder.WriteString(fmt.Sprintf(" AND project_task_id = $%d", nextArg))
		args = append(args, *filter.ProjectTaskID)
		nextArg++
	}
	if filter.AgentID != nil {
		builder.WriteString(fmt.Sprintf(" AND agent_id = $%d", nextArg))
		args = append(args, *filter.AgentID)
		nextArg++
	}

	builder.WriteString(fmt.Sprintf(" ORDER BY utility_score DESC, confidence DESC, created_at DESC LIMIT $%d", nextArg))
	args = append(args, limit)

	return r.list(ctx, builder.String(), args...)
}

func (r *MemoryRepo) ListCandidates(ctx context.Context, orgID uuid.UUID, olderThan time.Time) ([]Memory, error) {
	return r.list(ctx, `
		SELECT id, organization_id, project_id, project_task_id, agent_id, memory_type, scope, content, content_hash,
		       embedding::text, confidence, utility_score, extraction_score, status, is_hardened, sensitivity, trust_tier,
		       file_backed, file_path, file_last_scanned_at, superseded_by, superseded_at, archived_at, created_at, updated_at
		FROM memory
		WHERE organization_id = $1
		  AND status = 'candidate'
		  AND created_at < $2
		ORDER BY created_at ASC
	`, orgID, olderThan.UTC())
}

func (r *MemoryRepo) GetByContentHash(ctx context.Context, orgID uuid.UUID, contentHash string) (Memory, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, project_id, project_task_id, agent_id, memory_type, scope, content, content_hash,
		       embedding::text, confidence, utility_score, extraction_score, status, is_hardened, sensitivity, trust_tier,
		       file_backed, file_path, file_last_scanned_at, superseded_by, superseded_at, archived_at, created_at, updated_at
		FROM memory
		WHERE organization_id = $1
		  AND content_hash = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, orgID, strings.TrimSpace(contentHash))

	memory, err := scanMemory(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Memory{}, ErrNotFound
	}
	if err != nil {
		return Memory{}, mapDBError(err)
	}
	return memory, nil
}

func (r *MemoryRepo) HasActiveByContentHash(ctx context.Context, orgID uuid.UUID, contentHash string) (bool, error) {
	var exists bool
	if err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM memory
			WHERE organization_id = $1
			  AND content_hash = $2
			  AND status = 'active'
		)
	`, orgID, strings.TrimSpace(contentHash)).Scan(&exists); err != nil {
		return false, mapDBError(err)
	}
	return exists, nil
}

func (r *MemoryRepo) FindNearDuplicates(ctx context.Context, orgID uuid.UUID, embedding []float32, minCosine float64, limit int) ([]NearDuplicateMatch, error) {
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("organization id is required")
	}
	if len(embedding) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}

	rows, err := r.pool.Query(ctx, `
		SELECT id, (1 - (embedding <=> $2::vector))::float8 AS cosine_similarity
		FROM memory
		WHERE organization_id = $1
		  AND embedding IS NOT NULL
		  AND status IN ('candidate', 'active')
		  AND (1 - (embedding <=> $2::vector)) >= $3
		ORDER BY embedding <=> $2::vector ASC
		LIMIT $4
	`, orgID, embeddingToLiteral(embedding), minCosine, limit)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]NearDuplicateMatch, 0)
	for rows.Next() {
		var item NearDuplicateMatch
		if scanErr := rows.Scan(&item.MemoryID, &item.CosineSimilarity); scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *MemoryRepo) ListFileBacked(ctx context.Context, orgID uuid.UUID) ([]Memory, error) {
	return r.list(ctx, `
		SELECT id, organization_id, project_id, project_task_id, agent_id, memory_type, scope, content, content_hash,
		       embedding::text, confidence, utility_score, extraction_score, status, is_hardened, sensitivity, trust_tier,
		       file_backed, file_path, file_last_scanned_at, superseded_by, superseded_at, archived_at, created_at, updated_at
		FROM memory
		WHERE organization_id = $1
		  AND file_backed = true
		  AND status IN ('candidate','active')
		ORDER BY COALESCE(file_last_scanned_at, to_timestamp(0)) ASC, created_at ASC
	`, orgID)
}

func (r *MemoryRepo) ListActiveUnhardened(ctx context.Context, orgID uuid.UUID, olderThan time.Time) ([]Memory, error) {
	return r.list(ctx, `
		SELECT id, organization_id, project_id, project_task_id, agent_id, memory_type, scope, content, content_hash,
		       embedding::text, confidence, utility_score, extraction_score, status, is_hardened, sensitivity, trust_tier,
		       file_backed, file_path, file_last_scanned_at, superseded_by, superseded_at, archived_at, created_at, updated_at
		FROM memory
		WHERE organization_id = $1
		  AND status = 'active'
		  AND is_hardened = false
		  AND created_at < $2
		ORDER BY created_at ASC
	`, orgID, olderThan.UTC())
}

func (r *MemoryRepo) UpdateHardened(ctx context.Context, id uuid.UUID, hardened bool) (Memory, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE memory
		SET is_hardened = $2
		WHERE id = $1
		RETURNING id, organization_id, project_id, project_task_id, agent_id, memory_type, scope, content, content_hash,
		          embedding::text, confidence, utility_score, extraction_score, status, is_hardened, sensitivity, trust_tier,
		          file_backed, file_path, file_last_scanned_at, superseded_by, superseded_at, archived_at, created_at, updated_at
	`, id, hardened)

	updated, err := scanMemory(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Memory{}, ErrNotFound
	}
	if err != nil {
		return Memory{}, mapDBError(err)
	}
	return updated, nil
}

func (r *MemoryRepo) Archive(ctx context.Context, id uuid.UUID) (Memory, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE memory
		SET status = 'archived',
		    archived_at = now()
		WHERE id = $1
		RETURNING id, organization_id, project_id, project_task_id, agent_id, memory_type, scope, content, content_hash,
		          embedding::text, confidence, utility_score, extraction_score, status, is_hardened, sensitivity, trust_tier,
		          file_backed, file_path, file_last_scanned_at, superseded_by, superseded_at, archived_at, created_at, updated_at
	`, id)

	updated, err := scanMemory(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Memory{}, ErrNotFound
	}
	if err != nil {
		return Memory{}, mapDBError(err)
	}
	return updated, nil
}

func (r *MemoryRepo) Supersede(ctx context.Context, id, supersededBy uuid.UUID) (Memory, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE memory
		SET status = 'superseded',
		    superseded_by = $2,
		    superseded_at = now()
		WHERE id = $1
		RETURNING id, organization_id, project_id, project_task_id, agent_id, memory_type, scope, content, content_hash,
		          embedding::text, confidence, utility_score, extraction_score, status, is_hardened, sensitivity, trust_tier,
		          file_backed, file_path, file_last_scanned_at, superseded_by, superseded_at, archived_at, created_at, updated_at
	`, id, supersededBy)

	updated, err := scanMemory(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Memory{}, ErrNotFound
	}
	if err != nil {
		return Memory{}, mapDBError(err)
	}
	return updated, nil
}

func (r *MemoryRepo) GetSources(ctx context.Context, memoryID uuid.UUID) ([]MemorySource, error) {
	return NewMemorySourceRepo(r.pool).ListByMemory(ctx, memoryID)
}

func (r *MemoryRepo) list(ctx context.Context, query string, args ...any) ([]Memory, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]Memory, 0)
	for rows.Next() {
		item, scanErr := scanMemory(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}

	return items, nil
}

func scanMemory(row pgx.Row) (Memory, error) {
	var (
		item          Memory
		embeddingText *string
	)
	if err := row.Scan(
		&item.ID,
		&item.OrganizationID,
		&item.ProjectID,
		&item.ProjectTaskID,
		&item.AgentID,
		&item.MemoryType,
		&item.Scope,
		&item.Content,
		&item.ContentHash,
		&embeddingText,
		&item.Confidence,
		&item.UtilityScore,
		&item.ExtractionScore,
		&item.Status,
		&item.IsHardened,
		&item.Sensitivity,
		&item.TrustTier,
		&item.FileBacked,
		&item.FilePath,
		&item.FileLastScannedAt,
		&item.SupersededBy,
		&item.SupersededAt,
		&item.ArchivedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return Memory{}, err
	}

	if embeddingText != nil && strings.TrimSpace(*embeddingText) != "" {
		vector, err := parseEmbedding(*embeddingText)
		if err != nil {
			return Memory{}, err
		}
		item.Embedding = vector
	}

	return item, nil
}

type MemoryTaxonomyTag struct {
	ID             uuid.UUID
	MemoryID       uuid.UUID
	TaxonomyNodeID uuid.UUID
	AssignedBy     string
	Confidence     float64
	CreatedAt      time.Time
}

type MemoryTaxonomyTagRepo struct {
	pool *pgxpool.Pool
}

func NewMemoryTaxonomyTagRepo(pool *pgxpool.Pool) *MemoryTaxonomyTagRepo {
	return &MemoryTaxonomyTagRepo{pool: pool}
}

func (r *MemoryTaxonomyTagRepo) Create(ctx context.Context, tag MemoryTaxonomyTag) (MemoryTaxonomyTag, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO memory_taxonomy_tag (
			memory_id,
			taxonomy_node_id,
			assigned_by,
			confidence
		)
		VALUES ($1, $2, $3, $4)
		RETURNING id, memory_id, taxonomy_node_id, assigned_by, confidence, created_at
	`, tag.MemoryID, tag.TaxonomyNodeID, tag.AssignedBy, normalizeScore(tag.Confidence, 1))

	created, err := scanMemoryTaxonomyTag(row)
	if err != nil {
		return MemoryTaxonomyTag{}, mapDBError(err)
	}
	return created, nil
}

func (r *MemoryTaxonomyTagRepo) ListByMemory(ctx context.Context, memoryID uuid.UUID) ([]MemoryTaxonomyTag, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, memory_id, taxonomy_node_id, assigned_by, confidence, created_at
		FROM memory_taxonomy_tag
		WHERE memory_id = $1
		ORDER BY created_at ASC
	`, memoryID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]MemoryTaxonomyTag, 0)
	for rows.Next() {
		item, scanErr := scanMemoryTaxonomyTag(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *MemoryTaxonomyTagRepo) DeleteByMemory(ctx context.Context, memoryID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM memory_taxonomy_tag WHERE memory_id = $1`, memoryID)
	return mapDBError(err)
}

func scanMemoryTaxonomyTag(row pgx.Row) (MemoryTaxonomyTag, error) {
	var tag MemoryTaxonomyTag
	if err := row.Scan(&tag.ID, &tag.MemoryID, &tag.TaxonomyNodeID, &tag.AssignedBy, &tag.Confidence, &tag.CreatedAt); err != nil {
		return MemoryTaxonomyTag{}, err
	}
	return tag, nil
}

type MemoryEntityMention struct {
	ID          uuid.UUID
	MemoryID    uuid.UUID
	EntityID    uuid.UUID
	MentionText string
	Confidence  float64
	CreatedAt   time.Time
}

type MemoryEntityMentionRepo struct {
	pool *pgxpool.Pool
}

func NewMemoryEntityMentionRepo(pool *pgxpool.Pool) *MemoryEntityMentionRepo {
	return &MemoryEntityMentionRepo{pool: pool}
}

func (r *MemoryEntityMentionRepo) Create(ctx context.Context, mention MemoryEntityMention) (MemoryEntityMention, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO memory_entity_mention (
			memory_id,
			entity_id,
			mention_text,
			confidence
		)
		VALUES ($1, $2, $3, $4)
		RETURNING id, memory_id, entity_id, mention_text, confidence, created_at
	`, mention.MemoryID, mention.EntityID, mention.MentionText, normalizeScore(mention.Confidence, 1))

	created, err := scanMemoryEntityMention(row)
	if err != nil {
		return MemoryEntityMention{}, mapDBError(err)
	}
	return created, nil
}

func (r *MemoryEntityMentionRepo) ListByMemory(ctx context.Context, memoryID uuid.UUID) ([]MemoryEntityMention, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, memory_id, entity_id, mention_text, confidence, created_at
		FROM memory_entity_mention
		WHERE memory_id = $1
		ORDER BY created_at ASC
	`, memoryID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]MemoryEntityMention, 0)
	for rows.Next() {
		item, scanErr := scanMemoryEntityMention(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func (r *MemoryEntityMentionRepo) ListByEntity(ctx context.Context, entityID uuid.UUID) ([]MemoryEntityMention, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, memory_id, entity_id, mention_text, confidence, created_at
		FROM memory_entity_mention
		WHERE entity_id = $1
		ORDER BY created_at DESC
	`, entityID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]MemoryEntityMention, 0)
	for rows.Next() {
		item, scanErr := scanMemoryEntityMention(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func scanMemoryEntityMention(row pgx.Row) (MemoryEntityMention, error) {
	var item MemoryEntityMention
	if err := row.Scan(&item.ID, &item.MemoryID, &item.EntityID, &item.MentionText, &item.Confidence, &item.CreatedAt); err != nil {
		return MemoryEntityMention{}, err
	}
	return item, nil
}

type MemoryImport struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	RequestedBy      *uuid.UUID
	Status           string
	FileKey          string
	TotalRecords     *int
	ProcessedRecords int
	ImportedRecords  int
	RejectedRecords  int
	ErrorMessage     *string
	StartedAt        *time.Time
	CompletedAt      *time.Time
	CreatedAt        time.Time
}

type MemoryImportRepo struct {
	pool *pgxpool.Pool
}

func NewMemoryImportRepo(pool *pgxpool.Pool) *MemoryImportRepo {
	return &MemoryImportRepo{pool: pool}
}

func (r *MemoryImportRepo) Create(ctx context.Context, item MemoryImport) (MemoryImport, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO memory_import (
			organization_id,
			requested_by,
			status,
			file_key,
			total_records,
			processed_records,
			imported_records,
			rejected_records,
			error_message,
			started_at,
			completed_at
		)
		VALUES ($1, $2, COALESCE(NULLIF($3, ''), 'pending'), $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, organization_id, requested_by, status, file_key, total_records, processed_records, imported_records, rejected_records, error_message, started_at, completed_at, created_at
	`, item.OrganizationID, item.RequestedBy, item.Status, item.FileKey, item.TotalRecords, item.ProcessedRecords, item.ImportedRecords, item.RejectedRecords, item.ErrorMessage, item.StartedAt, item.CompletedAt)

	created, err := scanMemoryImport(row)
	if err != nil {
		return MemoryImport{}, mapDBError(err)
	}
	return created, nil
}

func (r *MemoryImportRepo) GetByID(ctx context.Context, id uuid.UUID) (MemoryImport, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, requested_by, status, file_key, total_records, processed_records, imported_records, rejected_records, error_message, started_at, completed_at, created_at
		FROM memory_import
		WHERE id = $1
	`, id)

	item, err := scanMemoryImport(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MemoryImport{}, ErrNotFound
	}
	if err != nil {
		return MemoryImport{}, mapDBError(err)
	}
	return item, nil
}

func (r *MemoryImportRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string, errorMessage *string, startedAt, completedAt *time.Time) (MemoryImport, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE memory_import
		SET status = $2,
		    error_message = $3,
		    started_at = $4,
		    completed_at = $5
		WHERE id = $1
		RETURNING id, organization_id, requested_by, status, file_key, total_records, processed_records, imported_records, rejected_records, error_message, started_at, completed_at, created_at
	`, id, status, errorMessage, startedAt, completedAt)

	updated, err := scanMemoryImport(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MemoryImport{}, ErrNotFound
	}
	if err != nil {
		return MemoryImport{}, mapDBError(err)
	}
	return updated, nil
}

func (r *MemoryImportRepo) UpdateProgress(ctx context.Context, id uuid.UUID, totalRecords *int, processedRecords, importedRecords, rejectedRecords int) (MemoryImport, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE memory_import
		SET total_records = COALESCE($2, total_records),
		    processed_records = $3,
		    imported_records = $4,
		    rejected_records = $5
		WHERE id = $1
		RETURNING id, organization_id, requested_by, status, file_key, total_records, processed_records, imported_records, rejected_records, error_message, started_at, completed_at, created_at
	`, id, totalRecords, processedRecords, importedRecords, rejectedRecords)

	updated, err := scanMemoryImport(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MemoryImport{}, ErrNotFound
	}
	if err != nil {
		return MemoryImport{}, mapDBError(err)
	}
	return updated, nil
}

func scanMemoryImport(row pgx.Row) (MemoryImport, error) {
	var item MemoryImport
	if err := row.Scan(
		&item.ID,
		&item.OrganizationID,
		&item.RequestedBy,
		&item.Status,
		&item.FileKey,
		&item.TotalRecords,
		&item.ProcessedRecords,
		&item.ImportedRecords,
		&item.RejectedRecords,
		&item.ErrorMessage,
		&item.StartedAt,
		&item.CompletedAt,
		&item.CreatedAt,
	); err != nil {
		return MemoryImport{}, err
	}
	return item, nil
}

type MemoryCompactionRun struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	RunType          string
	ScopeContext     string
	Status           string
	MemoriesExamined int
	MemoriesUpdated  int
	MemoriesArchived int
	MemoriesCreated  int
	ErrorMessage     *string
	StartedAt        *time.Time
	CompletedAt      *time.Time
	CreatedAt        time.Time
}

type MemoryCompactionRunRepo struct {
	pool *pgxpool.Pool
}

func NewMemoryCompactionRunRepo(pool *pgxpool.Pool) *MemoryCompactionRunRepo {
	return &MemoryCompactionRunRepo{pool: pool}
}

func (r *MemoryCompactionRunRepo) Create(ctx context.Context, item MemoryCompactionRun) (MemoryCompactionRun, error) {
	scopeContext := strings.TrimSpace(item.ScopeContext)
	if scopeContext == "" {
		scopeContext = `{}`
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO memory_compaction_run (
			organization_id,
			run_type,
			scope_context,
			status,
			memories_examined,
			memories_updated,
			memories_archived,
			memories_created,
			error_message,
			started_at,
			completed_at
		)
		VALUES ($1, $2, $3::jsonb, COALESCE(NULLIF($4, ''), 'pending'), $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, organization_id, run_type, scope_context::text, status, memories_examined, memories_updated, memories_archived, memories_created, error_message, started_at, completed_at, created_at
	`, item.OrganizationID, item.RunType, scopeContext, item.Status, item.MemoriesExamined, item.MemoriesUpdated, item.MemoriesArchived, item.MemoriesCreated, item.ErrorMessage, item.StartedAt, item.CompletedAt)

	created, err := scanMemoryCompactionRun(row)
	if err != nil {
		return MemoryCompactionRun{}, mapDBError(err)
	}
	return created, nil
}

func (r *MemoryCompactionRunRepo) GetByID(ctx context.Context, id uuid.UUID) (MemoryCompactionRun, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, run_type, scope_context::text, status, memories_examined, memories_updated, memories_archived, memories_created, error_message, started_at, completed_at, created_at
		FROM memory_compaction_run
		WHERE id = $1
	`, id)

	item, err := scanMemoryCompactionRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MemoryCompactionRun{}, ErrNotFound
	}
	if err != nil {
		return MemoryCompactionRun{}, mapDBError(err)
	}
	return item, nil
}

func (r *MemoryCompactionRunRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string, errorMessage *string, startedAt, completedAt *time.Time) (MemoryCompactionRun, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE memory_compaction_run
		SET status = $2,
		    error_message = $3,
		    started_at = $4,
		    completed_at = $5
		WHERE id = $1
		RETURNING id, organization_id, run_type, scope_context::text, status, memories_examined, memories_updated, memories_archived, memories_created, error_message, started_at, completed_at, created_at
	`, id, status, errorMessage, startedAt, completedAt)

	updated, err := scanMemoryCompactionRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MemoryCompactionRun{}, ErrNotFound
	}
	if err != nil {
		return MemoryCompactionRun{}, mapDBError(err)
	}
	return updated, nil
}

func (r *MemoryCompactionRunRepo) UpdateCounts(ctx context.Context, id uuid.UUID, examined, updated, archived, created int) (MemoryCompactionRun, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE memory_compaction_run
		SET memories_examined = $2,
		    memories_updated = $3,
		    memories_archived = $4,
		    memories_created = $5
		WHERE id = $1
		RETURNING id, organization_id, run_type, scope_context::text, status, memories_examined, memories_updated, memories_archived, memories_created, error_message, started_at, completed_at, created_at
	`, id, examined, updated, archived, created)

	result, err := scanMemoryCompactionRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MemoryCompactionRun{}, ErrNotFound
	}
	if err != nil {
		return MemoryCompactionRun{}, mapDBError(err)
	}
	return result, nil
}

func scanMemoryCompactionRun(row pgx.Row) (MemoryCompactionRun, error) {
	var item MemoryCompactionRun
	if err := row.Scan(
		&item.ID,
		&item.OrganizationID,
		&item.RunType,
		&item.ScopeContext,
		&item.Status,
		&item.MemoriesExamined,
		&item.MemoriesUpdated,
		&item.MemoriesArchived,
		&item.MemoriesCreated,
		&item.ErrorMessage,
		&item.StartedAt,
		&item.CompletedAt,
		&item.CreatedAt,
	); err != nil {
		return MemoryCompactionRun{}, err
	}
	return item, nil
}

type MemoryDedupReviewed struct {
	ID               uuid.UUID
	MemoryIDA        uuid.UUID
	MemoryIDB        uuid.UUID
	CosineSimilarity float64
	Decision         string
	ReviewedBy       string
	ReviewedAt       time.Time
	CreatedAt        time.Time
}

type MemoryDedupReviewedRepo struct {
	pool *pgxpool.Pool
}

func NewMemoryDedupReviewedRepo(pool *pgxpool.Pool) *MemoryDedupReviewedRepo {
	return &MemoryDedupReviewedRepo{pool: pool}
}

func (r *MemoryDedupReviewedRepo) Create(ctx context.Context, item MemoryDedupReviewed) (MemoryDedupReviewed, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO memory_dedup_reviewed (
			memory_id_a,
			memory_id_b,
			cosine_similarity,
			decision,
			reviewed_by,
			reviewed_at
		)
		VALUES ($1, $2, $3, $4, $5, COALESCE($6, now()))
		RETURNING id, memory_id_a, memory_id_b, cosine_similarity, decision, reviewed_by, reviewed_at, created_at
	`, item.MemoryIDA, item.MemoryIDB, item.CosineSimilarity, item.Decision, item.ReviewedBy, nullTime(item.ReviewedAt))

	created, err := scanMemoryDedupReviewed(row)
	if err != nil {
		return MemoryDedupReviewed{}, mapDBError(err)
	}
	return created, nil
}

func (r *MemoryDedupReviewedRepo) GetByPair(ctx context.Context, memoryIDA, memoryIDB uuid.UUID) (MemoryDedupReviewed, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, memory_id_a, memory_id_b, cosine_similarity, decision, reviewed_by, reviewed_at, created_at
		FROM memory_dedup_reviewed
		WHERE LEAST(memory_id_a::text, memory_id_b::text) = LEAST($1::text, $2::text)
		  AND GREATEST(memory_id_a::text, memory_id_b::text) = GREATEST($1::text, $2::text)
	`, memoryIDA, memoryIDB)

	item, err := scanMemoryDedupReviewed(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return MemoryDedupReviewed{}, ErrNotFound
	}
	if err != nil {
		return MemoryDedupReviewed{}, mapDBError(err)
	}
	return item, nil
}

func (r *MemoryDedupReviewedRepo) ListPendingReview(ctx context.Context) ([]MemoryDedupReviewed, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, memory_id_a, memory_id_b, cosine_similarity, decision, reviewed_by, reviewed_at, created_at
		FROM memory_dedup_reviewed
		WHERE decision = 'deferred'
		ORDER BY reviewed_at ASC
	`)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]MemoryDedupReviewed, 0)
	for rows.Next() {
		item, scanErr := scanMemoryDedupReviewed(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return items, nil
}

func scanMemoryDedupReviewed(row pgx.Row) (MemoryDedupReviewed, error) {
	var item MemoryDedupReviewed
	if err := row.Scan(
		&item.ID,
		&item.MemoryIDA,
		&item.MemoryIDB,
		&item.CosineSimilarity,
		&item.Decision,
		&item.ReviewedBy,
		&item.ReviewedAt,
		&item.CreatedAt,
	); err != nil {
		return MemoryDedupReviewed{}, err
	}
	return item, nil
}

func embeddingToLiteral(vector []float32) string {
	if len(vector) == 0 {
		return ""
	}
	parts := make([]string, len(vector))
	for index, value := range vector {
		parts[index] = strconv.FormatFloat(float64(value), 'f', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func parseEmbedding(value string) ([]float32, error) {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimPrefix(trimmed, "[")
	trimmed = strings.TrimSuffix(trimmed, "]")
	if strings.TrimSpace(trimmed) == "" {
		return nil, nil
	}

	parts := strings.Split(trimmed, ",")
	embedding := make([]float32, 0, len(parts))
	for _, part := range parts {
		number, err := strconv.ParseFloat(strings.TrimSpace(part), 32)
		if err != nil {
			return nil, fmt.Errorf("parse embedding component %q: %w", part, err)
		}
		embedding = append(embedding, float32(number))
	}
	return embedding, nil
}

func defaultMemoryStatus(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "candidate"
	}
	return trimmed
}

func defaultSensitivity(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "normal"
	}
	return trimmed
}

func normalizeScore(value float64, fallback float64) float64 {
	if value == 0 {
		return fallback
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fallback
	}
	return value
}

func nullTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}
