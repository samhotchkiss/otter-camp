package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/storage"
)

const (
	RetentionEnforceJobType      = "retention_enforce"
	RetentionChatDays            = 90
	RetentionRunDays             = 90
	RetentionModelInvocationDays = 90
	RetentionDomainEventDays     = 90
	RetentionRunArtifactDays     = 90
	RetentionAuditDays           = 365
	RetentionArchivedMemoryDays  = 365
	RetentionTraceSpanDays       = 7
)

type retentionEventPublisher interface {
	Publish(ctx context.Context, tx pgx.Tx, event eventbus.DomainEvent) error
}

type RetentionJob struct {
	pool   *pgxpool.Pool
	events retentionEventPublisher
	store  storage.Store
	now    func() time.Time
}

type RetentionJobOptions struct {
	Pool   *pgxpool.Pool
	Events retentionEventPublisher
	Store  storage.Store
	Now    func() time.Time
}

func NewRetentionJob(opts RetentionJobOptions) (*RetentionJob, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("retention job requires database pool")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &RetentionJob{pool: opts.Pool, events: opts.Events, store: opts.Store, now: opts.Now}, nil
}

func (j *RetentionJob) RegisterJobs(registrar interface {
	Register(jobType string, handler jobqueue.JobHandler)
}) {
	if j == nil || registrar == nil {
		return
	}
	registrar.Register(RetentionEnforceJobType, func(ctx context.Context, _ jobqueue.Job) error {
		return j.Run(ctx)
	})
}

func (j *RetentionJob) Run(ctx context.Context) error {
	rowsDeleted := map[string]int64{}
	now := j.now().UTC()

	count, err := j.deleteRunArtifacts(ctx, now.AddDate(0, 0, -RetentionRunArtifactDays))
	if err != nil {
		return err
	}
	rowsDeleted["run_artifact"] = count

	if rowsDeleted["chat_message"], err = j.deleteOlderThan(ctx, "chat_message", now.AddDate(0, 0, -RetentionChatDays)); err != nil {
		return err
	}
	if rowsDeleted["chat_turn"], err = j.deleteOlderThan(ctx, "chat_turn", now.AddDate(0, 0, -RetentionChatDays)); err != nil {
		return err
	}
	if rowsDeleted["run"], err = j.deleteOlderThan(ctx, "run", now.AddDate(0, 0, -RetentionRunDays)); err != nil {
		return err
	}
	if rowsDeleted["model_invocation"], err = j.deleteOlderThan(ctx, "model_invocation", now.AddDate(0, 0, -RetentionModelInvocationDays)); err != nil {
		return err
	}
	if rowsDeleted["domain_event"], err = j.deleteOlderThan(ctx, "domain_event", now.AddDate(0, 0, -RetentionDomainEventDays)); err != nil {
		return err
	}
	if rowsDeleted["audit_event"], err = j.deleteOlderThan(ctx, "audit_event", now.AddDate(0, 0, -RetentionAuditDays)); err != nil {
		return err
	}
	if rowsDeleted["memory"], err = j.deleteArchivedMemory(ctx, now.AddDate(0, 0, -RetentionArchivedMemoryDays)); err != nil {
		return err
	}
	if rowsDeleted["trace_span"], err = j.dropOldTraceSpanPartitions(ctx, now.AddDate(0, 0, -RetentionTraceSpanDays)); err != nil {
		return err
	}

	j.publishCompletion(ctx, rowsDeleted)
	return nil
}

