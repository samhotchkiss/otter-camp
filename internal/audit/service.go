package audit

import (
	"context"
	"encoding/json"
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

func (s *Service) Record(ctx context.Context, event Event) error {
	if s == nil || s.repo == nil {
		return fmt.Errorf("audit recorder is not configured")
	}
	if event.OrgID == uuid.Nil {
		return fmt.Errorf("organization id is required")
	}
	if strings.TrimSpace(event.EventType) == "" {
		return fmt.Errorf("event_type is required")
	}
	if strings.TrimSpace(event.PrincipalType) == "" {
		return fmt.Errorf("principal_type is required")
	}
	if event.PrincipalID == uuid.Nil {
		return fmt.Errorf("principal_id is required")
	}

	metadata, err := json.Marshal(event.Metadata)
	if err != nil {
		return fmt.Errorf("marshal audit metadata: %w", err)
	}
	if len(metadata) == 0 {
		metadata = []byte(`{}`)
	}

	return s.repo.Insert(ctx, repo.AuditEvent{
		OrganizationID:  event.OrgID,
		EventType:       strings.TrimSpace(event.EventType),
		PrincipalType:   strings.TrimSpace(event.PrincipalType),
		PrincipalID:     event.PrincipalID,
		DelegatedByType: event.DelegatedByType,
		DelegatedByID:   event.DelegatedByID,
		TargetType:      event.TargetType,
		TargetID:        event.TargetID,
		Metadata:        metadata,
	})
}

func (s *Service) RecordAsync(ctx context.Context, event Event) {
	if s == nil {
		return
	}

	go func() {
		if err := s.Record(ctx, event); err != nil {
			s.logger.Warn("audit record failed", "event_type", event.EventType, "error", err)
		}
	}()
}
