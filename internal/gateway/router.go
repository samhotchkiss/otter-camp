package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const maxFallbackHops = 3
const systemHaikuProfileID = "haiku"
const providerConnectionMetadataHealthRateLimitedUntil = "health_rate_limited_until"

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
	var earliestRateLimitRetryAfter time.Duration
	for hop := 0; hop < maxFallbackHops; hop++ {
		profile, err := r.profiles.GetCurrentByLogicalID(ctx, orgID, currentProfileID)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				if earliestRateLimitRetryAfter > 0 {
					return nil, ConnectionsRateLimitedError{RetryAfter: earliestRateLimitRetryAfter}
				}
				return nil, ErrNoHealthyConnection
			}
			return nil, err
		}

		selected, retryAfter, err := r.selectProviderConnection(ctx, orgID, profile.ProviderID)
		if err != nil {
			return nil, err
		}
		if selected != nil {
			return selected, nil
		}
		if retryAfter > 0 && (earliestRateLimitRetryAfter <= 0 || retryAfter < earliestRateLimitRetryAfter) {
			earliestRateLimitRetryAfter = retryAfter
		}

		if profile.FallbackProfileID == nil || strings.TrimSpace(*profile.FallbackProfileID) == "" {
			if earliestRateLimitRetryAfter > 0 {
				return nil, ConnectionsRateLimitedError{RetryAfter: earliestRateLimitRetryAfter}
			}
			return nil, ErrNoHealthyConnection
		}
		nextProfileID := strings.TrimSpace(*profile.FallbackProfileID)
		if _, seen := visited[nextProfileID]; seen {
			if earliestRateLimitRetryAfter > 0 {
				return nil, ConnectionsRateLimitedError{RetryAfter: earliestRateLimitRetryAfter}
			}
			return nil, ErrNoHealthyConnection
		}
		visited[nextProfileID] = struct{}{}
		currentProfileID = nextProfileID
	}

	if earliestRateLimitRetryAfter > 0 {
		return nil, ConnectionsRateLimitedError{RetryAfter: earliestRateLimitRetryAfter}
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

func (r *Router) selectProviderConnection(ctx context.Context, orgID, providerID uuid.UUID) (*repo.ProviderConnection, time.Duration, error) {
	connections, err := r.connections.ListByProvider(ctx, orgID, providerID)
	if err != nil {
		return nil, 0, err
	}

	now := time.Now().UTC()
	bestIndex := -1
	var bestState HealthState
	var bestUpdatedAt time.Time
	var bestPriority int
	var earliestRateLimitedReadyAt time.Time
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
		if state == HealthStateRateLimited {
			if readyAt, ok := persistedRateLimitRecoveryReadyAt(connection); ok {
				if earliestRateLimitedReadyAt.IsZero() || readyAt.Before(earliestRateLimitedReadyAt) {
					earliestRateLimitedReadyAt = readyAt
				}
			}
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
		if !earliestRateLimitedReadyAt.IsZero() && earliestRateLimitedReadyAt.After(now) {
			return nil, earliestRateLimitedReadyAt.Sub(now), nil
		}
		return nil, 0, nil
	}
	selected := connections[bestIndex]
	return &selected, 0, nil
}

func persistedHealthState(connection repo.ProviderConnection) HealthState {
	switch HealthState(strings.TrimSpace(connection.HealthStatus)) {
	case HealthStateHealthy:
		return HealthStateHealthy
	case HealthStateDegraded:
		return HealthStateDegraded
	case HealthStateRateLimited:
		if rateLimitBackoffExpired(connection) {
			return HealthStateDegraded
		}
		return HealthStateRateLimited
	case HealthStateUnavailable:
		if unavailableBackoffExpired(connection) {
			return HealthStateDegraded
		}
		return HealthStateUnavailable
	default:
		return HealthStateHealthy
	}
}

func EffectiveConnectionHealthState(connection repo.ProviderConnection) HealthState {
	return persistedHealthState(connection)
}

func ConnectionRecoveryReadyAt(connection repo.ProviderConnection) (time.Time, bool) {
	if readyAt, ok := persistedRateLimitRecoveryReadyAt(connection); ok {
		return readyAt, true
	}
	if connection.UpdatedAt.IsZero() {
		return time.Time{}, false
	}
	return connection.UpdatedAt.UTC().Add(healthProbeBackoffMax), true
}

func rateLimitBackoffExpired(connection repo.ProviderConnection) bool {
	readyAt, ok := ConnectionRecoveryReadyAt(connection)
	if !ok {
		return false
	}
	return !time.Now().UTC().Before(readyAt)
}

func unavailableBackoffExpired(connection repo.ProviderConnection) bool {
	readyAt, ok := ConnectionRecoveryReadyAt(connection)
	if !ok {
		return false
	}
	return !time.Now().UTC().Before(readyAt)
}

func persistedRateLimitRecoveryReadyAt(connection repo.ProviderConnection) (time.Time, bool) {
	metadata := providerConnectionMetadataMap(connection.Metadata)
	raw, _ := metadata[providerConnectionMetadataHealthRateLimitedUntil].(string)
	text := strings.TrimSpace(raw)
	if text == "" {
		return time.Time{}, false
	}
	readyAt, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return time.Time{}, false
	}
	return readyAt.UTC(), true
}

func providerConnectionMetadataMap(metadata json.RawMessage) map[string]any {
	result := map[string]any{}
	if len(metadata) == 0 {
		return result
	}
	_ = json.Unmarshal(metadata, &result)
	return result
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
