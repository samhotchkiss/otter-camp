//go:build integration

package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestAgentRepoCreateAndGetByIDRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	agentRepo := NewAgentRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "agent-roundtrip", DisplayName: "Agent Roundtrip"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	staff, err := agentRepo.Create(ctx, Agent{
		OrganizationID:       org.ID,
		DisplayName:          "Staff PM",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "staff prompt",
		OperatorInstructions: "",
		AgentType:            "pm",
		IsStarterTrio:        false,
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "agent"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create staff agent: %v", err)
	}

	staffByID, err := agentRepo.GetByID(ctx, staff.ID)
	if err != nil {
		t.Fatalf("GetByID staff: %v", err)
	}
	if staffByID.AgentClass != "staff" {
		t.Fatalf("staff agent_class = %q, want %q", staffByID.AgentClass, "staff")
	}
	if staffByID.TempProjectID != nil {
		t.Fatalf("staff TempProjectID = %v, want nil", staffByID.TempProjectID)
	}

	tempProjectID := uuid.New()
	tempTTL := int32(3600)
	tempExpiry := time.Now().UTC().Add(1 * time.Hour)
	temp, err := agentRepo.Create(ctx, Agent{
		OrganizationID:       org.ID,
		DisplayName:          "Temp Worker",
		AgentClass:           "temp",
		LifecycleStatus:      "active",
		SystemPrompt:         "temp prompt",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "agent"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		TempProjectID:        &tempProjectID,
		TempTTLSeconds:       &tempTTL,
		TempExpiresAt:        &tempExpiry,
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create temp agent: %v", err)
	}

	tempByID, err := agentRepo.GetByID(ctx, temp.ID)
	if err != nil {
		t.Fatalf("GetByID temp: %v", err)
	}
	if tempByID.AgentClass != "temp" {
		t.Fatalf("temp agent_class = %q, want %q", tempByID.AgentClass, "temp")
	}
	if tempByID.TempProjectID == nil || *tempByID.TempProjectID != tempProjectID {
		t.Fatalf("temp TempProjectID = %v, want %s", tempByID.TempProjectID, tempProjectID)
	}
}

func TestAgentRepoCountActiveTemps(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	agentRepo := NewAgentRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "agent-count", DisplayName: "Agent Count"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	for i := 0; i < 3; i++ {
		projectID := uuid.New()
		if _, err := agentRepo.Create(ctx, Agent{
			OrganizationID:       org.ID,
			DisplayName:          "Active Temp " + uuid.NewString(),
			AgentClass:           "temp",
			LifecycleStatus:      "active",
			SystemPrompt:         "temp",
			OperatorInstructions: "",
			AgentType:            "worker",
			PrivateMemory:        false,
			MemoryReadScopes:     []string{"org", "project", "agent"},
			ToolAllowList:        []string{},
			ToolDenyList:         []string{},
			TempProjectID:        &projectID,
			CreatedByType:        "system",
			CreatedByID:          uuid.Nil,
		}); err != nil {
			t.Fatalf("seed active temp %d: %v", i, err)
		}
	}

	expiredProjectID := uuid.New()
	if _, err := agentRepo.Create(ctx, Agent{
		OrganizationID:       org.ID,
		DisplayName:          "Expired Temp",
		AgentClass:           "temp",
		LifecycleStatus:      "expired",
		SystemPrompt:         "temp",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "agent"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		TempProjectID:        &expiredProjectID,
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	}); err != nil {
		t.Fatalf("seed expired temp: %v", err)
	}

	if _, err := agentRepo.Create(ctx, Agent{
		OrganizationID:       org.ID,
		DisplayName:          "Staff",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "staff",
		OperatorInstructions: "",
		AgentType:            "pm",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "agent"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	}); err != nil {
		t.Fatalf("seed staff: %v", err)
	}

	count, err := agentRepo.CountActiveTemps(ctx, org.ID)
	if err != nil {
		t.Fatalf("CountActiveTemps: %v", err)
	}
	if count != 3 {
		t.Fatalf("active temp count = %d, want 3", count)
	}
}

func TestAgentRepoListExpiredTemps(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	agentRepo := NewAgentRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "agent-expired", DisplayName: "Agent Expired"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	past := time.Now().UTC().Add(-1 * time.Hour)
	future := time.Now().UTC().Add(1 * time.Hour)

	expiredProjectID := uuid.New()
	expired, err := agentRepo.Create(ctx, Agent{
		OrganizationID:       org.ID,
		DisplayName:          "Expired By TTL",
		AgentClass:           "temp",
		LifecycleStatus:      "active",
		SystemPrompt:         "temp",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "agent"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		TempProjectID:        &expiredProjectID,
		TempExpiresAt:        &past,
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("seed expired ttl temp: %v", err)
	}

	futureProjectID := uuid.New()
	if _, err := agentRepo.Create(ctx, Agent{
		OrganizationID:       org.ID,
		DisplayName:          "Future Temp",
		AgentClass:           "temp",
		LifecycleStatus:      "active",
		SystemPrompt:         "temp",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "agent"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		TempProjectID:        &futureProjectID,
		TempExpiresAt:        &future,
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	}); err != nil {
		t.Fatalf("seed future temp: %v", err)
	}

	inactiveProjectID := uuid.New()
	if _, err := agentRepo.Create(ctx, Agent{
		OrganizationID:       org.ID,
		DisplayName:          "Expired Status Temp",
		AgentClass:           "temp",
		LifecycleStatus:      "expired",
		SystemPrompt:         "temp",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "agent"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		TempProjectID:        &inactiveProjectID,
		TempExpiresAt:        &past,
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	}); err != nil {
		t.Fatalf("seed non-active temp: %v", err)
	}

	expiredTemps, err := agentRepo.ListExpiredTemps(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListExpiredTemps: %v", err)
	}
	if len(expiredTemps) != 1 {
		t.Fatalf("expired temp count = %d, want 1", len(expiredTemps))
	}
	if expiredTemps[0].ID != expired.ID {
		t.Fatalf("expired temp id = %s, want %s", expiredTemps[0].ID, expired.ID)
	}
}

func TestAgentProfileTemplateRepoCRUDAllowsOrgAndSystemTemplates(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	templateRepo := NewAgentProfileTemplateRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "template-org", DisplayName: "Template Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	orgTemplate, err := templateRepo.Create(ctx, AgentProfileTemplate{
		OrganizationID:       &org.ID,
		DisplayName:          "Org Worker",
		Source:               "org",
		SystemPrompt:         "org prompt",
		OperatorInstructions: "org instructions",
		AgentType:            "worker",
		ToolAllowList:        []string{"file.*"},
		ToolDenyList:         []string{},
		MemoryReadScopes:     []string{"org", "project"},
		PrivateMemory:        false,
	})
	if err != nil {
		t.Fatalf("create org template: %v", err)
	}

	systemTemplate, err := templateRepo.Create(ctx, AgentProfileTemplate{
		OrganizationID:       nil,
		DisplayName:          "System PM",
		Source:               "system",
		SystemPrompt:         "system prompt",
		OperatorInstructions: "",
		AgentType:            "pm",
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		MemoryReadScopes:     []string{"org"},
		PrivateMemory:        false,
	})
	if err != nil {
		t.Fatalf("create system template: %v", err)
	}

	fetchedOrgTemplate, err := templateRepo.GetByID(ctx, orgTemplate.ID)
	if err != nil {
		t.Fatalf("GetByID org template: %v", err)
	}
	if fetchedOrgTemplate.OrganizationID == nil || *fetchedOrgTemplate.OrganizationID != org.ID {
		t.Fatalf("org template organization_id = %v, want %s", fetchedOrgTemplate.OrganizationID, org.ID)
	}

	fetchedSystemTemplate, err := templateRepo.GetByID(ctx, systemTemplate.ID)
	if err != nil {
		t.Fatalf("GetByID system template: %v", err)
	}
	if fetchedSystemTemplate.OrganizationID != nil {
		t.Fatalf("system template organization_id = %v, want nil", fetchedSystemTemplate.OrganizationID)
	}

	orgOnly, err := templateRepo.List(ctx, org.ID, false)
	if err != nil {
		t.Fatalf("List org-only: %v", err)
	}
	if len(orgOnly) != 1 {
		t.Fatalf("org-only template count = %d, want 1", len(orgOnly))
	}

	withSystem, err := templateRepo.List(ctx, org.ID, true)
	if err != nil {
		t.Fatalf("List with system: %v", err)
	}
	if len(withSystem) != 2 {
		t.Fatalf("with-system template count = %d, want 2", len(withSystem))
	}

	orgTemplate.DisplayName = "Org Worker Updated"
	orgTemplate.ToolAllowList = []string{"file.read", "file.write"}
	updated, err := templateRepo.Update(ctx, orgTemplate)
	if err != nil {
		t.Fatalf("Update org template: %v", err)
	}
	if updated.DisplayName != "Org Worker Updated" {
		t.Fatalf("updated display_name = %q, want %q", updated.DisplayName, "Org Worker Updated")
	}

	if err := templateRepo.Delete(ctx, orgTemplate.ID); err != nil {
		t.Fatalf("Delete org template: %v", err)
	}
	if _, err := templateRepo.GetByID(ctx, orgTemplate.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID after delete error = %v, want ErrNotFound", err)
	}
}
