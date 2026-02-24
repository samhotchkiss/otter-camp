package memory

import (
	"context"
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

	for start := 0; start < len(pairs); start += dedupBatchSize {
		end := start + dedupBatchSize
		if end > len(pairs) {
			end = len(pairs)
		}
		batch := append([]DedupPair(nil), pairs[start:end]...)
		orgID := batch[0].MemoryA.OrganizationID

		reviews, err := d.reviewer.ReviewDedup(ctx, orgID, batch)
		if err != nil {
			return err
		}
		if len(reviews) != len(batch) {
			return fmt.Errorf("dedup review count mismatch: got %d, want %d", len(reviews), len(batch))
		}

		for index := range batch {
			pair := batch[index]
			review := reviews[index]
			if review.Pair.MemoryA.ID != uuid.Nil && review.Pair.MemoryB.ID != uuid.Nil {
				pair = review.Pair
			}
			if err := d.applyReview(ctx, pair, review.Decision); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *Deduper) applyReview(ctx context.Context, pair DedupPair, decision DedupDecision) error {
	if pair.MemoryA.ID == uuid.Nil || pair.MemoryB.ID == uuid.Nil {
		return fmt.Errorf("dedup pair memory ids are required")
	}

	now := d.clock.Now()
	tx, err := d.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

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
		merged, mergeErr := d.merger.Merge(ctx, pair.MemoryA.OrganizationID, pair.MemoryA, pair.MemoryB)
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

	return tx.Commit(ctx)
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
	_, err := tx.Exec(ctx, `
		UPDATE memory
		SET status = 'superseded',
		    superseded_by = $2,
		    superseded_at = $3
		WHERE id = $1
	`, loserID, winnerID, at.UTC())
	return err
}
