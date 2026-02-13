package store

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

const (
	AgentJobScheduleCron     = "cron"
	AgentJobScheduleInterval = "interval"
	AgentJobScheduleOnce     = "once"
)

const (
	AgentJobPayloadMessage     = "message"
	AgentJobPayloadSystemEvent = "system_event"
)

const (
	AgentJobStatusActive    = "active"
	AgentJobStatusPaused    = "paused"
	AgentJobStatusCompleted = "completed"
	AgentJobStatusFailed    = "failed"
)

const (
	AgentJobRunStatusRunning = "running"
	AgentJobRunStatusSuccess = "success"
	AgentJobRunStatusError   = "error"
	AgentJobRunStatusTimeout = "timeout"
	AgentJobRunStatusSkipped = "skipped"
)

type AgentJob struct {
	ID                  string
	OrgID               string
	AgentID             string
	Name                string
	Description         *string
	ScheduleKind        string
	CronExpr            *string
	IntervalMS          *int64
	RunAt               *time.Time
	Timezone            string
	PayloadKind         string
	PayloadText         string
	RoomID              *string
	Enabled             bool
	Status              string
	LastRunAt           *time.Time
	LastRunStatus       *string
	LastRunError        *string
	NextRunAt           *time.Time
	RunCount            int
	ErrorCount          int
	MaxFailures         int
	ConsecutiveFailures int
	CreatedBy           *string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type AgentJobRun struct {
	ID          string
	JobID       string
	OrgID       string
	Status      string
	StartedAt   time.Time
	CompletedAt *time.Time
	DurationMS  *int
	Error       *string
	PayloadText string
	MessageID   *string
	CreatedAt   time.Time
}

type CreateAgentJobInput struct {
	AgentID      string
	Name         string
	Description  *string
	ScheduleKind string
	CronExpr     *string
	IntervalMS   *int64
	RunAt        *time.Time
	Timezone     *string
	PayloadKind  string
	PayloadText  string
	RoomID       *string
	Enabled      *bool
	Status       *string
	NextRunAt    *time.Time
	MaxFailures  *int
	CreatedBy    *string
}

type UpdateAgentJobInput struct {
	Name         *string
	Description  *string
	ScheduleKind *string
	CronExpr     *string
	IntervalMS   *int64
	RunAt        *time.Time
	Timezone     *string
	PayloadKind  *string
	PayloadText  *string
	RoomID       *string
	Enabled      *bool
	Status       *string
	NextRunAt    *time.Time
	MaxFailures  *int
}

type AgentJobFilter struct {
	AgentID *string
	Status  *string
	Enabled *bool
	Limit   int
}

type StartAgentJobRunInput struct {
	JobID       string
	PayloadText string
	StartedAt   time.Time
}

type CompleteAgentJobRunInput struct {
	JobID       string
	RunID       string
	RunStatus   string
	CompletedAt time.Time
	MessageID   *string
	RunError    *string
	NextRunAt   *time.Time
	CompleteJob bool
}

type CreateAgentJobMessageInput struct {
	JobID       string
	OrgID       string
	RoomID      string
	PayloadKind string
	PayloadText string
	CreatedAt   time.Time
}

type AgentJobStore struct {
	db *sql.DB
}

func NewAgentJobStore(db *sql.DB) *AgentJobStore {
	return &AgentJobStore{db: db}
}

const agentJobColumns = `
	id,
	org_id,
	agent_id,
	name,
	description,
	schedule_kind,
	cron_expr,
	interval_ms,
	run_at,
	timezone,
	payload_kind,
	payload_text,
	room_id,
	enabled,
	status,
	last_run_at,
	last_run_status,
	last_run_error,
	next_run_at,
	run_count,
	error_count,
	max_failures,
	consecutive_failures,
	created_by,
	created_at,
	updated_at
`

const agentJobRunColumns = `
	id,
	job_id,
	org_id,
	status,
	started_at,
	completed_at,
	duration_ms,
	error,
	payload_text,
	message_id,
	created_at
`

func (s *AgentJobStore) Create(ctx context.Context, input CreateAgentJobInput) (*AgentJob, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("agent job store is not configured")
	}

	workspaceID := strings.TrimSpace(middleware.WorkspaceFromContext(ctx))
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	normalizedInput, err := normalizeCreateAgentJobInput(input)
	if err != nil {
		return nil, err
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	job, err := scanAgentJob(conn.QueryRowContext(
		ctx,
		`INSERT INTO agent_jobs (
			org_id,
			agent_id,
			name,
			description,
			schedule_kind,
			cron_expr,
			interval_ms,
			run_at,
			timezone,
			payload_kind,
			payload_text,
			room_id,
			enabled,
			status,
			next_run_at,
			max_failures,
			created_by
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9,
			$10, $11, $12, $13, $14, $15, $16, $17
		)
		RETURNING`+agentJobColumns,
		workspaceID,
		normalizedInput.AgentID,
		normalizedInput.Name,
		nullableString(normalizedInput.Description),
		normalizedInput.ScheduleKind,
		nullableString(normalizedInput.CronExpr),
		nullableInt64(normalizedInput.IntervalMS),
		nullableTime(normalizedInput.RunAt),
		normalizedInput.Timezone,
		normalizedInput.PayloadKind,
		normalizedInput.PayloadText,
		nullableString(normalizedInput.RoomID),
		normalizedInput.Enabled,
		normalizedInput.Status,
		nullableTime(normalizedInput.NextRunAt),
		normalizedInput.MaxFailures,
		nullableString(normalizedInput.CreatedBy),
	))
	if err != nil {
		if isForeignKeyViolation(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to create agent job: %w", err)
	}

	return &job, nil
}

func (s *AgentJobStore) GetByID(ctx context.Context, jobID string) (*AgentJob, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("agent job store is not configured")
	}
	jobID = strings.TrimSpace(jobID)
	if !uuidRegex.MatchString(jobID) {
		return nil, fmt.Errorf("%w: invalid job_id", ErrValidation)
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	job, err := scanAgentJob(conn.QueryRowContext(
		ctx,
		`SELECT`+agentJobColumns+` FROM agent_jobs WHERE id = $1`,
		jobID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get agent job: %w", err)
	}

	return &job, nil
}

func (s *AgentJobStore) List(ctx context.Context, filter AgentJobFilter) ([]AgentJob, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("agent job store is not configured")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	where := []string{"1=1"}
	args := []any{}
	argPos := 1

	if filter.AgentID != nil {
		agentID := strings.TrimSpace(*filter.AgentID)
		if !uuidRegex.MatchString(agentID) {
			return nil, fmt.Errorf("%w: invalid agent_id", ErrValidation)
		}
		where = append(where, fmt.Sprintf("agent_id = $%d", argPos))
		args = append(args, agentID)
		argPos++
	}
	if filter.Status != nil {
		status, err := normalizeAgentJobStatus(*filter.Status)
		if err != nil {
			return nil, err
		}
		where = append(where, fmt.Sprintf("status = $%d", argPos))
		args = append(args, status)
		argPos++
	}
	if filter.Enabled != nil {
		where = append(where, fmt.Sprintf("enabled = $%d", argPos))
		args = append(args, *filter.Enabled)
		argPos++
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	args = append(args, limit)

	query := fmt.Sprintf(
		`SELECT%s
		 FROM agent_jobs
		 WHERE %s
		 ORDER BY created_at ASC, id ASC
		 LIMIT $%d`,
		agentJobColumns,
		strings.Join(where, " AND "),
		argPos,
	)
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list agent jobs: %w", err)
	}
	defer rows.Close()

	out := make([]AgentJob, 0, limit)
	for rows.Next() {
		job, scanErr := scanAgentJob(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan agent job: %w", scanErr)
		}
		out = append(out, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read agent jobs: %w", err)
	}

	return out, nil
}

func (s *AgentJobStore) Update(ctx context.Context, jobID string, input UpdateAgentJobInput) (*AgentJob, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("agent job store is not configured")
	}
	jobID = strings.TrimSpace(jobID)
	if !uuidRegex.MatchString(jobID) {
		return nil, fmt.Errorf("%w: invalid job_id", ErrValidation)
	}

	existing, err := s.GetByID(ctx, jobID)
	if err != nil {
		return nil, err
	}
	updated := *existing

	if input.Name != nil {
		updated.Name = strings.TrimSpace(*input.Name)
	}
	if input.Description != nil {
		description := strings.TrimSpace(*input.Description)
		if description == "" {
			updated.Description = nil
		} else {
			updated.Description = &description
		}
	}
	if input.ScheduleKind != nil {
		scheduleKind, scheduleErr := normalizeAgentJobScheduleKind(*input.ScheduleKind)
		if scheduleErr != nil {
			return nil, scheduleErr
		}
		updated.ScheduleKind = scheduleKind
	}
	if input.CronExpr != nil {
		cronExpr := strings.TrimSpace(*input.CronExpr)
		if cronExpr == "" {
			updated.CronExpr = nil
		} else {
			updated.CronExpr = &cronExpr
		}
	}
	if input.IntervalMS != nil {
		interval := *input.IntervalMS
		updated.IntervalMS = &interval
	}
	if input.RunAt != nil {
		runAt := input.RunAt.UTC()
		updated.RunAt = &runAt
	}
	if input.Timezone != nil {
		updated.Timezone = strings.TrimSpace(*input.Timezone)
	}
	if input.PayloadKind != nil {
		payloadKind, payloadErr := normalizeAgentJobPayloadKind(*input.PayloadKind)
		if payloadErr != nil {
			return nil, payloadErr
		}
		updated.PayloadKind = payloadKind
	}
	if input.PayloadText != nil {
		updated.PayloadText = strings.TrimSpace(*input.PayloadText)
	}
	if input.RoomID != nil {
		roomID := strings.TrimSpace(*input.RoomID)
		if roomID == "" {
			updated.RoomID = nil
		} else {
			updated.RoomID = &roomID
		}
	}
	if input.Enabled != nil {
		updated.Enabled = *input.Enabled
	}
	if input.Status != nil {
		status, statusErr := normalizeAgentJobStatus(*input.Status)
		if statusErr != nil {
			return nil, statusErr
		}
		updated.Status = status
	}
	if input.NextRunAt != nil {
		nextRunAt := input.NextRunAt.UTC()
		updated.NextRunAt = &nextRunAt
	}
	if input.MaxFailures != nil {
		updated.MaxFailures = *input.MaxFailures
	}

	if err := validateAgentJobRecord(updated); err != nil {
		return nil, err
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	job, err := scanAgentJob(conn.QueryRowContext(
		ctx,
		`UPDATE agent_jobs
		 SET name = $2,
		     description = $3,
		     schedule_kind = $4,
		     cron_expr = $5,
		     interval_ms = $6,
		     run_at = $7,
		     timezone = $8,
		     payload_kind = $9,
		     payload_text = $10,
		     room_id = $11,
		     enabled = $12,
		     status = $13,
		     next_run_at = $14,
		     max_failures = $15,
		     updated_at = NOW()
		 WHERE id = $1
		 RETURNING`+agentJobColumns,
		jobID,
		updated.Name,
		nullableString(updated.Description),
		updated.ScheduleKind,
		nullableString(updated.CronExpr),
		nullableInt64(updated.IntervalMS),
		nullableTime(updated.RunAt),
		updated.Timezone,
		updated.PayloadKind,
		updated.PayloadText,
		nullableString(updated.RoomID),
		updated.Enabled,
		updated.Status,
		nullableTime(updated.NextRunAt),
		updated.MaxFailures,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if isForeignKeyViolation(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to update agent job: %w", err)
	}

	return &job, nil
}

func (s *AgentJobStore) Delete(ctx context.Context, jobID string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("agent job store is not configured")
	}
	jobID = strings.TrimSpace(jobID)
	if !uuidRegex.MatchString(jobID) {
		return fmt.Errorf("%w: invalid job_id", ErrValidation)
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return err
	}
	defer conn.Close()

	result, err := conn.ExecContext(ctx, `DELETE FROM agent_jobs WHERE id = $1`, jobID)
	if err != nil {
		return fmt.Errorf("failed to delete agent job: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to delete agent job: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *AgentJobStore) PickupDue(ctx context.Context, limit int, now time.Time) ([]AgentJob, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("agent job store is not configured")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(
		ctx,
		`WITH due_jobs AS (
			SELECT id
			FROM agent_jobs
			WHERE enabled = true
			  AND status = 'active'
			  AND next_run_at IS NOT NULL
			  AND next_run_at <= $1
			ORDER BY next_run_at ASC, created_at ASC
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE agent_jobs j
		SET next_run_at = NULL,
		    updated_at = NOW()
		FROM due_jobs d
		WHERE j.id = d.id
		RETURNING`+agentJobColumns,
		now.UTC(),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to pick up due jobs: %w", err)
	}
	defer rows.Close()

	out := make([]AgentJob, 0, limit)
	for rows.Next() {
		job, scanErr := scanAgentJob(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan due job: %w", scanErr)
		}
		out = append(out, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read due jobs: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit due job pickup: %w", err)
	}
	return out, nil
}

func (s *AgentJobStore) StartRun(ctx context.Context, input StartAgentJobRunInput) (*AgentJobRun, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("agent job store is not configured")
	}
	jobID := strings.TrimSpace(input.JobID)
	if !uuidRegex.MatchString(jobID) {
		return nil, fmt.Errorf("%w: invalid job_id", ErrValidation)
	}
	payloadText := strings.TrimSpace(input.PayloadText)
	if payloadText == "" {
		return nil, fmt.Errorf("%w: payload_text is required", ErrValidation)
	}
	startedAt := input.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	} else {
		startedAt = startedAt.UTC()
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	run, err := scanAgentJobRun(conn.QueryRowContext(
		ctx,
		`INSERT INTO agent_job_runs (job_id, org_id, status, started_at, payload_text)
		 SELECT id, org_id, $2, $3, $4
		 FROM agent_jobs
		 WHERE id = $1
		 RETURNING`+agentJobRunColumns,
		jobID,
		AgentJobRunStatusRunning,
		startedAt,
		payloadText,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to start agent job run: %w", err)
	}
	return &run, nil
}

func (s *AgentJobStore) CompleteRun(ctx context.Context, input CompleteAgentJobRunInput) (*AgentJob, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("agent job store is not configured")
	}

	jobID := strings.TrimSpace(input.JobID)
	runID := strings.TrimSpace(input.RunID)
	if !uuidRegex.MatchString(jobID) {
		return nil, fmt.Errorf("%w: invalid job_id", ErrValidation)
	}
	if !uuidRegex.MatchString(runID) {
		return nil, fmt.Errorf("%w: invalid run_id", ErrValidation)
	}
	runStatus, err := normalizeAgentJobRunStatus(input.RunStatus)
	if err != nil {
		return nil, err
	}
	if runStatus == AgentJobRunStatusRunning {
		return nil, fmt.Errorf("%w: run_status cannot be running", ErrValidation)
	}

	if input.MessageID != nil {
		messageID := strings.TrimSpace(*input.MessageID)
		if messageID != "" && !uuidRegex.MatchString(messageID) {
			return nil, fmt.Errorf("%w: invalid message_id", ErrValidation)
		}
	}

	completedAt := input.CompletedAt
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	} else {
		completedAt = completedAt.UTC()
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	run, err := scanAgentJobRun(tx.QueryRowContext(
		ctx,
		`SELECT`+agentJobRunColumns+`
		 FROM agent_job_runs
		 WHERE id = $1 AND job_id = $2
		 FOR UPDATE`,
		runID,
		jobID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load agent job run: %w", err)
	}
	if run.Status != AgentJobRunStatusRunning {
		return nil, ErrConflict
	}

	durationMS := int(completedAt.Sub(run.StartedAt).Milliseconds())
	if durationMS < 0 {
		durationMS = 0
	}

	var messageIDValue interface{}
	if input.MessageID != nil && strings.TrimSpace(*input.MessageID) != "" {
		messageIDValue = strings.TrimSpace(*input.MessageID)
	}

	_, err = tx.ExecContext(
		ctx,
		`UPDATE agent_job_runs
		 SET status = $3,
		     completed_at = $4,
		     duration_ms = $5,
		     error = $6,
		     message_id = $7
		 WHERE id = $1 AND job_id = $2`,
		runID,
		jobID,
		runStatus,
		completedAt,
		durationMS,
		nullableString(input.RunError),
		messageIDValue,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to complete agent job run: %w", err)
	}

	job, err := scanAgentJob(tx.QueryRowContext(
		ctx,
		`SELECT`+agentJobColumns+` FROM agent_jobs WHERE id = $1 FOR UPDATE`,
		jobID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load agent job: %w", err)
	}

	nextRunValue := nullableTime(input.NextRunAt)
	lastRunError := nullableString(input.RunError)
	status := job.Status
	runCount := job.RunCount + 1
	errorCount := job.ErrorCount
	consecutiveFailures := job.ConsecutiveFailures

	switch runStatus {
	case AgentJobRunStatusSuccess, AgentJobRunStatusSkipped:
		consecutiveFailures = 0
		lastRunError = nil
	case AgentJobRunStatusError, AgentJobRunStatusTimeout:
		errorCount++
		consecutiveFailures++
		if consecutiveFailures >= job.MaxFailures {
			status = AgentJobStatusPaused
		}
	}
	if input.CompleteJob {
		status = AgentJobStatusCompleted
	}

	updatedJob, err := scanAgentJob(tx.QueryRowContext(
		ctx,
		`UPDATE agent_jobs
		 SET status = $2,
		     last_run_at = $3,
		     last_run_status = $4,
		     last_run_error = $5,
		     next_run_at = $6,
		     run_count = $7,
		     error_count = $8,
		     consecutive_failures = $9,
		     updated_at = NOW()
		 WHERE id = $1
		 RETURNING`+agentJobColumns,
		jobID,
		status,
		completedAt,
		runStatus,
		lastRunError,
		nextRunValue,
		runCount,
		errorCount,
		consecutiveFailures,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to update agent job run summary: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit agent job run completion: %w", err)
	}
	return &updatedJob, nil
}

func (s *AgentJobStore) ListRuns(ctx context.Context, jobID string, limit int) ([]AgentJobRun, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("agent job store is not configured")
	}
	jobID = strings.TrimSpace(jobID)
	if !uuidRegex.MatchString(jobID) {
		return nil, fmt.Errorf("%w: invalid job_id", ErrValidation)
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT`+agentJobRunColumns+`
		 FROM agent_job_runs
		 WHERE job_id = $1
		 ORDER BY started_at DESC, id DESC
		 LIMIT $2`,
		jobID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list agent job runs: %w", err)
	}
	defer rows.Close()

	out := make([]AgentJobRun, 0, limit)
	for rows.Next() {
		run, scanErr := scanAgentJobRun(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan agent job run: %w", scanErr)
		}
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read agent job runs: %w", err)
	}
	return out, nil
}

func (s *AgentJobStore) PruneRunHistory(ctx context.Context, jobID string, maxRuns int) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("agent job store is not configured")
	}
	jobID = strings.TrimSpace(jobID)
	if !uuidRegex.MatchString(jobID) {
		return 0, fmt.Errorf("%w: invalid job_id", ErrValidation)
	}
	if maxRuns <= 0 {
		maxRuns = 100
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	result, err := conn.ExecContext(
		ctx,
		`WITH ranked AS (
			SELECT id,
			       ROW_NUMBER() OVER (
					   PARTITION BY job_id
					   ORDER BY started_at DESC, id DESC
				   ) AS rn
			FROM agent_job_runs
			WHERE job_id = $1
		)
		DELETE FROM agent_job_runs r
		USING ranked
		WHERE r.id = ranked.id
		  AND ranked.rn > $2`,
		jobID,
		maxRuns,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to prune agent job runs: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to prune agent job runs: %w", err)
	}
	return int(affected), nil
}

func (s *AgentJobStore) CleanupStaleRuns(ctx context.Context, olderThan time.Duration, now time.Time) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("agent job store is not configured")
	}
	if olderThan <= 0 {
		olderThan = 5 * time.Minute
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	cutoff := now.Add(-olderThan)
	rows, err := tx.QueryContext(
		ctx,
		`WITH stale AS (
			SELECT id, job_id, started_at
			FROM agent_job_runs
			WHERE status = 'running'
			  AND started_at < $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE agent_job_runs r
		SET status = 'timeout',
		    completed_at = $2,
		    duration_ms = GREATEST(0, FLOOR(EXTRACT(EPOCH FROM ($2 - s.started_at)) * 1000)::INT),
		    error = 'stale running job exceeded timeout'
		FROM stale s
		WHERE r.id = s.id
		RETURNING r.job_id`,
		cutoff,
		now,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup stale runs: %w", err)
	}
	defer rows.Close()

	jobFailures := map[string]int{}
	for rows.Next() {
		var jobID string
		if scanErr := rows.Scan(&jobID); scanErr != nil {
			return 0, fmt.Errorf("failed to scan stale run cleanup: %w", scanErr)
		}
		jobFailures[jobID]++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("failed to read stale run cleanup rows: %w", err)
	}

	for jobID, failures := range jobFailures {
		_, err = tx.ExecContext(
			ctx,
			`UPDATE agent_jobs
			 SET last_run_at = $2,
			     last_run_status = 'timeout',
			     last_run_error = 'stale running job exceeded timeout',
			     run_count = run_count + $3,
			     error_count = error_count + $3,
			     consecutive_failures = consecutive_failures + $3,
			     status = CASE
					WHEN (consecutive_failures + $3) >= max_failures THEN 'paused'
					ELSE status
			     END,
			     updated_at = NOW()
			 WHERE id = $1`,
			jobID,
			now,
			failures,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to update stale job summary: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit stale run cleanup: %w", err)
	}

	total := 0
	for _, failures := range jobFailures {
		total += failures
	}
	return total, nil
}

func (s *AgentJobStore) EnsureRoomForJob(ctx context.Context, jobID string) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("agent job store is not configured")
	}
	jobID = strings.TrimSpace(jobID)
	if !uuidRegex.MatchString(jobID) {
		return "", fmt.Errorf("%w: invalid job_id", ErrValidation)
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	var (
		orgID   string
		agentID string
		roomID  sql.NullString
	)
	err = tx.QueryRowContext(
		ctx,
		`SELECT org_id, agent_id, room_id
		 FROM agent_jobs
		 WHERE id = $1
		 FOR UPDATE`,
		jobID,
	).Scan(&orgID, &agentID, &roomID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("failed to load agent job room: %w", err)
	}
	if roomID.Valid {
		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("failed to commit room lookup: %w", err)
		}
		return roomID.String, nil
	}

	var displayName string
	err = tx.QueryRowContext(
		ctx,
		`SELECT display_name FROM agents WHERE id = $1`,
		agentID,
	).Scan(&displayName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("failed to load agent for room creation: %w", err)
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = "Agent"
	}

	roomName := fmt.Sprintf("Scheduled - %s", displayName)
	var createdRoomID string
	err = tx.QueryRowContext(
		ctx,
		`INSERT INTO rooms (org_id, name, type)
		 VALUES ($1, $2, 'ad_hoc')
		 RETURNING id`,
		orgID,
		roomName,
	).Scan(&createdRoomID)
	if err != nil {
		return "", fmt.Errorf("failed to create scheduled job room: %w", err)
	}

	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO room_participants (org_id, room_id, participant_id, participant_type)
		 VALUES ($1, $2, $3, 'agent')
		 ON CONFLICT (room_id, participant_id) DO NOTHING`,
		orgID,
		createdRoomID,
		agentID,
	)
	if err != nil {
		return "", fmt.Errorf("failed to add scheduled room participant: %w", err)
	}

	_, err = tx.ExecContext(
		ctx,
		`UPDATE agent_jobs
		 SET room_id = $2,
		     updated_at = NOW()
		 WHERE id = $1`,
		jobID,
		createdRoomID,
	)
	if err != nil {
		return "", fmt.Errorf("failed to persist scheduled room reference: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("failed to commit scheduled room creation: %w", err)
	}
	return createdRoomID, nil
}

func (s *AgentJobStore) CreateJobMessage(ctx context.Context, input CreateAgentJobMessageInput) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("agent job store is not configured")
	}

	jobID := strings.TrimSpace(input.JobID)
	orgID := strings.TrimSpace(input.OrgID)
	roomID := strings.TrimSpace(input.RoomID)
	payloadText := strings.TrimSpace(input.PayloadText)
	if !uuidRegex.MatchString(jobID) {
		return "", fmt.Errorf("%w: invalid job_id", ErrValidation)
	}
	if !uuidRegex.MatchString(orgID) {
		return "", fmt.Errorf("%w: invalid org_id", ErrValidation)
	}
	if !uuidRegex.MatchString(roomID) {
		return "", fmt.Errorf("%w: invalid room_id", ErrValidation)
	}
	if payloadText == "" {
		return "", fmt.Errorf("%w: payload_text is required", ErrValidation)
	}
	payloadKind, err := normalizeAgentJobPayloadKind(input.PayloadKind)
	if err != nil {
		return "", err
	}

	senderType := "user"
	messageType := "message"
	if payloadKind == AgentJobPayloadSystemEvent {
		senderType = "system"
		messageType = "system"
	}
	senderID := deterministicAgentJobSenderID(orgID, jobID, payloadKind)

	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	} else {
		createdAt = createdAt.UTC()
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	var messageID string
	err = conn.QueryRowContext(
		ctx,
		`INSERT INTO chat_messages (
			org_id,
			room_id,
			sender_id,
			sender_type,
			body,
			type,
			attachments,
			created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, '[]'::jsonb, $7
		)
		RETURNING id`,
		orgID,
		roomID,
		senderID,
		senderType,
		payloadText,
		messageType,
		createdAt,
	).Scan(&messageID)
	if err != nil {
		return "", fmt.Errorf("failed to create scheduled job message: %w", err)
	}

	return messageID, nil
}

func normalizeCreateAgentJobInput(input CreateAgentJobInput) (CreateAgentJobInput, error) {
	input.AgentID = strings.TrimSpace(input.AgentID)
	if !uuidRegex.MatchString(input.AgentID) {
		return CreateAgentJobInput{}, fmt.Errorf("%w: invalid agent_id", ErrValidation)
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return CreateAgentJobInput{}, fmt.Errorf("%w: name is required", ErrValidation)
	}

	scheduleKind, err := normalizeAgentJobScheduleKind(input.ScheduleKind)
	if err != nil {
		return CreateAgentJobInput{}, err
	}
	input.ScheduleKind = scheduleKind

	payloadKind, err := normalizeAgentJobPayloadKind(input.PayloadKind)
	if err != nil {
		return CreateAgentJobInput{}, err
	}
	input.PayloadKind = payloadKind
	input.PayloadText = strings.TrimSpace(input.PayloadText)
	if input.PayloadText == "" {
		return CreateAgentJobInput{}, fmt.Errorf("%w: payload_text is required", ErrValidation)
	}

	if input.Description != nil {
		description := strings.TrimSpace(*input.Description)
		if description == "" {
			input.Description = nil
		} else {
			input.Description = &description
		}
	}
	if input.CronExpr != nil {
		cronExpr := strings.TrimSpace(*input.CronExpr)
		if cronExpr == "" {
			input.CronExpr = nil
		} else {
			input.CronExpr = &cronExpr
		}
	}
	if input.IntervalMS != nil && *input.IntervalMS <= 0 {
		return CreateAgentJobInput{}, fmt.Errorf("%w: interval_ms must be greater than zero", ErrValidation)
	}
	if input.RunAt != nil {
		runAt := input.RunAt.UTC()
		input.RunAt = &runAt
	}
	if input.RoomID != nil {
		roomID := strings.TrimSpace(*input.RoomID)
		if roomID == "" {
			input.RoomID = nil
		} else if !uuidRegex.MatchString(roomID) {
			return CreateAgentJobInput{}, fmt.Errorf("%w: invalid room_id", ErrValidation)
		} else {
			input.RoomID = &roomID
		}
	}
	if input.NextRunAt != nil {
		nextRunAt := input.NextRunAt.UTC()
		input.NextRunAt = &nextRunAt
	}
	if input.CreatedBy != nil {
		createdBy := strings.TrimSpace(*input.CreatedBy)
		if createdBy == "" {
			input.CreatedBy = nil
		} else if !uuidRegex.MatchString(createdBy) {
			return CreateAgentJobInput{}, fmt.Errorf("%w: invalid created_by", ErrValidation)
		} else {
			input.CreatedBy = &createdBy
		}
	}

	timezone := "UTC"
	if input.Timezone != nil {
		timezone = strings.TrimSpace(*input.Timezone)
		if timezone == "" {
			timezone = "UTC"
		}
	}
	input.Timezone = &timezone

	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	input.Enabled = &enabled

	status := AgentJobStatusActive
	if input.Status != nil {
		normalizedStatus, statusErr := normalizeAgentJobStatus(*input.Status)
		if statusErr != nil {
			return CreateAgentJobInput{}, statusErr
		}
		status = normalizedStatus
	}
	input.Status = &status

	maxFailures := 5
	if input.MaxFailures != nil {
		maxFailures = *input.MaxFailures
	}
	if maxFailures <= 0 {
		return CreateAgentJobInput{}, fmt.Errorf("%w: max_failures must be greater than zero", ErrValidation)
	}
	input.MaxFailures = &maxFailures

	switch input.ScheduleKind {
	case AgentJobScheduleCron:
		if input.CronExpr == nil {
			return CreateAgentJobInput{}, fmt.Errorf("%w: cron_expr is required for cron schedules", ErrValidation)
		}
	case AgentJobScheduleInterval:
		if input.IntervalMS == nil {
			return CreateAgentJobInput{}, fmt.Errorf("%w: interval_ms is required for interval schedules", ErrValidation)
		}
	case AgentJobScheduleOnce:
		if input.RunAt == nil {
			return CreateAgentJobInput{}, fmt.Errorf("%w: run_at is required for once schedules", ErrValidation)
		}
	}

	return input, nil
}

func validateAgentJobRecord(job AgentJob) error {
	if strings.TrimSpace(job.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrValidation)
	}
	if strings.TrimSpace(job.PayloadText) == "" {
		return fmt.Errorf("%w: payload_text is required", ErrValidation)
	}
	if _, err := normalizeAgentJobScheduleKind(job.ScheduleKind); err != nil {
		return err
	}
	if _, err := normalizeAgentJobPayloadKind(job.PayloadKind); err != nil {
		return err
	}
	if _, err := normalizeAgentJobStatus(job.Status); err != nil {
		return err
	}
	if job.IntervalMS != nil && *job.IntervalMS <= 0 {
		return fmt.Errorf("%w: interval_ms must be greater than zero", ErrValidation)
	}
	if job.MaxFailures <= 0 {
		return fmt.Errorf("%w: max_failures must be greater than zero", ErrValidation)
	}
	switch job.ScheduleKind {
	case AgentJobScheduleCron:
		if job.CronExpr == nil || strings.TrimSpace(*job.CronExpr) == "" {
			return fmt.Errorf("%w: cron_expr is required for cron schedules", ErrValidation)
		}
	case AgentJobScheduleInterval:
		if job.IntervalMS == nil {
			return fmt.Errorf("%w: interval_ms is required for interval schedules", ErrValidation)
		}
	case AgentJobScheduleOnce:
		if job.RunAt == nil {
			return fmt.Errorf("%w: run_at is required for once schedules", ErrValidation)
		}
	}
	if job.RoomID != nil && !uuidRegex.MatchString(strings.TrimSpace(*job.RoomID)) {
		return fmt.Errorf("%w: invalid room_id", ErrValidation)
	}
	if strings.TrimSpace(job.Timezone) == "" {
		return fmt.Errorf("%w: timezone is required", ErrValidation)
	}
	return nil
}

func normalizeAgentJobScheduleKind(raw string) (string, error) {
	normalized := strings.TrimSpace(strings.ToLower(raw))
	switch normalized {
	case AgentJobScheduleCron, AgentJobScheduleInterval, AgentJobScheduleOnce:
		return normalized, nil
	default:
		return "", fmt.Errorf("%w: invalid schedule_kind", ErrValidation)
	}
}

func normalizeAgentJobPayloadKind(raw string) (string, error) {
	normalized := strings.TrimSpace(strings.ToLower(raw))
	switch normalized {
	case AgentJobPayloadMessage, AgentJobPayloadSystemEvent:
		return normalized, nil
	default:
		return "", fmt.Errorf("%w: invalid payload_kind", ErrValidation)
	}
}

func normalizeAgentJobStatus(raw string) (string, error) {
	normalized := strings.TrimSpace(strings.ToLower(raw))
	switch normalized {
	case AgentJobStatusActive, AgentJobStatusPaused, AgentJobStatusCompleted, AgentJobStatusFailed:
		return normalized, nil
	default:
		return "", fmt.Errorf("%w: invalid status", ErrValidation)
	}
}

func normalizeAgentJobRunStatus(raw string) (string, error) {
	normalized := strings.TrimSpace(strings.ToLower(raw))
	switch normalized {
	case AgentJobRunStatusRunning, AgentJobRunStatusSuccess, AgentJobRunStatusError, AgentJobRunStatusTimeout, AgentJobRunStatusSkipped:
		return normalized, nil
	default:
		return "", fmt.Errorf("%w: invalid run_status", ErrValidation)
	}
}

func scanAgentJob(scanner interface{ Scan(...any) error }) (AgentJob, error) {
	var (
		job           AgentJob
		description   sql.NullString
		cronExpr      sql.NullString
		intervalMS    sql.NullInt64
		runAt         sql.NullTime
		roomID        sql.NullString
		lastRunAt     sql.NullTime
		lastRunStatus sql.NullString
		lastRunError  sql.NullString
		nextRunAt     sql.NullTime
		createdBy     sql.NullString
	)

	err := scanner.Scan(
		&job.ID,
		&job.OrgID,
		&job.AgentID,
		&job.Name,
		&description,
		&job.ScheduleKind,
		&cronExpr,
		&intervalMS,
		&runAt,
		&job.Timezone,
		&job.PayloadKind,
		&job.PayloadText,
		&roomID,
		&job.Enabled,
		&job.Status,
		&lastRunAt,
		&lastRunStatus,
		&lastRunError,
		&nextRunAt,
		&job.RunCount,
		&job.ErrorCount,
		&job.MaxFailures,
		&job.ConsecutiveFailures,
		&createdBy,
		&job.CreatedAt,
		&job.UpdatedAt,
	)
	if err != nil {
		return AgentJob{}, err
	}

	job.Description = nullableSQLStringPointer(description)
	if cronExpr.Valid {
		value := cronExpr.String
		job.CronExpr = &value
	}
	if intervalMS.Valid {
		value := intervalMS.Int64
		job.IntervalMS = &value
	}
	if runAt.Valid {
		value := runAt.Time.UTC()
		job.RunAt = &value
	}
	if roomID.Valid {
		value := roomID.String
		job.RoomID = &value
	}
	if lastRunAt.Valid {
		value := lastRunAt.Time.UTC()
		job.LastRunAt = &value
	}
	if lastRunStatus.Valid {
		value := lastRunStatus.String
		job.LastRunStatus = &value
	}
	if lastRunError.Valid {
		value := lastRunError.String
		job.LastRunError = &value
	}
	if nextRunAt.Valid {
		value := nextRunAt.Time.UTC()
		job.NextRunAt = &value
	}
	if createdBy.Valid {
		value := createdBy.String
		job.CreatedBy = &value
	}
	job.CreatedAt = job.CreatedAt.UTC()
	job.UpdatedAt = job.UpdatedAt.UTC()

	return job, nil
}

func scanAgentJobRun(scanner interface{ Scan(...any) error }) (AgentJobRun, error) {
	var (
		run         AgentJobRun
		completedAt sql.NullTime
		durationMS  sql.NullInt32
		runError    sql.NullString
		messageID   sql.NullString
	)

	err := scanner.Scan(
		&run.ID,
		&run.JobID,
		&run.OrgID,
		&run.Status,
		&run.StartedAt,
		&completedAt,
		&durationMS,
		&runError,
		&run.PayloadText,
		&messageID,
		&run.CreatedAt,
	)
	if err != nil {
		return AgentJobRun{}, err
	}

	if completedAt.Valid {
		value := completedAt.Time.UTC()
		run.CompletedAt = &value
	}
	if durationMS.Valid {
		value := int(durationMS.Int32)
		run.DurationMS = &value
	}
	if runError.Valid {
		value := runError.String
		run.Error = &value
	}
	if messageID.Valid {
		value := messageID.String
		run.MessageID = &value
	}
	run.StartedAt = run.StartedAt.UTC()
	run.CreatedAt = run.CreatedAt.UTC()

	return run, nil
}

func nullableInt64(value *int64) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func nullableSQLStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	trimmed := strings.TrimSpace(value.String)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func isForeignKeyViolation(err error) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "violates foreign key constraint")
}

func deterministicAgentJobSenderID(orgID, jobID, payloadKind string) string {
	seed := strings.TrimSpace(orgID) + ":" + strings.TrimSpace(jobID) + ":" + strings.TrimSpace(payloadKind)
	sum := md5.Sum([]byte(seed))
	encoded := hex.EncodeToString(sum[:])
	return fmt.Sprintf(
		"%s-%s-%s-%s-%s",
		encoded[0:8],
		encoded[8:12],
		encoded[12:16],
		encoded[16:20],
		encoded[20:32],
	)
}
