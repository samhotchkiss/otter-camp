package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	maxInlineContentBytes = 51200
	defaultListLimit      = 50
	maxListLimit          = 200
)

var (
	ErrConflict                = repo.ErrConflict
	ErrNotFound                = repo.ErrNotFound
	ErrInlineContentTooLarge   = errors.New("inline content exceeds 50KB limit")
	ErrInlineContentDisallowed = errors.New("inline content is not allowed for this content type")
)

type queryExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Run struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	ProjectID      *uuid.UUID
	TaskID         *uuid.UUID
	FlowNodeID     *uuid.UUID
	SessionID      *uuid.UUID
	TurnID         *uuid.UUID
	PrincipalType  string
	PrincipalID    uuid.UUID
	Status         string
	IdempotencyKey *string
	TriggerType    string
	Version        int
	FailureReason  *string
	FailureClass   *string
	Metadata       json.RawMessage
	CreatedAt      time.Time
	UpdatedAt      time.Time
	StartedAt      *time.Time
	CompletedAt    *time.Time
}

type RunListFilter struct {
	OrganizationID  uuid.UUID
	TaskID          *uuid.UUID
	Status          string
	PrincipalID     *uuid.UUID
	Limit           int
	CursorCreatedAt *time.Time
	CursorID        *uuid.UUID
}

type RunRepository struct {
	pool *pgxpool.Pool
	db   queryExecutor
}

func NewRunRepository(pool *pgxpool.Pool) *RunRepository {
	return &RunRepository{pool: pool, db: pool}
}

func (r *RunRepository) Create(ctx context.Context, run Run) (Run, error) {
	metadata := normalizeJSON(run.Metadata, json.RawMessage(`{}`))
	status := normalizeRunStatus(run.Status)
	if status == "" {
		status = "created"
	}

	row := r.db.QueryRow(ctx, `
		INSERT INTO run (
			organization_id,
			project_id,
			task_id,
			flow_node_id,
			session_id,
			turn_id,
			principal_type,
			principal_id,
			status,
			idempotency_key,
			trigger_type,
			version,
			failure_reason,
			failure_class,
			metadata,
			started_at,
			completed_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9,
			$10, $11, COALESCE(NULLIF($12, 0), 1), $13, $14, $15::jsonb, $16, $17
		)
		RETURNING id, organization_id, project_id, task_id, flow_node_id, session_id, turn_id,
		          principal_type, principal_id, status, idempotency_key, trigger_type, version,
		          failure_reason, failure_class, metadata, created_at, updated_at, started_at, completed_at
	`,
		run.OrganizationID,
		run.ProjectID,
		run.TaskID,
		run.FlowNodeID,
		run.SessionID,
		run.TurnID,
		strings.TrimSpace(run.PrincipalType),
		run.PrincipalID,
		status,
		trimStringPointer(run.IdempotencyKey),
		strings.TrimSpace(run.TriggerType),
		run.Version,
		trimStringPointer(run.FailureReason),
		trimStringPointer(run.FailureClass),
		metadata,
		run.StartedAt,
		run.CompletedAt,
	)

	item, err := scanRun(row)
	if err != nil {
		return Run{}, mapDBError(err)
	}
	return item, nil
}

func (r *RunRepository) Get(ctx context.Context, id uuid.UUID) (Run, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, organization_id, project_id, task_id, flow_node_id, session_id, turn_id,
		       principal_type, principal_id, status, idempotency_key, trigger_type, version,
		       failure_reason, failure_class, metadata, created_at, updated_at, started_at, completed_at
		FROM run
		WHERE id = $1
	`, id)

	item, err := scanRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, mapDBError(err)
	}
	return item, nil
}

func (r *RunRepository) GetByIdempotencyKey(ctx context.Context, organizationID uuid.UUID, key string) (Run, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, organization_id, project_id, task_id, flow_node_id, session_id, turn_id,
		       principal_type, principal_id, status, idempotency_key, trigger_type, version,
		       failure_reason, failure_class, metadata, created_at, updated_at, started_at, completed_at
		FROM run
		WHERE organization_id = $1
		  AND idempotency_key = $2
	`, organizationID, strings.TrimSpace(key))

	item, err := scanRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, mapDBError(err)
	}
	return item, nil
}

func (r *RunRepository) UpdateStatus(ctx context.Context, id uuid.UUID, expectedVersion int, status string, failureReason, failureClass *string) (Run, error) {
	if expectedVersion <= 0 {
		return Run{}, fmt.Errorf("expected_version must be >= 1")
	}
	status = normalizeRunStatus(status)
	if status == "" {
		return Run{}, fmt.Errorf("invalid status")
	}

	row := r.db.QueryRow(ctx, `
		UPDATE run
		SET status = $3,
		    version = version + 1,
		    failure_reason = $4,
		    failure_class = $5,
		    started_at = CASE
		        WHEN $3 = 'in_progress' THEN COALESCE(started_at, now())
		        ELSE started_at
		    END,
		    completed_at = CASE
		        WHEN $3 IN ('completed', 'failed', 'timed_out', 'cancelled', 'dead_letter') THEN COALESCE(completed_at, now())
		        ELSE completed_at
		    END,
		    updated_at = now()
		WHERE id = $1
		  AND version = $2
		RETURNING id, organization_id, project_id, task_id, flow_node_id, session_id, turn_id,
		          principal_type, principal_id, status, idempotency_key, trigger_type, version,
		          failure_reason, failure_class, metadata, created_at, updated_at, started_at, completed_at
	`, id, expectedVersion, status, trimStringPointer(failureReason), trimStringPointer(failureClass))

	item, err := scanRun(row)
	if err == nil {
		return item, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Run{}, mapDBError(err)
	}

	_, getErr := r.Get(ctx, id)
	if errors.Is(getErr, ErrNotFound) {
		return Run{}, ErrNotFound
	}
	if getErr != nil {
		return Run{}, getErr
	}
	return Run{}, ErrConflict
}

