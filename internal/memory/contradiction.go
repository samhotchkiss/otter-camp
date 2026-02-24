package memory

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type ContradictionDetectorOptions struct {
	Pool       *pgxpool.Pool
	Classifier ContradictionClassifier
	Clock      Clock
}

type ContradictionDetector struct {
	pool       *pgxpool.Pool
	classifier ContradictionClassifier
	clock      Clock
}

func NewContradictionDetector(opts ContradictionDetectorOptions) (*ContradictionDetector, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("contradiction detector pool is required")
	}
	if opts.Clock == nil {
		opts.Clock = wallClock{}
	}
	return &ContradictionDetector{
		pool:       opts.Pool,
		classifier: opts.Classifier,
		clock:      opts.Clock,
	}, nil
}

func (d *ContradictionDetector) Check(ctx context.Context, newMemory repo.Memory) ([]repo.Memory, error) {
	if newMemory.ID == uuid.Nil {
		return nil, fmt.Errorf("new memory id is required")
	}
	if newMemory.OrganizationID == uuid.Nil {
		return nil, fmt.Errorf("new memory organization id is required")
	}
	if len(newMemory.Embedding) == 0 {
		return nil, nil
	}

	entityIDs, err := d.entityIDsForMemory(ctx, newMemory.ID)
	if err != nil {
		return nil, err
	}
	if len(entityIDs) == 0 {
		return nil, nil
	}

	candidates, err := d.prescreenCandidates(ctx, newMemory, entityIDs)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	labels := make(map[uuid.UUID]ContradictionLabel, len(candidates))
	if d.classifier != nil {
		classified, classifyErr := d.classifier.ClassifyContradictions(ctx, newMemory.OrganizationID, newMemory, candidates)
		if classifyErr != nil {
			return nil, classifyErr
		}
		for id, label := range classified {
			labels[id] = label
		}
	}

	contradictions := make([]repo.Memory, 0)
	for _, candidate := range candidates {
		if labels[candidate.ID] == ContradictionLabelContradictory {
			contradictions = append(contradictions, candidate)
		}
	}
	if len(contradictions) == 0 {
		return nil, nil
	}

	tx, err := d.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	newConfidence := clampFloat(newMemory.Confidence-0.1, 0, 1)
	// Promotion from candidate->active is owned by the extraction pipeline (task 039).
	// Callers must re-check the stored confidence after Check() and keep the memory as
	// candidate when confidence drops below 0.2.
	if _, err := tx.Exec(ctx, `
		UPDATE memory
		SET confidence = $2
		WHERE id = $1
	`, newMemory.ID, newConfidence); err != nil {
		return nil, err
	}

	for _, candidate := range contradictions {
		updatedConfidence := clampFloat(candidate.Confidence-0.1, 0, 1)
		if updatedConfidence <= 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE memory
				SET status = 'archived',
				    archived_at = $2
				WHERE id = $1
			`, candidate.ID, d.clock.Now()); err != nil {
				return nil, err
			}
			continue
		}

		if _, err := tx.Exec(ctx, `
			UPDATE memory
			SET confidence = $2
			WHERE id = $1
		`, candidate.ID, updatedConfidence); err != nil {
			return nil, err
		}

		if newMemory.Confidence > 0.85 {
			if _, err := tx.Exec(ctx, `
				UPDATE memory
				SET status = 'superseded',
				    superseded_by = $2,
				    superseded_at = $3
				WHERE id = $1
			`, candidate.ID, newMemory.ID, d.clock.Now()); err != nil {
				return nil, err
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return contradictions, nil
}

func (d *ContradictionDetector) entityIDsForMemory(ctx context.Context, memoryID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT entity_id
		FROM memory_entity_mention
		WHERE memory_id = $1
	`, memoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, scanErr
		}
		ids = append(ids, id)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return ids, nil
}

func (d *ContradictionDetector) prescreenCandidates(ctx context.Context, newMemory repo.Memory, entityIDs []uuid.UUID) ([]repo.Memory, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT DISTINCT m.id, m.organization_id, m.project_id, m.project_task_id, m.agent_id, m.memory_type, m.scope, m.content, m.content_hash,
		       m.embedding::text, m.confidence, m.utility_score, m.extraction_score, m.status, m.is_hardened, m.sensitivity, m.trust_tier,
		       m.file_backed, m.file_path, m.file_last_scanned_at, m.superseded_by, m.superseded_at, m.archived_at, m.created_at, m.updated_at
		FROM memory m
		JOIN memory_entity_mention em ON em.memory_id = m.id
		WHERE m.organization_id = $1
		  AND m.id <> $2
		  AND m.status = 'active'
		  AND m.embedding IS NOT NULL
		  AND em.entity_id = ANY($4::uuid[])
		  AND (1 - (m.embedding <=> $3::vector)) >= 0.7
		  AND (1 - (m.embedding <=> $3::vector)) <= 0.87
	`, newMemory.OrganizationID, newMemory.ID, embeddingToLiteral(newMemory.Embedding), entityIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemoryRows(rows)
}
