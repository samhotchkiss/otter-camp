package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/syncmetrics"
)

const (
	GitHubSyncJobTypeRepoSync    = "repo_sync"
	GitHubSyncJobTypeIssueImport = "issue_import"
	GitHubSyncJobTypeWebhook     = "webhook_event"
)

const (
	GitHubSyncJobStatusQueued     = "queued"
	GitHubSyncJobStatusInProgress = "in_progress"
	GitHubSyncJobStatusRetrying   = "retrying"
	GitHubSyncJobStatusCompleted  = "completed"
	GitHubSyncJobStatusDeadLetter = "dead_letter"
)

type GitHubSyncJob struct {
	ID             string          `json:"id"`
	OrgID          string          `json:"org_id"`
	ProjectID      *string         `json:"project_id,omitempty"`
	JobType        string          `json:"job_type"`
	Status         string          `json:"status"`
	Payload        json.RawMessage `json:"payload"`
	SourceEventID  *string         `json:"source_event_id,omitempty"`
	AttemptCount   int             `json:"attempt_count"`
	MaxAttempts    int             `json:"max_attempts"`
	NextAttemptAt  time.Time       `json:"next_attempt_at"`
	LastError      *string         `json:"last_error,omitempty"`
	LastErrorClass *string         `json:"last_error_class,omitempty"`
	AttemptHistory json.RawMessage `json:"attempt_history"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
}

type GitHubSyncDeadLetter struct {
	ID             string          `json:"id"`
	OrgID          string          `json:"org_id"`
	JobID          string          `json:"job_id"`
	ProjectID      *string         `json:"project_id,omitempty"`
	JobType        string          `json:"job_type"`
	Payload        json.RawMessage `json:"payload"`
	AttemptCount   int             `json:"attempt_count"`
	MaxAttempts    int             `json:"max_attempts"`
	LastError      *string         `json:"last_error,omitempty"`
	LastErrorClass *string         `json:"last_error_class,omitempty"`
	AttemptHistory json.RawMessage `json:"attempt_history"`
	FailedAt       time.Time       `json:"failed_at"`
	ReplayedAt     *time.Time      `json:"replayed_at,omitempty"`
	ReplayedBy     *string         `json:"replayed_by,omitempty"`
}

type GitHubSyncQueueDepth struct {
	JobType    string `json:"job_type"`
	Queued     int    `json:"queued"`
	Retrying   int    `json:"retrying"`
	InProgress int    `json:"in_progress"`
	DeadLetter int    `json:"dead_letter"`
}

type EnqueueGitHubSyncJobInput struct {
	ProjectID     *string
	JobType       string
	Payload       json.RawMessage
	SourceEventID *string
	MaxAttempts   int
}

type RecordGitHubSyncFailureInput struct {
	ErrorClass   string
	ErrorMessage string
	Retryable    bool
	NextAttempt  *time.Time
	OccurredAt   time.Time
}

type RecordGitHubSyncFailureResult struct {
	Job        *GitHubSyncJob
	DeadLetter *GitHubSyncDeadLetter
}

type GitHubSyncJobStore struct {
	db *sql.DB
}

func NewGitHubSyncJobStore(db *sql.DB) *GitHubSyncJobStore {
	return &GitHubSyncJobStore{db: db}
}

const githubSyncJobColumns = `
	id,
	org_id,
	project_id,
	job_type,
	status,
	payload,
	source_event_id,
	attempt_count,
	max_attempts,
	next_attempt_at,
	last_error,
	last_error_class,
	attempt_history,
	created_at,
	updated_at,
	completed_at
`

const githubSyncDeadLetterColumns = `
	id,
	org_id,
	job_id,
	project_id,
	job_type,
	payload,
	attempt_count,
	max_attempts,
	last_error,
	last_error_class,
	attempt_history,
	failed_at,
	replayed_at,
	replayed_by
`

func (s *GitHubSyncJobStore) Enqueue(ctx context.Context, input EnqueueGitHubSyncJobInput) (*GitHubSyncJob, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	jobType := normalizeSyncJobType(input.JobType)
	if !isValidSyncJobType(jobType) {
		return nil, fmt.Errorf("invalid job_type")
	}

	payload := input.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}

	maxAttempts := input.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	job, err := scanGitHubSyncJob(conn.QueryRowContext(
		ctx,
		`INSERT INTO github_sync_jobs (
			org_id,
			project_id,
			job_type,
			payload,
			source_event_id,
			max_attempts
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (org_id, job_type, source_event_id)
		DO UPDATE SET
			payload = EXCLUDED.payload,
			max_attempts = EXCLUDED.max_attempts,
			updated_at = NOW()
		RETURNING`+githubSyncJobColumns,
		workspaceID,
		nullableString(input.ProjectID),
		jobType,
		payload,
		nullableString(input.SourceEventID),
		maxAttempts,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to enqueue github sync job: %w", err)
	}

	return &job, nil
}

func (s *GitHubSyncJobStore) PickupNext(ctx context.Context, jobTypes ...string) (*GitHubSyncJob, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	normalizedTypes, err := normalizeSyncJobTypes(jobTypes)
	if err != nil {
		return nil, err
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	whereClause := `
		j.status IN ('queued', 'retrying')
		AND j.next_attempt_at <= NOW()
		AND (
			j.project_id IS NULL
			OR NOT EXISTS (
				SELECT 1
				FROM github_sync_jobs running
				WHERE running.org_id = j.org_id
					AND running.project_id = j.project_id
					AND running.status = 'in_progress'
			)
		)
	`
	args := []any{}
	if len(normalizedTypes) > 0 {
		whereClause += ` AND j.job_type = ANY($1)`
		args = append(args, pq.Array(normalizedTypes))
	}

	query := fmt.Sprintf(`
		WITH next_job AS (
			SELECT j.id
			FROM github_sync_jobs j
			WHERE %s
			ORDER BY j.next_attempt_at ASC, j.created_at ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE github_sync_jobs
		SET status = $%d,
			attempt_count = attempt_count + 1,
			updated_at = NOW()
		FROM next_job
		WHERE github_sync_jobs.id = next_job.id
		RETURNING%s
	`, whereClause, len(args)+1, githubSyncJobColumns)
	args = append(args, GitHubSyncJobStatusInProgress)

	job, err := scanGitHubSyncJob(tx.QueryRowContext(ctx, query, args...))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to pick up github sync job: %w", err)
	}

	if job.OrgID != workspaceID {
		return nil, ErrForbidden
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit github sync pickup: %w", err)
	}

	syncmetrics.RecordJobPicked(job.JobType)
	return &job, nil
}

func (s *GitHubSyncJobStore) MarkCompleted(ctx context.Context, jobID string) (*GitHubSyncJob, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	if !uuidRegex.MatchString(strings.TrimSpace(jobID)) {
		return nil, fmt.Errorf("invalid job_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	job, err := scanGitHubSyncJob(conn.QueryRowContext(
		ctx,
		`UPDATE github_sync_jobs
		 SET status = $2,
		     completed_at = NOW(),
		     last_error = NULL,
		     last_error_class = NULL,
		     updated_at = NOW()
		 WHERE id = $1
		 RETURNING`+githubSyncJobColumns,
		jobID,
		GitHubSyncJobStatusCompleted,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to mark github sync job complete: %w", err)
	}
	if job.OrgID != workspaceID {
		return nil, ErrForbidden
	}

	syncmetrics.RecordJobCompleted(job.JobType, time.Since(job.CreatedAt))
	return &job, nil
}

func (s *GitHubSyncJobStore) RecordFailure(
	ctx context.Context,
	jobID string,
	input RecordGitHubSyncFailureInput,
) (*RecordGitHubSyncFailureResult, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	if !uuidRegex.MatchString(strings.TrimSpace(jobID)) {
		return nil, fmt.Errorf("invalid job_id")
	}

	errClass := strings.TrimSpace(input.ErrorClass)
	errMessage := strings.TrimSpace(input.ErrorMessage)
	occurredAt := input.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	job, err := scanGitHubSyncJob(tx.QueryRowContext(
		ctx,
		`SELECT`+githubSyncJobColumns+` FROM github_sync_jobs WHERE id = $1 FOR UPDATE`,
		jobID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load github sync job: %w", err)
	}
	if job.OrgID != workspaceID {
		return nil, ErrForbidden
	}

	attemptRecord := map[string]any{
		"attempt":     job.AttemptCount,
		"error_class": errClass,
		"error":       errMessage,
		"retryable":   input.Retryable,
		"occurred_at": occurredAt,
	}
	if input.NextAttempt != nil {
		attemptRecord["next_attempt_at"] = input.NextAttempt.UTC()
	}
	attemptRecordJSON, err := json.Marshal([]map[string]any{attemptRecord})
	if err != nil {
		return nil, fmt.Errorf("failed to encode attempt record: %w", err)
	}

	shouldDeadLetter := !input.Retryable || job.AttemptCount >= job.MaxAttempts
	status := GitHubSyncJobStatusRetrying
	nextAttemptAt := time.Now().UTC()
	if input.NextAttempt != nil {
		nextAttemptAt = input.NextAttempt.UTC()
	}
	if shouldDeadLetter {
		status = GitHubSyncJobStatusDeadLetter
		nextAttemptAt = time.Now().UTC()
	}

	updatedJob, err := scanGitHubSyncJob(tx.QueryRowContext(
		ctx,
		`UPDATE github_sync_jobs
		 SET status = $2,
		     next_attempt_at = $3,
		     last_error = $4,
		     last_error_class = $5,
		     attempt_history = attempt_history || $6::jsonb,
		     updated_at = NOW()
		 WHERE id = $1
		 RETURNING`+githubSyncJobColumns,
		jobID,
		status,
		nextAttemptAt,
		nullableString(&errMessage),
		nullableString(&errClass),
		attemptRecordJSON,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to update failed github sync job: %w", err)
	}

	result := &RecordGitHubSyncFailureResult{Job: &updatedJob}

	if shouldDeadLetter {
		deadLetter, err := s.upsertDeadLetterTx(ctx, tx, updatedJob)
		if err != nil {
			return nil, err
		}
		result.DeadLetter = deadLetter
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit github sync failure: %w", err)
	}

	syncmetrics.RecordJobFailure(updatedJob.JobType, input.Retryable && !shouldDeadLetter, shouldDeadLetter)
	return result, nil
}

func (s *GitHubSyncJobStore) ReplayDeadLetter(
	ctx context.Context,
	deadLetterID string,
	replayedBy *string,
) (*GitHubSyncJob, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	if !uuidRegex.MatchString(strings.TrimSpace(deadLetterID)) {
		return nil, fmt.Errorf("invalid dead_letter_id")
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	deadLetter, err := scanGitHubSyncDeadLetter(tx.QueryRowContext(
		ctx,
		`SELECT`+githubSyncDeadLetterColumns+` FROM github_sync_dead_letters WHERE id = $1 FOR UPDATE`,
		deadLetterID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load dead letter: %w", err)
	}
	if deadLetter.OrgID != workspaceID {
		return nil, ErrForbidden
	}

	if deadLetter.ReplayedAt == nil {
		_, err = tx.ExecContext(
			ctx,
			`UPDATE github_sync_dead_letters
			 SET replayed_at = NOW(),
			     replayed_by = $2
			 WHERE id = $1`,
			deadLetterID,
			nullableString(replayedBy),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to mark dead letter replayed: %w", err)
		}
	}

	job, err := scanGitHubSyncJob(tx.QueryRowContext(
		ctx,
		`UPDATE github_sync_jobs
		 SET status = $2,
		     next_attempt_at = NOW(),
		     attempt_count = 0,
		     last_error = NULL,
		     last_error_class = NULL,
		     attempt_history = '[]'::jsonb,
		     completed_at = NULL,
		     updated_at = NOW()
		 WHERE id = $1
		 RETURNING`+githubSyncJobColumns,
		deadLetter.JobID,
		GitHubSyncJobStatusQueued,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to replay dead-letter job: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit dead-letter replay: %w", err)
	}

	syncmetrics.RecordDeadLetterReplay(job.JobType)
	return &job, nil
}

func (s *GitHubSyncJobStore) ListDeadLetters(
	ctx context.Context,
	limit int,
) ([]GitHubSyncDeadLetter, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT`+githubSyncDeadLetterColumns+`
		 FROM github_sync_dead_letters
		 ORDER BY failed_at DESC
		 LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list dead letters: %w", err)
	}
	defer rows.Close()

	deadLetters := make([]GitHubSyncDeadLetter, 0, limit)
	for rows.Next() {
		deadLetter, err := scanGitHubSyncDeadLetter(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan dead letter: %w", err)
		}
		if deadLetter.OrgID != workspaceID {
			return nil, ErrForbidden
		}
		deadLetters = append(deadLetters, deadLetter)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read dead letters: %w", err)
	}

	return deadLetters, nil
}

func (s *GitHubSyncJobStore) QueueDepth(ctx context.Context) ([]GitHubSyncQueueDepth, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT
			job_type,
			COUNT(*) FILTER (WHERE status = 'queued') AS queued,
			COUNT(*) FILTER (WHERE status = 'retrying') AS retrying,
			COUNT(*) FILTER (WHERE status = 'in_progress') AS in_progress,
			COUNT(*) FILTER (WHERE status = 'dead_letter') AS dead_letter
		 FROM github_sync_jobs
		 GROUP BY job_type
		 ORDER BY job_type ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query github sync queue depth: %w", err)
	}
	defer rows.Close()

	depth := make([]GitHubSyncQueueDepth, 0)
	for rows.Next() {
		var row GitHubSyncQueueDepth
		if err := rows.Scan(&row.JobType, &row.Queued, &row.Retrying, &row.InProgress, &row.DeadLetter); err != nil {
			return nil, fmt.Errorf("failed to scan github sync queue depth row: %w", err)
		}
		depth = append(depth, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read github sync queue depth rows: %w", err)
	}

	return depth, nil
}

func (s *GitHubSyncJobStore) CountStuckJobs(ctx context.Context, olderThan time.Duration) (int, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return 0, ErrNoWorkspace
	}
	if olderThan <= 0 {
		olderThan = 15 * time.Minute
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	var stuckCount int
	err = conn.QueryRowContext(
		ctx,
		`SELECT COUNT(*)
		 FROM github_sync_jobs
		 WHERE status = $1
		   AND updated_at < NOW() - ($2::bigint * interval '1 second')`,
		GitHubSyncJobStatusInProgress,
		int64(olderThan.Seconds()),
	).Scan(&stuckCount)
	if err != nil {
		return 0, fmt.Errorf("failed to count stuck github sync jobs: %w", err)
	}

	return stuckCount, nil
}

func (s *GitHubSyncJobStore) upsertDeadLetterTx(
	ctx context.Context,
	tx *sql.Tx,
	job GitHubSyncJob,
) (*GitHubSyncDeadLetter, error) {
	deadLetter, err := scanGitHubSyncDeadLetter(tx.QueryRowContext(
		ctx,
		`INSERT INTO github_sync_dead_letters (
			org_id,
			job_id,
			project_id,
			job_type,
			payload,
			attempt_count,
			max_attempts,
			last_error,
			last_error_class,
			attempt_history
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (job_id)
		DO UPDATE SET
			attempt_count = EXCLUDED.attempt_count,
			max_attempts = EXCLUDED.max_attempts,
			last_error = EXCLUDED.last_error,
			last_error_class = EXCLUDED.last_error_class,
			attempt_history = EXCLUDED.attempt_history,
			failed_at = NOW()
		RETURNING`+githubSyncDeadLetterColumns,
		job.OrgID,
		job.ID,
		nullableString(job.ProjectID),
		job.JobType,
		job.Payload,
		job.AttemptCount,
		job.MaxAttempts,
		nullableString(job.LastError),
		nullableString(job.LastErrorClass),
		job.AttemptHistory,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to upsert dead letter: %w", err)
	}

	return &deadLetter, nil
}

func normalizeSyncJobType(jobType string) string {
	return strings.TrimSpace(strings.ToLower(jobType))
}

func normalizeSyncJobTypes(jobTypes []string) ([]string, error) {
	if len(jobTypes) == 0 {
		return nil, nil
	}

	out := make([]string, 0, len(jobTypes))
	seen := make(map[string]struct{}, len(jobTypes))
	for _, raw := range jobTypes {
		normalized := normalizeSyncJobType(raw)
		if !isValidSyncJobType(normalized) {
			return nil, fmt.Errorf("invalid job_type %q", raw)
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}

	return out, nil
}

func isValidSyncJobType(jobType string) bool {
	switch normalizeSyncJobType(jobType) {
	case GitHubSyncJobTypeRepoSync, GitHubSyncJobTypeIssueImport, GitHubSyncJobTypeWebhook:
		return true
	default:
		return false
	}
}

func scanGitHubSyncJob(scanner interface{ Scan(...any) error }) (GitHubSyncJob, error) {
	var job GitHubSyncJob
	var projectID sql.NullString
	var sourceEventID sql.NullString
	var lastError sql.NullString
	var lastErrorClass sql.NullString
	var completedAt sql.NullTime

	if err := scanner.Scan(
		&job.ID,
		&job.OrgID,
		&projectID,
		&job.JobType,
		&job.Status,
		&job.Payload,
		&sourceEventID,
		&job.AttemptCount,
		&job.MaxAttempts,
		&job.NextAttemptAt,
		&lastError,
		&lastErrorClass,
		&job.AttemptHistory,
		&job.CreatedAt,
		&job.UpdatedAt,
		&completedAt,
	); err != nil {
		return job, err
	}

	if projectID.Valid {
		job.ProjectID = &projectID.String
	}
	if sourceEventID.Valid {
		job.SourceEventID = &sourceEventID.String
	}
	if lastError.Valid {
		job.LastError = &lastError.String
	}
	if lastErrorClass.Valid {
		job.LastErrorClass = &lastErrorClass.String
	}
	if completedAt.Valid {
		job.CompletedAt = &completedAt.Time
	}
	if len(job.Payload) == 0 {
		job.Payload = json.RawMessage(`{}`)
	}
	if len(job.AttemptHistory) == 0 {
		job.AttemptHistory = json.RawMessage(`[]`)
	}

	return job, nil
}

func scanGitHubSyncDeadLetter(scanner interface{ Scan(...any) error }) (GitHubSyncDeadLetter, error) {
	var deadLetter GitHubSyncDeadLetter
	var projectID sql.NullString
	var lastError sql.NullString
	var lastErrorClass sql.NullString
	var replayedAt sql.NullTime
	var replayedBy sql.NullString

	if err := scanner.Scan(
		&deadLetter.ID,
		&deadLetter.OrgID,
		&deadLetter.JobID,
		&projectID,
		&deadLetter.JobType,
		&deadLetter.Payload,
		&deadLetter.AttemptCount,
		&deadLetter.MaxAttempts,
		&lastError,
		&lastErrorClass,
		&deadLetter.AttemptHistory,
		&deadLetter.FailedAt,
		&replayedAt,
		&replayedBy,
	); err != nil {
		return deadLetter, err
	}

	if projectID.Valid {
		deadLetter.ProjectID = &projectID.String
	}
	if lastError.Valid {
		deadLetter.LastError = &lastError.String
	}
	if lastErrorClass.Valid {
		deadLetter.LastErrorClass = &lastErrorClass.String
	}
	if replayedAt.Valid {
		deadLetter.ReplayedAt = &replayedAt.Time
	}
	if replayedBy.Valid {
		deadLetter.ReplayedBy = &replayedBy.String
	}
	if len(deadLetter.Payload) == 0 {
		deadLetter.Payload = json.RawMessage(`{}`)
	}
	if len(deadLetter.AttemptHistory) == 0 {
		deadLetter.AttemptHistory = json.RawMessage(`[]`)
	}

	return deadLetter, nil
}
