package browser

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
)

type queryExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type BrowserSessionRepository struct {
	db queryExecutor
}

func NewBrowserSessionRepository(pool *pgxpool.Pool) *BrowserSessionRepository {
	return &BrowserSessionRepository{db: pool}
}

func (r *BrowserSessionRepository) Create(ctx context.Context, session BrowserSession) (BrowserSession, error) {
	credentials := normalizeStringSlice(session.CredentialsInjected)
	row := r.db.QueryRow(ctx, `
		INSERT INTO browser_session (
			task_id,
			project_id,
			agent_id,
			organization_id,
			status,
			browser_type,
			current_url,
			user_agent,
			credentials_injected,
			idle_since,
			revoked_at
		)
		VALUES ($1, $2, $3, $4, COALESCE(NULLIF($5, ''), 'active'), COALESCE(NULLIF($6, ''), 'chromium'), $7, $8, $9::jsonb, $10, $11)
		RETURNING id, task_id, project_id, agent_id, organization_id, status, browser_type, current_url,
		          user_agent, credentials_injected, idle_since, revoked_at, created_at, updated_at
	`,
		session.TaskID,
		session.ProjectID,
		session.AgentID,
		session.OrganizationID,
		strings.TrimSpace(session.Status),
		strings.TrimSpace(session.BrowserType),
		trimStringPointer(session.CurrentURL),
		trimStringPointer(session.UserAgent),
		mustJSON(credentials, []byte(`[]`)),
		session.IdleSince,
		session.RevokedAt,
	)
	item, err := scanBrowserSession(row)
	if err != nil {
		return BrowserSession{}, mapDBError(err)
	}
	return item, nil
}

func (r *BrowserSessionRepository) Get(ctx context.Context, id uuid.UUID) (BrowserSession, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, task_id, project_id, agent_id, organization_id, status, browser_type, current_url,
		       user_agent, credentials_injected, idle_since, revoked_at, created_at, updated_at
		FROM browser_session
		WHERE id = $1
	`, id)
	item, err := scanBrowserSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return BrowserSession{}, ErrNotFound
	}
	if err != nil {
		return BrowserSession{}, mapDBError(err)
	}
	return item, nil
}

func (r *BrowserSessionRepository) GetActiveByTask(ctx context.Context, taskID uuid.UUID) (BrowserSession, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, task_id, project_id, agent_id, organization_id, status, browser_type, current_url,
		       user_agent, credentials_injected, idle_since, revoked_at, created_at, updated_at
		FROM browser_session
		WHERE task_id = $1
		  AND status = 'active'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, taskID)
	item, err := scanBrowserSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return BrowserSession{}, ErrNotFound
	}
	if err != nil {
		return BrowserSession{}, mapDBError(err)
	}
	return item, nil
}

func (r *BrowserSessionRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string, revokedAt *time.Time) (BrowserSession, error) {
	normalized := strings.TrimSpace(status)
	if normalized == "" {
		return BrowserSession{}, fmt.Errorf("status is required")
	}
	row := r.db.QueryRow(ctx, `
		UPDATE browser_session
		SET status = $2,
		    revoked_at = CASE
		        WHEN $2 = 'revoked' THEN COALESCE($3, now())
		        ELSE revoked_at
		    END
		WHERE id = $1
		RETURNING id, task_id, project_id, agent_id, organization_id, status, browser_type, current_url,
		          user_agent, credentials_injected, idle_since, revoked_at, created_at, updated_at
	`, id, normalized, revokedAt)
	item, err := scanBrowserSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return BrowserSession{}, ErrNotFound
	}
	if err != nil {
		return BrowserSession{}, mapDBError(err)
	}
	return item, nil
}

func (r *BrowserSessionRepository) UpdateCurrentURL(ctx context.Context, id uuid.UUID, currentURL *string) (BrowserSession, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE browser_session
		SET current_url = $2
		WHERE id = $1
		RETURNING id, task_id, project_id, agent_id, organization_id, status, browser_type, current_url,
		          user_agent, credentials_injected, idle_since, revoked_at, created_at, updated_at
	`, id, trimStringPointer(currentURL))
	item, err := scanBrowserSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return BrowserSession{}, ErrNotFound
	}
	if err != nil {
		return BrowserSession{}, mapDBError(err)
	}
	return item, nil
}

