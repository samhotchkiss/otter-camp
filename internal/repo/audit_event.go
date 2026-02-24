package repo

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

type AuditEvent struct {
	ID              uuid.UUID
	OrganizationID  uuid.UUID
	EventType       string
	PrincipalType   string
	PrincipalID     uuid.UUID
	DelegatedByType *string
	DelegatedByID   *uuid.UUID
	TargetType      *string
	TargetID        *uuid.UUID
	Metadata        json.RawMessage
	CreatedAt       time.Time
}

type AuditEventFilters struct {
	EventType   *string
	PrincipalID *uuid.UUID
	TargetType  *string
	TargetID    *uuid.UUID
	CreatedFrom *time.Time
	CreatedTo   *time.Time
}

type Pagination struct {
	Limit  int
	Offset int
}

type AuditEventRepo struct {
	pool *pgxpool.Pool
}

func NewAuditEventRepo(pool *pgxpool.Pool) *AuditEventRepo {
	return &AuditEventRepo{pool: pool}
}

func (r *AuditEventRepo) Insert(ctx context.Context, event AuditEvent) error {
	metadata := event.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	if !json.Valid(metadata) {
		return fmt.Errorf("metadata must be valid json")
	}

	_, err := r.pool.Exec(ctx, `
		INSERT INTO audit_event (
			organization_id,
			event_type,
			principal_type,
			principal_id,
			delegated_by_type,
			delegated_by_id,
			target_type,
			target_id,
			metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)
	`, event.OrganizationID, event.EventType, event.PrincipalType, event.PrincipalID, event.DelegatedByType, event.DelegatedByID, event.TargetType, event.TargetID, metadata)
	if err != nil {
		return mapDBError(err)
	}
	return nil
}

func (r *AuditEventRepo) ListByOrg(ctx context.Context, orgID uuid.UUID, filters AuditEventFilters, pagination Pagination) ([]AuditEvent, error) {
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("organization id is required")
	}

	limit := pagination.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	offset := pagination.Offset
	if offset < 0 {
		offset = 0
	}

	whereClause, args := buildAuditEventWhereClause(orgID, filters)
	args = append(args, limit, offset)
	limitPlaceholder := len(args) - 1
	offsetPlaceholder := len(args)

	query := fmt.Sprintf(`
		SELECT id, organization_id, event_type, principal_type, principal_id, delegated_by_type, delegated_by_id, target_type, target_id, metadata, created_at
		FROM audit_event
		%s
		ORDER BY created_at DESC, id DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, limitPlaceholder, offsetPlaceholder)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	events := make([]AuditEvent, 0)
	for rows.Next() {
		event, scanErr := scanAuditEvent(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		events = append(events, event)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}

	return events, nil
}

func (r *AuditEventRepo) CountByOrg(ctx context.Context, orgID uuid.UUID, filters AuditEventFilters) (int64, error) {
	if orgID == uuid.Nil {
		return 0, fmt.Errorf("organization id is required")
	}

	whereClause, args := buildAuditEventWhereClause(orgID, filters)
	query := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM audit_event
		%s
	`, whereClause)

	var count int64
	if err := r.pool.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, mapDBError(err)
	}
	return count, nil
}

func buildAuditEventWhereClause(orgID uuid.UUID, filters AuditEventFilters) (string, []any) {
	conditions := []string{"organization_id = $1"}
	args := []any{orgID}
	placeholder := 2

	if filters.EventType != nil && strings.TrimSpace(*filters.EventType) != "" {
		conditions = append(conditions, fmt.Sprintf("event_type = $%d", placeholder))
		args = append(args, strings.TrimSpace(*filters.EventType))
		placeholder++
	}

	if filters.PrincipalID != nil && *filters.PrincipalID != uuid.Nil {
		conditions = append(conditions, fmt.Sprintf("principal_id = $%d", placeholder))
		args = append(args, *filters.PrincipalID)
		placeholder++
	}

	if filters.TargetType != nil && strings.TrimSpace(*filters.TargetType) != "" {
		conditions = append(conditions, fmt.Sprintf("target_type = $%d", placeholder))
		args = append(args, strings.TrimSpace(*filters.TargetType))
		placeholder++
	}

	if filters.TargetID != nil && *filters.TargetID != uuid.Nil {
		conditions = append(conditions, fmt.Sprintf("target_id = $%d", placeholder))
		args = append(args, *filters.TargetID)
		placeholder++
	}

	if filters.CreatedFrom != nil {
		conditions = append(conditions, fmt.Sprintf("created_at >= $%d", placeholder))
		args = append(args, *filters.CreatedFrom)
		placeholder++
	}

	if filters.CreatedTo != nil {
		conditions = append(conditions, fmt.Sprintf("created_at <= $%d", placeholder))
		args = append(args, *filters.CreatedTo)
		placeholder++
	}

	return "WHERE " + strings.Join(conditions, " AND "), args
}

func scanAuditEvent(row pgx.Row) (AuditEvent, error) {
	var (
		event    AuditEvent
		metadata []byte
	)

	if err := row.Scan(
		&event.ID,
		&event.OrganizationID,
		&event.EventType,
		&event.PrincipalType,
		&event.PrincipalID,
		&event.DelegatedByType,
		&event.DelegatedByID,
		&event.TargetType,
		&event.TargetID,
		&metadata,
		&event.CreatedAt,
	); err != nil {
		return AuditEvent{}, err
	}

	if len(metadata) == 0 {
		event.Metadata = json.RawMessage(`{}`)
		return event, nil
	}
	if !json.Valid(metadata) {
		return AuditEvent{}, errors.New("invalid metadata json")
	}
	event.Metadata = json.RawMessage(metadata)
	return event, nil
}
