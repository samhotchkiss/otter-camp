//go:build integration

package repo

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestProviderConnectionRepoCRUDAndHealthStatusConstraint(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	providerRepo := NewModelProviderRepo(pool)
	connRepo := NewProviderConnectionRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "provider-conn-org", DisplayName: "Provider Conn Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	provider, err := providerRepo.Create(ctx, ModelProvider{
		Slug:        "provider-conn-test",
		DisplayName: "Provider Conn Test",
		APIBaseURL:  "https://api.provider.example",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	created, err := connRepo.Create(ctx, ProviderConnection{
		OrganizationID:   org.ID,
		ProviderID:       provider.ID,
		DisplayName:      "Primary",
		APIKeyRef:        "ref:provider-key",
		FailoverPriority: 10,
		HealthStatus:     "healthy",
		IsEnabled:        true,
		Metadata:         json.RawMessage(`{"region":"us"}`),
	})
	if err != nil {
		t.Fatalf("create provider connection: %v", err)
	}

	byID, err := connRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if byID.DisplayName != "Primary" {
		t.Fatalf("display_name = %q, want %q", byID.DisplayName, "Primary")
	}

	listed, err := connRepo.List(ctx, org.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List count = %d, want 1", len(listed))
	}

	updated, err := connRepo.Update(ctx, ProviderConnection{
		ID:               created.ID,
		DisplayName:      "Primary Updated",
		APIKeyRef:        "ref:provider-key-v2",
		FailoverPriority: 20,
		Metadata:         json.RawMessage(`{"region":"eu"}`),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.DisplayName != "Primary Updated" {
		t.Fatalf("display_name = %q, want %q", updated.DisplayName, "Primary Updated")
	}

	healthUpdated, err := connRepo.SetHealthStatus(ctx, created.ID, "degraded")
	if err != nil {
		t.Fatalf("SetHealthStatus: %v", err)
	}
	if healthUpdated.HealthStatus != "degraded" {
		t.Fatalf("health_status = %q, want %q", healthUpdated.HealthStatus, "degraded")
	}

	enabledUpdated, err := connRepo.SetEnabled(ctx, created.ID, false)
	if err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if enabledUpdated.IsEnabled {
		t.Fatal("expected provider connection to be disabled")
	}

	byProvider, err := connRepo.ListByProvider(ctx, org.ID, provider.ID)
	if err != nil {
		t.Fatalf("ListByProvider: %v", err)
	}
	if len(byProvider) != 1 {
		t.Fatalf("ListByProvider count = %d, want 1", len(byProvider))
	}

	_, err = connRepo.SetHealthStatus(ctx, created.ID, "unknown")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("SetHealthStatus invalid status error = %v, want ErrConflict", err)
	}
}

func TestModelProfileRepoDeprecateAndCurrentUniqueness(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	providerRepo := NewModelProviderRepo(pool)
	profileRepo := NewModelProfileRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "profile-org", DisplayName: "Profile Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	providerA, err := providerRepo.Create(ctx, ModelProvider{Slug: "profile-provider-a", DisplayName: "Profile Provider A", APIBaseURL: "https://a.example", IsEnabled: true})
	if err != nil {
		t.Fatalf("create provider A: %v", err)
	}
	providerB, err := providerRepo.Create(ctx, ModelProvider{Slug: "profile-provider-b", DisplayName: "Profile Provider B", APIBaseURL: "https://b.example", IsEnabled: true})
	if err != nil {
		t.Fatalf("create provider B: %v", err)
	}

	profile, err := profileRepo.Create(ctx, ModelProfile{
		LogicalProfileID:    "standard",
		OrganizationID:      &org.ID,
		ProviderID:          providerA.ID,
		ModelName:           "model-a",
		ContextWindowTokens: 128000,
		MaxOutputTokens:     8192,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}

	_, err = profileRepo.Create(ctx, ModelProfile{
		LogicalProfileID:    "standard",
		OrganizationID:      &org.ID,
		Version:             2,
		IsCurrent:           true,
		ProviderID:          providerA.ID,
		ModelName:           "model-a2",
		ContextWindowTokens: 128000,
		MaxOutputTokens:     8192,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("create duplicate current profile error = %v, want ErrConflict", err)
	}

	deprecated, err := profileRepo.Deprecate(ctx, profile.ID, ModelProfile{
		ProviderID:          providerB.ID,
		ModelName:           "model-b",
		ContextWindowTokens: 256000,
		MaxOutputTokens:     16384,
		SupportsStreaming:   true,
		SupportsVision:      true,
		InvocationPurpose:   "agent_turn",
	})
	if err != nil {
		t.Fatalf("Deprecate: %v", err)
	}
	if deprecated.Version != profile.Version+1 {
		t.Fatalf("new version = %d, want %d", deprecated.Version, profile.Version+1)
	}
	if !deprecated.IsCurrent {
		t.Fatal("expected new profile to be current")
	}

	all, err := profileRepo.ListAll(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAll count = %d, want 2", len(all))
	}

	var foundOld, foundNew bool
	for _, item := range all {
		switch item.ID {
		case profile.ID:
			foundOld = true
			if item.IsCurrent {
				t.Fatal("expected old profile row to be non-current")
			}
		case deprecated.ID:
			foundNew = true
			if !item.IsCurrent {
				t.Fatal("expected new profile row to be current")
			}
		}
	}
	if !foundOld || !foundNew {
		t.Fatalf("missing rows after deprecate: old=%v new=%v", foundOld, foundNew)
	}
}

func TestModelProfileAssignmentRepoUpsert(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	assignmentRepo := NewModelProfileAssignmentRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "assignment-org", DisplayName: "Assignment Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	scopeID := org.ID
	first, err := assignmentRepo.Upsert(ctx, ModelProfileAssignment{
		OrganizationID:    org.ID,
		ScopeType:         "organization",
		ScopeID:           scopeID,
		LogicalProfileID:  "standard",
		InvocationPurpose: "agent_turn",
	})
	if err != nil {
		t.Fatalf("first Upsert: %v", err)
	}

	second, err := assignmentRepo.Upsert(ctx, ModelProfileAssignment{
		OrganizationID:    org.ID,
		ScopeType:         "organization",
		ScopeID:           scopeID,
		LogicalProfileID:  "high-capability",
		InvocationPurpose: "agent_turn",
	})
	if err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("upsert row ID = %s, want %s", second.ID, first.ID)
	}
	if second.LogicalProfileID != "high-capability" {
		t.Fatalf("logical_profile_id = %q, want %q", second.LogicalProfileID, "high-capability")
	}

	listed, err := assignmentRepo.ListByOrg(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListByOrg: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListByOrg count = %d, want 1", len(listed))
	}

	if err := assignmentRepo.Delete(ctx, first.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = assignmentRepo.GetByScope(ctx, org.ID, "organization", scopeID, "agent_turn")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByScope after delete error = %v, want ErrNotFound", err)
	}
}