func (j *RetentionJob) deleteOlderThan(ctx context.Context, table string, cutoff time.Time) (int64, error) {
	tx, err := j.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin retention delete for %s: %w", table, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	query := fmt.Sprintf("DELETE FROM %s WHERE created_at < $1", table)
	tag, err := tx.Exec(ctx, query, cutoff.UTC())
	if err != nil {
		return 0, fmt.Errorf("delete old rows for %s: %w", table, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit retention delete for %s: %w", table, err)
	}
	return tag.RowsAffected(), nil
}

func (j *RetentionJob) deleteArchivedMemory(ctx context.Context, cutoff time.Time) (int64, error) {
	tx, err := j.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin retention delete for memory: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		DELETE FROM memory
		WHERE superseded_by IS NOT NULL
		  AND created_at < $1
	`, cutoff.UTC())
	if err != nil {
		return 0, fmt.Errorf("delete archived memory rows: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit retention delete for memory: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (j *RetentionJob) dropOldTraceSpanPartitions(ctx context.Context, cutoff time.Time) (int64, error) {
	tx, err := j.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin trace_span partition retention: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT child_ns.nspname, child.relname
		FROM pg_inherits inh
		JOIN pg_class parent ON parent.oid = inh.inhparent
		JOIN pg_namespace parent_ns ON parent_ns.oid = parent.relnamespace
		JOIN pg_class child ON child.oid = inh.inhrelid
		JOIN pg_namespace child_ns ON child_ns.oid = child.relnamespace
		WHERE parent_ns.nspname = current_schema()
		  AND parent.relname = 'trace_span'
		  AND pg_get_expr(child.relpartbound, child.oid) LIKE 'FOR VALUES FROM (%'
		  AND ((regexp_match(pg_get_expr(child.relpartbound, child.oid), $$TO \('([^']+)'\)$$))[1])::timestamptz < $1
	`, cutoff.UTC())
	if err != nil {
		return 0, fmt.Errorf("query trace_span partitions for retention: %w", err)
	}
	defer rows.Close()

	type partitionRef struct {
		schema string
		name   string
	}
	partitions := make([]partitionRef, 0)
	for rows.Next() {
		var partition partitionRef
		if scanErr := rows.Scan(&partition.schema, &partition.name); scanErr != nil {
			return 0, fmt.Errorf("scan trace_span partition: %w", scanErr)
		}
		partitions = append(partitions, partition)
	}
	if rows.Err() != nil {
		return 0, fmt.Errorf("iterate trace_span partitions: %w", rows.Err())
	}

	var dropped int64
	for _, partition := range partitions {
		qualified := pgx.Identifier{partition.schema, partition.name}.Sanitize()
		if _, err := tx.Exec(ctx, fmt.Sprintf("ALTER TABLE trace_span DETACH PARTITION %s", qualified)); err != nil {
			return 0, fmt.Errorf("detach trace_span partition %s: %w", partition.name, err)
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf("DROP TABLE %s", qualified)); err != nil {
			return 0, fmt.Errorf("drop trace_span partition %s: %w", partition.name, err)
		}
		dropped++
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit trace_span partition retention: %w", err)
	}
	return dropped, nil
}

func (j *RetentionJob) deleteRunArtifacts(ctx context.Context, cutoff time.Time) (int64, error) {
	tx, err := j.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin retention delete for run_artifact: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT id, storage_key
		FROM run_artifact
		WHERE created_at < $1
	`, cutoff.UTC())
	if err != nil {
		return 0, fmt.Errorf("query old run_artifact rows: %w", err)
	}
	defer rows.Close()

	ids := make([]uuid.UUID, 0)
	keys := make([]string, 0)
	for rows.Next() {
		var id uuid.UUID
		var storageKey string
		if scanErr := rows.Scan(&id, &storageKey); scanErr != nil {
			return 0, fmt.Errorf("scan old run_artifact row: %w", scanErr)
		}
		ids = append(ids, id)
		keys = append(keys, storageKey)
	}
	if rows.Err() != nil {
		return 0, fmt.Errorf("iterate old run_artifact rows: %w", rows.Err())
	}

	for _, key := range keys {
		if j.store == nil {
			continue
		}
		_ = j.store.Delete(ctx, key)
	}

	if len(ids) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("commit retention no-op for run_artifact: %w", err)
		}
		return 0, nil
	}

	tag, err := tx.Exec(ctx, `DELETE FROM run_artifact WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return 0, fmt.Errorf("delete old run_artifact rows: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit retention delete for run_artifact: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (j *RetentionJob) publishCompletion(ctx context.Context, rowsDeleted map[string]int64) {
	if j.events == nil {
		return
	}

	var orgID uuid.UUID
	if err := j.pool.QueryRow(ctx, `SELECT id FROM organization ORDER BY created_at ASC LIMIT 1`).Scan(&orgID); err != nil || orgID == uuid.Nil {
		return
	}

	payload, err := json.Marshal(map[string]any{
		"tables_processed": len(rowsDeleted),
		"rows_deleted":     rowsDeleted,
	})
	if err != nil {
		return
	}
	_ = j.events.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: orgID,
		EventType:      "system.retention.completed",
		ActorType:      "system",
		Payload:        payload,
	})
}