func (r *RunRepository) List(ctx context.Context, filter RunListFilter) ([]Run, error) {
	if filter.OrganizationID == uuid.Nil {
		return nil, fmt.Errorf("organization_id is required")
	}
	limit := normalizeLimit(filter.Limit)

	query := `
		SELECT id, organization_id, project_id, task_id, flow_node_id, session_id, turn_id,
		       principal_type, principal_id, status, idempotency_key, trigger_type, version,
		       failure_reason, failure_class, metadata, created_at, updated_at, started_at, completed_at
		FROM run
		WHERE organization_id = $1`
	args := []any{filter.OrganizationID}
	argPos := 2

	if filter.TaskID != nil {
		query += fmt.Sprintf(" AND task_id = $%d", argPos)
		args = append(args, *filter.TaskID)
		argPos++
	}
	if status := normalizeRunStatus(filter.Status); status != "" {
		query += fmt.Sprintf(" AND status = $%d", argPos)
		args = append(args, status)
		argPos++
	}
	if filter.PrincipalID != nil {
		query += fmt.Sprintf(" AND principal_id = $%d", argPos)
		args = append(args, *filter.PrincipalID)
		argPos++
	}
	if filter.CursorCreatedAt != nil && filter.CursorID != nil {
		query += fmt.Sprintf(" AND (created_at, id) < ($%d, $%d)", argPos, argPos+1)
		args = append(args, filter.CursorCreatedAt.UTC(), *filter.CursorID)
		argPos += 2
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", argPos)
	args = append(args, limit)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]Run, 0)
	for rows.Next() {
		item, scanErr := scanRun(rows)
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

func (r *RunRepository) ListByTask(ctx context.Context, organizationID, taskID uuid.UUID, limit int) ([]Run, error) {
	filter := RunListFilter{OrganizationID: organizationID, TaskID: &taskID, Limit: limit}
	return r.List(ctx, filter)
}

func (r *RunRepository) ListInProgressUpdatedBefore(ctx context.Context, before time.Time) ([]Run, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, organization_id, project_id, task_id, flow_node_id, session_id, turn_id,
		       principal_type, principal_id, status, idempotency_key, trigger_type, version,
		       failure_reason, failure_class, metadata, created_at, updated_at, started_at, completed_at
		FROM run
		WHERE status = 'in_progress'
		  AND updated_at < $1
		ORDER BY updated_at ASC, id ASC
	`, before.UTC())
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]Run, 0)
	for rows.Next() {
		item, scanErr := scanRun(rows)
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

func (r *RunRepository) ListPausedUpdatedBefore(ctx context.Context, before time.Time) ([]Run, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, organization_id, project_id, task_id, flow_node_id, session_id, turn_id,
		       principal_type, principal_id, status, idempotency_key, trigger_type, version,
		       failure_reason, failure_class, metadata, created_at, updated_at, started_at, completed_at
		FROM run
		WHERE status = 'paused'
		  AND updated_at < $1
		ORDER BY updated_at ASC, id ASC
	`, before.UTC())
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]Run, 0)
	for rows.Next() {
		item, scanErr := scanRun(rows)
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

func (r *RunRepository) ListOrphanedInProgress(ctx context.Context, since time.Time) ([]Run, error) {
	rows, err := r.db.Query(ctx, `
		SELECT r.id, r.organization_id, r.project_id, r.task_id, r.flow_node_id, r.session_id, r.turn_id,
		       r.principal_type, r.principal_id, r.status, r.idempotency_key, r.trigger_type, r.version,
		       r.failure_reason, r.failure_class, r.metadata, r.created_at, r.updated_at, r.started_at, r.completed_at
		FROM run r
		WHERE r.status = 'in_progress'
		  AND NOT EXISTS (
			  SELECT 1
			  FROM run_event re
			  WHERE re.run_id = r.id
			    AND re.created_at > $1
		  )
		ORDER BY r.updated_at ASC, r.id ASC
	`, since.UTC())
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]Run, 0)
	for rows.Next() {
		item, scanErr := scanRun(rows)
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

func (r *RunRepository) CountDeadLetterByTaskFlowNode(ctx context.Context, taskID, flowNodeID uuid.UUID) (int, error) {
	if taskID == uuid.Nil || flowNodeID == uuid.Nil {
		return 0, nil
	}
	var count int
	if err := r.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM run
		WHERE task_id = $1
		  AND flow_node_id = $2
		  AND status = 'dead_letter'
	`, taskID, flowNodeID).Scan(&count); err != nil {
		return 0, mapDBError(err)
	}
	return count, nil
}

func (r *RunRepository) Cancel(ctx context.Context, id uuid.UUID, expectedVersion int) (Run, error) {
	return r.UpdateStatus(ctx, id, expectedVersion, "cancelling", nil, nil)
}

type RunStep struct {
	ID          uuid.UUID
	RunID       uuid.UUID
	StepNumber  int
	Status      string
	ToolName    *string
	ToolTier    *string
	StartedAt   *time.Time
	CompletedAt *time.Time
	Metadata    json.RawMessage
	CreatedAt   time.Time
}

type RunStepRepository struct {
	db queryExecutor
}

func NewRunStepRepository(pool *pgxpool.Pool) *RunStepRepository {
	return &RunStepRepository{db: pool}
}

func (r *RunStepRepository) Create(ctx context.Context, step RunStep) (RunStep, error) {
	metadata := normalizeJSON(step.Metadata, json.RawMessage(`{}`))
	status := normalizeRunStepStatus(step.Status)
	if status == "" {
		status = "pending"
	}

	row := r.db.QueryRow(ctx, `
		INSERT INTO run_step (
			run_id,
			step_number,
			status,
			tool_name,
			tool_tier,
			started_at,
			completed_at,
			metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb)
		RETURNING id, run_id, step_number, status, tool_name, tool_tier, started_at, completed_at, metadata, created_at
	`, step.RunID, step.StepNumber, status, trimStringPointer(step.ToolName), trimStringPointer(step.ToolTier), step.StartedAt, step.CompletedAt, metadata)

	item, err := scanRunStep(row)
	if err != nil {
		return RunStep{}, mapDBError(err)
	}
	return item, nil
}

func (r *RunStepRepository) Get(ctx context.Context, id uuid.UUID) (RunStep, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, run_id, step_number, status, tool_name, tool_tier, started_at, completed_at, metadata, created_at
		FROM run_step
		WHERE id = $1
	`, id)
	item, err := scanRunStep(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunStep{}, ErrNotFound
	}
	if err != nil {
		return RunStep{}, mapDBError(err)
	}
	return item, nil
}

