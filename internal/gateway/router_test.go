package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type stubProfileLookup struct {
	profiles map[string]repo.ModelProfile
}

func (s *stubProfileLookup) GetCurrentByLogicalID(_ context.Context, _ uuid.UUID, logicalProfileID string) (repo.ModelProfile, error) {
	profile, ok := s.profiles[logicalProfileID]
	if !ok {
		return repo.ModelProfile{}, repo.ErrNotFound
	}
	return profile, nil
}

type stubConnectionLookup struct {
	items map[uuid.UUID][]repo.ProviderConnection
}

func (s *stubConnectionLookup) ListByProvider(_ context.Context, _ uuid.UUID, providerID uuid.UUID) ([]repo.ProviderConnection, error) {
	return s.items[providerID], nil
}

func TestRouterSelectConnectionSkipsUnavailable(t *testing.T) {
	orgID := uuid.New()
	providerID := uuid.New()
	unavailableID := uuid.New()
	healthyID := uuid.New()

	health := NewHealthChecker()
	for i := 0; i < 5; i++ {
		health.RecordFailure(unavailableID, ProviderHTTPError{StatusCode: 500})
	}

	router := NewRouter(
		&stubProfileLookup{
			profiles: map[string]repo.ModelProfile{
				"standard": {
					LogicalProfileID: "standard",
					ProviderID:       providerID,
				},
			},
		},
		&stubConnectionLookup{
			items: map[uuid.UUID][]repo.ProviderConnection{
				providerID: {
					{ID: unavailableID, ProviderID: providerID, IsEnabled: true, FailoverPriority: 1},
					{ID: healthyID, ProviderID: providerID, IsEnabled: true, FailoverPriority: 2},
				},
			},
		},
		health,
	)

	selected, err := router.SelectConnection(context.Background(), orgID, "standard", "agent_turn", PrioritySyncInteractive)
	if err != nil {
		t.Fatalf("SelectConnection: %v", err)
	}
	if selected.ID != healthyID {
		t.Fatalf("selected connection = %s, want %s", selected.ID, healthyID)
	}
}

func TestRouterSelectConnectionSkipsPersistedUnavailableOnColdStart(t *testing.T) {
	orgID := uuid.New()
	providerID := uuid.New()
	unavailableID := uuid.New()
	healthyID := uuid.New()

	router := NewRouter(
		&stubProfileLookup{
			profiles: map[string]repo.ModelProfile{
				"standard": {
					LogicalProfileID: "standard",
					ProviderID:       providerID,
				},
			},
		},
		&stubConnectionLookup{
			items: map[uuid.UUID][]repo.ProviderConnection{
				providerID: {
					{ID: unavailableID, ProviderID: providerID, IsEnabled: true, FailoverPriority: 1, HealthStatus: string(HealthStateUnavailable)},
					{ID: healthyID, ProviderID: providerID, IsEnabled: true, FailoverPriority: 2, HealthStatus: string(HealthStateHealthy)},
				},
			},
		},
		NewHealthChecker(),
	)

	selected, err := router.SelectConnection(context.Background(), orgID, "standard", "agent_turn", PrioritySyncInteractive)
	if err != nil {
		t.Fatalf("SelectConnection: %v", err)
	}
	if selected.ID != healthyID {
		t.Fatalf("selected connection = %s, want %s", selected.ID, healthyID)
	}
}

func TestRouterSelectConnectionPrefersPersistedHealthyOverRateLimitedOnColdStart(t *testing.T) {
	orgID := uuid.New()
	providerID := uuid.New()
	rateLimitedID := uuid.New()
	healthyID := uuid.New()

	router := NewRouter(
		&stubProfileLookup{
			profiles: map[string]repo.ModelProfile{
				"standard": {
					LogicalProfileID: "standard",
					ProviderID:       providerID,
				},
			},
		},
		&stubConnectionLookup{
			items: map[uuid.UUID][]repo.ProviderConnection{
				providerID: {
					{ID: rateLimitedID, ProviderID: providerID, IsEnabled: true, FailoverPriority: 1, HealthStatus: string(HealthStateRateLimited)},
					{ID: healthyID, ProviderID: providerID, IsEnabled: true, FailoverPriority: 2, HealthStatus: string(HealthStateHealthy)},
				},
			},
		},
		NewHealthChecker(),
	)

	selected, err := router.SelectConnection(context.Background(), orgID, "standard", "agent_turn", PrioritySyncInteractive)
	if err != nil {
		t.Fatalf("SelectConnection: %v", err)
	}
	if selected.ID != healthyID {
		t.Fatalf("selected connection = %s, want %s", selected.ID, healthyID)
	}
}

