package gateway

import (
	"context"
	"errors"
	"testing"

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
