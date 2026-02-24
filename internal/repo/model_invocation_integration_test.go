//go:build integration

package repo

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestModelInvocationRepoCreateAndUpdateCompletion(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	providerRepo := NewModelProviderRepo(pool)
	connectionRepo := NewProviderConnectionRepo(pool)
	invocationRepo := NewModelInvocationRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "invocation-org", DisplayName: "Invocation Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	provider, err := providerRepo.Create(ctx, ModelProvider{
		Slug:        "invocation-provider",
		DisplayName: "Invocation Provider",
		APIBaseURL:  "https://provider.example/v1",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	connection, err := connectionRepo.Create(ctx, ProviderConnection{
		OrganizationID: org.ID,
		ProviderID:     provider.ID,
		DisplayName:    "Primary",
		APIKeyRef:      "ref:provider-primary",
		IsEnabled:      true,
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}

	profileID := "standard"
	created, err := invocationRepo.Create(ctx, ModelInvocation{
		OrganizationID:       org.ID,
		ModelProviderID:      provider.ID,
		ProviderConnectionID: &connection.ID,
		ModelProfileID:       &profileID,
		InvocationPurpose:    "agent_turn",
		ModelName:            "gpt-4o-mini",
		IsStreaming:          true,
	})
	if err != nil {
		t.Fatalf("create invocation: %v", err)
	}

	promptKey := "invocations/prompt.json.gz"
	responseKey := "invocations/response.json.gz"
	if err := invocationRepo.UpdateCompletion(ctx, created.ID, 100, 55, 7, 25, 90, &promptKey, &responseKey); err != nil {
		t.Fatalf("update completion: %v", err)
	}

	stored, err := invocationRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.Status != "completed" {
		t.Fatalf("status = %q, want completed", stored.Status)
	}
	if stored.CompletedAt == nil {
		t.Fatal("completed_at is nil, want timestamp")
	}
	if stored.InputTokens == nil || *stored.InputTokens != 100 {
		t.Fatalf("input_tokens = %v, want 100", stored.InputTokens)
	}
	if stored.OutputTokens == nil || *stored.OutputTokens != 55 {
		t.Fatalf("output_tokens = %v, want 55", stored.OutputTokens)
	}
	if stored.CacheReadTokens == nil || *stored.CacheReadTokens != 7 {
		t.Fatalf("cache_read_tokens = %v, want 7", stored.CacheReadTokens)
	}
}

func TestModelInvocationRepoListByOrgIsScoped(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	providerRepo := NewModelProviderRepo(pool)
	connectionRepo := NewProviderConnectionRepo(pool)
	invocationRepo := NewModelInvocationRepo(pool)

	orgA, err := orgRepo.Create(ctx, Organization{Slug: "invocation-list-a", DisplayName: "Invocation List A"})
	if err != nil {
		t.Fatalf("create org A: %v", err)
	}
	orgB, err := orgRepo.Create(ctx, Organization{Slug: "invocation-list-b", DisplayName: "Invocation List B"})
	if err != nil {
		t.Fatalf("create org B: %v", err)
	}

	provider, err := providerRepo.Create(ctx, ModelProvider{
		Slug:        "invocation-list-provider",
		DisplayName: "Invocation List Provider",
		APIBaseURL:  "https://provider.example/v1",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	connectionA, err := connectionRepo.Create(ctx, ProviderConnection{
		OrganizationID: orgA.ID,
		ProviderID:     provider.ID,
		DisplayName:    "Primary A",
		APIKeyRef:      "ref:provider-a",
		IsEnabled:      true,
	})
	if err != nil {
		t.Fatalf("create connection A: %v", err)
	}
	connectionB, err := connectionRepo.Create(ctx, ProviderConnection{
		OrganizationID: orgB.ID,
		ProviderID:     provider.ID,
		DisplayName:    "Primary B",
		APIKeyRef:      "ref:provider-b",
		IsEnabled:      true,
	})
	if err != nil {
		t.Fatalf("create connection B: %v", err)
	}

	runID := uuid.New()
	profileID := "standard"
	if _, err := invocationRepo.Create(ctx, ModelInvocation{
		OrganizationID:       orgA.ID,
		ModelProviderID:      provider.ID,
		ProviderConnectionID: &connectionA.ID,
		ModelProfileID:       &profileID,
		InvocationPurpose:    "agent_turn",
		ModelName:            "gpt-4o-mini",
		SessionID:            ptrUUID(uuid.New()),
		RunID:                &runID,
	}); err != nil {
		t.Fatalf("create invocation for org A: %v", err)
	}
	if _, err := invocationRepo.Create(ctx, ModelInvocation{
		OrganizationID:       orgB.ID,
		ModelProviderID:      provider.ID,
		ProviderConnectionID: &connectionB.ID,
		ModelProfileID:       &profileID,
		InvocationPurpose:    "agent_turn",
		ModelName:            "gpt-4o-mini",
	}); err != nil {
		t.Fatalf("create invocation for org B: %v", err)
	}

	byOrg, err := invocationRepo.ListByOrg(ctx, orgA.ID)
	if err != nil {
		t.Fatalf("ListByOrg: %v", err)
	}
	if len(byOrg) != 1 {
		t.Fatalf("ListByOrg count = %d, want 1", len(byOrg))
	}
	if byOrg[0].OrganizationID != orgA.ID {
		t.Fatalf("org id = %s, want %s", byOrg[0].OrganizationID, orgA.ID)
	}

	byRun, err := invocationRepo.ListByRun(ctx, orgA.ID, runID)
	if err != nil {
		t.Fatalf("ListByRun: %v", err)
	}
	if len(byRun) != 1 {
		t.Fatalf("ListByRun count = %d, want 1", len(byRun))
	}
}

func ptrUUID(value uuid.UUID) *uuid.UUID {
	return &value
}
