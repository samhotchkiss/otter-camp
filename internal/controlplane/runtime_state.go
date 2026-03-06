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
	"github.com/jackc/pgx/v5/pgxpool"
)

type RuntimeState struct {
	ID                  uuid.UUID
	OrganizationID      uuid.UUID
	ScopeType           string
	ScopeID             uuid.UUID
	ActiveRunID         *uuid.UUID
	ActivePrincipalType *string
	ActivePrincipalID   *uuid.UUID
	LockAcquiredAt      *time.Time
	LastWakeupAt        *time.Time
	Metadata            json.RawMessage
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type RuntimeStateContract struct {
	Status              string     `json:"status,omitempty"`
	TaskID              *uuid.UUID `json:"task_id,omitempty"`
	SessionID           *uuid.UUID `json:"session_id,omitempty"`
	FlowNodeID          *uuid.UUID `json:"flow_node_id,omitempty"`
	FlowNodeExecutionID *uuid.UUID `json:"flow_node_execution_id,omitempty"`
	ProviderSessionID   string     `json:"provider_session_id,omitempty"`
	LastProgressAt      *time.Time `json:"last_progress_at,omitempty"`
	LastProgressEvent   string     `json:"last_progress_event,omitempty"`
	PendingWakeReason   string     `json:"pending_wake_reason,omitempty"`
	DeferredRunID       *uuid.UUID `json:"deferred_run_id,omitempty"`
	ResumeDisposition   string     `json:"resume_disposition,omitempty"`
	FailureClass        string     `json:"failure_class,omitempty"`
	FailureReason       string     `json:"failure_reason,omitempty"`
	RetiredAt           *time.Time `json:"retired_at,omitempty"`
	RetireReason        string     `json:"retire_reason,omitempty"`
	WakeupSource        string     `json:"wakeup_source,omitempty"`
	WakeupKind          string     `json:"wakeup_kind,omitempty"`
}

type runtimeStateRepository interface {
	Ensure(ctx context.Context, organizationID uuid.UUID, scopeType string, scopeID uuid.UUID) (RuntimeState, error)
	GetByScope(ctx context.Context, scopeType string, scopeID uuid.UUID) (RuntimeState, error)
	UpdateMetadata(ctx context.Context, stateID uuid.UUID, metadata json.RawMessage) (RuntimeState, error)
	ListByTask(ctx context.Context, taskID uuid.UUID) ([]RuntimeState, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]RuntimeState, error)
	SetActive(ctx context.Context, stateID uuid.UUID, runID uuid.UUID, principalType string, principalID *uuid.UUID, lockAcquiredAt, lastWakeupAt time.Time) (RuntimeState, error)
	ClearActive(ctx context.Context, stateID uuid.UUID) (RuntimeState, error)
}

type RuntimeStateRepository struct {
	db queryExecutor
}

func NewRuntimeStateRepository(pool *pgxpool.Pool) *RuntimeStateRepository {
	return &RuntimeStateRepository{db: pool}
}

func (r *RuntimeStateRepository) Ensure(ctx context.Context, organizationID uuid.UUID, scopeType string, scopeID uuid.UUID) (RuntimeState, error) {
	scopeType = normalizeRuntimeScopeType(scopeType)
	if organizationID == uuid.Nil || scopeType == "" || scopeID == uuid.Nil {
		return RuntimeState{}, fmt.Errorf("organization_id, scope_type, and scope_id are required")
	}

	if _, err := r.db.Exec(ctx, `
		INSERT INTO runtime_state (
			organization_id,
			scope_type,
			scope_id
		)
		VALUES ($1, $2, $3)
		ON CONFLICT (scope_type, scope_id) DO NOTHING
	`, organizationID, scopeType, scopeID); err != nil {
		return RuntimeState{}, mapDBError(err)
	}

	return r.GetByScope(ctx, scopeType, scopeID)
}

func (r *RuntimeStateRepository) GetByScope(ctx context.Context, scopeType string, scopeID uuid.UUID) (RuntimeState, error) {
	scopeType = normalizeRuntimeScopeType(scopeType)
	if scopeType == "" || scopeID == uuid.Nil {
		return RuntimeState{}, ErrNotFound
	}

	row := r.db.QueryRow(ctx, `
		SELECT id, organization_id, scope_type, scope_id, active_run_id, active_principal_type,
		       active_principal_id, lock_acquired_at, last_wakeup_at, metadata, created_at, updated_at
		FROM runtime_state
		WHERE scope_type = $1
		  AND scope_id = $2
	`, scopeType, scopeID)

	item, err := scanRuntimeState(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeState{}, ErrNotFound
	}
	if err != nil {
		return RuntimeState{}, mapDBError(err)
	}
	return item, nil
}

