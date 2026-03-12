package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const maxFallbackHops = 3
const systemHaikuProfileID = "haiku"

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

func (r *Router) SelectConnection(ctx context.Context, orgID uuid.UUID, profileID, invocationPurpose string, priority PriorityTier) (*repo.ProviderConnection, error) {
	if r == nil || r.profiles == nil || r.connections == nil {
		return nil, fmt.Errorf("gateway router is not configured")
	}
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("organization id is required")
	}

	currentProfileID := routedProfileID(strings.TrimSpace(profileID), invocationPurpose)
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

func routedProfileID(profileID, invocationPurpose string) string {
	switch strings.TrimSpace(invocationPurpose) {
	case "listening_eval",
		"summarization",
		"skill_summarization",
		"memory_extraction",
		"memory_retrieval",
		"memory_retrieval_classification",
		"memory_dedup",
		"memory_contradiction":
		return systemHaikuProfileID
	default:
		return profileID
	}
}

func (r *Router) selectProviderConnection(ctx context.Context, orgID, providerID uuid.UUID) (*repo.ProviderConnection, error) {
	connections, err := r.connections.ListByProvider(ctx, orgID, providerID)
	if err != nil {
		return nil, err
	}

	bestIndex := -1
	var bestState HealthState
	var bestUpdatedAt time.Time
	var bestPriority int
	for index, connection := range connections {
		if !connection.IsEnabled {
			continue
		}

		state := persistedHealthState(connection)
		if r.health != nil {
			if inMemory, known := r.health.GetStateKnown(connection.ID); known {
				state = inMemory
			}
		}
		if state == HealthStateUnavailable {
			continue
		}

		if bestIndex == -1 || connectionPreferredOver(state, connection, bestState, bestUpdatedAt, bestPriority) {
			bestIndex = index
			bestState = state
			bestUpdatedAt = connection.UpdatedAt
			bestPriority = connection.FailoverPriority
		}
	}

	if bestIndex == -1 {
		return nil, nil
	}
	selected := connections[bestIndex]
	return &selected, nil
}

func persistedHealthState(connection repo.ProviderConnection) HealthState {
	switch HealthState(strings.TrimSpace(connection.HealthStatus)) {
	case HealthStateHealthy:
		return HealthStateHealthy
	case HealthStateDegraded:
		return HealthStateDegraded
	case HealthStateRateLimited:
		if rateLimitBackoffExpired(connection.UpdatedAt) {
			return HealthStateDegraded
		}
		return HealthStateRateLimited
	case HealthStateUnavailable:
		return HealthStateUnavailable
	default:
		return HealthStateHealthy
	}
}

func rateLimitBackoffExpired(updatedAt time.Time) bool {
	if updatedAt.IsZero() {
		return false
	}
	return time.Since(updatedAt.UTC()) >= healthProbeBackoffMax
}

func connectionPreferredOver(state HealthState, connection repo.ProviderConnection, bestState HealthState, bestUpdatedAt time.Time, bestPriority int) bool {
	stateRank := healthRank(state)
	bestRank := healthRank(bestState)
	if stateRank != bestRank {
		return stateRank < bestRank
	}

	if state != HealthStateHealthy {
		leftUpdated := connection.UpdatedAt.UTC()
		rightUpdated := bestUpdatedAt.UTC()
		switch {
		case leftUpdated.IsZero() != rightUpdated.IsZero():
			return !leftUpdated.IsZero()
		case !leftUpdated.Equal(rightUpdated):
			return leftUpdated.Before(rightUpdated)
		}
	}

	if connection.FailoverPriority != bestPriority {
		return connection.FailoverPriority < bestPriority
	}
	return false
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
