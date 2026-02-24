//go:build integration

package model

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestProfileResolverResolveScopePrecedenceIntegration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	providerRepo := repo.NewModelProviderRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)
	assignmentRepo := repo.NewModelProfileAssignmentRepo(pool)
	resolver := NewProfileResolver(assignmentRepo, profileRepo)

	org, err := orgRepo.Create(ctx, repo.Organization{Slug: "resolver-org", DisplayName: "Resolver Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	provider, err := providerRepo.Create(ctx, repo.ModelProvider{
		Slug:        "resolver-provider",
		DisplayName: "Resolver Provider",
		APIBaseURL:  "https://provider.example",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	orgID := &org.ID
	profiles := []repo.ModelProfile{
		{LogicalProfileID: "org-profile", OrganizationID: orgID, Version: 1, IsCurrent: true, ProviderID: provider.ID, ModelName: "org-model", ContextWindowTokens: 1000, MaxOutputTokens: 512, SupportsStreaming: true, SupportsVision: false, InvocationPurpose: "agent_turn"},
		{LogicalProfileID: "project-profile", OrganizationID: orgID, Version: 1, IsCurrent: true, ProviderID: provider.ID, ModelName: "project-model", ContextWindowTokens: 1000, MaxOutputTokens: 512, SupportsStreaming: true, SupportsVision: false, InvocationPurpose: "agent_turn"},
		{LogicalProfileID: "agent-profile", OrganizationID: orgID, Version: 1, IsCurrent: true, ProviderID: provider.ID, ModelName: "agent-model", ContextWindowTokens: 1000, MaxOutputTokens: 512, SupportsStreaming: true, SupportsVision: false, InvocationPurpose: "agent_turn"},
		{LogicalProfileID: "flow-profile", OrganizationID: orgID, Version: 1, IsCurrent: true, ProviderID: provider.ID, ModelName: "flow-model", ContextWindowTokens: 1000, MaxOutputTokens: 512, SupportsStreaming: true, SupportsVision: false, InvocationPurpose: "agent_turn"},
	}
	for _, profile := range profiles {
		if _, err := profileRepo.Create(ctx, profile); err != nil {
			t.Fatalf("create profile %s: %v", profile.LogicalProfileID, err)
		}
	}

	projectID := uuid.New()
	agentID := uuid.New()
	flowNodeID := uuid.New()

	assignments := []repo.ModelProfileAssignment{
		{OrganizationID: org.ID, ScopeType: "organization", ScopeID: org.ID, LogicalProfileID: "org-profile", InvocationPurpose: "agent_turn"},
		{OrganizationID: org.ID, ScopeType: "project", ScopeID: projectID, LogicalProfileID: "project-profile", InvocationPurpose: "agent_turn"},
		{OrganizationID: org.ID, ScopeType: "agent", ScopeID: agentID, LogicalProfileID: "agent-profile", InvocationPurpose: "agent_turn"},
	}
	for _, assignment := range assignments {
		if _, err := assignmentRepo.Upsert(ctx, assignment); err != nil {
			t.Fatalf("upsert assignment %s: %v", assignment.ScopeType, err)
		}
	}

	// flow_node assignment is intentionally missing, so agent-level must win.
	resolved, err := resolver.Resolve(ctx, org.ID, "agent_turn",
		Scope{Type: "flow_node", ID: flowNodeID},
		Scope{Type: "agent", ID: agentID},
		Scope{Type: "project", ID: projectID},
	)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.LogicalProfileID != "agent-profile" {
		t.Fatalf("logical_profile_id = %q, want %q", resolved.LogicalProfileID, "agent-profile")
	}

	if _, err := assignmentRepo.Upsert(ctx, repo.ModelProfileAssignment{
		OrganizationID:    org.ID,
		ScopeType:         "flow_node",
		ScopeID:           flowNodeID,
		LogicalProfileID:  "flow-profile",
		InvocationPurpose: "agent_turn",
	}); err != nil {
		t.Fatalf("upsert flow_node assignment: %v", err)
	}

	resolved, err = resolver.Resolve(ctx, org.ID, "agent_turn",
		Scope{Type: "flow_node", ID: flowNodeID},
		Scope{Type: "agent", ID: agentID},
		Scope{Type: "project", ID: projectID},
	)
	if err != nil {
		t.Fatalf("Resolve with flow assignment: %v", err)
	}
	if resolved.LogicalProfileID != "flow-profile" {
		t.Fatalf("logical_profile_id = %q, want %q", resolved.LogicalProfileID, "flow-profile")
	}
}

func TestProfileResolverResolveFallbackAndNoAssignmentIntegration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	providerRepo := repo.NewModelProviderRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)
	assignmentRepo := repo.NewModelProfileAssignmentRepo(pool)
	resolver := NewProfileResolver(assignmentRepo, profileRepo)

	org, err := orgRepo.Create(ctx, repo.Organization{Slug: "resolver-fallback-org", DisplayName: "Resolver Fallback Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	provider, err := providerRepo.Create(ctx, repo.ModelProvider{
		Slug:        "resolver-fallback-provider",
		DisplayName: "Resolver Fallback Provider",
		APIBaseURL:  "https://provider.example",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	_, err = resolver.Resolve(ctx, org.ID, "agent_turn", Scope{Type: "agent", ID: uuid.New()})
	if !errors.Is(err, ErrNoProfileAssigned) {
		t.Fatalf("Resolve no-assignment err = %v, want ErrNoProfileAssigned", err)
	}

	orgID := &org.ID
	fallbackToB := "profile-b"
	_, err = profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    "profile-a",
		OrganizationID:      orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          provider.ID,
		ModelName:           "profile-a-model",
		ContextWindowTokens: 1000,
		MaxOutputTokens:     512,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
		FallbackProfileID:   &fallbackToB,
	})
	if err != nil {
		t.Fatalf("create profile-a: %v", err)
	}
	_, err = profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    "profile-b",
		OrganizationID:      orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          provider.ID,
		ModelName:           "profile-b-model",
		ContextWindowTokens: 1000,
		MaxOutputTokens:     512,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	})
	if err != nil {
		t.Fatalf("create profile-b: %v", err)
	}

	agentID := uuid.New()
	if _, err := assignmentRepo.Upsert(ctx, repo.ModelProfileAssignment{
		OrganizationID:    org.ID,
		ScopeType:         "agent",
		ScopeID:           agentID,
		LogicalProfileID:  "profile-a",
		InvocationPurpose: "agent_turn",
	}); err != nil {
		t.Fatalf("upsert assignment profile-a: %v", err)
	}

	resolved, err := resolver.Resolve(ctx, org.ID, "agent_turn", Scope{Type: "agent", ID: agentID})
	if err != nil {
		t.Fatalf("Resolve fallback: %v", err)
	}
	if resolved.LogicalProfileID != "profile-b" {
		t.Fatalf("logical_profile_id = %q, want %q", resolved.LogicalProfileID, "profile-b")
	}

	fallbackToA := "profile-a"
	if _, err := profileRepo.Deprecate(ctx, resolved.ID, repo.ModelProfile{
		ProviderID:          provider.ID,
		ModelName:           "profile-b-model-v2",
		ContextWindowTokens: 1000,
		MaxOutputTokens:     512,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
		FallbackProfileID:   &fallbackToA,
	}); err != nil {
		t.Fatalf("deprecate profile-b for cycle: %v", err)
	}

	_, err = resolver.Resolve(ctx, org.ID, "agent_turn", Scope{Type: "agent", ID: agentID})
	if !errors.Is(err, ErrFallbackCycle) {
		t.Fatalf("Resolve fallback cycle err = %v, want ErrFallbackCycle", err)
	}
}
