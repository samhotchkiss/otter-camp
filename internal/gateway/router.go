package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const maxFallbackHops = 3

type modelProfileLookup interface {
	GetCurrentByLogicalID(ctx context.Context, organizationID uuid.UUID, logicalProfileID string) (repo.ModelProfile, error)
}

type providerConnectionLookup interface {
	ListByProvider(ctx context.Context, organizationID, providerID uuid.UUID) ([]repo.ProviderConnection, error)
}

type Router struct {
	profiles    modelProfileLookup
	connections providerConnectionLookup
	health      *HealthChecker
}

func NewRouter(profiles modelProfileLookup, connections providerConnectionLookup, health *HealthChecker) *Router {
	if health == nil {
		health = NewHealthChecker()
	}
	return &Router{
		profiles:    profiles,
		connections: connections,
		health:      health,
	}
}

func (r *Router) SelectConnection(ctx context.Context, orgID uuid.UUID, profileID string, priority PriorityTier) (*repo.ProviderConnection, error) {
	if r == nil || r.profiles == nil || r.connections == nil {
		return nil, fmt.Errorf("gateway router is not configured")
	}
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("organization id is required")
	}

	currentProfileID := strings.TrimSpace(profileID)
	if currentProfileID == "" {
		return nil, fmt.Errorf("profile id is required")
	}
	_ = priority

	visited := map[string]struct{}{currentProfileID: {}}
	for hop := 0; hop < maxFallbackHops; hop++ {
		profile, err := r.profiles.GetCurrentByLogicalID(ctx, orgID, currentProfileID)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return nil, ErrNoHealthyConnection
			}
			return nil, err
		}

		selected, err := r.selectProviderConnection(ctx, orgID, profile.ProviderID)
		if err != nil {
			return nil, err
		}
		if selected != nil {
			return selected, nil
		}

		if profile.FallbackProfileID == nil || strings.TrimSpace(*profile.FallbackProfileID) == "" {
			return nil, ErrNoHealthyConnection
		}
		nextProfileID := strings.TrimSpace(*profile.FallbackProfileID)
		if _, seen := visited[nextProfileID]; seen {
			return nil, ErrNoHealthyConnection
		}
		visited[nextProfileID] = struct{}{}
		currentProfileID = nextProfileID
	}

	return nil, ErrNoHealthyConnection
}

func (r *Router) selectProviderConnection(ctx context.Context, orgID, providerID uuid.UUID) (*repo.ProviderConnection, error) {
	connections, err := r.connections.ListByProvider(ctx, orgID, providerID)
	if err != nil {
		return nil, err
	}

	bestIndex := -1
	bestRank := int(^uint(0) >> 1)
	for index, connection := range connections {
		if !connection.IsEnabled {
			continue
		}

		state := r.health.GetState(connection.ID)
		if state == HealthStateUnavailable {
			continue
		}

		rank := healthRank(state)*100000 + connection.FailoverPriority
		if rank < bestRank {
			bestRank = rank
			bestIndex = index
		}
	}

	if bestIndex == -1 {
		return nil, nil
	}
	selected := connections[bestIndex]
	return &selected, nil
}

func healthRank(state HealthState) int {
	switch state {
	case HealthStateHealthy:
		return 0
	case HealthStateDegraded:
		return 1
	case HealthStateRateLimited:
		return 2
	case HealthStateUnavailable:
		return 3
	default:
		return 4
	}
}