func (r *RunStepRepository) GetByRunAndNumber(ctx context.Context, runID uuid.UUID, stepNumber int) (RunStep, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, run_id, step_number, status, tool_name, tool_tier, started_at, completed_at, metadata, created_at
		FROM run_step
		WHERE run_id = $1
		  AND step_number = $2
	`, runID, stepNumber)
	item, err := scanRunStep(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunStep{}, ErrNotFound
	}
	if err != nil {
		return RunStep{}, mapDBError(err)
	}
	return item, nil
}

func (r *RunStepRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string) (RunStep, error) {
	status = normalizeRunStepStatus(status)
	if status == "" {
		return RunStep{}, fmt.Errorf("invalid status")
	}

	row := r.db.QueryRow(ctx, `
		UPDATE run_step
		SET status = $2,
		    started_at = CASE WHEN $2 = 'in_progress' THEN COALESCE(started_at, now()) ELSE started_at END,
		    completed_at = CASE WHEN $2 IN ('completed', 'failed', 'cancelled', 'skipped') THEN COALESCE(completed_at, now()) ELSE completed_at END
		WHERE id = $1
		RETURNING id, run_id, step_number, status, tool_name, tool_tier, started_at, completed_at, metadata, created_at
	`, id, status)
	item, err := scanRunStep(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunStep{}, ErrNotFound
	}
	if err != nil {
		return RunStep{}, mapDBError(err)
	}
	return item, nil
}

func (r *RunStepRepository) ListByRun(ctx context.Context, runID uuid.UUID) ([]RunStep, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, run_id, step_number, status, tool_name, tool_tier, started_at, completed_at, metadata, created_at
		FROM run_step
		WHERE run_id = $1
		ORDER BY step_number ASC
	`, runID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]RunStep, 0)
	for rows.Next() {
		item, scanErr := scanRunStep(rows)
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

type RunAttempt struct {
	ID            uuid.UUID
	RunStepID     uuid.UUID
	AttemptNumber int
	Trigger       string
	Status        string
	FailureReason *string
	FailureClass  *string
	Output        json.RawMessage
	OutputSummary *string
	WorkerType    *string
	WorkerID      *string
	InputTokens   int
	OutputTokens  int
	StartedAt     *time.Time
	CompletedAt   *time.Time
	DurationMS    *int
	Metadata      json.RawMessage
	CreatedAt     time.Time
}

type RunAttemptRepository struct {
	db queryExecutor
}

func NewRunAttemptRepository(pool *pgxpool.Pool) *RunAttemptRepository {
	return &RunAttemptRepository{db: pool}
}

func (r *RunAttemptRepository) Create(ctx context.Context, attempt RunAttempt) (RunAttempt, error) {
	if attempt.AttemptNumber <= 0 {
		return RunAttempt{}, fmt.Errorf("attempt_number must be >= 1")
	}
	metadata := normalizeJSON(attempt.Metadata, json.RawMessage(`{}`))
	status := normalizeRunAttemptStatus(attempt.Status)
	if status == "" {
		status = "pending"
	}
	trigger := normalizeRunAttemptTrigger(attempt.Trigger)
	if trigger == "" {
		return RunAttempt{}, fmt.Errorf("invalid trigger")
	}

	row := r.db.QueryRow(ctx, `
		INSERT INTO run_attempt (
			run_step_id,
			attempt_number,
			trigger,
			status,
			failure_reason,
			failure_class,
			output,
			output_summary,
			worker_type,
			worker_id,
			input_tokens,
			output_tokens,
			started_at,
			completed_at,
			duration_ms,
			metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7::jsonb, 'null'::jsonb), $8, $9, $10, $11, $12, $13, $14, $15, $16::jsonb)
		RETURNING id, run_step_id, attempt_number, trigger, status, failure_reason, failure_class, output,
		          output_summary, worker_type, worker_id, input_tokens, output_tokens, started_at,
		          completed_at, duration_ms, metadata, created_at
	`,
		attempt.RunStepID,
		attempt.AttemptNumber,
		trigger,
		status,
		trimStringPointer(attempt.FailureReason),
		trimStringPointer(attempt.FailureClass),
		nullableJSON(attempt.Output),
		trimStringPointer(attempt.OutputSummary),
		trimStringPointer(attempt.WorkerType),
		trimStringPointer(attempt.WorkerID),
		attempt.InputTokens,
		attempt.OutputTokens,
		attempt.StartedAt,
		attempt.CompletedAt,
		attempt.DurationMS,
		metadata,
	)

	item, err := scanRunAttempt(row)
	if err != nil {
		return RunAttempt{}, mapDBError(err)
	}
	return item, nil
}

func (r *RunAttemptRepository) Get(ctx context.Context, id uuid.UUID) (RunAttempt, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, run_step_id, attempt_number, trigger, status, failure_reason, failure_class, output,
		       output_summary, worker_type, worker_id, input_tokens, output_tokens, started_at,
		       completed_at, duration_ms, metadata, created_at
		FROM run_attempt
		WHERE id = $1
	`, id)
	item, err := scanRunAttempt(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunAttempt{}, ErrNotFound
	}
	if err != nil {
		return RunAttempt{}, mapDBError(err)
	}
	return item, nil
}

func (r *RunAttemptRepository) GetLatestByStep(ctx context.Context, runStepID uuid.UUID) (RunAttempt, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, run_step_id, attempt_number, trigger, status, failure_reason, failure_class, output,
		       output_summary, worker_type, worker_id, input_tokens, output_tokens, started_at,
		       completed_at, duration_ms, metadata, created_at
		FROM run_attempt
		WHERE run_step_id = $1
		ORDER BY attempt_number DESC
		LIMIT 1
	`, runStepID)
	item, err := scanRunAttempt(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunAttempt{}, ErrNotFound
	}
	if err != nil {
		return RunAttempt{}, mapDBError(err)
	}
	return item, nil
}

