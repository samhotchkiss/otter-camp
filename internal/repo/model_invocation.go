package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ModelInvocation struct {
	ID                       uuid.UUID
	OrganizationID           uuid.UUID
	ModelProviderID          uuid.UUID
	ProviderConnectionID     *uuid.UUID
	ModelProfileID           *string
	InvocationPurpose        string
	Status                   string
	PromptStorageKey         *string
	ResponseStorageKey       *string
	InputTokens              *int
	OutputTokens             *int
	CacheReadTokens          *int
	ModelName                string
	IsStreaming              bool
	LatencyMS                *int
	TotalDurationMS          *int
	AttemptNumber            int
	FallbackFromInvocationID *uuid.UUID
	FailureClass             *string
	ErrorCode                *string
	ErrorMessage             *string
	Metadata                 json.RawMessage
	AgentID                  *uuid.UUID
	ProjectID                *uuid.UUID
	ProjectTaskID            *uuid.UUID
	SessionID                *uuid.UUID
	TurnID                   *uuid.UUID
	RunID                    *uuid.UUID
	RunStepID                *uuid.UUID
	RunAttemptID             *uuid.UUID
	CreatedAt                time.Time
	CompletedAt              *time.Time
}

type ModelInvocationRepo struct {
	db projectTaskExecutor
}

func NewModelInvocationRepo(pool *pgxpool.Pool) *ModelInvocationRepo {
	return &ModelInvocationRepo{db: pool}
}

func (r *ModelInvocationRepo) Create(ctx context.Context, invocation ModelInvocation) (ModelInvocation, error) {
	metadata := invocation.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}

	status := invocation.Status
	if status == "" {
		status = "pending"
	}

	attemptNumber := invocation.AttemptNumber
	if attemptNumber <= 0 {
		attemptNumber = 1
	}

	row := r.db.QueryRow(ctx, `
		INSERT INTO model_invocation (
			organization_id,
			model_provider_id,
			provider_connection_id,
			model_profile_id,
			invocation_purpose,
			status,
			prompt_storage_key,
			response_storage_key,
			input_tokens,
			output_tokens,
			cache_read_tokens,
			model_name,
			is_streaming,
			latency_ms,
			total_duration_ms,
			attempt_number,
			fallback_from_invocation_id,
			failure_class,
			error_code,
			error_message,
			metadata,
			agent_id,
			project_id,
			project_task_id,
			session_id,
			turn_id,
			run_id,
			run_step_id,
			run_attempt_id,
			completed_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21::jsonb,
			$22, $23, $24, $25, $26, $27, $28, $29, $30
		)
		RETURNING id, organization_id, model_provider_id, provider_connection_id, model_profile_id, invocation_purpose, status, prompt_storage_key, response_storage_key,
		          input_tokens, output_tokens, cache_read_tokens, model_name, is_streaming, latency_ms, total_duration_ms, attempt_number,
		          fallback_from_invocation_id, failure_class, error_code, error_message, metadata, agent_id, project_id, project_task_id, session_id, turn_id,
		          run_id, run_step_id, run_attempt_id, created_at, completed_at
	`, invocation.OrganizationID, invocation.ModelProviderID, invocation.ProviderConnectionID, invocation.ModelProfileID, invocation.InvocationPurpose, status, invocation.PromptStorageKey, invocation.ResponseStorageKey,
		invocation.InputTokens, invocation.OutputTokens, invocation.CacheReadTokens, invocation.ModelName, invocation.IsStreaming, invocation.LatencyMS, invocation.TotalDurationMS,
		attemptNumber, invocation.FallbackFromInvocationID, invocation.FailureClass, invocation.ErrorCode, invocation.ErrorMessage, metadata, invocation.AgentID, invocation.ProjectID, invocation.ProjectTaskID,
		invocation.SessionID, invocation.TurnID, invocation.RunID, invocation.RunStepID, invocation.RunAttemptID, invocation.CompletedAt,
	)

	created, err := scanModelInvocation(row)
	if err != nil {
		return ModelInvocation{}, mapDBError(err)
	}
	return created, nil
}

func (r *ModelInvocationRepo) GetByID(ctx context.Context, id uuid.UUID) (ModelInvocation, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, organization_id, model_provider_id, provider_connection_id, model_profile_id, invocation_purpose, status, prompt_storage_key, response_storage_key,
		       input_tokens, output_tokens, cache_read_tokens, model_name, is_streaming, latency_ms, total_duration_ms, attempt_number,
		       fallback_from_invocation_id, failure_class, error_code, error_message, metadata, agent_id, project_id, project_task_id, session_id, turn_id,
		       run_id, run_step_id, run_attempt_id, created_at, completed_at
		FROM model_invocation
		WHERE id = $1
	`, id)

	invocation, err := scanModelInvocation(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelInvocation{}, ErrNotFound
	}
	if err != nil {
		return ModelInvocation{}, mapDBError(err)
	}
	return invocation, nil
}

func (r *ModelInvocationRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string, errorCode, errorMessage *string) (ModelInvocation, error) {
	return r.UpdateFailure(ctx, id, status, nil, errorCode, errorMessage)
}

func (r *ModelInvocationRepo) UpdateRouting(ctx context.Context, id, providerID, connectionID uuid.UUID) (ModelInvocation, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE model_invocation
		SET model_provider_id = $2,
		    provider_connection_id = $3
		WHERE id = $1
		RETURNING id, organization_id, model_provider_id, provider_connection_id, model_profile_id, invocation_purpose, status, prompt_storage_key, response_storage_key,
		          input_tokens, output_tokens, cache_read_tokens, model_name, is_streaming, latency_ms, total_duration_ms, attempt_number,
		          fallback_from_invocation_id, failure_class, error_code, error_message, metadata, agent_id, project_id, project_task_id, session_id, turn_id,
		          run_id, run_step_id, run_attempt_id, created_at, completed_at
	`, id, providerID, connectionID)

	updated, err := scanModelInvocation(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelInvocation{}, ErrNotFound
	}
	if err != nil {
		return ModelInvocation{}, mapDBError(err)
	}
	return updated, nil
}

