package gateway

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type PriorityTier string

const (
	PrioritySyncInteractive PriorityTier = "sync_interactive"
	PrioritySyncSystem      PriorityTier = "sync_system"
	PriorityAsyncAgent      PriorityTier = "async_agent"
	PriorityAsyncSystem     PriorityTier = "async_system"
)

var priorityOrder = []PriorityTier{
	PrioritySyncInteractive,
	PrioritySyncSystem,
	PriorityAsyncAgent,
	PriorityAsyncSystem,
}

var defaultQueueTimeouts = map[PriorityTier]time.Duration{
	PrioritySyncInteractive: 30 * time.Second,
	PrioritySyncSystem:      60 * time.Second,
	PriorityAsyncAgent:      300 * time.Second,
	PriorityAsyncSystem:     600 * time.Second,
}

func (p PriorityTier) IsValid() bool {
	switch p {
	case PrioritySyncInteractive, PrioritySyncSystem, PriorityAsyncAgent, PriorityAsyncSystem:
		return true
	default:
		return false
	}
}

type GatewayRequest struct {
	OrganizationID    uuid.UUID
	ProfileID         string
	InvocationPurpose string
	Priority          PriorityTier
	InvocationID      uuid.UUID
}

type GatewayResponse struct {
	ConnectionID  uuid.UUID
	ProviderID    uuid.UUID
	AttemptNumber int
}

type ConnectionSelector interface {
	SelectConnection(ctx context.Context, orgID uuid.UUID, profileID, invocationPurpose string, priority PriorityTier) (*repo.ProviderConnection, error)
}

type GatewayExecutor interface {
	Execute(ctx context.Context, req GatewayRequest, connection repo.ProviderConnection) (GatewayResponse, error)
}

type InvocationRetryRecorder interface {
	RecordRetry(ctx context.Context, failedInvocationID uuid.UUID, attemptNumber int) (uuid.UUID, error)
}