func (r *RunAttemptRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string, failureReason, failureClass *string, output json.RawMessage, outputSummary *string) (RunAttempt, error) {
	status = normalizeRunAttemptStatus(status)
	if status == "" {
		return RunAttempt{}, fmt.Errorf("invalid status")
	}

	row := r.db.QueryRow(ctx, `
		UPDATE run_attempt
		SET status = $2,
		    failure_reason = $3,
		    failure_class = $4,
		    output = COALESCE(NULLIF($5::jsonb, 'null'::jsonb), output),
		    output_summary = COALESCE($6, output_summary),
		    started_at = CASE WHEN $2 = 'in_progress' THEN COALESCE(started_at, now()) ELSE started_at END,
		    completed_at = CASE WHEN $2 IN ('completed', 'failed', 'timed_out', 'cancelled') THEN COALESCE(completed_at, now()) ELSE completed_at END,
		    duration_ms = CASE
		        WHEN $2 IN ('completed', 'failed', 'timed_out', 'cancelled') AND started_at IS NOT NULL
		            THEN GREATEST(EXTRACT(EPOCH FROM (COALESCE(completed_at, now()) - started_at)) * 1000, 0)::int
		        ELSE duration_ms
		    END
		WHERE id = $1
		RETURNING id, run_step_id, attempt_number, trigger, status, failure_reason, failure_class, output,
		          output_summary, worker_type, worker_id, input_tokens, output_tokens, started_at,
		          completed_at, duration_ms, metadata, created_at
	`, id, status, trimStringPointer(failureReason), trimStringPointer(failureClass), nullableJSON(output), trimStringPointer(outputSummary))

	item, err := scanRunAttempt(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunAttempt{}, ErrNotFound
	}
	if err != nil {
		return RunAttempt{}, mapDBError(err)
	}
	return item, nil
}