func (r *RuntimeStateRepository) SetActive(
	ctx context.Context,
	stateID uuid.UUID,
	runID uuid.UUID,
	principalType string,
	principalID *uuid.UUID,
	lockAcquiredAt, lastWakeupAt time.Time,
) (RuntimeState, error) {
	principalType = normalizePrincipalType(principalType)
	if stateID == uuid.Nil || runID == uuid.Nil || principalType == "" {
		return RuntimeState{}, fmt.Errorf("state_id, run_id, and principal_type are required")
	}

	if principalType == "system" {
		principalID = nil
	}

	row := r.db.QueryRow(ctx, `
		UPDATE runtime_state
		SET active_run_id = $2,
		    active_principal_type = $3,
		    active_principal_id = $4,
		    lock_acquired_at = $5,
		    last_wakeup_at = $6
		WHERE id = $1
		RETURNING id, organization_id, scope_type, scope_id, active_run_id, active_principal_type,
		          active_principal_id, lock_acquired_at, last_wakeup_at, metadata, created_at, updated_at
	`, stateID, runID, principalType, principalID, lockAcquiredAt.UTC(), lastWakeupAt.UTC())

	item, err := scanRuntimeState(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeState{}, ErrNotFound
	}
	if err != nil {
		return RuntimeState{}, mapDBError(err)
	}
	return item, nil
}

func (r *RuntimeStateRepository) UpdateMetadata(ctx context.Context, stateID uuid.UUID, metadata json.RawMessage) (RuntimeState, error) {
	if stateID == uuid.Nil {
		return RuntimeState{}, ErrNotFound
	}

	row := r.db.QueryRow(ctx, `
		UPDATE runtime_state
		SET metadata = $2::jsonb
		WHERE id = $1
		RETURNING id, organization_id, scope_type, scope_id, active_run_id, active_principal_type,
		          active_principal_id, lock_acquired_at, last_wakeup_at, metadata, created_at, updated_at
	`, stateID, normalizeJSON(metadata, json.RawMessage(`{}`)))

	item, err := scanRuntimeState(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeState{}, ErrNotFound
	}
	if err != nil {
		return RuntimeState{}, mapDBError(err)
	}
	return item, nil
}

func (r *RuntimeStateRepository) ClearActive(ctx context.Context, stateID uuid.UUID) (RuntimeState, error) {
	if stateID == uuid.Nil {
		return RuntimeState{}, ErrNotFound
	}

	row := r.db.QueryRow(ctx, `
		UPDATE runtime_state
		SET active_run_id = NULL,
		    active_principal_type = NULL,
		    active_principal_id = NULL,
		    lock_acquired_at = NULL
		WHERE id = $1
		RETURNING id, organization_id, scope_type, scope_id, active_run_id, active_principal_type,
		          active_principal_id, lock_acquired_at, last_wakeup_at, metadata, created_at, updated_at
	`, stateID)

	item, err := scanRuntimeState(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeState{}, ErrNotFound
	}
	if err != nil {
		return RuntimeState{}, mapDBError(err)
	}
	return item, nil
}