func (r *BrowserSessionRepository) UpdateIdleSince(ctx context.Context, id uuid.UUID, idleSince *time.Time) (BrowserSession, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE browser_session
		SET idle_since = $2
		WHERE id = $1
		RETURNING id, task_id, project_id, agent_id, organization_id, status, browser_type, current_url,
		          user_agent, credentials_injected, idle_since, revoked_at, created_at, updated_at
	`, id, idleSince)
	item, err := scanBrowserSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return BrowserSession{}, ErrNotFound
	}
	if err != nil {
		return BrowserSession{}, mapDBError(err)
	}
	return item, nil
}

func (r *BrowserSessionRepository) ListByAgent(ctx context.Context, agentID uuid.UUID) ([]BrowserSession, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, task_id, project_id, agent_id, organization_id, status, browser_type, current_url,
		       user_agent, credentials_injected, idle_since, revoked_at, created_at, updated_at
		FROM browser_session
		WHERE agent_id = $1
		ORDER BY created_at DESC
	`, agentID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]BrowserSession, 0)
	for rows.Next() {
		item, scanErr := scanBrowserSession(rows)
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

type BrowserActionRepository struct {
	db queryExecutor
}

func NewBrowserActionRepository(pool *pgxpool.Pool) *BrowserActionRepository {
	return &BrowserActionRepository{db: pool}
}

func (r *BrowserActionRepository) Create(ctx context.Context, action BrowserAction) (BrowserAction, error) {
	input := action.Input
	if len(input) == 0 {
		input = []byte(`{}`)
	}
	status := strings.TrimSpace(action.Status)
	if status == "" {
		status = StatusPending
	}
	row := r.db.QueryRow(ctx, `
		INSERT INTO browser_action (
			browser_session_id,
			run_id,
			run_step_id,
			action_type,
			input,
			output,
			screenshot_artifact_id,
			status,
			error_message,
			url_at_execution,
			started_at,
			completed_at,
			duration_ms
		)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, browser_session_id, run_id, run_step_id, action_type, input, output,
		          screenshot_artifact_id, status, error_message, url_at_execution, started_at,
		          completed_at, duration_ms, created_at
	`,
		action.BrowserSessionID,
		action.RunID,
		action.RunStepID,
		strings.TrimSpace(action.ActionType),
		input,
		normalizeJSON(action.Output, []byte(`null`)),
		action.ScreenshotArtifactID,
		status,
		trimStringPointer(action.ErrorMessage),
		trimStringPointer(action.URLAtExecution),
		action.StartedAt,
		action.CompletedAt,
		action.DurationMS,
	)
	item, err := scanBrowserAction(row)
	if err != nil {
		return BrowserAction{}, mapDBError(err)
	}
	return item, nil
}

func (r *BrowserActionRepository) Get(ctx context.Context, id uuid.UUID) (BrowserAction, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, browser_session_id, run_id, run_step_id, action_type, input, output,
		       screenshot_artifact_id, status, error_message, url_at_execution, started_at,
		       completed_at, duration_ms, created_at
		FROM browser_action
		WHERE id = $1
	`, id)
	item, err := scanBrowserAction(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return BrowserAction{}, ErrNotFound
	}
	if err != nil {
		return BrowserAction{}, mapDBError(err)
	}
	return item, nil
}

func (r *BrowserActionRepository) UpdateCompletion(ctx context.Context, id uuid.UUID, update ActionCompletion) (BrowserAction, error) {
	status := strings.TrimSpace(update.Status)
	if status == "" {
		status = StatusCompleted
	}
	row := r.db.QueryRow(ctx, `
		UPDATE browser_action
		SET status = $2,
		    output = CASE
		        WHEN $3::jsonb IS NULL THEN output
		        ELSE $3::jsonb
		    END,
		    error_message = $4,
		    screenshot_artifact_id = $5,
		    url_at_execution = $6,
		    started_at = COALESCE(started_at, $7),
		    completed_at = COALESCE($8, now()),
		    duration_ms = $9
		WHERE id = $1
		RETURNING id, browser_session_id, run_id, run_step_id, action_type, input, output,
		          screenshot_artifact_id, status, error_message, url_at_execution, started_at,
		          completed_at, duration_ms, created_at
	`,
		id,
		status,
		normalizeJSON(update.Output, nil),
		trimStringPointer(update.ErrorMessage),
		update.ScreenshotArtifactID,
		trimStringPointer(update.URLAtExecution),
		update.StartedAt,
		update.CompletedAt,
		update.DurationMS,
	)
	item, err := scanBrowserAction(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return BrowserAction{}, ErrNotFound
	}
	if err != nil {
		return BrowserAction{}, mapDBError(err)
	}
	return item, nil
}

func (r *BrowserActionRepository) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]BrowserAction, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, browser_session_id, run_id, run_step_id, action_type, input, output,
		       screenshot_artifact_id, status, error_message, url_at_execution, started_at,
		       completed_at, duration_ms, created_at
		FROM browser_action
		WHERE browser_session_id = $1
		ORDER BY created_at ASC
	`, sessionID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]BrowserAction, 0)
	for rows.Next() {
		item, scanErr := scanBrowserAction(rows)
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

func (r *BrowserActionRepository) ListByRun(ctx context.Context, runID uuid.UUID) ([]BrowserAction, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, browser_session_id, run_id, run_step_id, action_type, input, output,
		       screenshot_artifact_id, status, error_message, url_at_execution, started_at,
		       completed_at, duration_ms, created_at
		FROM browser_action
		WHERE run_id = $1
		ORDER BY created_at ASC
	`, runID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]BrowserAction, 0)
	for rows.Next() {
		item, scanErr := scanBrowserAction(rows)
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

type BrowserHandoffRepository struct {
	db queryExecutor
}

func NewBrowserHandoffRepository(pool *pgxpool.Pool) *BrowserHandoffRepository {
	return &BrowserHandoffRepository{db: pool}
}

func (r *BrowserHandoffRepository) Create(ctx context.Context, handoff BrowserHandoff) (BrowserHandoff, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO browser_handoff (
			id,
			browser_session_id,
			inbox_item_id,
			run_id,
			agent_id,
			target_user_id,
			reason,
			pre_handoff_screenshot_artifact_id,
			post_handoff_screenshot_artifact_id,
			status,
			expires_at,
			completed_at
		)
		VALUES (
			COALESCE(NULLIF($1::text, '')::uuid, gen_random_uuid()),
			$2, $3, $4, $5, $6, $7, $8, $9,
			COALESCE(NULLIF($10, ''), 'pending'),
			COALESCE($11, now() + interval '24 hours'),
			$12
		)
		RETURNING id, browser_session_id, inbox_item_id, run_id, agent_id, target_user_id, reason,
		          pre_handoff_screenshot_artifact_id, post_handoff_screenshot_artifact_id, status,
		          expires_at, completed_at, created_at
	`,
		nilUUIDString(handoff.ID),
		handoff.BrowserSessionID,
		handoff.InboxItemID,
		handoff.RunID,
		handoff.AgentID,
		handoff.TargetUserID,
		strings.TrimSpace(handoff.Reason),
		handoff.PreHandoffScreenshotArtifactID,
		handoff.PostHandoffScreenshotArtifactID,
		strings.TrimSpace(handoff.Status),
		timePointer(handoff.ExpiresAt),
		handoff.CompletedAt,
	)
	item, err := scanBrowserHandoff(row)
	if err != nil {
		return BrowserHandoff{}, mapDBError(err)
	}
	return item, nil
}