func (r *RunAttemptRepository) ListByStep(ctx context.Context, runStepID uuid.UUID) ([]RunAttempt, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, run_step_id, attempt_number, trigger, status, failure_reason, failure_class, output,
		       output_summary, worker_type, worker_id, input_tokens, output_tokens, started_at,
		       completed_at, duration_ms, metadata, created_at
		FROM run_attempt
		WHERE run_step_id = $1
		ORDER BY attempt_number ASC
	`, runStepID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]RunAttempt, 0)
	for rows.Next() {
		item, scanErr := scanRunAttempt(rows)
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

func (r *RunAttemptRepository) ListByRun(ctx context.Context, runID uuid.UUID) ([]RunAttempt, error) {
	rows, err := r.db.Query(ctx, `
		SELECT ra.id, ra.run_step_id, ra.attempt_number, ra.trigger, ra.status, ra.failure_reason, ra.failure_class, ra.output,
		       ra.output_summary, ra.worker_type, ra.worker_id, ra.input_tokens, ra.output_tokens, ra.started_at,
		       ra.completed_at, ra.duration_ms, ra.metadata, ra.created_at
		FROM run_attempt ra
		JOIN run_step rs ON rs.id = ra.run_step_id
		WHERE rs.run_id = $1
		ORDER BY rs.step_number ASC, ra.attempt_number ASC
	`, runID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]RunAttempt, 0)
	for rows.Next() {
		item, scanErr := scanRunAttempt(rows)
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

type ToolExecution struct {
	ID             uuid.UUID
	RunID          uuid.UUID
	RunStepID      *uuid.UUID
	RunAttemptID   *uuid.UUID
	ToolName       string
	ToolTier       string
	ToolDomain     string
	Capability     *string
	PolicyDecision string
	Input          json.RawMessage
	Output         json.RawMessage
	Status         string
	ErrorMessage   *string
	StartedAt      *time.Time
	CompletedAt    *time.Time
	DurationMS     *int
	Metadata       json.RawMessage
	CreatedAt      time.Time
}

type ToolExecutionRepository struct {
	db queryExecutor
}

func NewToolExecutionRepository(pool *pgxpool.Pool) *ToolExecutionRepository {
	return &ToolExecutionRepository{db: pool}
}

func (r *ToolExecutionRepository) Create(ctx context.Context, exec ToolExecution) (ToolExecution, error) {
	input := normalizeJSON(exec.Input, json.RawMessage(`{}`))
	metadata := normalizeJSON(exec.Metadata, json.RawMessage(`{}`))
	status := normalizeToolExecutionStatus(exec.Status)
	if status == "" {
		status = "pending"
	}

	row := r.db.QueryRow(ctx, `
		INSERT INTO tool_execution (
			run_id,
			run_step_id,
			run_attempt_id,
			tool_name,
			tool_tier,
			tool_domain,
			capability,
			policy_decision,
			input,
			output,
			status,
			error_message,
			started_at,
			completed_at,
			duration_ms,
			metadata
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9::jsonb, NULLIF($10::jsonb, 'null'::jsonb), $11, $12, $13, $14, $15, $16::jsonb
		)
		RETURNING id, run_id, run_step_id, run_attempt_id, tool_name, tool_tier, tool_domain, capability,
		          policy_decision, input, output, status, error_message, started_at, completed_at, duration_ms,
		          metadata, created_at
	`,
		exec.RunID,
		exec.RunStepID,
		exec.RunAttemptID,
		strings.TrimSpace(exec.ToolName),
		strings.TrimSpace(exec.ToolTier),
		strings.TrimSpace(exec.ToolDomain),
		trimStringPointer(exec.Capability),
		strings.TrimSpace(exec.PolicyDecision),
		input,
		nullableJSON(exec.Output),
		status,
		trimStringPointer(exec.ErrorMessage),
		exec.StartedAt,
		exec.CompletedAt,
		exec.DurationMS,
		metadata,
	)

	item, err := scanToolExecution(row)
	if err != nil {
		return ToolExecution{}, mapDBError(err)
	}
	return item, nil
}

func (r *ToolExecutionRepository) Get(ctx context.Context, id uuid.UUID) (ToolExecution, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, run_id, run_step_id, run_attempt_id, tool_name, tool_tier, tool_domain, capability,
		       policy_decision, input, output, status, error_message, started_at, completed_at, duration_ms,
		       metadata, created_at
		FROM tool_execution
		WHERE id = $1
	`, id)
	item, err := scanToolExecution(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ToolExecution{}, ErrNotFound
	}
	if err != nil {
		return ToolExecution{}, mapDBError(err)
	}
	return item, nil
}

func (r *ToolExecutionRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string, output json.RawMessage, errorMessage *string) (ToolExecution, error) {
	status = normalizeToolExecutionStatus(status)
	if status == "" {
		return ToolExecution{}, fmt.Errorf("invalid status")
	}

	row := r.db.QueryRow(ctx, `
		UPDATE tool_execution
		SET status = $2,
		    output = COALESCE(NULLIF($3::jsonb, 'null'::jsonb), output),
		    error_message = $4,
		    started_at = CASE WHEN $2 = 'in_progress' THEN COALESCE(started_at, now()) ELSE started_at END,
		    completed_at = CASE WHEN $2 IN ('completed', 'failed', 'policy_denied', 'timed_out') THEN COALESCE(completed_at, now()) ELSE completed_at END,
		    duration_ms = CASE
		        WHEN $2 IN ('completed', 'failed', 'policy_denied', 'timed_out') AND started_at IS NOT NULL
		            THEN GREATEST(EXTRACT(EPOCH FROM (COALESCE(completed_at, now()) - started_at)) * 1000, 0)::int
		        ELSE duration_ms
		    END
		WHERE id = $1
		RETURNING id, run_id, run_step_id, run_attempt_id, tool_name, tool_tier, tool_domain, capability,
		          policy_decision, input, output, status, error_message, started_at, completed_at, duration_ms,
		          metadata, created_at
	`, id, status, nullableJSON(output), trimStringPointer(errorMessage))
	item, err := scanToolExecution(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ToolExecution{}, ErrNotFound
	}
	if err != nil {
		return ToolExecution{}, mapDBError(err)
	}
	return item, nil
}

func (r *ToolExecutionRepository) ListByRun(ctx context.Context, runID uuid.UUID) ([]ToolExecution, error) {
	return r.list(ctx, `
		SELECT id, run_id, run_step_id, run_attempt_id, tool_name, tool_tier, tool_domain, capability,
		       policy_decision, input, output, status, error_message, started_at, completed_at, duration_ms,
		       metadata, created_at
		FROM tool_execution
		WHERE run_id = $1
		ORDER BY created_at ASC
	`, runID)
}