func (r *RuntimeStateRepository) ListByTask(ctx context.Context, taskID uuid.UUID) ([]RuntimeState, error) {
	if taskID == uuid.Nil {
		return nil, nil
	}

	rows, err := r.db.Query(ctx, `
		SELECT id, organization_id, scope_type, scope_id, active_run_id, active_principal_type,
		       active_principal_id, lock_acquired_at, last_wakeup_at, metadata, created_at, updated_at
		FROM runtime_state
		WHERE (scope_type = 'task' AND scope_id = $1)
		   OR (scope_type = 'session' AND scope_id IN (
		        SELECT id
		        FROM chat_session
		        WHERE scope_type = 'project_task'
		          AND scope_id = $1
		   ))
		ORDER BY created_at ASC, id ASC
	`, taskID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	var items []RuntimeState
	for rows.Next() {
		item, scanErr := scanRuntimeState(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDBError(err)
	}
	return items, nil
}

func (r *RuntimeStateRepository) ListByProject(ctx context.Context, projectID uuid.UUID) ([]RuntimeState, error) {
	if projectID == uuid.Nil {
		return nil, nil
	}

	rows, err := r.db.Query(ctx, `
		SELECT id, organization_id, scope_type, scope_id, active_run_id, active_principal_type,
		       active_principal_id, lock_acquired_at, last_wakeup_at, metadata, created_at, updated_at
		FROM runtime_state
		WHERE (scope_type = 'task' AND scope_id IN (
		        SELECT id FROM project_task WHERE project_id = $1
		   ))
		   OR (scope_type = 'session' AND scope_id IN (
		        SELECT id
		        FROM chat_session
		        WHERE (scope_type = 'project' AND scope_id = $1)
		           OR (scope_type = 'project_task' AND scope_id IN (
		                SELECT id FROM project_task WHERE project_id = $1
		           ))
		   ))
		ORDER BY created_at ASC, id ASC
	`, projectID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	var items []RuntimeState
	for rows.Next() {
		item, scanErr := scanRuntimeState(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDBError(err)
	}
	return items, nil
}

type runtimeStateScanner interface {
	Scan(dest ...any) error
}

func scanRuntimeState(row runtimeStateScanner) (RuntimeState, error) {
	var item RuntimeState
	if err := row.Scan(
		&item.ID,
		&item.OrganizationID,
		&item.ScopeType,
		&item.ScopeID,
		&item.ActiveRunID,
		&item.ActivePrincipalType,
		&item.ActivePrincipalID,
		&item.LockAcquiredAt,
		&item.LastWakeupAt,
		&item.Metadata,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return RuntimeState{}, err
	}
	item.ScopeType = normalizeRuntimeScopeType(item.ScopeType)
	item.Metadata = normalizeJSON(item.Metadata, json.RawMessage(`{}`))
	return item, nil
}

func normalizeRuntimeScopeType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "task":
		return "task"
	case "session":
		return "session"
	default:
		return ""
	}
}

func (s RuntimeState) Contract() RuntimeStateContract {
	return runtimeStateContractFromMetadata(s.Metadata)
}

func runtimeStateContractFromMetadata(metadata json.RawMessage) RuntimeStateContract {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return RuntimeStateContract{}
	}
	var contract RuntimeStateContract
	if err := json.Unmarshal(metadata, &contract); err != nil {
		return RuntimeStateContract{}
	}
	return contract.normalize()
}

func (c RuntimeStateContract) JSON() json.RawMessage {
	normalized := c.normalize()
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return normalizeJSON(encoded, json.RawMessage(`{}`))
}

func (c RuntimeStateContract) normalize() RuntimeStateContract {
	c.Status = normalizeRuntimeContractStatus(c.Status)
	c.ProviderSessionID = strings.TrimSpace(c.ProviderSessionID)
	c.LastProgressEvent = strings.TrimSpace(c.LastProgressEvent)
	c.PendingWakeReason = strings.TrimSpace(c.PendingWakeReason)
	c.ResumeDisposition = normalizeRuntimeResumeDisposition(c.ResumeDisposition)
	c.FailureClass = normalizeFailureClass(c.FailureClass)
	c.FailureReason = strings.TrimSpace(c.FailureReason)
	c.RetireReason = strings.TrimSpace(c.RetireReason)
	c.WakeupSource = strings.TrimSpace(c.WakeupSource)
	c.WakeupKind = strings.TrimSpace(c.WakeupKind)
	if c.LastProgressAt != nil {
		ts := c.LastProgressAt.UTC()
		c.LastProgressAt = &ts
	}
	if c.RetiredAt != nil {
		ts := c.RetiredAt.UTC()
		c.RetiredAt = &ts
	}
	return c
}

func normalizeRuntimeContractStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "active":
		return "active"
	case "stale":
		return "stale"
	case "resumable":
		return "resumable"
	case "terminal":
		return "terminal"
	case "retired":
		return "retired"
	default:
		return ""
	}
}

func normalizeRuntimeResumeDisposition(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "resumable":
		return "resumable"
	case "terminal":
		return "terminal"
	default:
		return ""
	}
}
