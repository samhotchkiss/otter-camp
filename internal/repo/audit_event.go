package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
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
	IP              string
	Outcome         string
	Metadata        map[string]any
	CreatedAt       time.Time
}

type AuditEventFilters struct {
	EventType     *string
	PrincipalType *string
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

var (
	auditOpenAIKeyPattern      = regexp.MustCompile(`sk-[A-Za-z0-9][A-Za-z0-9-]{19,}`)
	auditBearerPattern         = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._-]{20,}`)
	auditJWTPattern            = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
	auditKnownEnvSecretPattern = regexp.MustCompile(`(?m)(OPENAI_API_KEY|ANTHROPIC_API_KEY|OTTERCAMP_MASTER_KEY|OTTERCAMP_DB_URL)=([^\s"']+)`)
)

func NewAuditEventRepo(pool *pgxpool.Pool) *AuditEventRepo {
	return &AuditEventRepo{pool: pool}
}

func (r *AuditEventRepo) Insert(ctx context.Context, event AuditEvent) error {
	metadata, normalizedIP, normalizedOutcome, err := normalizeMetadata(event.Metadata, event.IP, event.Outcome)
	if err != nil {
		return fmt.Errorf("normalize metadata: %w", err)
	}
	event.IP = normalizedIP
	event.Outcome = normalizedOutcome

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
			  AND ($3::text IS NULL OR principal_type = $3)
			  AND ($4::uuid IS NULL OR principal_id = $4)
			  AND ($5::text IS NULL OR target_type = $5)
			  AND ($6::uuid IS NULL OR target_id = $6)
			  AND ($7::timestamptz IS NULL OR created_at >= $7)
			  AND ($8::timestamptz IS NULL OR created_at <= $8)
			ORDER BY created_at DESC
			LIMIT $9
			OFFSET $10
		`,
		organizationID,
		normalizeOptionalText(filters.EventType),
		normalizeOptionalText(filters.PrincipalType),
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
			  AND ($3::text IS NULL OR principal_type = $3)
			  AND ($4::uuid IS NULL OR principal_id = $4)
			  AND ($5::text IS NULL OR target_type = $5)
			  AND ($6::uuid IS NULL OR target_id = $6)
			  AND ($7::timestamptz IS NULL OR created_at >= $7)
			  AND ($8::timestamptz IS NULL OR created_at <= $8)
		`,
		organizationID,
		normalizeOptionalText(filters.EventType),
		normalizeOptionalText(filters.PrincipalType),
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
	event.IP = strings.TrimSpace(toMetadataString(event.Metadata["ip"]))
	if event.IP == "" {
		event.IP = strings.TrimSpace(toMetadataString(event.Metadata["ip_address"]))
	}
	event.Outcome = normalizeAuditOutcome(toMetadataString(event.Metadata["outcome"]))

	return event, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func normalizeMetadata(metadata map[string]any, ip, outcome string) ([]byte, string, string, error) {
	if len(metadata) == 0 {
		metadata = map[string]any{}
	}
	metadata = scrubMetadata(metadata)

	normalizedIP := strings.TrimSpace(ip)
	if normalizedIP == "" {
		normalizedIP = strings.TrimSpace(toMetadataString(metadata["ip"]))
	}
	if normalizedIP == "" {
		normalizedIP = strings.TrimSpace(toMetadataString(metadata["ip_address"]))
	}
	normalizedOutcome := normalizeAuditOutcome(outcome)
	if strings.TrimSpace(outcome) == "" {
		normalizedOutcome = normalizeAuditOutcome(toMetadataString(metadata["outcome"]))
	}

	metadata["ip"] = normalizedIP
	metadata["outcome"] = normalizedOutcome

	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, "", "", err
	}
	return encoded, normalizedIP, normalizedOutcome, nil
}

func scrubMetadata(metadata map[string]any) map[string]any {
	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = scrubMetadataValue(value)
	}
	return cloned
}

func scrubMetadataValue(value any) any {
	switch typed := value.(type) {
	case string:
		return scrubAuditString(typed)
	case map[string]any:
		return scrubMetadata(typed)
	case []any:
		items := make([]any, len(typed))
		for idx := range typed {
			items[idx] = scrubMetadataValue(typed[idx])
		}
		return items
	default:
		return typed
	}
}

func scrubAuditString(input string) string {
	out := auditOpenAIKeyPattern.ReplaceAllString(input, "[REDACTED]")
	out = auditBearerPattern.ReplaceAllString(out, "Bearer [REDACTED]")
	out = auditJWTPattern.ReplaceAllString(out, "[JWT_REDACTED]")
	out = auditKnownEnvSecretPattern.ReplaceAllString(out, "${1}=[REDACTED]")
	return out
}

func toMetadataString(value any) string {
	typed, _ := value.(string)
	return typed
}

func normalizeAuditOutcome(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "failure", "error":
		return "failure"
	default:
		return "success"
	}
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
