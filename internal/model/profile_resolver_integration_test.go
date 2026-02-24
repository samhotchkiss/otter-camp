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
	connRepo := repo.NewProviderConnectionRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)
	assignmentRepo := repo.NewModelProfileAssignmentRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{Slug: "resolver-org", DisplayName: "Resolver Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	providerOrg, err := providerRepo.Create(ctx, repo.ModelProvider{Slug: "resolver-provider-org", DisplayName: "Resolver Provider Org", APIBaseURL: "https://org.example", IsEnabled: true})
	if err != nil {
		t.Fatalf("create org provider: %v", err)
	}
	providerProject, err := providerRepo.Create(ctx, repo.ModelProvider{Slug: "resolver-provider-project", DisplayName: "Resolver Provider Project", APIBaseURL: "https://project.example", IsEnabled: true})
	if err != nil {
		t.Fatalf("create project provider: %v", err)
	}
	providerAgent, err := providerRepo.Create(ctx, repo.ModelProvider{Slug: "resolver-provider-agent", DisplayName: "Resolver Provider Agent", APIBaseURL: "https://agent.example", IsEnabled: true})
	if err != nil {
		t.Fatalf("create agent provider: %v", err)
	}

	for _, p := range []repo.ModelProvider{providerOrg, providerProject, providerAgent} {
		if _, err := connRepo.Create(ctx, repo.ProviderConnection{
			OrganizationID:   org.ID,
			ProviderID:       p.ID,
			DisplayName:      p.Slug,
			APIKeyRef:        "ref:test",
			FailoverPriority: 10,
			HealthStatus:     "healthy",
			IsEnabled:        true,
		}); err != nil {
			t.Fatalf("create provider connection for %s: %v", p.Slug, err)
		}
	}

	if _, err := profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    "org-profile",
		OrganizationID:      &org.ID,
		ProviderID:          providerOrg.ID,
		ModelName:           "org-model",
		ContextWindowTokens: 128000,
		MaxOutputTokens:     8192,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	}); err != nil {
		t.Fatalf("create org profile: %v", err)
	}
	if _, err := profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    "project-profile",
		OrganizationID:      &org.ID,
		ProviderID:          providerProject.ID,
		ModelName:           "project-model",
		ContextWindowTokens: 128000,
		MaxOutputTokens:     8192,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	}); err != nil {
		t.Fatalf("create project profile: %v", err)
	}
	if _, err := profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    "agent-profile",
		OrganizationID:      &org.ID,
		ProviderID:          providerAgent.ID,
		ModelName:           "agent-model",
		ContextWindowTokens: 128000,
		MaxOutputTokens:     8192,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	}); err != nil {
		t.Fatalf("create agent profile: %v", err)
	}

	projectID := uuid.New()
	agentID := uuid.New()
	flowNodeID := uuid.New()

	orgAssignment, err := assignmentRepo.Upsert(ctx, repo.ModelProfileAssignment{
		OrganizationID:    org.ID,
		ScopeType:         ScopeTypeOrganization,
		ScopeID:           org.ID,
		LogicalProfileID:  "org-profile",
		InvocationPurpose: "agent_turn",
	})
	if err != nil {
		t.Fatalf("upsert org assignment: %v", err)
	}
	projectAssignment, err := assignmentRepo.Upsert(ctx, repo.ModelProfileAssignment{
		OrganizationID:    org.ID,
		ScopeType:         ScopeTypeProject,
		ScopeID:           projectID,
		LogicalProfileID:  "project-profile",
		InvocationPurpose: "agent_turn",
	})
	if err != nil {
		t.Fatalf("upsert project assignment: %v", err)
	}
	agentAssignment, err := assignmentRepo.Upsert(ctx, repo.ModelProfileAssignment{
		OrganizationID:    org.ID,
		ScopeType:         ScopeTypeAgent,
		ScopeID:           agentID,
		LogicalProfileID:  "agent-profile",
		InvocationPurpose: "agent_turn",
	})
	if err != nil {
		t.Fatalf("upsert agent assignment: %v", err)
	}

	resolver := NewProfileResolver(assignmentRepo, profileRepo, connRepo)

	resolved, err := resolver.Resolve(ctx, org.ID, "agent_turn",
		Scope{Type: ScopeTypeFlowNode, ID: flowNodeID},
		Scope{Type: ScopeTypeAgent, ID: agentID},
		Scope{Type: ScopeTypeProject, ID: projectID},
		Scope{Type: ScopeTypeOrganization, ID: org.ID},
	)
	if err != nil {
		t.Fatalf("Resolve (agent expected): %v", err)
	}
	if resolved.LogicalProfileID != "agent-profile" {
		t.Fatalf("resolved profile = %q, want %q", resolved.LogicalProfileID, "agent-profile")
	}

	if err := assignmentRepo.Delete(ctx, agentAssignment.ID); err != nil {
		t.Fatalf("delete agent assignment: %v", err)
	}

	resolved, err = resolver.Resolve(ctx, org.ID, "agent_turn",
		Scope{Type: ScopeTypeFlowNode, ID: flowNodeID},
		Scope{Type: ScopeTypeAgent, ID: agentID},
		Scope{Type: ScopeTypeProject, ID: projectID},
		Scope{Type: ScopeTypeOrganization, ID: org.ID},
	)
	if err != nil {
		t.Fatalf("Resolve (project expected): %v", err)
	}
	if resolved.LogicalProfileID != "project-profile" {
		t.Fatalf("resolved profile = %q, want %q", resolved.LogicalProfileID, "project-profile")
	}

	if err := assignmentRepo.Delete(ctx, projectAssignment.ID); err != nil {
		t.Fatalf("delete project assignment: %v", err)
	}

	resolved, err = resolver.Resolve(ctx, org.ID, "agent_turn",
		Scope{Type: ScopeTypeFlowNode, ID: flowNodeID},
		Scope{Type: ScopeTypeAgent, ID: agentID},
		Scope{Type: ScopeTypeProject, ID: projectID},
		Scope{Type: ScopeTypeOrganization, ID: org.ID},
	)
	if err != nil {
		t.Fatalf("Resolve (organization expected): %v", err)
	}
	if resolved.LogicalProfileID != "org-profile" {
		t.Fatalf("resolved profile = %q, want %q", resolved.LogicalProfileID, "org-profile")
	}

	if err := assignmentRepo.Delete(ctx, orgAssignment.ID); err != nil {
		t.Fatalf("delete org assignment: %v", err)
	}

	_, err = resolver.Resolve(ctx, org.ID, "agent_turn",
		Scope{Type: ScopeTypeFlowNode, ID: flowNodeID},
		Scope{Type: ScopeTypeAgent, ID: agentID},
		Scope{Type: ScopeTypeProject, ID: projectID},
		Scope{Type: ScopeTypeOrganization, ID: org.ID},
	)
	if !errors.Is(err, ErrNoProfileAssigned) {
		t.Fatalf("Resolve no assignments error = %v, want ErrNoProfileAssigned", err)
	}
}
