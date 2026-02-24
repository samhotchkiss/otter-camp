package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const synthesisInvocationPurpose = "memory_synthesis"

type EntitySynthesizerOptions struct {
	Pool  *pgxpool.Pool
	Model SynthesisModel
	Clock Clock
}

type EntitySynthesizer struct {
	pool  *pgxpool.Pool
	model SynthesisModel
	clock Clock
}

func NewEntitySynthesizer(opts EntitySynthesizerOptions) (*EntitySynthesizer, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("entity synthesizer pool is required")
	}
	if opts.Model == nil {
		return nil, fmt.Errorf("entity synthesizer model is required")
	}
	if opts.Clock == nil {
		opts.Clock = wallClock{}
	}

	return &EntitySynthesizer{
		pool:  opts.Pool,
		model: opts.Model,
		clock: opts.Clock,
	}, nil
}

func (s *EntitySynthesizer) SynthesizeProfile(ctx context.Context, entityID uuid.UUID) (*EntityProfile, error) {
	entity, err := s.getEntity(ctx, entityID)
	if err != nil {
		return nil, err
	}
	memories, err := s.listActiveMemoriesForEntity(ctx, entityID)
	if err != nil {
		return nil, err
	}
	if len(memories) < 5 {
		return nil, nil
	}

	content, err := s.model.SynthesizeEntityProfile(ctx, entity.OrganizationID, entity, memories)
	if err != nil {
		return nil, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("synthesis model returned empty profile")
	}

	scopeMemory := mostSpecificScopeMemory(memories)
	now := s.clock.Now()

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var oldSynthesisID *uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT synthesis_memory_id
		FROM memory_entity
		WHERE id = $1
		FOR UPDATE
	`, entityID).Scan(&oldSynthesisID); err != nil {
		return nil, err
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO memory (
			organization_id,
			project_id,
			project_task_id,
			agent_id,
			memory_type,
			scope,
			content,
			content_hash,
			confidence,
			utility_score,
			status,
			sensitivity,
			trust_tier,
			file_backed,
			file_last_scanned_at
		)
		VALUES ($1, $2, $3, $4, 'entity_profile', $5, $6, $7, 0.90, 0.90, 'active', 'normal', 0.90, false, $8)
		RETURNING id, organization_id, project_id, project_task_id, agent_id, memory_type, scope, content, content_hash,
		          embedding::text, confidence, utility_score, extraction_score, status, is_hardened, sensitivity, trust_tier,
		          file_backed, file_path, file_last_scanned_at, superseded_by, superseded_at, archived_at, created_at, updated_at
	`, entity.OrganizationID, scopeMemory.ProjectID, scopeMemory.ProjectTaskID, scopeMemory.AgentID, scopeMemory.Scope, content, hashContent(content), now)

	created, err := scanMemory(row)
	if err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE memory_entity
		SET synthesis_memory_id = $2
		WHERE id = $1
	`, entityID, created.ID); err != nil {
		return nil, err
	}

	if oldSynthesisID != nil && *oldSynthesisID != uuid.Nil {
		if _, err := tx.Exec(ctx, `
			UPDATE memory
			SET status = 'superseded',
			    superseded_by = $2,
			    superseded_at = $3
			WHERE id = $1
		`, *oldSynthesisID, created.ID, now); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &EntityProfile{
		EntityID: entity.ID,
		Memory:   created,
	}, nil
}

func (s *EntitySynthesizer) GetProfile(ctx context.Context, entityID uuid.UUID) (*repo.Memory, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT m.id, m.organization_id, m.project_id, m.project_task_id, m.agent_id, m.memory_type, m.scope, m.content, m.content_hash,
		       m.embedding::text, m.confidence, m.utility_score, m.extraction_score, m.status, m.is_hardened, m.sensitivity, m.trust_tier,
		       m.file_backed, m.file_path, m.file_last_scanned_at, m.superseded_by, m.superseded_at, m.archived_at, m.created_at, m.updated_at
		FROM memory_entity e
		JOIN memory m ON m.id = e.synthesis_memory_id
		WHERE e.id = $1
	`, entityID)

	item, err := scanMemory(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

func (s *EntitySynthesizer) getEntity(ctx context.Context, entityID uuid.UUID) (repo.MemoryEntity, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, canonical_name, entity_type, synthesis_memory_id, metadata, created_at, updated_at
		FROM memory_entity
		WHERE id = $1
	`, entityID)

	var entity repo.MemoryEntity
	if err := row.Scan(
		&entity.ID,
		&entity.OrganizationID,
		&entity.CanonicalName,
		&entity.EntityType,
		&entity.SynthesisMemoryID,
		&entity.Metadata,
		&entity.CreatedAt,
		&entity.UpdatedAt,
	); err != nil {
		return repo.MemoryEntity{}, err
	}
	return entity, nil
}

func (s *EntitySynthesizer) listActiveMemoriesForEntity(ctx context.Context, entityID uuid.UUID) ([]repo.Memory, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT m.id, m.organization_id, m.project_id, m.project_task_id, m.agent_id, m.memory_type, m.scope, m.content, m.content_hash,
		       m.embedding::text, m.confidence, m.utility_score, m.extraction_score, m.status, m.is_hardened, m.sensitivity, m.trust_tier,
		       m.file_backed, m.file_path, m.file_last_scanned_at, m.superseded_by, m.superseded_at, m.archived_at, m.created_at, m.updated_at
		FROM memory_entity_mention mem
		JOIN memory m ON m.id = mem.memory_id
		WHERE mem.entity_id = $1
		  AND m.status = 'active'
		ORDER BY m.created_at ASC
	`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanMemoryRows(rows)
}

func mostSpecificScopeMemory(memories []repo.Memory) repo.Memory {
	if len(memories) == 0 {
		return repo.Memory{Scope: "org"}
	}

	score := func(scope string) int {
		switch strings.TrimSpace(scope) {
		case "task":
			return 4
		case "project":
			return 3
		case "agent":
			return 2
		default:
			return 1
		}
	}

	ordered := append([]repo.Memory(nil), memories...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return score(ordered[i].Scope) > score(ordered[j].Scope)
	})
	return ordered[0]
}