func (r *ToolExecutionRepository) ListByStep(ctx context.Context, runStepID uuid.UUID) ([]ToolExecution, error) {
	return r.list(ctx, `
		SELECT id, run_id, run_step_id, run_attempt_id, tool_name, tool_tier, tool_domain, capability,
		       policy_decision, input, output, status, error_message, started_at, completed_at, duration_ms,
		       metadata, created_at
		FROM tool_execution
		WHERE run_step_id = $1
		ORDER BY created_at ASC
	`, runStepID)
}

func (r *ToolExecutionRepository) ListDenied(ctx context.Context, runID uuid.UUID) ([]ToolExecution, error) {
	return r.list(ctx, `
		SELECT id, run_id, run_step_id, run_attempt_id, tool_name, tool_tier, tool_domain, capability,
		       policy_decision, input, output, status, error_message, started_at, completed_at, duration_ms,
		       metadata, created_at
		FROM tool_execution
		WHERE run_id = $1
		  AND policy_decision = 'denied'
		ORDER BY created_at ASC
	`, runID)
}

func (r *ToolExecutionRepository) list(ctx context.Context, query string, arg any) ([]ToolExecution, error) {
	rows, err := r.db.Query(ctx, query, arg)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ToolExecution, 0)
	for rows.Next() {
		item, scanErr := scanToolExecution(rows)
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

type RunArtifact struct {
	ID            uuid.UUID
	RunID         uuid.UUID
	RunStepID     *uuid.UUID
	RunAttemptID  *uuid.UUID
	ArtifactType  string
	StorageKey    string
	ContentType   string
	ByteSize      int
	InlineContent *string
	Filename      *string
	Metadata      json.RawMessage
	CreatedAt     time.Time
}

type RunArtifactRepository struct {
	db queryExecutor
}

func NewRunArtifactRepository(pool *pgxpool.Pool) *RunArtifactRepository {
	return &RunArtifactRepository{db: pool}
}

func (r *RunArtifactRepository) Create(ctx context.Context, artifact RunArtifact) (RunArtifact, error) {
	if artifact.ByteSize > maxInlineContentBytes && trimStringPointer(artifact.InlineContent) != nil {
		return RunArtifact{}, ErrInlineContentTooLarge
	}
	if isImageContentType(artifact.ContentType) && trimStringPointer(artifact.InlineContent) != nil {
		return RunArtifact{}, ErrInlineContentDisallowed
	}
	metadata := normalizeJSON(artifact.Metadata, json.RawMessage(`{}`))
	inline := trimStringPointer(artifact.InlineContent)
	if artifact.ByteSize > maxInlineContentBytes || isImageContentType(artifact.ContentType) {
		inline = nil
	}

	row := r.db.QueryRow(ctx, `
		INSERT INTO run_artifact (
			run_id,
			run_step_id,
			run_attempt_id,
			artifact_type,
			storage_key,
			content_type,
			byte_size,
			inline_content,
			filename,
			metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)
		RETURNING id, run_id, run_step_id, run_attempt_id, artifact_type, storage_key,
		          content_type, byte_size, inline_content, filename, metadata, created_at
	`,
		artifact.RunID,
		artifact.RunStepID,
		artifact.RunAttemptID,
		strings.TrimSpace(artifact.ArtifactType),
		strings.TrimSpace(artifact.StorageKey),
		strings.TrimSpace(artifact.ContentType),
		artifact.ByteSize,
		inline,
		trimStringPointer(artifact.Filename),
		metadata,
	)

	item, err := scanRunArtifact(row)
	if err != nil {
		return RunArtifact{}, mapDBError(err)
	}
	return item, nil
}

func (r *RunArtifactRepository) Get(ctx context.Context, id uuid.UUID) (RunArtifact, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, run_id, run_step_id, run_attempt_id, artifact_type, storage_key,
		       content_type, byte_size, inline_content, filename, metadata, created_at
		FROM run_artifact
		WHERE id = $1
	`, id)
	item, err := scanRunArtifact(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunArtifact{}, ErrNotFound
	}
	if err != nil {
		return RunArtifact{}, mapDBError(err)
	}
	return item, nil
}

func (r *RunArtifactRepository) GetByStorageKey(ctx context.Context, storageKey string) (RunArtifact, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, run_id, run_step_id, run_attempt_id, artifact_type, storage_key,
		       content_type, byte_size, inline_content, filename, metadata, created_at
		FROM run_artifact
		WHERE storage_key = $1
	`, strings.TrimSpace(storageKey))
	item, err := scanRunArtifact(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunArtifact{}, ErrNotFound
	}
	if err != nil {
		return RunArtifact{}, mapDBError(err)
	}
	return item, nil
}

func (r *RunArtifactRepository) ListByRun(ctx context.Context, runID uuid.UUID) ([]RunArtifact, error) {
	return r.list(ctx, `
		SELECT id, run_id, run_step_id, run_attempt_id, artifact_type, storage_key,
		       content_type, byte_size, inline_content, filename, metadata, created_at
		FROM run_artifact
		WHERE run_id = $1
		ORDER BY created_at ASC
	`, runID)
}

func (r *RunArtifactRepository) ListByStep(ctx context.Context, runStepID uuid.UUID) ([]RunArtifact, error) {
	return r.list(ctx, `
		SELECT id, run_id, run_step_id, run_attempt_id, artifact_type, storage_key,
		       content_type, byte_size, inline_content, filename, metadata, created_at
		FROM run_artifact
		WHERE run_step_id = $1
		ORDER BY created_at ASC
	`, runStepID)
}

