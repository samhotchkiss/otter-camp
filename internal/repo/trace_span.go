package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TraceSpan struct {
	ID             uuid.UUID
	TraceID        uuid.UUID
	ParentID       *uuid.UUID
	OrganizationID *uuid.UUID
	SpanName       string
	Service        string
	Kind           string
	Status         string
	Attributes     json.RawMessage
	Events         json.RawMessage
	StartedAt      time.Time
	EndedAt        *time.Time
	DurationMS     *int
	CreatedAt      time.Time
}

type TraceSpanFilters struct {
	RunID         *uuid.UUID
	TaskID        *uuid.UUID
	AgentID       *uuid.UUID
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
	Limit         int
}

type TraceSpanRepo struct {
	pool *pgxpool.Pool
}

func NewTraceSpanRepo(pool *pgxpool.Pool) *TraceSpanRepo {
	return &TraceSpanRepo{pool: pool}
}

func (r *TraceSpanRepo) Create(ctx context.Context, span TraceSpan) (TraceSpan, error) {
	if strings.TrimSpace(span.SpanName) == "" {
		return TraceSpan{}, fmt.Errorf("span_name is required")
	}
	if span.TraceID == uuid.Nil {
		return TraceSpan{}, fmt.Errorf("trace_id is required")
	}
	if strings.TrimSpace(span.Kind) == "" {
		return TraceSpan{}, fmt.Errorf("kind is required")
	}
	if strings.TrimSpace(span.Status) == "" {
		span.Status = "unset"
	}
	if strings.TrimSpace(span.Service) == "" {
		span.Service = "ottercamp"
	}
	if span.StartedAt.IsZero() {
		span.StartedAt = time.Now().UTC()
	}
	createdAt := time.Now().UTC()
	if err := r.ensureCreatedAtPartition(ctx, createdAt); err != nil {
		return TraceSpan{}, err
	}

	attributes := span.Attributes
	if len(attributes) == 0 {
		attributes = json.RawMessage(`{}`)
	}
	events := span.Events
	if len(events) == 0 {
		events = json.RawMessage(`[]`)
	}
	if !json.Valid(attributes) || !json.Valid(events) {
		return TraceSpan{}, fmt.Errorf("trace span attributes/events must be valid json")
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO trace_span (
			trace_id,
			parent_id,
			organization_id,
			span_name,
			service,
			kind,
			status,
			attributes,
			events,
			started_at,
			ended_at,
			duration_ms,
			created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb, $10, $11, $12, $13)
		RETURNING id, trace_id, parent_id, organization_id, span_name, service, kind, status,
		          attributes, events, started_at, ended_at, duration_ms, created_at
	`, span.TraceID, span.ParentID, span.OrganizationID, strings.TrimSpace(span.SpanName), strings.TrimSpace(span.Service), strings.TrimSpace(span.Kind), strings.TrimSpace(span.Status), attributes, events, span.StartedAt.UTC(), span.EndedAt, span.DurationMS, createdAt)

	created, err := scanTraceSpan(row)
	if err != nil {
		return TraceSpan{}, mapDBError(err)
	}
	return created, nil
}

func (r *TraceSpanRepo) ensureCreatedAtPartition(ctx context.Context, createdAt time.Time) error {
	if r.pool == nil {
		return fmt.Errorf("trace span repository requires database pool")
	}

	start, end := traceSpanWeekBounds(createdAt.UTC())
	partitionName := "trace_span_p_" + start.Format("20060102")
	partitionIdent := pgx.Identifier{partitionName}.Sanitize()
	traceIDIndex := pgx.Identifier{partitionName + "_trace_id_idx"}.Sanitize()
	orgCreatedIndex := pgx.Identifier{partitionName + "_org_created_idx"}.Sanitize()

	if _, err := r.pool.Exec(ctx, fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s PARTITION OF trace_span FOR VALUES FROM ('%s') TO ('%s')",
		partitionIdent,
		start.Format(time.RFC3339),
		end.Format(time.RFC3339),
	)); err != nil {
		return mapDBError(err)
	}
	if _, err := r.pool.Exec(ctx, fmt.Sprintf(
		"CREATE INDEX IF NOT EXISTS %s ON %s (trace_id)",
		traceIDIndex,
		partitionIdent,
	)); err != nil {
		return mapDBError(err)
	}
	if _, err := r.pool.Exec(ctx, fmt.Sprintf(
		"CREATE INDEX IF NOT EXISTS %s ON %s (organization_id, created_at)",
		orgCreatedIndex,
		partitionIdent,
	)); err != nil {
		return mapDBError(err)
	}
	return nil
}

// traceSpanWeekBounds returns the [start, end) UTC day boundary for the given
// timestamp. The name is kept for backward compatibility; it now returns a
// single-day window to match the daily partition naming convention used by
// TraceSpanPartitionJob (trace_span_p_YYYYMMDD = one UTC day each).
func traceSpanWeekBounds(at time.Time) (time.Time, time.Time) {
	start := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
	return start, start.Add(24 * time.Hour)
}

func (r *TraceSpanRepo) GetByID(ctx context.Context, id uuid.UUID) (TraceSpan, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, trace_id, parent_id, organization_id, span_name, service, kind, status,
		       attributes, events, started_at, ended_at, duration_ms, created_at
		FROM trace_span
		WHERE id = $1
	`, id)
	item, err := scanTraceSpan(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return TraceSpan{}, ErrNotFound
		}
		return TraceSpan{}, mapDBError(err)
	}
	return item, nil
}

