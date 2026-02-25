package cli

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

var (
	ErrNotFound = repo.ErrNotFound
	ErrConflict = repo.ErrConflict
)

type queryExecutor interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Execution struct {
	ID               uuid.UUID
	RunID            uuid.UUID
	RunStepID        uuid.UUID
	TaskID           uuid.UUID
	ProjectID        uuid.UUID
	AgentID          uuid.UUID
	Command          string
	WorkingDirectory string
	RiskLevel        RiskLevel
	PolicyDecision   string
	ExitCode         *int
	StdoutArtifactID *uuid.UUID
	StderrArtifactID *uuid.UUID
	EnvVarsUsed      json.RawMessage
	StartedAt        *time.Time
	CompletedAt      *time.Time
	DurationMS       *int
	Metadata         json.RawMessage
	CreatedAt        time.Time
}

type CompletionUpdate struct {
	PolicyDecision   string
	ExitCode         *int
	StdoutArtifactID *uuid.UUID
	StderrArtifactID *uuid.UUID
	CompletedAt      *time.Time
	DurationMS       *int
	Metadata         json.RawMessage
}

type Repository struct {
	db queryExecutor
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{db: pool}
}

func (r *Repository) Create(ctx context.Context, item Execution) (Execution, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO cli_execution (
			run_id,
			run_step_id,
			task_id,
			project_id,
			agent_id,
			command,
			working_directory,
			risk_level,
			policy_decision,
			exit_code,
			stdout_artifact_id,
			stderr_artifact_id,
			env_vars_used,
			started_at,
			completed_at,
			duration_ms,
			metadata
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9,
			$10, $11, $12, $13::jsonb, $14, $15, $16, $17::jsonb
		)
		RETURNING id, run_id, run_step_id, task_id, project_id, agent_id, command, working_directory,
		          risk_level, policy_decision, exit_code, stdout_artifact_id, stderr_artifact_id,
		          env_vars_used, started_at, completed_at, duration_ms, metadata, created_at
	`,
		item.RunID,
		item.RunStepID,
		item.TaskID,
		item.ProjectID,
		item.AgentID,
		strings.TrimSpace(item.Command),
		strings.TrimSpace(item.WorkingDirectory),
		normalizeRiskLevel(item.RiskLevel),
		normalizePolicyDecision(item.PolicyDecision),
		item.ExitCode,
		item.StdoutArtifactID,
		item.StderrArtifactID,
		normalizeJSON(item.EnvVarsUsed),
		item.StartedAt,
		item.CompletedAt,
		normalizeDuration(item.DurationMS),
		normalizeJSON(item.Metadata),
	)

	created, err := scanExecution(row)
	if err != nil {
		return Execution{}, mapDBError(err)
	}
	return created, nil
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (Execution, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, run_id, run_step_id, task_id, project_id, agent_id, command, working_directory,
		       risk_level, policy_decision, exit_code, stdout_artifact_id, stderr_artifact_id,
		       env_vars_used, started_at, completed_at, duration_ms, metadata, created_at
		FROM cli_execution
		WHERE id = $1
	`, id)

	item, err := scanExecution(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, ErrNotFound
	}
	if err != nil {
		return Execution{}, mapDBError(err)
	}
	return item, nil
}

func (r *Repository) UpdateCompletion(ctx context.Context, id uuid.UUID, update CompletionUpdate) (Execution, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE cli_execution
		SET policy_decision = COALESCE(NULLIF($2, ''), policy_decision),
		    exit_code = $3,
		    stdout_artifact_id = $4,
		    stderr_artifact_id = $5,
		    completed_at = $6,
		    duration_ms = $7,
		    metadata = $8::jsonb
		WHERE id = $1
		RETURNING id, run_id, run_step_id, task_id, project_id, agent_id, command, working_directory,
		          risk_level, policy_decision, exit_code, stdout_artifact_id, stderr_artifact_id,
		          env_vars_used, started_at, completed_at, duration_ms, metadata, created_at
	`,
		id,
		normalizePolicyDecision(update.PolicyDecision),
		update.ExitCode,
		update.StdoutArtifactID,
		update.StderrArtifactID,
		update.CompletedAt,
		normalizeDuration(update.DurationMS),
		normalizeJSON(update.Metadata),
	)

	item, err := scanExecution(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, ErrNotFound
	}
	if err != nil {
		return Execution{}, mapDBError(err)
	}
	return item, nil
}

func (r *Repository) ListByRun(ctx context.Context, runID uuid.UUID) ([]Execution, error) {
	return r.list(ctx, `
		SELECT id, run_id, run_step_id, task_id, project_id, agent_id, command, working_directory,
		       risk_level, policy_decision, exit_code, stdout_artifact_id, stderr_artifact_id,
		       env_vars_used, started_at, completed_at, duration_ms, metadata, created_at
		FROM cli_execution
		WHERE run_id = $1
		ORDER BY created_at ASC
	`, runID)
}

func (r *Repository) ListByTask(ctx context.Context, taskID uuid.UUID) ([]Execution, error) {
	return r.list(ctx, `
		SELECT id, run_id, run_step_id, task_id, project_id, agent_id, command, working_directory,
		       risk_level, policy_decision, exit_code, stdout_artifact_id, stderr_artifact_id,
		       env_vars_used, started_at, completed_at, duration_ms, metadata, created_at
		FROM cli_execution
		WHERE task_id = $1
		ORDER BY created_at ASC
	`, taskID)
}

func (r *Repository) list(ctx context.Context, query string, id uuid.UUID) ([]Execution, error) {
	rows, err := r.db.Query(ctx, query, id)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]Execution, 0)
	for rows.Next() {
		item, scanErr := scanExecution(rows)
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

func scanExecution(row pgx.Row) (Execution, error) {
	var item Execution
	if err := row.Scan(
		&item.ID,
		&item.RunID,
		&item.RunStepID,
		&item.TaskID,
		&item.ProjectID,
		&item.AgentID,
		&item.Command,
		&item.WorkingDirectory,
		&item.RiskLevel,
		&item.PolicyDecision,
		&item.ExitCode,
		&item.StdoutArtifactID,
		&item.StderrArtifactID,
		&item.EnvVarsUsed,
		&item.StartedAt,
		&item.CompletedAt,
		&item.DurationMS,
		&item.Metadata,
		&item.CreatedAt,
	); err != nil {
		return Execution{}, err
	}
	item.EnvVarsUsed = normalizeJSON(item.EnvVarsUsed)
	item.Metadata = normalizeJSON(item.Metadata)
	return item, nil
}

func normalizeRiskLevel(level RiskLevel) RiskLevel {
	switch level {
	case RiskLow, RiskMedium, RiskHigh, RiskCritical:
		return level
	default:
		return RiskLow
	}
}

func normalizePolicyDecision(decision string) string {
	switch strings.TrimSpace(strings.ToLower(decision)) {
	case "allowed", "denied":
		return strings.TrimSpace(strings.ToLower(decision))
	default:
		return ""
	}
}

func normalizeDuration(duration *int) *int {
	if duration == nil {
		return nil
	}
	if *duration < 0 {
		zero := 0
		return &zero
	}
	return duration
}

func normalizeJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || !json.Valid(raw) {
		return json.RawMessage(`{}`)
	}
	return raw
}

func mapDBError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23503", "23514":
			return fmt.Errorf("%w: %s", ErrConflict, pgErr.Message)
		}
	}
	return err
}