func (r *BrowserHandoffRepository) Get(ctx context.Context, id uuid.UUID) (BrowserHandoff, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, browser_session_id, inbox_item_id, run_id, agent_id, target_user_id, reason,
		       pre_handoff_screenshot_artifact_id, post_handoff_screenshot_artifact_id, status,
		       expires_at, completed_at, created_at
		FROM browser_handoff
		WHERE id = $1
	`, id)
	item, err := scanBrowserHandoff(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return BrowserHandoff{}, ErrNotFound
	}
	if err != nil {
		return BrowserHandoff{}, mapDBError(err)
	}
	return item, nil
}

func (r *BrowserHandoffRepository) GetByInboxItem(ctx context.Context, inboxItemID uuid.UUID) (BrowserHandoff, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, browser_session_id, inbox_item_id, run_id, agent_id, target_user_id, reason,
		       pre_handoff_screenshot_artifact_id, post_handoff_screenshot_artifact_id, status,
		       expires_at, completed_at, created_at
		FROM browser_handoff
		WHERE inbox_item_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, inboxItemID)
	item, err := scanBrowserHandoff(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return BrowserHandoff{}, ErrNotFound
	}
	if err != nil {
		return BrowserHandoff{}, mapDBError(err)
	}
	return item, nil
}

func (r *BrowserHandoffRepository) UpdateStatus(ctx context.Context, id uuid.UUID, update HandoffStatusUpdate) (BrowserHandoff, error) {
	status := strings.TrimSpace(update.Status)
	if status == "" {
		return BrowserHandoff{}, fmt.Errorf("status is required")
	}
	row := r.db.QueryRow(ctx, `
		UPDATE browser_handoff
		SET status = $2,
		    post_handoff_screenshot_artifact_id = COALESCE($3, post_handoff_screenshot_artifact_id),
		    completed_at = CASE
		        WHEN $2 IN ('completed', 'cancelled', 'expired') THEN COALESCE($4, now())
		        ELSE completed_at
		    END
		WHERE id = $1
		RETURNING id, browser_session_id, inbox_item_id, run_id, agent_id, target_user_id, reason,
		          pre_handoff_screenshot_artifact_id, post_handoff_screenshot_artifact_id, status,
		          expires_at, completed_at, created_at
	`,
		id,
		status,
		update.PostHandoffScreenshotArtifactID,
		update.CompletedAt,
	)
	item, err := scanBrowserHandoff(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return BrowserHandoff{}, ErrNotFound
	}
	if err != nil {
		return BrowserHandoff{}, mapDBError(err)
	}
	return item, nil
}

func (r *BrowserHandoffRepository) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]BrowserHandoff, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, browser_session_id, inbox_item_id, run_id, agent_id, target_user_id, reason,
		       pre_handoff_screenshot_artifact_id, post_handoff_screenshot_artifact_id, status,
		       expires_at, completed_at, created_at
		FROM browser_handoff
		WHERE browser_session_id = $1
		ORDER BY created_at ASC
	`, sessionID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]BrowserHandoff, 0)
	for rows.Next() {
		item, scanErr := scanBrowserHandoff(rows)
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

func (r *BrowserHandoffRepository) ListExpiredPending(ctx context.Context, now time.Time) ([]BrowserHandoff, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, browser_session_id, inbox_item_id, run_id, agent_id, target_user_id, reason,
		       pre_handoff_screenshot_artifact_id, post_handoff_screenshot_artifact_id, status,
		       expires_at, completed_at, created_at
		FROM browser_handoff
		WHERE status = 'pending'
		  AND expires_at < $1
		ORDER BY expires_at ASC, id ASC
	`, now.UTC())
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]BrowserHandoff, 0)
	for rows.Next() {
		item, scanErr := scanBrowserHandoff(rows)
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

func scanBrowserSession(row pgx.Row) (BrowserSession, error) {
	var (
		item        BrowserSession
		credentials []byte
	)
	if err := row.Scan(
		&item.ID,
		&item.TaskID,
		&item.ProjectID,
		&item.AgentID,
		&item.OrganizationID,
		&item.Status,
		&item.BrowserType,
		&item.CurrentURL,
		&item.UserAgent,
		&credentials,
		&item.IdleSince,
		&item.RevokedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return BrowserSession{}, err
	}
	if len(credentials) == 0 {
		item.CredentialsInjected = []string{}
		return item, nil
	}
	if err := json.Unmarshal(credentials, &item.CredentialsInjected); err != nil {
		return BrowserSession{}, fmt.Errorf("decode credentials_injected: %w", err)
	}
	item.CredentialsInjected = normalizeStringSlice(item.CredentialsInjected)
	return item, nil
}

func scanBrowserAction(row pgx.Row) (BrowserAction, error) {
	var item BrowserAction
	if err := row.Scan(
		&item.ID,
		&item.BrowserSessionID,
		&item.RunID,
		&item.RunStepID,
		&item.ActionType,
		&item.Input,
		&item.Output,
		&item.ScreenshotArtifactID,
		&item.Status,
		&item.ErrorMessage,
		&item.URLAtExecution,
		&item.StartedAt,
		&item.CompletedAt,
		&item.DurationMS,
		&item.CreatedAt,
	); err != nil {
		return BrowserAction{}, err
	}
	if len(item.Input) == 0 {
		item.Input = []byte(`{}`)
	}
	return item, nil
}

func scanBrowserHandoff(row pgx.Row) (BrowserHandoff, error) {
	var item BrowserHandoff
	if err := row.Scan(
		&item.ID,
		&item.BrowserSessionID,
		&item.InboxItemID,
		&item.RunID,
		&item.AgentID,
		&item.TargetUserID,
		&item.Reason,
		&item.PreHandoffScreenshotArtifactID,
		&item.PostHandoffScreenshotArtifactID,
		&item.Status,
		&item.ExpiresAt,
		&item.CompletedAt,
		&item.CreatedAt,
	); err != nil {
		return BrowserHandoff{}, err
	}
	item.Reason = strings.TrimSpace(item.Reason)
	item.Status = strings.TrimSpace(item.Status)
	item.ExpiresAt = item.ExpiresAt.UTC()
	item.CreatedAt = item.CreatedAt.UTC()
	if item.CompletedAt != nil {
		completed := item.CompletedAt.UTC()
		item.CompletedAt = &completed
	}
	return item, nil
}

func mapDBError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
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

func normalizeStringSlice(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return []string{}
	}
	return out
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

func normalizeJSON(value json.RawMessage, fallback json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(value))
	if trimmed == "" {
		if len(fallback) == 0 {
			return nil
		}
		return fallback
	}
	return json.RawMessage(trimmed)
}

func mustJSON(value any, fallback []byte) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fallback
	}
	return encoded
}

func nilUUIDString(id uuid.UUID) any {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	t := value.UTC()
	return &t
}
