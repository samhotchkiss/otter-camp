package model

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestProfileResolverResolveScopePriority(t *testing.T) {
	orgID := uuid.New()
	flowNodeID := uuid.New()
	agentID := uuid.New()
	projectID := uuid.New()

	assignments := &stubAssignmentRepo{
		items: map[string]repo.ModelProfileAssignment{
			assignmentKey(orgID, "flow_node", flowNodeID, "agent_turn"): {
				OrganizationID:    orgID,
				ScopeType:         "flow_node",
				ScopeID:           flowNodeID,
				LogicalProfileID:  "flow-profile",
				InvocationPurpose: "agent_turn",
			},
			assignmentKey(orgID, "agent", agentID, "agent_turn"): {
				OrganizationID:    orgID,
				ScopeType:         "agent",
				ScopeID:           agentID,
				LogicalProfileID:  "agent-profile",
				InvocationPurpose: "agent_turn",
			},
			assignmentKey(orgID, "project", projectID, "agent_turn"): {
				OrganizationID:    orgID,
				ScopeType:         "project",
				ScopeID:           projectID,
				LogicalProfileID:  "project-profile",
				InvocationPurpose: "agent_turn",
			},
			assignmentKey(orgID, "organization", orgID, "agent_turn"): {
				OrganizationID:    orgID,
				ScopeType:         "organization",
				ScopeID:           orgID,
				LogicalProfileID:  "org-profile",
				InvocationPurpose: "agent_turn",
			},
		},
	}
	profiles := &stubProfileRepo{
		items: map[string]repo.ModelProfile{
			profileKey(orgID, "flow-profile"):    {LogicalProfileID: "flow-profile"},
			profileKey(orgID, "agent-profile"):   {LogicalProfileID: "agent-profile"},
			profileKey(orgID, "project-profile"): {LogicalProfileID: "project-profile"},
			profileKey(orgID, "org-profile"):     {LogicalProfileID: "org-profile"},
		},
	}
	resolver := NewProfileResolver(assignments, profiles)

	got, err := resolver.Resolve(context.Background(), orgID, "agent_turn",
		Scope{Type: "project", ID: projectID},
		Scope{Type: "flow_node", ID: flowNodeID},
		Scope{Type: "agent", ID: agentID},
	)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got.LogicalProfileID != "flow-profile" {
		t.Fatalf("logical_profile_id = %q, want %q", got.LogicalProfileID, "flow-profile")
	}
}

func TestProfileResolverResolveFallsBackToAgentWhenFlowNodeMissing(t *testing.T) {
	orgID := uuid.New()
	flowNodeID := uuid.New()
	agentID := uuid.New()
	assignments := &stubAssignmentRepo{
		items: map[string]repo.ModelProfileAssignment{
			assignmentKey(orgID, "agent", agentID, "agent_turn"): {
				OrganizationID:    orgID,
				ScopeType:         "agent",
				ScopeID:           agentID,
				LogicalProfileID:  "agent-profile",
				InvocationPurpose: "agent_turn",
			},
		},
	}
	profiles := &stubProfileRepo{
		items: map[string]repo.ModelProfile{
			profileKey(orgID, "agent-profile"): {LogicalProfileID: "agent-profile"},
		},
	}
	resolver := NewProfileResolver(assignments, profiles)

	got, err := resolver.Resolve(context.Background(), orgID, "agent_turn",
		Scope{Type: "flow_node", ID: flowNodeID},
		Scope{Type: "agent", ID: agentID},
	)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got.LogicalProfileID != "agent-profile" {
		t.Fatalf("logical_profile_id = %q, want %q", got.LogicalProfileID, "agent-profile")
	}
}

func TestProfileResolverResolveNoAssignmentReturnsErrNoProfileAssigned(t *testing.T) {
	orgID := uuid.New()
	resolver := NewProfileResolver(&stubAssignmentRepo{items: map[string]repo.ModelProfileAssignment{}}, &stubProfileRepo{items: map[string]repo.ModelProfile{}})

	_, err := resolver.Resolve(context.Background(), orgID, "agent_turn", Scope{Type: "agent", ID: uuid.New()})
	if !errors.Is(err, ErrNoProfileAssigned) {
		t.Fatalf("Resolve error = %v, want ErrNoProfileAssigned", err)
	}
}

