package audit

import (
	"context"

	"github.com/google/uuid"
)

type AuditRecorder interface {
	Record(ctx context.Context, event Event) error
	RecordAsync(ctx context.Context, event Event)
}

type Event struct {
	OrgID           uuid.UUID
	EventType       string
	PrincipalType   string
	PrincipalID     uuid.UUID
	DelegatedByType *string
	DelegatedByID   *uuid.UUID
	TargetType      *string
	TargetID        *uuid.UUID
	Metadata        map[string]any
}

type NoopRecorder struct{}

func (NoopRecorder) Record(context.Context, Event) error {
	return nil
}

func (NoopRecorder) RecordAsync(context.Context, Event) {}
