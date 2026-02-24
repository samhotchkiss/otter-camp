package audit

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type Repository interface {
	Insert(ctx context.Context, event repo.AuditEvent) error
}

type Service struct {
	repo   Repository
	logger *slog.Logger
}

func NewService(repository Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		repo:   repository,
		logger: logger,
	}
}

func NewNoopRecorder() AuditRecorder {
	return noopRecorder{}
}

func (s *Service) Record(ctx context.Context, event Event) error {
	if s == nil || s.repo == nil {
		return fmt.Errorf("audit recorder is not configured")
	}
	if event.OrgID == uuid.Nil {
		return fmt.Errorf("organization_id is required")
	}
	if strings.TrimSpace(event.EventType) == "" {
		return fmt.Errorf("event_type is required")
	}
	if strings.TrimSpace(event.PrincipalType) == "" {
		return fmt.Errorf("principal_type is required")
	}

	normalizedPrincipalType := normalizePrincipalType(event.PrincipalType)
	normalizedDelegatedByType, err := normalizeOptionalType(event.DelegatedByType)
	if err != nil {
		return err
	}
	normalizedTargetType, err := normalizeOptionalType(event.TargetType)
	if err != nil {
		return err
	}
	if (normalizedDelegatedByType == nil) != (event.DelegatedByID == nil) {
		return fmt.Errorf("delegated_by_type and delegated_by_id must both be set or both be nil")
	}

	return s.repo.Insert(ctx, repo.AuditEvent{
		OrganizationID:  event.OrgID,
		EventType:       strings.TrimSpace(event.EventType),
		PrincipalType:   normalizedPrincipalType,
		PrincipalID:     event.PrincipalID,
		DelegatedByType: normalizedDelegatedByType,
		DelegatedByID:   event.DelegatedByID,
		TargetType:      normalizedTargetType,
		TargetID:        event.TargetID,
		Metadata:        cloneMetadata(event.Metadata),
	})
}

func (s *Service) RecordAsync(ctx context.Context, event Event) {
	if s == nil {
		return
	}

	go func(background context.Context, eventCopy Event) {
		if err := s.Record(background, eventCopy); err != nil {
			s.logger.Warn("audit record async insert failed", "error", err, "event_type", eventCopy.EventType, "organization_id", eventCopy.OrgID)
		}
	}(withoutCancel(ctx), cloneEvent(event))
}

type noopRecorder struct{}

func (n noopRecorder) Record(context.Context, Event) error {
	return nil
}

func (n noopRecorder) RecordAsync(context.Context, Event) {}

func cloneEvent(event Event) Event {
	clone := event
	clone.Metadata = cloneMetadata(event.Metadata)
	return clone
}

func cloneMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(metadata))
	for k, v := range metadata {
		cloned[k] = v
	}
	return cloned
}

func normalizeOptionalType(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized := normalizePrincipalType(*value)
	if normalized == "" {
		return nil, fmt.Errorf("type value must not be blank when provided")
	}
	return &normalized, nil
}

func withoutCancel(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}