func TestRouterSelectConnectionTreatsExpiredPersistedRateLimitAsDegraded(t *testing.T) {
	orgID := uuid.New()
	providerID := uuid.New()
	expiredRateLimitedID := uuid.New()
	freshRateLimitedID := uuid.New()

	router := NewRouter(
		&stubProfileLookup{
			profiles: map[string]repo.ModelProfile{
				"standard": {
					LogicalProfileID: "standard",
					ProviderID:       providerID,
				},
			},
		},
		&stubConnectionLookup{
			items: map[uuid.UUID][]repo.ProviderConnection{
				providerID: {
					{
						ID:               freshRateLimitedID,
						ProviderID:       providerID,
						IsEnabled:        true,
						FailoverPriority: 1,
						HealthStatus:     string(HealthStateRateLimited),
						UpdatedAt:        time.Now().UTC(),
					},
					{
						ID:               expiredRateLimitedID,
						ProviderID:       providerID,
						IsEnabled:        true,
						FailoverPriority: 2,
						HealthStatus:     string(HealthStateRateLimited),
						UpdatedAt:        time.Now().UTC().Add(-2 * healthProbeBackoffMax),
					},
				},
			},
		},
		NewHealthChecker(),
	)

	selected, err := router.SelectConnection(context.Background(), orgID, "standard", "agent_turn", PrioritySyncInteractive)
	if err != nil {
		t.Fatalf("SelectConnection: %v", err)
	}
	if selected.ID != expiredRateLimitedID {
		t.Fatalf("selected connection = %s, want expired persisted rate-limited connection %s", selected.ID, expiredRateLimitedID)
	}
}

func TestRouterSelectConnectionTreatsExpiredPersistedUnavailableAsDegraded(t *testing.T) {
	orgID := uuid.New()
	providerID := uuid.New()
	expiredUnavailableID := uuid.New()
	freshUnavailableID := uuid.New()

	router := NewRouter(
		&stubProfileLookup{
			profiles: map[string]repo.ModelProfile{
				"standard": {
					LogicalProfileID: "standard",
					ProviderID:       providerID,
				},
			},
		},
		&stubConnectionLookup{
			items: map[uuid.UUID][]repo.ProviderConnection{
				providerID: {
					{
						ID:               freshUnavailableID,
						ProviderID:       providerID,
						IsEnabled:        true,
						FailoverPriority: 1,
						HealthStatus:     string(HealthStateUnavailable),
						UpdatedAt:        time.Now().UTC(),
					},
					{
						ID:               expiredUnavailableID,
						ProviderID:       providerID,
						IsEnabled:        true,
						FailoverPriority: 2,
						HealthStatus:     string(HealthStateUnavailable),
						UpdatedAt:        time.Now().UTC().Add(-2 * healthProbeBackoffMax),
					},
				},
			},
		},
		NewHealthChecker(),
	)

	selected, err := router.SelectConnection(context.Background(), orgID, "standard", "agent_turn", PrioritySyncInteractive)
	if err != nil {
		t.Fatalf("SelectConnection: %v", err)
	}
	if selected.ID != expiredUnavailableID {
		t.Fatalf("selected connection = %s, want expired persisted unavailable connection %s", selected.ID, expiredUnavailableID)
	}
}

func TestRouterSelectConnectionReturnsUnavailableRecoveryWindowWhenAllConnectionsRecovering(t *testing.T) {
	orgID := uuid.New()
	providerID := uuid.New()
	firstID := uuid.New()
	secondID := uuid.New()
	now := time.Now().UTC()

	router := NewRouter(
		&stubProfileLookup{
			profiles: map[string]repo.ModelProfile{
				"standard": {
					LogicalProfileID: "standard",
					ProviderID:       providerID,
				},
			},
		},
		&stubConnectionLookup{
			items: map[uuid.UUID][]repo.ProviderConnection{
				providerID: {
					{
						ID:               firstID,
						ProviderID:       providerID,
						IsEnabled:        true,
						FailoverPriority: 1,
						HealthStatus:     string(HealthStateUnavailable),
						UpdatedAt:        now,
					},
					{
						ID:               secondID,
						ProviderID:       providerID,
						IsEnabled:        true,
						FailoverPriority: 2,
						HealthStatus:     string(HealthStateUnavailable),
						UpdatedAt:        now.Add(-15 * time.Second),
					},
				},
			},
		},
		NewHealthChecker(),
	)

	_, err := router.SelectConnection(context.Background(), orgID, "standard", "agent_turn", PrioritySyncInteractive)
	var unavailable ConnectionsUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("SelectConnection error = %v, want ConnectionsUnavailableError", err)
	}
	if unavailable.RetryAfter < 40*time.Second || unavailable.RetryAfter > 50*time.Second {
		t.Fatalf("retry_after = %s, want about 45s", unavailable.RetryAfter)
	}
}

