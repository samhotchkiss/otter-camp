//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

func TestModelAPIProfileCRUDAndHistory(t *testing.T) {
	testServer, orgA, adminA, memberA, _, _ := newModelTestServer(t)
	defer testServer.Close()

	providerRepo := repo.NewModelProviderRepo(testServer.Pool)
	provider, err := providerRepo.Create(context.Background(), repo.ModelProvider{
		Slug:        "provider-profile-crud-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		DisplayName: "CRUD Provider",
		APIBaseURL:  "https://provider.example",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	adminToken := loginToken(t, testServer.URL, adminA.Email, "admin-password")
	memberToken := loginToken(t, testServer.URL, memberA.Email, "member-password")

	forbiddenCreate := mustJSON(t, http.MethodPost, testServer.URL+"/v1/model/profiles", map[string]any{
		"display_name": "Member Forbidden",
		"provider_id":  provider.ID.String(),
		"model_name":   "gpt-4o-mini",
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if forbiddenCreate.StatusCode != http.StatusForbidden {
		t.Fatalf("member create profile status=%d want=%d body=%s", forbiddenCreate.StatusCode, http.StatusForbidden, string(forbiddenCreate.Body))
	}

	created := mustJSON(t, http.MethodPost, testServer.URL+"/v1/model/profiles", map[string]any{
		"display_name": "Standard (GPT-4o)",
		"provider_id":  provider.ID.String(),
		"model_name":   "gpt-4o-mini",
		"temperature":  0.2,
		"max_tokens":   1024,
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create profile status=%d want=%d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}
	logicalID := jsonPathString(t, created.Body, "data", "logical_profile_id")
	if logicalID == "" {
		t.Fatalf("missing logical_profile_id body=%s", string(created.Body))
	}
	if gotDisplay := jsonPathString(t, created.Body, "data", "display_name"); gotDisplay != "Standard (GPT-4o)" {
		t.Fatalf("create display_name=%q want=%q body=%s", gotDisplay, "Standard (GPT-4o)", string(created.Body))
	}
	if version := int(jsonPathFloatValue(t, created.Body, "data", "version")); version != 1 {
		t.Fatalf("created version=%d want=1 body=%s", version, string(created.Body))
	}
	if current := jsonPathBoolValue(t, created.Body, "data", "is_current"); !current {
		t.Fatalf("created is_current=%v want=true body=%s", current, string(created.Body))
	}

	got := mustJSON(t, http.MethodGet, testServer.URL+"/v1/model/profiles/"+logicalID, nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if got.StatusCode != http.StatusOK {
		t.Fatalf("get profile status=%d want=%d body=%s", got.StatusCode, http.StatusOK, string(got.Body))
	}
	if gotID := jsonPathString(t, got.Body, "data", "logical_profile_id"); gotID != logicalID {
		t.Fatalf("get logical_profile_id=%q want=%q body=%s", gotID, logicalID, string(got.Body))
	}

	updated := mustJSON(t, http.MethodPatch, testServer.URL+"/v1/model/profiles/"+logicalID, map[string]any{
		"display_name": "Updated Name",
		"model_name":   "gpt-4o-mini-v2",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("patch profile status=%d want=%d body=%s", updated.StatusCode, http.StatusOK, string(updated.Body))
	}
	if version := int(jsonPathFloatValue(t, updated.Body, "data", "version")); version != 2 {
		t.Fatalf("updated version=%d want=2 body=%s", version, string(updated.Body))
	}
	if gotDisplay := jsonPathString(t, updated.Body, "data", "display_name"); gotDisplay != "Updated Name" {
		t.Fatalf("patch display_name=%q want=%q body=%s", gotDisplay, "Updated Name", string(updated.Body))
	}

	history := mustJSON(t, http.MethodGet, testServer.URL+"/v1/model/profiles/"+logicalID+"/history", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if history.StatusCode != http.StatusOK {
		t.Fatalf("history status=%d want=%d body=%s", history.StatusCode, http.StatusOK, string(history.Body))
	}
	historyRows, ok := jsonPathValue(t, history.Body, "data").([]any)
	if !ok {
		t.Fatalf("history data type=%T want=[]any body=%s", jsonPathValue(t, history.Body, "data"), string(history.Body))
	}
	if len(historyRows) != 2 {
		t.Fatalf("history len=%d want=2 body=%s", len(historyRows), string(history.Body))
	}

	notFoundPatch := mustJSON(t, http.MethodPatch, testServer.URL+"/v1/model/providers/"+uuid.NewString(), map[string]any{
		"display_name": "Updated Name",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if notFoundPatch.StatusCode != http.StatusNotFound {
		t.Fatalf("patch unknown provider status=%d want=%d body=%s", notFoundPatch.StatusCode, http.StatusNotFound, string(notFoundPatch.Body))
	}

	_ = orgA
}

func TestModelAPIAssignmentUpsertListDeleteAndOrgDeleteGuard(t *testing.T) {
	testServer, orgA, adminA, _, _, _ := newModelTestServer(t)
	defer testServer.Close()

	providerRepo := repo.NewModelProviderRepo(testServer.Pool)
	provider, err := providerRepo.Create(context.Background(), repo.ModelProvider{
		Slug:        "provider-assignment-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		DisplayName: "Assignment Provider",
		APIBaseURL:  "https://provider.example",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	token := loginToken(t, testServer.URL, adminA.Email, "admin-password")

	createdProfile := mustJSON(t, http.MethodPost, testServer.URL+"/v1/model/profiles", map[string]any{
		"display_name": "Assignment Profile",
		"provider_id":  provider.ID.String(),
		"model_name":   "gpt-4o-mini",
	}, map[string]string{"Authorization": "Bearer " + token})
	if createdProfile.StatusCode != http.StatusCreated {
		t.Fatalf("create profile status=%d want=%d body=%s", createdProfile.StatusCode, http.StatusCreated, string(createdProfile.Body))
	}
	logicalID := jsonPathString(t, createdProfile.Body, "data", "logical_profile_id")

	projectID := uuid.New()
	upsertProject := mustJSON(t, http.MethodPut, testServer.URL+"/v1/model/assignments/project/"+projectID.String(), map[string]any{
		"logical_profile_id": logicalID,
	}, map[string]string{"Authorization": "Bearer " + token})
	if upsertProject.StatusCode != http.StatusOK {
		t.Fatalf("upsert project assignment status=%d want=%d body=%s", upsertProject.StatusCode, http.StatusOK, string(upsertProject.Body))
	}

	listProject := mustJSON(t, http.MethodGet, testServer.URL+"/v1/model/assignments?scope_type=project", nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if listProject.StatusCode != http.StatusOK {
		t.Fatalf("list project assignments status=%d want=%d body=%s", listProject.StatusCode, http.StatusOK, string(listProject.Body))
	}
	projectRows, ok := jsonPathValue(t, listProject.Body, "data").([]any)
	if !ok {
		t.Fatalf("project assignment rows type=%T want=[]any body=%s", jsonPathValue(t, listProject.Body, "data"), string(listProject.Body))
	}
	if len(projectRows) != 1 {
		t.Fatalf("project assignment rows len=%d want=1 body=%s", len(projectRows), string(listProject.Body))
	}

	deleteProject := mustJSON(t, http.MethodDelete, testServer.URL+"/v1/model/assignments/project/"+projectID.String(), nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if deleteProject.StatusCode != http.StatusNoContent {
		t.Fatalf("delete project assignment status=%d want=%d body=%s", deleteProject.StatusCode, http.StatusNoContent, string(deleteProject.Body))
	}

	listProjectAfterDelete := mustJSON(t, http.MethodGet, testServer.URL+"/v1/model/assignments?scope_type=project", nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if listProjectAfterDelete.StatusCode != http.StatusOK {
		t.Fatalf("list after delete status=%d want=%d body=%s", listProjectAfterDelete.StatusCode, http.StatusOK, string(listProjectAfterDelete.Body))
	}
	projectRowsAfterDelete, ok := jsonPathValue(t, listProjectAfterDelete.Body, "data").([]any)
	if !ok {
		t.Fatalf("project assignment rows type=%T want=[]any body=%s", jsonPathValue(t, listProjectAfterDelete.Body, "data"), string(listProjectAfterDelete.Body))
	}
	if len(projectRowsAfterDelete) != 0 {
		t.Fatalf("project assignment rows len=%d want=0 body=%s", len(projectRowsAfterDelete), string(listProjectAfterDelete.Body))
	}

	upsertOrg1 := mustJSON(t, http.MethodPut, testServer.URL+"/v1/model/assignments/org/"+orgA.ID.String(), map[string]any{
		"logical_profile_id": logicalID,
	}, map[string]string{"Authorization": "Bearer " + token})
	if upsertOrg1.StatusCode != http.StatusOK {
		t.Fatalf("upsert org assignment status=%d want=%d body=%s", upsertOrg1.StatusCode, http.StatusOK, string(upsertOrg1.Body))
	}
	firstUpdatedAt := jsonPathString(t, upsertOrg1.Body, "data", "updated_at")

	time.Sleep(10 * time.Millisecond)
	upsertOrg2 := mustJSON(t, http.MethodPut, testServer.URL+"/v1/model/assignments/org/"+orgA.ID.String(), map[string]any{
		"logical_profile_id": logicalID,
	}, map[string]string{"Authorization": "Bearer " + token})
	if upsertOrg2.StatusCode != http.StatusOK {
		t.Fatalf("second upsert org assignment status=%d want=%d body=%s", upsertOrg2.StatusCode, http.StatusOK, string(upsertOrg2.Body))
	}
	if firstID, secondID := jsonPathString(t, upsertOrg1.Body, "data", "scope_id"), jsonPathString(t, upsertOrg2.Body, "data", "scope_id"); firstID != secondID {
		t.Fatalf("scope_id changed first=%q second=%q", firstID, secondID)
	}
	secondUpdatedAt := jsonPathString(t, upsertOrg2.Body, "data", "updated_at")
	if firstUpdatedAt == secondUpdatedAt {
		t.Fatalf("updated_at not refreshed first=%q second=%q", firstUpdatedAt, secondUpdatedAt)
	}

	deleteOrg := mustJSON(t, http.MethodDelete, testServer.URL+"/v1/model/assignments/org/"+orgA.ID.String(), nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if deleteOrg.StatusCode != http.StatusConflict {
		t.Fatalf("delete org assignment status=%d want=%d body=%s", deleteOrg.StatusCode, http.StatusConflict, string(deleteOrg.Body))
	}
}

func TestModelAPIProviderConnectionSupportsSubscriptionAuthMode(t *testing.T) {
	testServer, _, adminA, _, _, _ := newModelTestServer(t)
	defer testServer.Close()

	providerRepo := repo.NewModelProviderRepo(testServer.Pool)
	provider, err := providerRepo.Create(context.Background(), repo.ModelProvider{
		Slug:        "anthropic",
		DisplayName: "Anthropic",
		APIBaseURL:  "https://api.anthropic.com",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	adminToken := loginToken(t, testServer.URL, adminA.Email, "admin-password")
	created := mustJSON(t, http.MethodPost, testServer.URL+"/v1/model/providers/"+provider.ID.String()+"/connections", map[string]any{
		"display_name":                          "Anthropic Subscription",
		"auth_mode":                             "subscription",
		"subscription_access_token_secret_ref":  "anthropic-subscription-access",
		"subscription_refresh_token_secret_ref": "anthropic-subscription-refresh",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create connection status=%d want=%d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}
	if mode := jsonPathString(t, created.Body, "data", "auth_mode"); mode != "subscription" {
		t.Fatalf("auth_mode=%q want=subscription body=%s", mode, string(created.Body))
	}
	if requiresReauth := jsonPathBoolValue(t, created.Body, "data", "requires_reauth"); requiresReauth {
		t.Fatalf("requires_reauth=%v want=false body=%s", requiresReauth, string(created.Body))
	}
	if accessRef := jsonPathString(t, created.Body, "data", "subscription", "access_token_secret_ref"); accessRef != "ref:anthropic-subscription-access" {
		t.Fatalf("subscription access ref=%q want=%q body=%s", accessRef, "ref:anthropic-subscription-access", string(created.Body))
	}

	connectionID := jsonPathString(t, created.Body, "data", "id")
	patched := mustJSON(t, http.MethodPatch, testServer.URL+"/v1/model/providers/"+provider.ID.String()+"/connections/"+connectionID, map[string]any{
		"subscription_refresh_token_secret_ref": "",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if patched.StatusCode != http.StatusOK {
		t.Fatalf("patch connection status=%d want=%d body=%s", patched.StatusCode, http.StatusOK, string(patched.Body))
	}
	if requiresReauth := jsonPathBoolValue(t, patched.Body, "data", "requires_reauth"); !requiresReauth {
		t.Fatalf("requires_reauth=%v want=true body=%s", requiresReauth, string(patched.Body))
	}
}

func TestModelAPIUsageQueryAndOrgIsolation(t *testing.T) {
	testServer, orgA, adminA, _, orgB, adminB := newModelTestServer(t)
	defer testServer.Close()

	ctx := context.Background()
	providerRepo := repo.NewModelProviderRepo(testServer.Pool)
	profileRepo := repo.NewModelProfileRepo(testServer.Pool)
	rollupRepo := repo.NewModelUsageRollupRepo(testServer.Pool)

	provider, err := providerRepo.Create(ctx, repo.ModelProvider{
		Slug:        "provider-usage-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		DisplayName: "Usage Provider",
		APIBaseURL:  "https://provider.example",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	// Org-scoped profile in org A.
	orgAID := orgA.ID
	orgProfile, err := profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    "org-a-profile-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		OrganizationID:      &orgAID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          provider.ID,
		ModelName:           "gpt-4o-mini",
		ContextWindowTokens: 100000,
		MaxOutputTokens:     2048,
		SupportsStreaming:   true,
		InvocationPurpose:   defaultInvocationPurpose,
	})
	if err != nil {
		t.Fatalf("create org profile: %v", err)
	}

	// System profile should be visible to all orgs.
	systemLogicalID := "system-profile-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    systemLogicalID,
		OrganizationID:      nil,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          provider.ID,
		ModelName:           "gpt-4o-mini-system",
		ContextWindowTokens: 100000,
		MaxOutputTokens:     2048,
		SupportsStreaming:   true,
		InvocationPurpose:   defaultInvocationPurpose,
	}); err != nil {
		t.Fatalf("create system profile: %v", err)
	}

	today := time.Now().UTC()
	agentA := uuid.New()
	agentB := uuid.New()
	if _, err := rollupRepo.Upsert(ctx, repo.ModelUsageRollup{
		OrganizationID:      orgA.ID,
		RollupDate:          today,
		RollupType:          "agent",
		RollupID:            &agentA,
		TotalInvocations:    3,
		TotalInputTokens:    120,
		TotalOutputTokens:   60,
		TotalCostMicrocents: 1000,
	}); err != nil {
		t.Fatalf("upsert rollup A: %v", err)
	}
	if _, err := rollupRepo.Upsert(ctx, repo.ModelUsageRollup{
		OrganizationID:      orgA.ID,
		RollupDate:          today,
		RollupType:          "agent",
		RollupID:            &agentB,
		TotalInvocations:    5,
		TotalInputTokens:    200,
		TotalOutputTokens:   80,
		TotalCostMicrocents: 2500,
	}); err != nil {
		t.Fatalf("upsert rollup B: %v", err)
	}

	tokenA := loginToken(t, testServer.URL, adminA.Email, "admin-password")
	usage := mustJSON(t, http.MethodGet, testServer.URL+"/v1/usage?group_by=agent&period=today", nil, map[string]string{
		"Authorization": "Bearer " + tokenA,
	})
	if usage.StatusCode != http.StatusOK {
		t.Fatalf("usage status=%d want=%d body=%s", usage.StatusCode, http.StatusOK, string(usage.Body))
	}
	usageRows, ok := jsonPathValue(t, usage.Body, "data", "data").([]any)
	if !ok {
		t.Fatalf("usage rows type=%T want=[]any body=%s", jsonPathValue(t, usage.Body, "data", "data"), string(usage.Body))
	}
	if len(usageRows) != 2 {
		t.Fatalf("usage rows len=%d want=2 body=%s", len(usageRows), string(usage.Body))
	}

	totalInvocations := 0
	for _, row := range usageRows {
		item, ok := row.(map[string]any)
		if !ok {
			continue
		}
		value, ok := item["total_invocations"].(float64)
		if !ok {
			continue
		}
		totalInvocations += int(value)
	}
	if totalInvocations != 8 {
		t.Fatalf("usage total invocations=%d want=8 body=%s", totalInvocations, string(usage.Body))
	}

	rollupByAgent := mustJSON(t, http.MethodGet, testServer.URL+"/v1/model/usage-rollup?group_by=agent&period=today", nil, map[string]string{
		"Authorization": "Bearer " + tokenA,
	})
	if rollupByAgent.StatusCode != http.StatusOK {
		t.Fatalf("usage-rollup agent status=%d want=%d body=%s", rollupByAgent.StatusCode, http.StatusOK, string(rollupByAgent.Body))
	}
	rollupAgentRows, ok := jsonPathValue(t, rollupByAgent.Body, "data", "data").([]any)
	if !ok {
		t.Fatalf("usage-rollup agent rows type=%T want=[]any body=%s", jsonPathValue(t, rollupByAgent.Body, "data", "data"), string(rollupByAgent.Body))
	}
	if len(rollupAgentRows) != 2 {
		t.Fatalf("usage-rollup agent rows len=%d want=2 body=%s", len(rollupAgentRows), string(rollupByAgent.Body))
	}
	totalRollupCost := int64(0)
	for _, row := range rollupAgentRows {
		item, ok := row.(map[string]any)
		if !ok {
			continue
		}
		value, ok := item["total_cost_microcents"].(float64)
		if !ok {
			continue
		}
		totalRollupCost += int64(value)
	}
	if totalRollupCost != 3500 {
		t.Fatalf("usage-rollup agent total_cost_microcents=%d want=3500 body=%s", totalRollupCost, string(rollupByAgent.Body))
	}

	rollupByPeriod := mustJSON(t, http.MethodGet, testServer.URL+"/v1/model/usage-rollup?group_by=period&period=today", nil, map[string]string{
		"Authorization": "Bearer " + tokenA,
	})
	if rollupByPeriod.StatusCode != http.StatusOK {
		t.Fatalf("usage-rollup period status=%d want=%d body=%s", rollupByPeriod.StatusCode, http.StatusOK, string(rollupByPeriod.Body))
	}
	rollupPeriodRows, ok := jsonPathValue(t, rollupByPeriod.Body, "data", "data").([]any)
	if !ok {
		t.Fatalf("usage-rollup period rows type=%T want=[]any body=%s", jsonPathValue(t, rollupByPeriod.Body, "data", "data"), string(rollupByPeriod.Body))
	}
	if len(rollupPeriodRows) != 1 {
		t.Fatalf("usage-rollup period rows len=%d want=1 body=%s", len(rollupPeriodRows), string(rollupByPeriod.Body))
	}
	if got := jsonPathString(t, rollupByPeriod.Body, "data", "data", "0", "rollup_type"); got != "period" {
		t.Fatalf("usage-rollup period rollup_type=%q want=period body=%s", got, string(rollupByPeriod.Body))
	}
	if got := jsonPathString(t, rollupByPeriod.Body, "data", "data", "0", "rollup_date"); got != today.Format("2006-01-02") {
		t.Fatalf("usage-rollup period rollup_date=%q want=%q body=%s", got, today.Format("2006-01-02"), string(rollupByPeriod.Body))
	}
	if got := int(jsonPathFloatValue(t, rollupByPeriod.Body, "data", "data", "0", "total_cost_microcents")); got != 3500 {
		t.Fatalf("usage-rollup period total_cost_microcents=%d want=3500 body=%s", got, string(rollupByPeriod.Body))
	}

	authServiceB, err := authsvc.NewService(authsvc.Options{
		Users:        repo.NewHumanUserRepo(testServer.Pool),
		Sessions:     repo.NewAuthSessionRepo(testServer.Pool),
		APIKeys:      repo.NewAPIKeyRepo(testServer.Pool),
		DefaultOrgID: orgB.ID,
		AuthMode:     "standard",
		BcryptCost:   bcrypt.MinCost,
	})
	if err != nil {
		t.Fatalf("new auth service org B: %v", err)
	}
	handlerB := NewHandlerWithOptions(HandlerOptions{
		Version:     "test-version",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthService: authServiceB,
		Pool:        testServer.Pool,
		RouteRegistrars: []RouteRegistrar{
			NewModelRouteRegistrar(testServer.Pool),
		},
	})
	serverB := httptest.NewServer(handlerB)
	defer serverB.Close()

	tokenB := loginToken(t, serverB.URL, adminB.Email, "admin-password")
	orgBProfiles := mustJSON(t, http.MethodGet, serverB.URL+"/v1/model/profiles", nil, map[string]string{
		"Authorization": "Bearer " + tokenB,
	})
	if orgBProfiles.StatusCode != http.StatusOK {
		t.Fatalf("org B list profiles status=%d want=%d body=%s", orgBProfiles.StatusCode, http.StatusOK, string(orgBProfiles.Body))
	}
	rows, ok := jsonPathValue(t, orgBProfiles.Body, "data").([]any)
	if !ok {
		t.Fatalf("org B list profile rows type=%T want=[]any body=%s", jsonPathValue(t, orgBProfiles.Body, "data"), string(orgBProfiles.Body))
	}
	if len(rows) == 0 {
		t.Fatalf("expected system profile to be visible to org B body=%s", string(orgBProfiles.Body))
	}
	visibleSystem := false
	for _, row := range rows {
		item, ok := row.(map[string]any)
		if !ok {
			continue
		}
		logicalID, _ := item["logical_profile_id"].(string)
		if logicalID == orgProfile.LogicalProfileID {
			t.Fatalf("org B unexpectedly sees org A profile %q body=%s", logicalID, string(orgBProfiles.Body))
		}
		if logicalID == systemLogicalID {
			visibleSystem = true
		}
	}
	if !visibleSystem {
		t.Fatalf("org B did not see system profile %q body=%s", systemLogicalID, string(orgBProfiles.Body))
	}
}

func newModelTestServer(t *testing.T) (*authIntegrationServer, repo.Organization, repo.HumanUser, repo.HumanUser, repo.Organization, repo.HumanUser) {
	t.Helper()

	pool := testdb.New(t)

	orgASlug := "model-api-org-a-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	orgBSlug := "model-api-org-b-" + strings.ReplaceAll(uuid.NewString(), "-", "")

	orgA, adminA := createOrgAndUser(t, pool, orgASlug, "admin-a+"+orgASlug+"@example.com", "Admin A", "admin", "admin-password")
	_, memberA := createOrgAndUser(t, pool, orgASlug, "member-a+"+orgASlug+"@example.com", "Member A", "member", "member-password")

	orgB, adminB := createOrgAndUser(t, pool, orgBSlug, "admin-b+"+orgBSlug+"@example.com", "Admin B", "admin", "admin-password")

	authService, err := authsvc.NewService(authsvc.Options{
		Users:        repo.NewHumanUserRepo(pool),
		Sessions:     repo.NewAuthSessionRepo(pool),
		APIKeys:      repo.NewAPIKeyRepo(pool),
		DefaultOrgID: orgA.ID,
		AuthMode:     "standard",
		BcryptCost:   bcrypt.MinCost,
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	handler := NewHandlerWithOptions(HandlerOptions{
		Version:     "test-version",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthService: authService,
		Pool:        pool,
		RouteRegistrars: []RouteRegistrar{
			NewModelRouteRegistrar(pool),
		},
	})

	ts := httptest.NewServer(handler)
	return &authIntegrationServer{URL: ts.URL, Pool: pool, ts: ts}, orgA, adminA, memberA, orgB, adminB
}
