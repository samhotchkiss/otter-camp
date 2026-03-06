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

type runtimeStateRepository interface {
	Ensure(ctx context.Context, organizationID uuid.UUID, scopeType string, scopeID uuid.UUID) (RuntimeState, error)
	GetByScope(ctx context.Context, scopeType string, scopeID uuid.UUID) (RuntimeState, error)
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
