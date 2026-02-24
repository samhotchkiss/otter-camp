//go:build integration

package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestCapabilityPolicyMigrationConstraintsAndInstanceUnique(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	repo := NewCapabilityPolicyRepo(pool)

	var checkCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM pg_constraint
		WHERE conrelid = 'capability_policy'::regclass
		  AND conname IN (
			'capability_policy_instance_org_null_chk',
			'capability_policy_project_requires_project_id_chk',
			'capability_policy_agent_requires_agent_id_chk'
		  )
	`).Scan(&checkCount); err != nil {
		t.Fatalf("query check constraints: %v", err)
	}
	if checkCount != 3 {
		t.Fatalf("check constraint count = %d, want 3", checkCount)
	}

	if _, err := repo.Create(ctx, CapabilityPolicy{
		PolicyLayer:   "instance",
		Capability:    "system.file.write",
		Effect:        "deny",
		CreatedByType: "system",
	}); err != nil {
		t.Fatalf("create first instance policy: %v", err)
	}

	_, err := repo.Create(ctx, CapabilityPolicy{
		PolicyLayer:   "instance",
		Capability:    "system.file.write",
		Effect:        "allow",
		CreatedByType: "system",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate instance capability err = %v, want ErrConflict", err)
	}
}

func TestCapabilityPolicyListForEvaluationLayerOrder(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	projectRepo := NewProjectRepo(pool)
	agentRepo := NewAgentRepo(pool)
	policyRepo := NewCapabilityPolicyRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{
		Slug:        "cap-policy-org-" + uuid.NewString()[:8],
		DisplayName: "Capability Policy Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := projectRepo.Create(ctx, Project{
		OrganizationID: org.ID,
		Slug:           "cap-policy-project-" + uuid.NewString()[:8],
		DisplayName:    "Capability Policy Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	agent, err := agentRepo.Create(ctx, Agent{
		OrganizationID:       org.ID,
		DisplayName:          "Capability Policy Agent",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "prompt",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "agent"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	capability := "system.file.write"
	policies := []CapabilityPolicy{
		{PolicyLayer: "request", OrganizationID: &org.ID, ProjectID: &project.ID, AgentID: &agent.ID, Capability: capability, Effect: "allow", Priority: 100, CreatedByType: "system"},
		{PolicyLayer: "agent_profile", OrganizationID: &org.ID, AgentID: &agent.ID, Capability: capability, Effect: "allow", Priority: 100, CreatedByType: "system"},
		{PolicyLayer: "project", OrganizationID: &org.ID, ProjectID: &project.ID, Capability: capability, Effect: "allow", Priority: 100, CreatedByType: "system"},
		{PolicyLayer: "org", OrganizationID: &org.ID, Capability: capability, Effect: "allow", Priority: 100, CreatedByType: "system"},
		{PolicyLayer: "instance", Capability: capability, Effect: "deny", Priority: 100, CreatedByType: "system"},
	}
	for _, item := range policies {
		if _, err := policyRepo.Create(ctx, item); err != nil {
			t.Fatalf("create %s policy: %v", item.PolicyLayer, err)
		}
	}

	got, err := policyRepo.ListForEvaluation(ctx, org.ID, &project.ID, &agent.ID, capability)
	if err != nil {
		t.Fatalf("ListForEvaluation: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("ListForEvaluation len = %d, want 5", len(got))
	}

	wantLayers := []string{"instance", "org", "project", "agent_profile", "request"}
	for i := range wantLayers {
		if got[i].PolicyLayer != wantLayers[i] {
			t.Fatalf("ListForEvaluation layer[%d] = %q, want %q", i, got[i].PolicyLayer, wantLayers[i])
		}
	}
}

func TestCapabilityPolicyListByLayerRequestScopeIsolation(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	projectRepo := NewProjectRepo(pool)
	policyRepo := NewCapabilityPolicyRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{
		Slug:        "cap-request-scope-org-" + uuid.NewString()[:8],
		DisplayName: "Capability Request Scope Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	projectA, err := projectRepo.Create(ctx, Project{
		OrganizationID: org.ID,
		Slug:           "cap-request-project-a-" + uuid.NewString()[:8],
		DisplayName:    "Capability Request Project A",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project A: %v", err)
	}
	projectB, err := projectRepo.Create(ctx, Project{
		OrganizationID: org.ID,
		Slug:           "cap-request-project-b-" + uuid.NewString()[:8],
		DisplayName:    "Capability Request Project B",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project B: %v", err)
	}

	capability := "mcp.connection.use"
	if _, err := policyRepo.Create(ctx, CapabilityPolicy{
		PolicyLayer:    "request",
		OrganizationID: &org.ID,
		ProjectID:      &projectA.ID,
		Capability:     capability,
		Effect:         "allow",
		CreatedByType:  "system",
	}); err != nil {
		t.Fatalf("create request policy: %v", err)
	}

	matching, err := policyRepo.ListByLayer(ctx, "request", capability, &org.ID, &projectA.ID, nil)
	if err != nil {
		t.Fatalf("ListByLayer matching project: %v", err)
	}
	if len(matching) != 1 {
		t.Fatalf("matching request policies len = %d, want 1", len(matching))
	}

	nonMatching, err := policyRepo.ListByLayer(ctx, "request", capability, &org.ID, &projectB.ID, nil)
	if err != nil {
		t.Fatalf("ListByLayer non-matching project: %v", err)
	}
	if len(nonMatching) != 0 {
		t.Fatalf("non-matching request policies len = %d, want 0", len(nonMatching))
	}
}
