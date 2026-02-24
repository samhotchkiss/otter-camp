package model

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestProfileResolverResolveScopePriority(t *testing.T) {
	orgID := uuid.New()
	agentID := uuid.New()
	flowNodeID := uuid.New()

	assignments := &fakeAssignmentsRepo{
		items: map[string]repo.ModelProfileAssignment{
			assignmentKey(orgID, ScopeTypeAgent, agentID, "agent_turn"): {
				OrganizationID:   orgID,
				ScopeType:        ScopeTypeAgent,
				ScopeID:          agentID,
				LogicalProfileID: "agent-profile",
			},
			assignmentKey(orgID, ScopeTypeFlowNode, flowNodeID, "agent_turn"): {
				OrganizationID:   orgID,
				ScopeType:        ScopeTypeFlowNode,
				ScopeID:          flowNodeID,
				LogicalProfileID: "flow-profile",
			},
		},
	}

	flowProvider := uuid.New()
	agentProvider := uuid.New()
	profiles := &fakeProfilesRepo{
		items: map[string]repo.ModelProfile{
			"flow-profile":  {LogicalProfileID: "flow-profile", ProviderID: flowProvider},
			"agent-profile": {LogicalProfileID: "agent-profile", ProviderID: agentProvider},
		},
	}
	connections := &fakeProviderConnectionRepo{
		items: map[uuid.UUID][]repo.ProviderConnection{
			flowProvider:  {{ProviderID: flowProvider, IsEnabled: true, HealthStatus: "healthy"}},
			agentProvider: {{ProviderID: agentProvider, IsEnabled: true, HealthStatus: "healthy"}},
		},
	}

	resolver := NewProfileResolver(assignments, profiles, connections)
	resolved, err := resolver.Resolve(context.Background(), orgID, "agent_turn",
		Scope{Type: ScopeTypeFlowNode, ID: flowNodeID},
		Scope{Type: ScopeTypeAgent, ID: agentID},
	)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.LogicalProfileID != "flow-profile" {
		t.Fatalf("resolved profile = %q, want %q", resolved.LogicalProfileID, "flow-profile")
	}
}

func TestProfileResolverResolveNoAssignment(t *testing.T) {
	resolver := NewProfileResolver(
		&fakeAssignmentsRepo{items: map[string]repo.ModelProfileAssignment{}},
		&fakeProfilesRepo{items: map[string]repo.ModelProfile{}},
		&fakeProviderConnectionRepo{items: map[uuid.UUID][]repo.ProviderConnection{}},
	)

	_, err := resolver.Resolve(context.Background(), uuid.New(), "agent_turn", Scope{Type: ScopeTypeOrganization, ID: uuid.New()})
	if !errors.Is(err, ErrNoProfileAssigned) {
		t.Fatalf("Resolve error = %v, want ErrNoProfileAssigned", err)
	}
}

func TestProfileResolverResolveFallbackChain(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()
	providerA := uuid.New()
	providerB := uuid.New()
	fallbackID := "profile-b"

	assignments := &fakeAssignmentsRepo{
		items: map[string]repo.ModelProfileAssignment{
			assignmentKey(orgID, ScopeTypeProject, projectID, "agent_turn"): {
				OrganizationID:   orgID,
				ScopeType:        ScopeTypeProject,
				ScopeID:          projectID,
				LogicalProfileID: "profile-a",
			},
		},
	}
	profiles := &fakeProfilesRepo{
		items: map[string]repo.ModelProfile{
			"profile-a": {LogicalProfileID: "profile-a", ProviderID: providerA, FallbackProfileID: &fallbackID},
			"profile-b": {LogicalProfileID: "profile-b", ProviderID: providerB},
		},
	}
	connections := &fakeProviderConnectionRepo{
		items: map[uuid.UUID][]repo.ProviderConnection{
			providerA: {{ProviderID: providerA, IsEnabled: true, HealthStatus: "unavailable"}},
			providerB: {{ProviderID: providerB, IsEnabled: true, HealthStatus: "healthy"}},
		},
	}

	resolver := NewProfileResolver(assignments, profiles, connections)
	resolved, err := resolver.Resolve(context.Background(), orgID, "agent_turn", Scope{Type: ScopeTypeProject, ID: projectID})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.LogicalProfileID != "profile-b" {
		t.Fatalf("resolved profile = %q, want %q", resolved.LogicalProfileID, "profile-b")
	}
}