func TestRouterSelectConnectionReturnsRateLimitedBackoffWhenAllConnectionsCoolingDown(t *testing.T) {
	orgID := uuid.New()
	providerID := uuid.New()
	now := time.Now().UTC()
	firstID := uuid.New()
	secondID := uuid.New()

	firstMetadata, err := json.Marshal(map[string]any{
		providerConnectionMetadataHealthRateLimitedUntil: now.Add(25 * time.Minute).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("marshal first metadata: %v", err)
	}
	secondMetadata, err := json.Marshal(map[string]any{
		providerConnectionMetadataHealthRateLimitedUntil: now.Add(40 * time.Minute).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("marshal second metadata: %v", err)
	}

	router := NewRouter(
		&stubProfileLookup{
			profiles: map[string]repo.ModelProfile{
				"standard": {
					LogicalProfileID: "standard",
					ProviderID:       providerID,
				},
			},
		},
		&stubConnectionLookup{
			items: map[uuid.UUID][]repo.ProviderConnection{
				providerID: {
					{
						ID:               firstID,
						ProviderID:       providerID,
						IsEnabled:        true,
						FailoverPriority: 1,
						HealthStatus:     string(HealthStateRateLimited),
						UpdatedAt:        now,
						Metadata:         firstMetadata,
					},
					{
						ID:               secondID,
						ProviderID:       providerID,
						IsEnabled:        true,
						FailoverPriority: 2,
						HealthStatus:     string(HealthStateRateLimited),
						UpdatedAt:        now,
						Metadata:         secondMetadata,
					},
				},
			},
		},
		NewHealthChecker(),
	)

	_, err = router.SelectConnection(context.Background(), orgID, "standard", "agent_turn", PrioritySyncInteractive)
	var rateLimited ConnectionsRateLimitedError
	if !errors.As(err, &rateLimited) {
		t.Fatalf("SelectConnection error = %v, want ConnectionsRateLimitedError", err)
	}
	if rateLimited.RetryAfter < 24*time.Minute || rateLimited.RetryAfter > 26*time.Minute {
		t.Fatalf("retry_after = %s, want about 25m", rateLimited.RetryAfter)
	}
}

func TestRouterSelectConnectionPrefersOldestDegradedConnectionBeforePriority(t *testing.T) {
	orgID := uuid.New()
	providerID := uuid.New()
	olderDegradedID := uuid.New()
	newerDegradedID := uuid.New()

	router := NewRouter(
		&stubProfileLookup{
			profiles: map[string]repo.ModelProfile{
				"standard": {
					LogicalProfileID: "standard",
					ProviderID:       providerID,
				},
			},
		},
		&stubConnectionLookup{
			items: map[uuid.UUID][]repo.ProviderConnection{
				providerID: {
					{
						ID:               newerDegradedID,
						ProviderID:       providerID,
						IsEnabled:        true,
						FailoverPriority: 1,
						HealthStatus:     string(HealthStateDegraded),
						UpdatedAt:        time.Now().UTC(),
					},
					{
						ID:               olderDegradedID,
						ProviderID:       providerID,
						IsEnabled:        true,
						FailoverPriority: 2,
						HealthStatus:     string(HealthStateDegraded),
						UpdatedAt:        time.Now().UTC().Add(-2 * time.Minute),
					},
				},
			},
		},
		NewHealthChecker(),
	)

	selected, err := router.SelectConnection(context.Background(), orgID, "standard", "agent_turn", PrioritySyncInteractive)
	if err != nil {
		t.Fatalf("SelectConnection: %v", err)
	}
	if selected.ID != olderDegradedID {
		t.Fatalf("selected connection = %s, want older degraded connection %s", selected.ID, olderDegradedID)
	}
}

func TestRouterSelectConnectionUsesFallbackProfileChain(t *testing.T) {
	orgID := uuid.New()
	providerA := uuid.New()
	providerB := uuid.New()
	connectionA := uuid.New()
	connectionB := uuid.New()
	fallback := "haiku"

	health := NewHealthChecker()
	for i := 0; i < 5; i++ {
		health.RecordFailure(connectionA, ProviderHTTPError{StatusCode: 500})
	}

	router := NewRouter(
		&stubProfileLookup{
			profiles: map[string]repo.ModelProfile{
				"standard": {LogicalProfileID: "standard", ProviderID: providerA, FallbackProfileID: &fallback},
				"haiku":    {LogicalProfileID: "haiku", ProviderID: providerB},
			},
		},
		&stubConnectionLookup{
			items: map[uuid.UUID][]repo.ProviderConnection{
				providerA: {{ID: connectionA, ProviderID: providerA, IsEnabled: true, FailoverPriority: 1}},
				providerB: {{ID: connectionB, ProviderID: providerB, IsEnabled: true, FailoverPriority: 1}},
			},
		},
		health,
	)

	selected, err := router.SelectConnection(context.Background(), orgID, "standard", "agent_turn", PrioritySyncInteractive)
	if err != nil {
		t.Fatalf("SelectConnection: %v", err)
	}
	if selected.ID != connectionB {
		t.Fatalf("selected connection = %s, want %s", selected.ID, connectionB)
	}
}

func TestRouterSelectConnectionFallbackMaxHops(t *testing.T) {
	orgID := uuid.New()
	providerID := uuid.New()
	fallbackA := "b"
	fallbackB := "c"
	fallbackC := "d"

	router := NewRouter(
		&stubProfileLookup{
			profiles: map[string]repo.ModelProfile{
				"a": {LogicalProfileID: "a", ProviderID: providerID, FallbackProfileID: &fallbackA},
				"b": {LogicalProfileID: "b", ProviderID: providerID, FallbackProfileID: &fallbackB},
				"c": {LogicalProfileID: "c", ProviderID: providerID, FallbackProfileID: &fallbackC},
				"d": {LogicalProfileID: "d", ProviderID: providerID},
			},
		},
		&stubConnectionLookup{
			items: map[uuid.UUID][]repo.ProviderConnection{
				providerID: {},
			},
		},
		NewHealthChecker(),
	)

	_, err := router.SelectConnection(context.Background(), orgID, "a", "agent_turn", PrioritySyncInteractive)
	if !errors.Is(err, ErrNoHealthyConnection) {
		t.Fatalf("SelectConnection error = %v, want ErrNoHealthyConnection", err)
	}
}

func TestRouterSelectConnectionRoutesListeningEvalToHaiku(t *testing.T) {
	orgID := uuid.New()
	haikuProvider := uuid.New()
	haikuConnection := uuid.New()
	otherProvider := uuid.New()
	otherConnection := uuid.New()

	router := NewRouter(
		&stubProfileLookup{
			profiles: map[string]repo.ModelProfile{
				"high-capability": {LogicalProfileID: "high-capability", ProviderID: otherProvider},
				"haiku":           {LogicalProfileID: "haiku", ProviderID: haikuProvider},
			},
		},
		&stubConnectionLookup{
			items: map[uuid.UUID][]repo.ProviderConnection{
				otherProvider: {{ID: otherConnection, ProviderID: otherProvider, IsEnabled: true, FailoverPriority: 1}},
				haikuProvider: {{ID: haikuConnection, ProviderID: haikuProvider, IsEnabled: true, FailoverPriority: 1}},
			},
		},
		NewHealthChecker(),
	)

	selected, err := router.SelectConnection(context.Background(), orgID, "high-capability", "listening_eval", PrioritySyncInteractive)
	if err != nil {
		t.Fatalf("SelectConnection: %v", err)
	}
	if selected.ID != haikuConnection {
		t.Fatalf("selected connection = %s, want %s", selected.ID, haikuConnection)
	}
}

func TestRoutedProfileIDRoutesMemoryRetrievalClassificationToHaiku(t *testing.T) {
	got := routedProfileID("high-capability", "memory_retrieval_classification")
	if got != systemHaikuProfileID {
		t.Fatalf("routed profile id = %q, want %q", got, systemHaikuProfileID)
	}
}
