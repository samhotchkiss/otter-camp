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

type TraceSpanFilter struct {
	OrganizationID uuid.UUID
	RunID          *uuid.UUID
	TaskID         *uuid.UUID
	AgentID        *uuid.UUID
	From           *time.Time
	To             *time.Time
	Limit          int
}

func (r *TraceSpanRepo) List(ctx context.Context, filter TraceSpanFilter) ([]TraceSpan, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	args := []any{filter.OrganizationID}
	where := []string{"organization_id = $1"}

	if filter.RunID != nil {
		args = append(args, filter.RunID.String())
		where = append(where, fmt.Sprintf("attributes->>'run_id' = $%d", len(args)))
	}
	if filter.TaskID != nil {
		args = append(args, filter.TaskID.String())
		where = append(where, fmt.Sprintf("attributes->>'task_id' = $%d", len(args)))
	}
	if filter.AgentID != nil {
		args = append(args, filter.AgentID.String())
		where = append(where, fmt.Sprintf("attributes->>'agent_id' = $%d", len(args)))
	}
	if filter.From != nil {
		args = append(args, *filter.From)
		where = append(where, fmt.Sprintf("created_at >= $%d", len(args)))
	}
	if filter.To != nil {
		args = append(args, *filter.To)
		where = append(where, fmt.Sprintf("created_at <= $%d", len(args)))
	}
	args = append(args, limit)

	query := fmt.Sprintf(`
		SELECT id, trace_id, parent_id, organization_id, span_name, service, kind, status,
		       attributes, events, started_at, ended_at, duration_ms, created_at
		FROM trace_span
		WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d
	`, strings.Join(where, " AND "), len(args))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	var spans []TraceSpan
	for rows.Next() {
		span, err := scanTraceSpan(rows)
		if err != nil {
			return nil, mapDBError(err)
		}
		spans = append(spans, span)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return spans, nil
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
