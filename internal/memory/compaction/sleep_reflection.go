package compaction

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	MemorySleepReflectionJobType = "memory_sleep_reflection"

	candidateReviewBatchSize   = 50
	minRetainedExtractionScore = 40
)

type SleepReflectionPayload struct {
	OrganizationID  uuid.UUID `json:"organization_id"`
	CompactionRunID uuid.UUID `json:"compaction_run_id"`
}

type SleepReflectorOptions struct {
	Pool         *pgxpool.Pool
	Runs         *repo.MemoryCompactionRunRepo
	Deduplicator CandidateDeduplicator
	Now          func() time.Time
}

type SleepReflector struct {
	pool         *pgxpool.Pool
	runs         *repo.MemoryCompactionRunRepo
	deduplicator CandidateDeduplicator
	now          func() time.Time
}

type CandidateMemory struct {
	ID              uuid.UUID
	Content         string
	ExtractionScore *int
	UtilityScore    float64
	Confidence      float64
}

type CandidateReviewAction string

const (
	CandidateReviewKeep    CandidateReviewAction = "keep"
	CandidateReviewArchive CandidateReviewAction = "archive"
	CandidateReviewMerge   CandidateReviewAction = "merge"
)

type CandidateReview struct {
	MemoryID        uuid.UUID
	Action          CandidateReviewAction
	MergeIntoID     *uuid.UUID
	ExtractionScore *int
}

type CandidateDeduplicator interface {
	ReviewCandidateBatch(ctx context.Context, orgID uuid.UUID, batch []CandidateMemory) ([]CandidateReview, error)
}

func NewSleepReflector(opts SleepReflectorOptions) (*SleepReflector, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("sleep reflector requires database pool")
	}
	if opts.Deduplicator == nil {
		return nil, fmt.Errorf("sleep reflector deduplicator is required")
	}

	r := &SleepReflector{
		pool:         opts.Pool,
		runs:         opts.Runs,
		deduplicator: opts.Deduplicator,
		now:          opts.Now,
	}
	if r.runs == nil {
		r.runs = repo.NewMemoryCompactionRunRepo(opts.Pool)
	}
	if r.now == nil {
		r.now = time.Now
	}
	return r, nil
}

func (r *SleepReflector) RegisterJobs(registrar interface {
	Register(jobType string, handler jobqueue.JobHandler)
}) {
	if r == nil || registrar == nil {
		return
	}

	registrar.Register(MemorySleepReflectionJobType, func(ctx context.Context, job jobqueue.Job) error {
		var payload SleepReflectionPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("decode memory sleep reflection payload: %w", err)
		}
		if payload.OrganizationID == uuid.Nil {
			return fmt.Errorf("memory sleep reflection payload missing organization_id")
		}
		if payload.CompactionRunID == uuid.Nil {
			return fmt.Errorf("memory sleep reflection payload missing compaction_run_id")
		}
		return r.Run(ctx, payload.OrganizationID, payload.CompactionRunID)
	})
}

func (r *SleepReflector) Run(ctx context.Context, orgID uuid.UUID, compactionRunID uuid.UUID) (err error) {
	if r == nil || r.pool == nil || r.runs == nil {
		return fmt.Errorf("sleep reflector is not configured")
	}
	if orgID == uuid.Nil {
		return fmt.Errorf("organization id is required")
	}
	if compactionRunID == uuid.Nil {
		return fmt.Errorf("compaction run id is required")
	}
	if _, err := r.runs.FailStalePending(ctx, r.now().UTC().Add(-time.Hour), repo.MemoryCompactionPendingTimeoutReason); err != nil {
		return err
	}

	startedAt := r.now().UTC()
	if _, err := r.runs.UpdateStatus(ctx, compactionRunID, "running", nil, &startedAt, nil); err != nil {
		return err
	}

	var (
		examined int
		updated  int
		archived int
	)
	defer func() {
		if err != nil {
			message := strings.TrimSpace(err.Error())
			if message == "" {
				message = repo.MemoryCompactionDefaultFailedMessage
			}
			failedAt := r.now().UTC()
			_, _ = r.runs.UpdateStatus(ctx, compactionRunID, "failed", &message, &startedAt, &failedAt)
			return
		}

		_, _ = r.runs.UpdateCounts(ctx, compactionRunID, examined, updated, archived, 0)
		completedAt := r.now().UTC()
		_, _ = r.runs.UpdateStatus(ctx, compactionRunID, "completed", nil, &startedAt, &completedAt)
	}()

	candidateExamined, candidateUpdated, candidateArchived, err := r.reviewCandidates(ctx, orgID)
	if err != nil {
		return err
	}
	examined += candidateExamined
	updated += candidateUpdated
	archived += candidateArchived

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	episodicRes, err := tx.Exec(ctx, `
		UPDATE memory
		SET confidence = confidence * 0.5
		WHERE organization_id = $1
		  AND memory_type = 'episodic'
		  AND status = 'active'
		  AND created_at < now() - interval '30 days'
		  AND updated_at < now() - interval '29 days'
	`, orgID)
	if err != nil {
		return err
	}
	updated += int(episodicRes.RowsAffected())

	proceduralRes, err := tx.Exec(ctx, `
		UPDATE memory
		SET confidence = confidence * 0.5
		WHERE organization_id = $1
		  AND memory_type = 'procedural'
		  AND status = 'active'
		  AND created_at < now() - interval '180 days'
		  AND updated_at < now() - interval '179 days'
	`, orgID)
	if err != nil {
		return err
	}
	updated += int(proceduralRes.RowsAffected())

	archivedAt := r.now().UTC()
	archiveRes, err := tx.Exec(ctx, `
		UPDATE memory
		SET status = 'archived',
		    archived_at = COALESCE(archived_at, $2)
		WHERE organization_id = $1
		  AND memory_type = 'episodic'
		  AND status = 'active'
		  AND created_at < now() - interval '30 days'
		  AND confidence < 0.1
	`, orgID, archivedAt)
	if err != nil {
		return err
	}
	archived += int(archiveRes.RowsAffected())

	examined += int(episodicRes.RowsAffected()) + int(proceduralRes.RowsAffected())

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}