func (r *ModelInvocationRepo) UpdateFailure(ctx context.Context, id uuid.UUID, status string, failureClass, errorCode, errorMessage *string) (ModelInvocation, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE model_invocation
		SET status = $2,
		    failure_class = $3,
		    error_code = $4,
		    error_message = $5,
		    completed_at = CASE WHEN $2 IN ('completed', 'failed', 'cancelled') THEN COALESCE(completed_at, now()) ELSE completed_at END
		WHERE id = $1
		RETURNING id, organization_id, model_provider_id, provider_connection_id, model_profile_id, invocation_purpose, status, prompt_storage_key, response_storage_key,
		          input_tokens, output_tokens, cache_read_tokens, model_name, is_streaming, latency_ms, total_duration_ms, attempt_number,
		          fallback_from_invocation_id, failure_class, error_code, error_message, metadata, agent_id, project_id, project_task_id, session_id, turn_id,
		          run_id, run_step_id, run_attempt_id, created_at, completed_at
	`, id, status, failureClass, errorCode, errorMessage)

	updated, err := scanModelInvocation(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelInvocation{}, ErrNotFound
	}
	if err != nil {
		return ModelInvocation{}, mapDBError(err)
	}
	return updated, nil
}

func (r *ModelInvocationRepo) UpdateCompletion(ctx context.Context, id uuid.UUID, inputTokens, outputTokens, cacheTokens, latencyMS, totalDurationMS int, promptKey, responseKey *string) error {
	command, err := r.db.Exec(ctx, `
		UPDATE model_invocation
		SET status = 'completed',
		    input_tokens = $2,
		    output_tokens = $3,
		    cache_read_tokens = $4,
		    latency_ms = $5,
		    total_duration_ms = $6,
		    prompt_storage_key = $7,
		    response_storage_key = $8,
		    completed_at = now()
		WHERE id = $1
	`, id, inputTokens, outputTokens, cacheTokens, latencyMS, totalDurationMS, promptKey, responseKey)
	if err != nil {
		return mapDBError(err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *ModelInvocationRepo) ListByOrg(ctx context.Context, organizationID uuid.UUID) ([]ModelInvocation, error) {
	return r.list(ctx, `
		SELECT id, organization_id, model_provider_id, provider_connection_id, model_profile_id, invocation_purpose, status, prompt_storage_key, response_storage_key,
		       input_tokens, output_tokens, cache_read_tokens, model_name, is_streaming, latency_ms, total_duration_ms, attempt_number,
		       fallback_from_invocation_id, failure_class, error_code, error_message, metadata, agent_id, project_id, project_task_id, session_id, turn_id,
		       run_id, run_step_id, run_attempt_id, created_at, completed_at
		FROM model_invocation
		WHERE organization_id = $1
		ORDER BY created_at DESC
	`, organizationID)
}

func (r *ModelInvocationRepo) ListByAgent(ctx context.Context, organizationID, agentID uuid.UUID) ([]ModelInvocation, error) {
	return r.list(ctx, `
		SELECT id, organization_id, model_provider_id, provider_connection_id, model_profile_id, invocation_purpose, status, prompt_storage_key, response_storage_key,
		       input_tokens, output_tokens, cache_read_tokens, model_name, is_streaming, latency_ms, total_duration_ms, attempt_number,
		       fallback_from_invocation_id, error_code, error_message, metadata, agent_id, project_id, project_task_id, session_id, turn_id,
		       run_id, run_step_id, run_attempt_id, created_at, completed_at
		FROM model_invocation
		WHERE organization_id = $1
		  AND agent_id = $2
		ORDER BY created_at DESC
	`, organizationID, agentID)
}

func (r *ModelInvocationRepo) ListByProject(ctx context.Context, organizationID, projectID uuid.UUID) ([]ModelInvocation, error) {
	return r.list(ctx, `
		SELECT id, organization_id, model_provider_id, provider_connection_id, model_profile_id, invocation_purpose, status, prompt_storage_key, response_storage_key,
		       input_tokens, output_tokens, cache_read_tokens, model_name, is_streaming, latency_ms, total_duration_ms, attempt_number,
		       fallback_from_invocation_id, failure_class, error_code, error_message, metadata, agent_id, project_id, project_task_id, session_id, turn_id,
		       run_id, run_step_id, run_attempt_id, created_at, completed_at
		FROM model_invocation
		WHERE organization_id = $1
		  AND project_id = $2
		ORDER BY created_at DESC
	`, organizationID, projectID)
}

func (r *ModelInvocationRepo) ListByTask(ctx context.Context, organizationID, taskID uuid.UUID) ([]ModelInvocation, error) {
	return r.list(ctx, `
		SELECT id, organization_id, model_provider_id, provider_connection_id, model_profile_id, invocation_purpose, status, prompt_storage_key, response_storage_key,
		       input_tokens, output_tokens, cache_read_tokens, model_name, is_streaming, latency_ms, total_duration_ms, attempt_number,
		       fallback_from_invocation_id, failure_class, error_code, error_message, metadata, agent_id, project_id, project_task_id, session_id, turn_id,
		       run_id, run_step_id, run_attempt_id, created_at, completed_at
		FROM model_invocation
		WHERE organization_id = $1
		  AND project_task_id = $2
		ORDER BY created_at DESC
	`, organizationID, taskID)
}

func (r *ModelInvocationRepo) ListBySession(ctx context.Context, organizationID, sessionID uuid.UUID) ([]ModelInvocation, error) {
	return r.list(ctx, `
		SELECT id, organization_id, model_provider_id, provider_connection_id, model_profile_id, invocation_purpose, status, prompt_storage_key, response_storage_key,
		       input_tokens, output_tokens, cache_read_tokens, model_name, is_streaming, latency_ms, total_duration_ms, attempt_number,
		       fallback_from_invocation_id, failure_class, error_code, error_message, metadata, agent_id, project_id, project_task_id, session_id, turn_id,
		       run_id, run_step_id, run_attempt_id, created_at, completed_at
		FROM model_invocation
		WHERE organization_id = $1
		  AND session_id = $2
		ORDER BY created_at DESC
	`, organizationID, sessionID)
}

func (r *ModelInvocationRepo) ListByRun(ctx context.Context, organizationID, runID uuid.UUID) ([]ModelInvocation, error) {
	return r.list(ctx, `
		SELECT id, organization_id, model_provider_id, provider_connection_id, model_profile_id, invocation_purpose, status, prompt_storage_key, response_storage_key,
		       input_tokens, output_tokens, cache_read_tokens, model_name, is_streaming, latency_ms, total_duration_ms, attempt_number,
		       fallback_from_invocation_id, failure_class, error_code, error_message, metadata, agent_id, project_id, project_task_id, session_id, turn_id,
		       run_id, run_step_id, run_attempt_id, created_at, completed_at
		FROM model_invocation
		WHERE organization_id = $1
		  AND run_id = $2
		ORDER BY created_at DESC
	`, organizationID, runID)
}

func (r *ModelInvocationRepo) ClearProjectReferences(ctx context.Context, projectID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		UPDATE model_invocation
		SET
			project_id = NULL,
			project_task_id = NULL
		WHERE project_id = $1
		   OR project_task_id IN (
				SELECT id
				FROM project_task
				WHERE project_id = $1
		   )
	`, projectID)
	if err != nil {
		return mapDBError(err)
	}
	return nil
}

