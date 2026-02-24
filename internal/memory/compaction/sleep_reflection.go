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

	candidateReviewBatchSize = 50
)

type SleepReflectionPayload struct {
	OrganizationID  uuid.UUID `json:"organization_id"`
	CompactionRunID uuid.UUID `json:"compaction_run_id"`
}

type SleepReflectorOptions struct {
	Pool *pgxpool.Pool
	Runs *repo.MemoryCompactionRunRepo
	Now  func() time.Time
}

type SleepReflector struct {
	pool *pgxpool.Pool
	runs *repo.MemoryCompactionRunRepo
	now  func() time.Time
}

func NewSleepReflector(opts SleepReflectorOptions) (*SleepReflector, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("sleep reflector requires database pool")
	}

	r := &SleepReflector{
		pool: opts.Pool,
		runs: opts.Runs,
		now:  opts.Now,
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
			failedAt := r.now().UTC()
			_, _ = r.runs.UpdateStatus(ctx, compactionRunID, "failed", &message, &startedAt, &failedAt)
			return
		}

		_, _ = r.runs.UpdateCounts(ctx, compactionRunID, examined, updated, archived, 0)
		completedAt := r.now().UTC()
		_, _ = r.runs.UpdateStatus(ctx, compactionRunID, "completed", nil, &startedAt, &completedAt)
	}()

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM memory
		WHERE organization_id = $1
		  AND status = 'candidate'
		  AND is_hardened = false
		  AND created_at < now() - interval '1 day'
		  AND created_at >= now() - interval '7 days'
	`, orgID).Scan(&examined); err != nil {
		return err
	}

	episodicRes, err := tx.Exec(ctx, `
		UPDATE memory
		SET confidence = confidence * 0.5
		WHERE organization_id = $1
		  AND memory_type = 'episodic'
		  AND status = 'active'
		  AND created_at < now() - interval '30 days'
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
	archived = int(archiveRes.RowsAffected())

	examined += int(episodicRes.RowsAffected()) + int(proceduralRes.RowsAffected())

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
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
