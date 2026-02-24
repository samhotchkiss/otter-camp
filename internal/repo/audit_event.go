package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
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
	Metadata        map[string]any
	CreatedAt       time.Time
}

type AuditEventFilters struct {
	EventType     *string
	PrincipalID   *uuid.UUID
	TargetType    *string
	TargetID      *uuid.UUID
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
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
	metadata, err := normalizeMetadata(event.Metadata)
	if err != nil {
		return fmt.Errorf("normalize metadata: %w", err)
	}

	_, err = r.pool.Exec(ctx, `
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
	return err
}

func (r *AuditEventRepo) ListByOrg(ctx context.Context, organizationID uuid.UUID, filters AuditEventFilters, pagination Pagination) ([]AuditEvent, error) {
	if err := validateTargetFilter(filters.TargetType, filters.TargetID); err != nil {
		return nil, err
	}

	limit, offset := normalizePagination(pagination)
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			organization_id,
			event_type,
			principal_type,
			principal_id,
			delegated_by_type,
			delegated_by_id,
			target_type,
			target_id,
			metadata,
			created_at
		FROM audit_event
		WHERE organization_id = $1
		  AND ($2::text IS NULL OR event_type = $2)
		  AND ($3::uuid IS NULL OR principal_id = $3)
		  AND ($4::text IS NULL OR target_type = $4)
		  AND ($5::uuid IS NULL OR target_id = $5)
		  AND ($6::timestamptz IS NULL OR created_at >= $6)
		  AND ($7::timestamptz IS NULL OR created_at <= $7)
		ORDER BY created_at DESC
		LIMIT $8
		OFFSET $9
	`,
		organizationID,
		normalizeOptionalText(filters.EventType),
		filters.PrincipalID,
		normalizeOptionalText(filters.TargetType),
		filters.TargetID,
		filters.CreatedAfter,
		filters.CreatedBefore,
		limit,
		offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]AuditEvent, 0)
	for rows.Next() {
		event, scanErr := scanAuditEvent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return events, nil
}

func (r *AuditEventRepo) CountByOrg(ctx context.Context, organizationID uuid.UUID, filters AuditEventFilters) (int64, error) {
	if err := validateTargetFilter(filters.TargetType, filters.TargetID); err != nil {
		return 0, err
	}

	var count int64
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM audit_event
		WHERE organization_id = $1
		  AND ($2::text IS NULL OR event_type = $2)
		  AND ($3::uuid IS NULL OR principal_id = $3)
		  AND ($4::text IS NULL OR target_type = $4)
		  AND ($5::uuid IS NULL OR target_id = $5)
		  AND ($6::timestamptz IS NULL OR created_at >= $6)
		  AND ($7::timestamptz IS NULL OR created_at <= $7)
	`,
		organizationID,
		normalizeOptionalText(filters.EventType),
		filters.PrincipalID,
		normalizeOptionalText(filters.TargetType),
		filters.TargetID,
		filters.CreatedAfter,
		filters.CreatedBefore,
	).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

func scanAuditEvent(row scanner) (AuditEvent, error) {
	var (
		event        AuditEvent
		metadataJSON []byte
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
		&metadataJSON,
		&event.CreatedAt,
	); err != nil {
		return AuditEvent{}, err
	}

	event.Metadata = make(map[string]any)
	if len(metadataJSON) == 0 {
		return event, nil
	}
	if err := json.Unmarshal(metadataJSON, &event.Metadata); err != nil {
		return AuditEvent{}, fmt.Errorf("decode metadata: %w", err)
	}

	return event, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func normalizeMetadata(metadata map[string]any) ([]byte, error) {
	if len(metadata) == 0 {
		return []byte(`{}`), nil
	}
	return json.Marshal(metadata)
}

func validateTargetFilter(targetType *string, targetID *uuid.UUID) error {
	if (targetType == nil) != (targetID == nil) {
		return fmt.Errorf("target_type and target_id filters must both be set or both be nil")
	}
	return nil
}

func normalizeOptionalText(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func normalizePagination(p Pagination) (int, int) {
	limit := p.Limit
	switch {
	case limit <= 0:
		limit = 50
	case limit > 500:
		limit = 500
	}

	offset := p.Offset
	if offset < 0 {
		offset = 0
	}

	return limit, offset
}