func (r *RunArtifactRepository) list(ctx context.Context, query string, arg any) ([]RunArtifact, error) {
	rows, err := r.db.Query(ctx, query, arg)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]RunArtifact, 0)
	for rows.Next() {
		item, scanErr := scanRunArtifact(rows)
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

type RunEvent struct {
	ID           uuid.UUID
	RunID        uuid.UUID
	RunStepID    *uuid.UUID
	RunAttemptID *uuid.UUID
	Sequence     int
	EventType    string
	ActorType    string
	ActorID      *uuid.UUID
	Payload      json.RawMessage
	CreatedAt    time.Time
}

type RunEventRepository struct {
	pool *pgxpool.Pool
	db   queryExecutor
}

func NewRunEventRepository(pool *pgxpool.Pool) *RunEventRepository {
	return &RunEventRepository{pool: pool, db: pool}
}

func (r *RunEventRepository) Append(ctx context.Context, event RunEvent) (RunEvent, error) {
	if r.pool == nil {
		return RunEvent{}, fmt.Errorf("run event repository requires a database pool")
	}
	if event.RunID == uuid.Nil {
		return RunEvent{}, fmt.Errorf("run_id is required")
	}
	eventType := normalizeRunEventType(event.EventType)
	if eventType == "" {
		return RunEvent{}, fmt.Errorf("invalid event_type")
	}
	actorType := normalizeRunEventActorType(event.ActorType)
	if actorType == "" {
		return RunEvent{}, fmt.Errorf("invalid actor_type")
	}
	if (actorType == "human_user" || actorType == "agent") && (event.ActorID == nil || *event.ActorID == uuid.Nil) {
		return RunEvent{}, fmt.Errorf("actor_id is required for actor_type %s", actorType)
	}
	if actorType == "system" || actorType == "supervisor" {
		event.ActorID = nil
	}
	payload := normalizeJSON(event.Payload, json.RawMessage(`{}`))

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return RunEvent{}, mapDBError(err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var runID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM run WHERE id = $1 FOR UPDATE`, event.RunID).Scan(&runID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RunEvent{}, ErrNotFound
		}
		return RunEvent{}, mapDBError(err)
	}

	var nextSequence int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(sequence), 0) + 1
		FROM run_event
		WHERE run_id = $1
	`, runID).Scan(&nextSequence); err != nil {
		return RunEvent{}, mapDBError(err)
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO run_event (
			run_id,
			run_step_id,
			run_attempt_id,
			sequence,
			event_type,
			actor_type,
			actor_id,
			payload
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb)
		RETURNING id, run_id, run_step_id, run_attempt_id, sequence, event_type, actor_type, actor_id, payload, created_at
	`, runID, event.RunStepID, event.RunAttemptID, nextSequence, eventType, actorType, event.ActorID, payload)

	created, err := scanRunEvent(row)
	if err != nil {
		return RunEvent{}, mapDBError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RunEvent{}, mapDBError(err)
	}
	return created, nil
}

func (r *RunEventRepository) ListByRun(ctx context.Context, runID uuid.UUID, fromSequence int) ([]RunEvent, error) {
	if fromSequence < 0 {
		fromSequence = 0
	}
	rows, err := r.db.Query(ctx, `
		SELECT id, run_id, run_step_id, run_attempt_id, sequence, event_type, actor_type, actor_id, payload, created_at
		FROM run_event
		WHERE run_id = $1
		  AND sequence > $2
		ORDER BY sequence ASC
	`, runID, fromSequence)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]RunEvent, 0)
	for rows.Next() {
		item, scanErr := scanRunEvent(rows)
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

func (r *RunEventRepository) GetLatestHeartbeat(ctx context.Context, runID uuid.UUID) (RunEvent, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, run_id, run_step_id, run_attempt_id, sequence, event_type, actor_type, actor_id, payload, created_at
		FROM run_event
		WHERE run_id = $1
		  AND event_type = 'heartbeat'
		ORDER BY created_at DESC, sequence DESC
		LIMIT 1
	`, runID)
	item, err := scanRunEvent(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunEvent{}, ErrNotFound
	}
	if err != nil {
		return RunEvent{}, mapDBError(err)
	}
	return item, nil
}

func scanRun(row pgx.Row) (Run, error) {
	var item Run
	if err := row.Scan(
		&item.ID,
		&item.OrganizationID,
		&item.ProjectID,
		&item.TaskID,
		&item.FlowNodeID,
		&item.SessionID,
		&item.TurnID,
		&item.PrincipalType,
		&item.PrincipalID,
		&item.Status,
		&item.IdempotencyKey,
		&item.TriggerType,
		&item.Version,
		&item.FailureReason,
		&item.FailureClass,
		&item.Metadata,
		&item.CreatedAt,
		&item.UpdatedAt,
		&item.StartedAt,
		&item.CompletedAt,
	); err != nil {
		return Run{}, err
	}
	item.Metadata = normalizeJSON(item.Metadata, json.RawMessage(`{}`))
	return item, nil
}

func scanRunStep(row pgx.Row) (RunStep, error) {
	var item RunStep
	if err := row.Scan(
		&item.ID,
		&item.RunID,
		&item.StepNumber,
		&item.Status,
		&item.ToolName,
		&item.ToolTier,
		&item.StartedAt,
		&item.CompletedAt,
		&item.Metadata,
		&item.CreatedAt,
	); err != nil {
		return RunStep{}, err
	}
	item.Metadata = normalizeJSON(item.Metadata, json.RawMessage(`{}`))
	return item, nil
}

func scanRunAttempt(row pgx.Row) (RunAttempt, error) {
	var item RunAttempt
	if err := row.Scan(
		&item.ID,
		&item.RunStepID,
		&item.AttemptNumber,
		&item.Trigger,
		&item.Status,
		&item.FailureReason,
		&item.FailureClass,
		&item.Output,
		&item.OutputSummary,
		&item.WorkerType,
		&item.WorkerID,
		&item.InputTokens,
		&item.OutputTokens,
		&item.StartedAt,
		&item.CompletedAt,
		&item.DurationMS,
		&item.Metadata,
		&item.CreatedAt,
	); err != nil {
		return RunAttempt{}, err
	}
	item.Output = normalizeNullableJSON(item.Output)
	item.Metadata = normalizeJSON(item.Metadata, json.RawMessage(`{}`))
	return item, nil
}

func scanToolExecution(row pgx.Row) (ToolExecution, error) {
	var item ToolExecution
	if err := row.Scan(
		&item.ID,
		&item.RunID,
		&item.RunStepID,
		&item.RunAttemptID,
		&item.ToolName,
		&item.ToolTier,
		&item.ToolDomain,
		&item.Capability,
		&item.PolicyDecision,
		&item.Input,
		&item.Output,
		&item.Status,
		&item.ErrorMessage,
		&item.StartedAt,
		&item.CompletedAt,
		&item.DurationMS,
		&item.Metadata,
		&item.CreatedAt,
	); err != nil {
		return ToolExecution{}, err
	}
	item.Input = normalizeJSON(item.Input, json.RawMessage(`{}`))
	item.Output = normalizeNullableJSON(item.Output)
	item.Metadata = normalizeJSON(item.Metadata, json.RawMessage(`{}`))
	return item, nil
}

func scanRunArtifact(row pgx.Row) (RunArtifact, error) {
	var item RunArtifact
	if err := row.Scan(
		&item.ID,
		&item.RunID,
		&item.RunStepID,
		&item.RunAttemptID,
		&item.ArtifactType,
		&item.StorageKey,
		&item.ContentType,
		&item.ByteSize,
		&item.InlineContent,
		&item.Filename,
		&item.Metadata,
		&item.CreatedAt,
	); err != nil {
		return RunArtifact{}, err
	}
	item.Metadata = normalizeJSON(item.Metadata, json.RawMessage(`{}`))
	return item, nil
}

func scanRunEvent(row pgx.Row) (RunEvent, error) {
	var item RunEvent
	if err := row.Scan(
		&item.ID,
		&item.RunID,
		&item.RunStepID,
		&item.RunAttemptID,
		&item.Sequence,
		&item.EventType,
		&item.ActorType,
		&item.ActorID,
		&item.Payload,
		&item.CreatedAt,
	); err != nil {
		return RunEvent{}, err
	}
	item.Payload = normalizeJSON(item.Payload, json.RawMessage(`{}`))
	return item, nil
}

func normalizeRunStatus(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "created", "in_progress", "paused", "completed", "failed", "timed_out", "cancelled", "cancelling", "dead_letter":
		return strings.TrimSpace(strings.ToLower(value))
	default:
		return ""
	}
}

