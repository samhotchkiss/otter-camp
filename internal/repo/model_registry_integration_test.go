//go:build integration

package repo

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestProviderConnectionRepoCRUDAndHealthStatus(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	providerRepo := NewModelProviderRepo(pool)
	connectionRepo := NewProviderConnectionRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "provider-conn-org", DisplayName: "Provider Conn Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	provider, err := providerRepo.Create(ctx, ModelProvider{
		Slug:        "provider-conn-openai",
		DisplayName: "OpenAI",
		APIBaseURL:  "https://api.openai.com/v1",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	created, err := connectionRepo.Create(ctx, ProviderConnection{
		OrganizationID:   org.ID,
		ProviderID:       provider.ID,
		DisplayName:      "Primary",
		APIKeyRef:        "ref:openai-primary",
		FailoverPriority: 10,
		HealthStatus:     "healthy",
		IsEnabled:        true,
		Metadata:         json.RawMessage(`{"region":"us-east"}`),
	})
	if err != nil {
		t.Fatalf("create provider_connection: %v", err)
	}

	byID, err := connectionRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if byID.DisplayName != "Primary" {
		t.Fatalf("display_name = %q, want %q", byID.DisplayName, "Primary")
	}

	listed, err := connectionRepo.List(ctx, org.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List count = %d, want 1", len(listed))
	}

	byProvider, err := connectionRepo.ListByProvider(ctx, org.ID, provider.ID)
	if err != nil {
		t.Fatalf("ListByProvider: %v", err)
	}
	if len(byProvider) != 1 {
		t.Fatalf("ListByProvider count = %d, want 1", len(byProvider))
	}

	overrideURL := "https://gateway.example/v1"
	updated, err := connectionRepo.Update(ctx, ProviderConnection{
		ID:                 created.ID,
		DisplayName:        "Primary Updated",
		APIKeyRef:          "ref:openai-updated",
		APIBaseURLOverride: &overrideURL,
		FailoverPriority:   5,
		Metadata:           json.RawMessage(`{"region":"us-west"}`),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.DisplayName != "Primary Updated" {
		t.Fatalf("updated display_name = %q, want %q", updated.DisplayName, "Primary Updated")
	}
	if updated.APIBaseURLOverride == nil || *updated.APIBaseURLOverride != overrideURL {
		t.Fatalf("api_base_url_override = %v, want %q", updated.APIBaseURLOverride, overrideURL)
	}

	degraded, err := connectionRepo.SetHealthStatus(ctx, created.ID, "degraded")
	if err != nil {
		t.Fatalf("SetHealthStatus: %v", err)
	}
	if degraded.HealthStatus != "degraded" {
		t.Fatalf("health_status = %q, want %q", degraded.HealthStatus, "degraded")
	}

	retryUntil := time.Now().UTC().Add(17 * time.Minute).Truncate(time.Second)
	rateLimited, err := connectionRepo.SetHealthStatusWithRetryUntil(ctx, created.ID, "rate_limited", &retryUntil)
	if err != nil {
		t.Fatalf("SetHealthStatusWithRetryUntil: %v", err)
	}
	metadata := map[string]any{}
	if err := json.Unmarshal(rateLimited.Metadata, &metadata); err != nil {
		t.Fatalf("unmarshal rate-limited metadata: %v", err)
	}
	rawRetryUntil, _ := metadata[providerConnectionMetadataHealthRateLimitedUntil].(string)
	parsedRetryUntil, err := time.Parse(time.RFC3339, rawRetryUntil)
	if err != nil {
		t.Fatalf("parse rate_limited_until %q: %v", rawRetryUntil, err)
	}
	if !parsedRetryUntil.UTC().Equal(retryUntil.UTC()) {
		t.Fatalf("rate_limited_until = %s, want %s", parsedRetryUntil.UTC().Format(time.RFC3339), retryUntil.UTC().Format(time.RFC3339))
	}

	healthy, err := connectionRepo.SetHealthStatus(ctx, created.ID, "healthy")
	if err != nil {
		t.Fatalf("clear rate-limited health status: %v", err)
	}
	metadata = map[string]any{}
	if err := json.Unmarshal(healthy.Metadata, &metadata); err != nil {
		t.Fatalf("unmarshal healthy metadata: %v", err)
	}
	if _, exists := metadata[providerConnectionMetadataHealthRateLimitedUntil]; exists {
		t.Fatalf("expected %q metadata key to be cleared", providerConnectionMetadataHealthRateLimitedUntil)
	}

	disabled, err := connectionRepo.SetEnabled(ctx, created.ID, false)
	if err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if disabled.IsEnabled {
		t.Fatal("expected connection to be disabled")
	}

	_, err = connectionRepo.SetHealthStatus(ctx, created.ID, "unknown")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown health_status error = %v, want ErrConflict", err)
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
	provider, err := providerRepo.Create(ctx, ModelProvider{
		Slug:        "profile-provider",
		DisplayName: "Profile Provider",
		APIBaseURL:  "https://provider.example",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	created, err := profileRepo.Create(ctx, ModelProfile{
		LogicalProfileID:    "standard",
		OrganizationID:      &org.ID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          provider.ID,
		ModelName:           "model-v1",
		ContextWindowTokens: 200000,
		MaxOutputTokens:     4096,
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
		ProviderID:          provider.ID,
		ModelName:           "model-v2-direct",
		ContextWindowTokens: 200000,
		MaxOutputTokens:     4096,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate current profile error = %v, want ErrConflict", err)
	}

	newVersion, err := profileRepo.Deprecate(ctx, created.ID, ModelProfile{
		ProviderID:          provider.ID,
		ModelName:           "model-v2",
		ContextWindowTokens: 300000,
		MaxOutputTokens:     8192,
		SupportsStreaming:   true,
		SupportsVision:      true,
		InvocationPurpose:   "agent_turn",
	})
	if err != nil {
		t.Fatalf("Deprecate: %v", err)
	}
	if newVersion.Version != 2 {
		t.Fatalf("new version = %d, want 2", newVersion.Version)
	}
	if !newVersion.IsCurrent {
		t.Fatal("expected new profile to be current")
	}

	current, err := profileRepo.GetCurrentByLogicalID(ctx, org.ID, "standard")
	if err != nil {
		t.Fatalf("GetCurrentByLogicalID: %v", err)
	}
	if current.ID != newVersion.ID {
		t.Fatalf("current profile id = %s, want %s", current.ID, newVersion.ID)
	}

	all, err := profileRepo.ListAll(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAll count = %d, want 2", len(all))
	}
	oldCurrentCount := 0
	newCurrentCount := 0
	for _, profile := range all {
		if profile.Version == 1 && !profile.IsCurrent {
			oldCurrentCount++
		}
		if profile.Version == 2 && profile.IsCurrent {
			newCurrentCount++
		}
	}
	if oldCurrentCount != 1 || newCurrentCount != 1 {
		t.Fatalf("unexpected current-state counts old=%d new=%d", oldCurrentCount, newCurrentCount)
	}
}

func TestModelProfileRepoDeprecateUsesNextHistoricalVersion(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgRepo := NewOrgRepo(pool)
	providerRepo := NewModelProviderRepo(pool)
	profileRepo := NewModelProfileRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "profile-version-org", DisplayName: "Profile Version Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	provider, err := providerRepo.Create(ctx, ModelProvider{
		Slug:              "profile-version-provider",
		DisplayName:       "Profile Version Provider",
		APIBaseURL:        "https://api.example.com",
		SupportedFeatures: []string{"chat"},
		IsEnabled:         true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	current, err := profileRepo.Create(ctx, ModelProfile{
		LogicalProfileID:    "standard",
		OrganizationID:      &org.ID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          provider.ID,
		ModelName:           "model-v1",
		ContextWindowTokens: 4096,
		MaxOutputTokens:     1024,
		SupportsStreaming:   true,
		InvocationPurpose:   "agent_turn",
	})
	if err != nil {
		t.Fatalf("create current profile: %v", err)
	}

	v2, err := profileRepo.Deprecate(ctx, current.ID, ModelProfile{
		ProviderID:          provider.ID,
		ModelName:           "model-v2",
		ContextWindowTokens: current.ContextWindowTokens,
		MaxOutputTokens:     current.MaxOutputTokens,
		SupportsStreaming:   current.SupportsStreaming,
		SupportsVision:      current.SupportsVision,
		InvocationPurpose:   current.InvocationPurpose,
	})
	if err != nil {
		t.Fatalf("Deprecate to v2: %v", err)
	}

	v3, err := profileRepo.Deprecate(ctx, v2.ID, ModelProfile{
		ProviderID:          provider.ID,
		ModelName:           "model-v3",
		ContextWindowTokens: current.ContextWindowTokens,
		MaxOutputTokens:     current.MaxOutputTokens,
		SupportsStreaming:   current.SupportsStreaming,
		SupportsVision:      current.SupportsVision,
		InvocationPurpose:   current.InvocationPurpose,
	})
	if err != nil {
		t.Fatalf("Deprecate to v3: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE model_profile
		SET is_current = false
		WHERE id = $1
	`, v3.ID); err != nil {
		t.Fatalf("clear v3 current flag: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE model_profile
		SET is_current = true
		WHERE id = $1
	`, v2.ID); err != nil {
		t.Fatalf("restore v2 current flag: %v", err)
	}

	v4, err := profileRepo.Deprecate(ctx, v2.ID, ModelProfile{
		ProviderID:          provider.ID,
		ModelName:           "model-v4",
		ContextWindowTokens: current.ContextWindowTokens,
		MaxOutputTokens:     current.MaxOutputTokens,
		SupportsStreaming:   current.SupportsStreaming,
		SupportsVision:      current.SupportsVision,
		InvocationPurpose:   current.InvocationPurpose,
	})
	if err != nil {
		t.Fatalf("Deprecate after rewind: %v", err)
	}

	if v4.Version != 4 {
		t.Fatalf("new version = %d, want 4", v4.Version)
	}
	if !v4.IsCurrent {
		t.Fatal("expected new profile to be current")
	}
}

func TestModelProfileAssignmentRepoUpsertAndDelete(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgRepo := NewOrgRepo(pool)
	assignmentRepo := NewModelProfileAssignmentRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "assignment-org", DisplayName: "Assignment Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	scopeID := uuid.New()
	first, err := assignmentRepo.Upsert(ctx, ModelProfileAssignment{
		OrganizationID:    org.ID,
		ScopeType:         "project",
		ScopeID:           scopeID,
		LogicalProfileID:  "profile-a",
		InvocationPurpose: "agent_turn",
	})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	second, err := assignmentRepo.Upsert(ctx, ModelProfileAssignment{
		OrganizationID:    org.ID,
		ScopeType:         "project",
		ScopeID:           scopeID,
		LogicalProfileID:  "profile-b",
		InvocationPurpose: "agent_turn",
	})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("upsert created duplicate rows: first=%s second=%s", first.ID, second.ID)
	}
	if second.LogicalProfileID != "profile-b" {
		t.Fatalf("logical_profile_id = %q, want %q", second.LogicalProfileID, "profile-b")
	}

	listed, err := assignmentRepo.ListByOrg(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListByOrg: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListByOrg count = %d, want 1", len(listed))
	}

	got, err := assignmentRepo.GetByScope(ctx, org.ID, "project", scopeID, "agent_turn")
	if err != nil {
		t.Fatalf("GetByScope: %v", err)
	}
	if got.LogicalProfileID != "profile-b" {
		t.Fatalf("GetByScope logical_profile_id = %q, want %q", got.LogicalProfileID, "profile-b")
	}

	if err := assignmentRepo.Delete(ctx, got.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := assignmentRepo.GetByScope(ctx, org.ID, "project", scopeID, "agent_turn"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByScope after delete error = %v, want ErrNotFound", err)
	}
}