func (r *SleepReflector) reviewCandidates(ctx context.Context, orgID uuid.UUID) (examined, updated, archived int, err error) {
	candidates, err := r.listCandidates(ctx, orgID)
	if err != nil {
		return 0, 0, 0, err
	}
	return processCandidateBatches(ctx, orgID, candidates, r.deduplicator, r.applyCandidateReviews)
}

type candidateReviewApplier func(ctx context.Context, orgID uuid.UUID, batch []CandidateMemory, reviews []CandidateReview) (updated, archived int, err error)

func processCandidateBatches(
	ctx context.Context,
	orgID uuid.UUID,
	candidates []CandidateMemory,
	deduplicator CandidateDeduplicator,
	applier candidateReviewApplier,
) (examined, updated, archived int, err error) {
	if deduplicator == nil {
		return 0, 0, 0, fmt.Errorf("candidate deduplicator is required")
	}
	if applier == nil {
		return 0, 0, 0, fmt.Errorf("candidate review applier is required")
	}

	examined = len(candidates)
	for start := 0; start < len(candidates); start += candidateReviewBatchSize {
		end := start + candidateReviewBatchSize
		if end > len(candidates) {
			end = len(candidates)
		}

		batch := candidates[start:end]
		reviews, reviewErr := deduplicator.ReviewCandidateBatch(ctx, orgID, batch)
		if reviewErr != nil {
			return 0, 0, 0, reviewErr
		}
		batchUpdated, batchArchived, applyErr := applier(ctx, orgID, batch, reviews)
		if applyErr != nil {
			return 0, 0, 0, applyErr
		}
		updated += batchUpdated
		archived += batchArchived
	}

	return examined, updated, archived, nil
}

func (r *SleepReflector) listCandidates(ctx context.Context, orgID uuid.UUID) ([]CandidateMemory, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, content, extraction_score, utility_score::float8, confidence::float8
		FROM memory
		WHERE organization_id = $1
		  AND status = 'candidate'
		  AND is_hardened = false
		  AND created_at < now() - interval '1 day'
		  AND created_at >= now() - interval '7 days'
		ORDER BY created_at ASC, id ASC
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]CandidateMemory, 0)
	for rows.Next() {
		var item CandidateMemory
		if scanErr := rows.Scan(
			&item.ID,
			&item.Content,
			&item.ExtractionScore,
			&item.UtilityScore,
			&item.Confidence,
		); scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *SleepReflector) applyCandidateReviews(ctx context.Context, orgID uuid.UUID, batch []CandidateMemory, reviews []CandidateReview) (updated, archived int, err error) {
	if len(reviews) == 0 || len(batch) == 0 {
		return 0, 0, nil
	}

	batchIDs := make(map[uuid.UUID]struct{}, len(batch))
	for _, item := range batch {
		batchIDs[item.ID] = struct{}{}
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	processed := make(map[uuid.UUID]struct{}, len(reviews))
	for _, review := range reviews {
		if _, inBatch := batchIDs[review.MemoryID]; !inBatch {
			continue
		}
		if _, seen := processed[review.MemoryID]; seen {
			continue
		}
		processed[review.MemoryID] = struct{}{}

		action := review.Action
		if action == "" {
			action = CandidateReviewKeep
		}

		switch action {
		case CandidateReviewArchive, CandidateReviewMerge:
			archiveRes, execErr := tx.Exec(ctx, `
				UPDATE memory
				SET status = 'archived',
				    archived_at = COALESCE(archived_at, now())
				WHERE organization_id = $1
				  AND id = $2
				  AND status = 'candidate'
			`, orgID, review.MemoryID)
			if execErr != nil {
				return 0, 0, execErr
			}
			archived += int(archiveRes.RowsAffected())
			continue
		case CandidateReviewKeep:
		default:
			return 0, 0, fmt.Errorf("unsupported candidate review action %q", action)
		}

		if review.ExtractionScore == nil {
			continue
		}
		if *review.ExtractionScore < minRetainedExtractionScore {
			archiveRes, execErr := tx.Exec(ctx, `
				UPDATE memory
				SET status = 'archived',
				    archived_at = COALESCE(archived_at, now())
				WHERE organization_id = $1
				  AND id = $2
				  AND status = 'candidate'
			`, orgID, review.MemoryID)
			if execErr != nil {
				return 0, 0, execErr
			}
			archived += int(archiveRes.RowsAffected())
			continue
		}

		updateRes, execErr := tx.Exec(ctx, `
			UPDATE memory
			SET extraction_score = GREATEST(COALESCE(extraction_score, 0), $3)
			WHERE organization_id = $1
			  AND id = $2
			  AND status = 'candidate'
			  AND COALESCE(extraction_score, 0) < $3
		`, orgID, review.MemoryID, *review.ExtractionScore)
		if execErr != nil {
			return 0, 0, execErr
		}
		updated += int(updateRes.RowsAffected())
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}

	return updated, archived, nil
}

func CalculateDecay(initial float64, elapsed time.Duration, halfLife time.Duration) float64 {
	if initial <= 0 {
		return 0
	}
	if halfLife <= 0 {
		return initial
	}
	steps := int(elapsed / halfLife)
	if steps <= 0 {
		return initial
	}
	value := initial
	for i := 0; i < steps; i++ {
		value *= 0.5
	}
	return value
}