func normalizeRunStepStatus(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "pending", "in_progress", "completed", "failed", "cancelled", "skipped":
		return strings.TrimSpace(strings.ToLower(value))
	default:
		return ""
	}
}

func normalizeRunAttemptStatus(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "pending", "in_progress", "completed", "failed", "timed_out", "cancelled":
		return strings.TrimSpace(strings.ToLower(value))
	default:
		return ""
	}
}

func normalizeRunAttemptTrigger(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "initial", "retry_transient", "retry_policy", "supervisor_recovery":
		return strings.TrimSpace(strings.ToLower(value))
	default:
		return ""
	}
}

func normalizeToolExecutionStatus(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "pending", "in_progress", "completed", "failed", "policy_denied", "timed_out":
		return strings.TrimSpace(strings.ToLower(value))
	default:
		return ""
	}
}

func normalizeRunEventType(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "run_started", "run_completed", "run_failed", "run_cancelled", "run_timed_out", "run_paused",
		"step_started", "step_completed", "step_failed", "attempt_started", "attempt_completed", "attempt_failed",
		"tool_called", "tool_returned", "heartbeat", "output_chunk", "policy_denied", "budget_exceeded", "supervisor_recovery":
		return strings.TrimSpace(strings.ToLower(value))
	default:
		return ""
	}
}

func normalizeRunEventActorType(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "human_user", "agent", "system", "supervisor":
		return strings.TrimSpace(strings.ToLower(value))
	default:
		return ""
	}
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return defaultListLimit
	}
	if limit > maxListLimit {
		return maxListLimit
	}
	return limit
}

func normalizeJSON(value json.RawMessage, fallback json.RawMessage) json.RawMessage {
	if len(value) == 0 || !json.Valid(value) {
		return fallback
	}
	return value
}

func nullableJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	if !json.Valid(value) {
		return nil
	}
	return value
}

func normalizeNullableJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	if !json.Valid(value) {
		return nil
	}
	return value
}

func trimStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func isImageContentType(contentType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(contentType))
	return normalized == "image/png" || normalized == "image/jpeg"
}

func mapDBError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23503", "23514":
			return fmt.Errorf("%w: %s", repo.ErrConflict, pgErr.Message)
		}
	}
	return err
}
