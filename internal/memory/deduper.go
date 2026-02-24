package memory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const dedupBatchSize = 10

type DeduperOptions struct {
	Pool     *pgxpool.Pool
	Reviewer DedupReviewer
	Merger   DedupMerger
	Clock    Clock
}

type Deduper struct {
	pool     *pgxpool.Pool
	reviewer DedupReviewer
	merger   DedupMerger
	clock    Clock
}

func NewDeduper(opts DeduperOptions) (*Deduper, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("deduper pool is required")
	}
	if opts.Reviewer == nil {
		return nil, fmt.Errorf("deduper reviewer is required")
	}
	if opts.Clock == nil {
		opts.Clock = wallClock{}
	}
	return &Deduper{
		pool:     opts.Pool,
		reviewer: opts.Reviewer,
		merger:   opts.Merger,
		clock:    opts.Clock,
	}, nil
}

func (d *Deduper) ReviewCluster(ctx context.Context, pairs []DedupPair) error {
	if len(pairs) == 0 {
		return nil
	}

	for _, inputPair := range pairs {
		tx, err := d.pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return err
		}
		committed := false
		func() {
			defer func() {
				if !committed {
					_ = tx.Rollback(ctx)
				}
			}()

			shouldProcess, processErr := shouldProcessDedupPair(ctx, tx, inputPair)
			if processErr != nil {
				err = processErr
				return
			}
			if !shouldProcess {
				if commitErr := tx.Commit(ctx); commitErr != nil {
					err = commitErr
					return
				}
				committed = true
				return
			}

			reviews, reviewErr := d.reviewer.ReviewDedup(ctx, inputPair.MemoryA.OrganizationID, []DedupPair{inputPair})
			if reviewErr != nil {
				err = reviewErr
				return
			}
			if len(reviews) != 1 {
				err = fmt.Errorf("dedup review count mismatch: got %d, want %d", len(reviews), 1)
				return
			}

			pair := inputPair
			review := reviews[0]
			if review.Pair.MemoryA.ID != uuid.Nil && review.Pair.MemoryB.ID != uuid.Nil {
				pair = review.Pair
			}
			if applyErr := d.applyReviewTx(ctx, tx, pair, review.Decision, d.clock.Now()); applyErr != nil {
				err = applyErr
				return
			}

			if commitErr := tx.Commit(ctx); commitErr != nil {
				err = commitErr
				return
			}
			committed = true
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *Deduper) applyReviewTx(ctx context.Context, tx pgx.Tx, pair DedupPair, decision DedupDecision, now time.Time) error {
	if pair.MemoryA.ID == uuid.Nil || pair.MemoryB.ID == uuid.Nil {
		return fmt.Errorf("dedup pair memory ids are required")
	}

	if err := upsertDedupDecision(ctx, tx, pair, decision, now); err != nil {
		return err
	}

	switch decision {
	case DedupDecisionKeepBoth:
		// Decision persisted only.
	case DedupDecisionSupersedeA:
		if err := supersedeMemory(ctx, tx, pair.MemoryA.ID, pair.MemoryB.ID, now); err != nil {
			return err
		}
	case DedupDecisionSupersedeB:
		if err := supersedeMemory(ctx, tx, pair.MemoryB.ID, pair.MemoryA.ID, now); err != nil {
			return err
		}
	case DedupDecisionMerge:
		if d.merger == nil {
			return fmt.Errorf("merge decision received but merger is not configured")
		}
		merged, mergeErr := d.merger.Merge(ctx, tx, pair.MemoryA.OrganizationID, pair.MemoryA, pair.MemoryB)
		if mergeErr != nil {
			return mergeErr
		}
		if merged.ID == uuid.Nil {
			return fmt.Errorf("merger returned empty memory id")
		}
		if err := supersedeMemory(ctx, tx, pair.MemoryA.ID, merged.ID, now); err != nil {
			return err
		}
		if err := supersedeMemory(ctx, tx, pair.MemoryB.ID, merged.ID, now); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported dedup decision %q", decision)
	}

	return nil
}

func shouldProcessDedupPair(ctx context.Context, tx pgx.Tx, pair DedupPair) (bool, error) {
	var existingDecision string
	decisionErr := tx.QueryRow(ctx, `
		SELECT decision
		FROM memory_dedup_reviewed
		WHERE LEAST(memory_id_a::text, memory_id_b::text) = LEAST($1::text, $2::text)
		  AND GREATEST(memory_id_a::text, memory_id_b::text) = GREATEST($1::text, $2::text)
	`, pair.MemoryA.ID, pair.MemoryB.ID).Scan(&existingDecision)
	switch {
	case errors.Is(decisionErr, pgx.ErrNoRows):
		return true, nil
	case decisionErr != nil:
		return false, decisionErr
	}
	if existingDecision != "deferred" {
		return false, nil
	}

	var lockedID uuid.UUID
	lockErr := tx.QueryRow(ctx, `
		SELECT id
		FROM memory_dedup_reviewed
		WHERE decision = 'deferred'
		  AND LEAST(memory_id_a::text, memory_id_b::text) = LEAST($1::text, $2::text)
		  AND GREATEST(memory_id_a::text, memory_id_b::text) = GREATEST($1::text, $2::text)
		ORDER BY id
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, pair.MemoryA.ID, pair.MemoryB.ID).Scan(&lockedID)
	if errors.Is(lockErr, pgx.ErrNoRows) {
		return false, nil
	}
	if lockErr != nil {
		return false, lockErr
	}
	return true, nil
}

func upsertDedupDecision(ctx context.Context, tx pgx.Tx, pair DedupPair, decision DedupDecision, reviewedAt time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO memory_dedup_reviewed (
			memory_id_a,
			memory_id_b,
			cosine_similarity,
			decision,
			reviewed_by,
			reviewed_at
		)
		VALUES ($1, $2, $3, $4, 'llm', $5)
		ON CONFLICT ((LEAST(memory_id_a::text, memory_id_b::text)), (GREATEST(memory_id_a::text, memory_id_b::text)))
		DO UPDATE SET
			cosine_similarity = EXCLUDED.cosine_similarity,
			decision = EXCLUDED.decision,
			reviewed_by = EXCLUDED.reviewed_by,
			reviewed_at = EXCLUDED.reviewed_at
	`, pair.MemoryA.ID, pair.MemoryB.ID, pair.CosineSimilarity, string(decision), reviewedAt.UTC())
	return err
}

func supersedeMemory(ctx context.Context, tx pgx.Tx, loserID, winnerID uuid.UUID, at time.Time) error {
	result, err := tx.Exec(ctx, `
		UPDATE memory
		SET status = 'superseded',
		    superseded_by = $2,
		    superseded_at = $3
		WHERE id = $1
	`, loserID, winnerID, at.UTC())
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("memory %s not found for supersession", loserID)
	}
	return nil
}