func (r *ModelInvocationRepo) list(ctx context.Context, query string, args ...any) ([]ModelInvocation, error) {
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]ModelInvocation, 0)
	for rows.Next() {
		item, scanErr := scanModelInvocation(rows)
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

func scanModelInvocation(row pgx.Row) (ModelInvocation, error) {
	var invocation ModelInvocation
	if err := row.Scan(
		&invocation.ID,
		&invocation.OrganizationID,
		&invocation.ModelProviderID,
		&invocation.ProviderConnectionID,
		&invocation.ModelProfileID,
		&invocation.InvocationPurpose,
		&invocation.Status,
		&invocation.PromptStorageKey,
		&invocation.ResponseStorageKey,
		&invocation.InputTokens,
		&invocation.OutputTokens,
		&invocation.CacheReadTokens,
		&invocation.ModelName,
		&invocation.IsStreaming,
		&invocation.LatencyMS,
		&invocation.TotalDurationMS,
		&invocation.AttemptNumber,
		&invocation.FallbackFromInvocationID,
		&invocation.FailureClass,
		&invocation.ErrorCode,
		&invocation.ErrorMessage,
		&invocation.Metadata,
		&invocation.AgentID,
		&invocation.ProjectID,
		&invocation.ProjectTaskID,
		&invocation.SessionID,
		&invocation.TurnID,
		&invocation.RunID,
		&invocation.RunStepID,
		&invocation.RunAttemptID,
		&invocation.CreatedAt,
		&invocation.CompletedAt,
	); err != nil {
		return ModelInvocation{}, err
	}

	if len(invocation.Metadata) == 0 {
		invocation.Metadata = json.RawMessage(`{}`)
	}
	if !json.Valid(invocation.Metadata) {
		return ModelInvocation{}, fmt.Errorf("invalid metadata json")
	}

	return invocation, nil
}