func (r *TraceSpanRepo) ListByOrganization(ctx context.Context, organizationID uuid.UUID, filters TraceSpanFilters) ([]TraceSpan, error) {
	limit := filters.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	query := `
		SELECT id, trace_id, parent_id, organization_id, span_name, service, kind, status,
		       attributes, events, started_at, ended_at, duration_ms, created_at
		FROM trace_span
		WHERE organization_id = $1
	`
	args := []any{organizationID}
	nextArg := 2

	if filters.RunID != nil {
		query += fmt.Sprintf(" AND attributes->>'run_id' = $%d", nextArg)
		args = append(args, filters.RunID.String())
		nextArg++
	}
	if filters.TaskID != nil {
		query += fmt.Sprintf(" AND attributes->>'task_id' = $%d", nextArg)
		args = append(args, filters.TaskID.String())
		nextArg++
	}
	if filters.AgentID != nil {
		query += fmt.Sprintf(" AND attributes->>'agent_id' = $%d", nextArg)
		args = append(args, filters.AgentID.String())
		nextArg++
	}
	if filters.CreatedAfter != nil {
		query += fmt.Sprintf(" AND created_at >= $%d", nextArg)
		args = append(args, filters.CreatedAfter.UTC())
		nextArg++
	}
	if filters.CreatedBefore != nil {
		query += fmt.Sprintf(" AND created_at <= $%d", nextArg)
		args = append(args, filters.CreatedBefore.UTC())
		nextArg++
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", nextArg)
	args = append(args, limit)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	items := make([]TraceSpan, 0, limit)
	for rows.Next() {
		item, scanErr := scanTraceSpan(rows)
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

func scanTraceSpan(row interface{ Scan(dest ...any) error }) (TraceSpan, error) {
	var item TraceSpan
	if err := row.Scan(
		&item.ID,
		&item.TraceID,
		&item.ParentID,
		&item.OrganizationID,
		&item.SpanName,
		&item.Service,
		&item.Kind,
		&item.Status,
		&item.Attributes,
		&item.Events,
		&item.StartedAt,
		&item.EndedAt,
		&item.DurationMS,
		&item.CreatedAt,
	); err != nil {
		return TraceSpan{}, err
	}
	return item, nil
}
