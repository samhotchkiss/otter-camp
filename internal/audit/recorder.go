package audit

import (
	"context"

	"github.com/google/uuid"
)

// SystemPrincipalID is used for system-initiated audit actions.
var SystemPrincipalID = uuid.MustParse("00000000-0000-0000-0000-000000000000")

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
	IP              string
	Outcome         string
	Metadata        map[string]any
}