func TestProfileResolverResolveFallbackCycle(t *testing.T) {
	orgID := uuid.New()
	agentID := uuid.New()
	providerA := uuid.New()
	providerB := uuid.New()

	fallbackToB := "profile-b"
	fallbackToA := "profile-a"

	assignments := &fakeAssignmentsRepo{
		items: map[string]repo.ModelProfileAssignment{
			assignmentKey(orgID, ScopeTypeAgent, agentID, "agent_turn"): {
				OrganizationID:   orgID,
				ScopeType:        ScopeTypeAgent,
				ScopeID:          agentID,
				LogicalProfileID: "profile-a",
			},
		},
	}
	profiles := &fakeProfilesRepo{
		items: map[string]repo.ModelProfile{
			"profile-a": {LogicalProfileID: "profile-a", ProviderID: providerA, FallbackProfileID: &fallbackToB},
			"profile-b": {LogicalProfileID: "profile-b", ProviderID: providerB, FallbackProfileID: &fallbackToA},
		},
	}
	connections := &fakeProviderConnectionRepo{
		items: map[uuid.UUID][]repo.ProviderConnection{
			providerA: {{ProviderID: providerA, IsEnabled: true, HealthStatus: "unavailable"}},
			providerB: {{ProviderID: providerB, IsEnabled: true, HealthStatus: "unavailable"}},
		},
	}

	resolver := NewProfileResolver(assignments, profiles, connections)
	_, err := resolver.Resolve(context.Background(), orgID, "agent_turn", Scope{Type: ScopeTypeAgent, ID: agentID})
	if !errors.Is(err, ErrFallbackCycle) {
		t.Fatalf("Resolve error = %v, want ErrFallbackCycle", err)
	}
}

type fakeAssignmentsRepo struct {
	items map[string]repo.ModelProfileAssignment
}

func (f *fakeAssignmentsRepo) GetByScope(_ context.Context, organizationID uuid.UUID, scopeType string, scopeID uuid.UUID, purpose string) (repo.ModelProfileAssignment, error) {
	assignment, ok := f.items[assignmentKey(organizationID, scopeType, scopeID, purpose)]
	if !ok {
		return repo.ModelProfileAssignment{}, repo.ErrNotFound
	}
	return assignment, nil
}

type fakeProfilesRepo struct {
	items map[string]repo.ModelProfile
}

func (f *fakeProfilesRepo) GetCurrentByLogicalID(_ context.Context, _ uuid.UUID, logicalProfileID string) (repo.ModelProfile, error) {
	profile, ok := f.items[logicalProfileID]
	if !ok {
		return repo.ModelProfile{}, repo.ErrNotFound
	}
	return profile, nil
}

type fakeProviderConnectionRepo struct {
	items map[uuid.UUID][]repo.ProviderConnection
}

func (f *fakeProviderConnectionRepo) ListByProvider(_ context.Context, _ uuid.UUID, providerID uuid.UUID) ([]repo.ProviderConnection, error) {
	connections, ok := f.items[providerID]
	if !ok {
		return []repo.ProviderConnection{}, nil
	}
	return connections, nil
}

func assignmentKey(orgID uuid.UUID, scopeType string, scopeID uuid.UUID, purpose string) string {
	return fmt.Sprintf("%s|%s|%s|%s", orgID.String(), scopeType, scopeID.String(), purpose)
}
