package observability

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

var ErrTraceSpanAppendOnly = errors.New("trace spans are append-only")

type TraceSpanService struct {
	repo *repo.TraceSpanRepo
}

func NewTraceSpanService(pool *pgxpool.Pool) (*TraceSpanService, error) {
	if pool == nil {
		return nil, fmt.Errorf("trace span service requires database pool")
	}
	return &TraceSpanService{repo: repo.NewTraceSpanRepo(pool)}, nil
}

func (s *TraceSpanService) Create(ctx context.Context, span repo.TraceSpan) (repo.TraceSpan, error) {
	if s == nil || s.repo == nil {
		return repo.TraceSpan{}, fmt.Errorf("trace span service is not configured")
	}
	if span.StartedAt.IsZero() {
		span.StartedAt = time.Now().UTC()
	}
	return s.repo.Create(ctx, span)
}

func (s *TraceSpanService) Update(_ context.Context, _ uuid.UUID, _ map[string]any) error {
	return ErrTraceSpanAppendOnly
}