func TestProfileResolverResolveFallbackChainTraversal(t *testing.T) {
	orgID := uuid.New()
	agentID := uuid.New()
	fallback := "profile-b"

	assignments := &stubAssignmentRepo{
		items: map[string]repo.ModelProfileAssignment{
			assignmentKey(orgID, "agent", agentID, "agent_turn"): {
				OrganizationID:    orgID,
				ScopeType:         "agent",
				ScopeID:           agentID,
				LogicalProfileID:  "profile-a",
				InvocationPurpose: "agent_turn",
			},
		},
	}
	profiles := &stubProfileRepo{
		items: map[string]repo.ModelProfile{
			profileKey(orgID, "profile-a"): {
				LogicalProfileID:  "profile-a",
				FallbackProfileID: &fallback,
			},
			profileKey(orgID, "profile-b"): {
				LogicalProfileID: "profile-b",
			},
		},
	}

	resolver := NewProfileResolver(assignments, profiles)
	got, err := resolver.Resolve(context.Background(), orgID, "agent_turn", Scope{Type: "agent", ID: agentID})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got.LogicalProfileID != "profile-b" {
		t.Fatalf("logical_profile_id = %q, want %q", got.LogicalProfileID, "profile-b")
	}
}

func TestProfileResolverResolveFallbackCycleDetected(t *testing.T) {
	orgID := uuid.New()
	agentID := uuid.New()
	aFallback := "profile-b"
	bFallback := "profile-a"

	assignments := &stubAssignmentRepo{
		items: map[string]repo.ModelProfileAssignment{
			assignmentKey(orgID, "agent", agentID, "agent_turn"): {
				OrganizationID:    orgID,
				ScopeType:         "agent",
				ScopeID:           agentID,
				LogicalProfileID:  "profile-a",
				InvocationPurpose: "agent_turn",
			},
		},
	}
	profiles := &stubProfileRepo{
		items: map[string]repo.ModelProfile{
			profileKey(orgID, "profile-a"): {
				LogicalProfileID:  "profile-a",
				FallbackProfileID: &aFallback,
			},
			profileKey(orgID, "profile-b"): {
				LogicalProfileID:  "profile-b",
				FallbackProfileID: &bFallback,
			},
		},
	}

	resolver := NewProfileResolver(assignments, profiles)
	_, err := resolver.Resolve(context.Background(), orgID, "agent_turn", Scope{Type: "agent", ID: agentID})
	if !errors.Is(err, ErrFallbackCycle) {
		t.Fatalf("Resolve error = %v, want ErrFallbackCycle", err)
	}
}

type stubAssignmentRepo struct {
	items map[string]repo.ModelProfileAssignment
}

func (s *stubAssignmentRepo) GetByScope(_ context.Context, organizationID uuid.UUID, scopeType string, scopeID uuid.UUID, invocationPurpose string) (repo.ModelProfileAssignment, error) {
	item, ok := s.items[assignmentKey(organizationID, scopeType, scopeID, invocationPurpose)]
	if !ok {
		return repo.ModelProfileAssignment{}, repo.ErrNotFound
	}
	return item, nil
}

type stubProfileRepo struct {
	items map[string]repo.ModelProfile
}

func (s *stubProfileRepo) GetCurrentByLogicalID(_ context.Context, organizationID uuid.UUID, logicalProfileID string) (repo.ModelProfile, error) {
	if item, ok := s.items[profileKey(organizationID, logicalProfileID)]; ok {
		return item, nil
	}
	if item, ok := s.items[profileKey(uuid.Nil, logicalProfileID)]; ok {
		return item, nil
	}
	return repo.ModelProfile{}, repo.ErrNotFound
}

func assignmentKey(orgID uuid.UUID, scopeType string, scopeID uuid.UUID, purpose string) string {
	return orgID.String() + "|" + scopeType + "|" + scopeID.String() + "|" + purpose
}

func profileKey(orgID uuid.UUID, logicalProfileID string) string {
	return orgID.String() + "|" + logicalProfileID
}
