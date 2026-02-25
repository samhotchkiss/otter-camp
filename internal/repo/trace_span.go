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
			duration_ms
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb, $10, $11, $12)
		RETURNING id, trace_id, parent_id, organization_id, span_name, service, kind, status,
		          attributes, events, started_at, ended_at, duration_ms, created_at
	`, span.TraceID, span.ParentID, span.OrganizationID, strings.TrimSpace(span.SpanName), strings.TrimSpace(span.Service), strings.TrimSpace(span.Kind), strings.TrimSpace(span.Status), attributes, events, span.StartedAt.UTC(), span.EndedAt, span.DurationMS)

	created, err := scanTraceSpan(row)
	if err != nil {
		return TraceSpan{}, mapDBError(err)
	}
	return created, nil
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
