package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestModelRoutesRegistered(t *testing.T) {
	registrar := NewModelRouteRegistrar(nil)
	router := chi.NewRouter()
	registrar.RegisterRoutes(router)

	routes := make(map[string]struct{})
	if err := chi.Walk(router, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[strings.ToUpper(method)+" "+route] = struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("walk routes: %v", err)
	}

	required := []string{
		"GET /model/providers",
		"PATCH /model/providers/{id}",
		"GET /model/providers/{id}/connections",
		"POST /model/providers/{id}/connections",
		"PATCH /model/providers/{id}/connections/{cid}",
		"DELETE /model/providers/{id}/connections/{cid}",
		"GET /model/profiles",
		"POST /model/profiles",
		"PATCH /model/profiles/{logical_profile_id}",
		"GET /model/profiles/{logical_profile_id}",
		"GET /model/profiles/{logical_profile_id}/history",
		"GET /model/assignments",
		"PUT /model/assignments/{scope_type}/{scope_id}",
		"DELETE /model/assignments/{scope_type}/{scope_id}",
		"GET /model/usage-rollup",
		"GET /usage",
		"GET /usage/summary",
	}
	for _, key := range required {
		if _, ok := routes[key]; !ok {
			t.Fatalf("missing route %q", key)
		}
	}
}

func TestPatchProfileVersioningCreatesSequentialCurrentVersion(t *testing.T) {
	orgID := uuid.New()
	providerID := uuid.New()
	providers := &fakeModelProviderRepo{
		providers: map[uuid.UUID]repo.ModelProvider{
			providerID: {
				ID:          providerID,
				Slug:        "test-provider",
				DisplayName: "Test Provider",
				APIBaseURL:  "https://provider.test",
				IsEnabled:   true,
				CreatedAt:   time.Now().UTC(),
				UpdatedAt:   time.Now().UTC(),
			},
		},
	}
	profiles := newFakeModelProfileRepo()

	h := modelHandlers{
		providers: providers,
		profiles:  profiles,
	}

	createReq := newModelRequest(t, http.MethodPost, "/v1/model/profiles", map[string]any{
		"display_name": "Standard",
		"provider_id":  providerID.String(),
		"model_name":   "gpt-4o-mini",
		"max_tokens":   1024,
	}, orgID, "admin")
	createResp := httptest.NewRecorder()
	h.createProfile(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status=%d want=%d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}
	logicalID := modelJSONPathString(t, createResp.Body.Bytes(), "data", "logical_profile_id")
	if logicalID == "" {
		t.Fatalf("missing logical_profile_id in create response: %s", createResp.Body.String())
	}

	patchReqV2 := newModelRequest(t, http.MethodPatch, "/v1/model/profiles/"+logicalID, map[string]any{
		"model_name": "gpt-4o-mini-v2",
	}, orgID, "admin")
	patchReqV2 = withRouteParams(patchReqV2, map[string]string{"logical_profile_id": logicalID})
	patchRespV2 := httptest.NewRecorder()
	h.patchProfile(patchRespV2, patchReqV2)
	if patchRespV2.Code != http.StatusOK {
		t.Fatalf("patch v2 status=%d want=%d body=%s", patchRespV2.Code, http.StatusOK, patchRespV2.Body.String())
	}

	patchReqV3 := newModelRequest(t, http.MethodPatch, "/v1/model/profiles/"+logicalID, map[string]any{
		"model_name": "gpt-4o-mini-v3",
	}, orgID, "admin")
	patchReqV3 = withRouteParams(patchReqV3, map[string]string{"logical_profile_id": logicalID})
	patchRespV3 := httptest.NewRecorder()
	h.patchProfile(patchRespV3, patchReqV3)
	if patchRespV3.Code != http.StatusOK {
		t.Fatalf("patch v3 status=%d want=%d body=%s", patchRespV3.Code, http.StatusOK, patchRespV3.Body.String())
	}

	history, err := profiles.ListHistoryByLogicalID(context.Background(), orgID, logicalID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("history len=%d want=3", len(history))
	}

	var currentCount int
	var latest repo.ModelProfile
	for _, row := range history {
		if row.IsCurrent {
			currentCount++
			latest = row
		}
	}
	if currentCount != 1 {
		t.Fatalf("current count=%d want=1", currentCount)
	}
	if latest.Version != 3 {
		t.Fatalf("latest version=%d want=3", latest.Version)
	}
}

func TestPatchProfileEmptyBodyReturns422(t *testing.T) {
	orgID := uuid.New()
	providerID := uuid.New()
	providers := &fakeModelProviderRepo{
		providers: map[uuid.UUID]repo.ModelProvider{
			providerID: {
				ID:          providerID,
				Slug:        "test-provider",
				DisplayName: "Test Provider",
				APIBaseURL:  "https://provider.test",
				IsEnabled:   true,
				CreatedAt:   time.Now().UTC(),
				UpdatedAt:   time.Now().UTC(),
			},
		},
	}
	profiles := newFakeModelProfileRepo()
	created, err := profiles.Create(context.Background(), repo.ModelProfile{
		LogicalProfileID:    uuid.NewString(),
		OrganizationID:      &orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          providerID,
		ModelName:           "gpt-4o-mini",
		DisplayName:         "Initial Name",
		ContextWindowTokens: 10000,
		MaxOutputTokens:     512,
		SupportsStreaming:   true,
		InvocationPurpose:   defaultInvocationPurpose,
	})
	if err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	h := modelHandlers{
		providers: providers,
		profiles:  profiles,
	}

	patchReq := newModelRequest(t, http.MethodPatch, "/v1/model/profiles/"+created.LogicalProfileID, map[string]any{}, orgID, "admin")
	patchReq = withRouteParams(patchReq, map[string]string{"logical_profile_id": created.LogicalProfileID})
	patchResp := httptest.NewRecorder()
	h.patchProfile(patchResp, patchReq)
	if patchResp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("patch status=%d want=%d body=%s", patchResp.Code, http.StatusUnprocessableEntity, patchResp.Body.String())
	}

	history, err := profiles.ListHistoryByLogicalID(context.Background(), orgID, created.LogicalProfileID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history len=%d want=1", len(history))
	}
}

func TestAssignmentHierarchyPutListDeleteGuard(t *testing.T) {
	orgID := uuid.New()
	assignments := newFakeModelAssignmentRepo()
	profiles := newFakeModelProfileRepo()
	logicalID := "profile-a"
	_, _ = profiles.Create(context.Background(), repo.ModelProfile{
		LogicalProfileID:    logicalID,
		OrganizationID:      &orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          uuid.New(),
		ModelName:           "test-model",
		ContextWindowTokens: 10000,
		MaxOutputTokens:     512,
		SupportsStreaming:   true,
		InvocationPurpose:   defaultInvocationPurpose,
	})

	h := modelHandlers{
		assignments: assignments,
		profiles:    profiles,
	}

	putReq := newModelRequest(t, http.MethodPut, "/v1/model/assignments/org/"+orgID.String(), map[string]any{
		"logical_profile_id": logicalID,
	}, orgID, "admin")
	putReq = withRouteParams(putReq, map[string]string{
		"scope_type": "org",
		"scope_id":   orgID.String(),
	})
	putResp := httptest.NewRecorder()
	h.putAssignment(putResp, putReq)
	if putResp.Code != http.StatusOK {
		t.Fatalf("put status=%d want=%d body=%s", putResp.Code, http.StatusOK, putResp.Body.String())
	}

	listReq := newModelRequest(t, http.MethodGet, "/v1/model/assignments?scope_type=organization", nil, orgID, "admin")
	listResp := httptest.NewRecorder()
	h.listAssignments(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list status=%d want=%d body=%s", listResp.Code, http.StatusOK, listResp.Body.String())
	}
	data, ok := modelJSONPathValue(t, listResp.Body.Bytes(), "data").([]any)
	if !ok || len(data) != 1 {
		t.Fatalf("list data len=%d want=1 body=%s", len(data), listResp.Body.String())
	}

	deleteReq := newModelRequest(t, http.MethodDelete, "/v1/model/assignments/org/"+orgID.String(), nil, orgID, "admin")
	deleteReq = withRouteParams(deleteReq, map[string]string{
		"scope_type": "org",
		"scope_id":   orgID.String(),
	})
	deleteResp := httptest.NewRecorder()
	h.deleteAssignment(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusConflict {
		t.Fatalf("delete status=%d want=%d body=%s", deleteResp.Code, http.StatusConflict, deleteResp.Body.String())
	}
}

func newModelRequest(t *testing.T, method, path string, payload any, orgID uuid.UUID, role string) *http.Request {
	t.Helper()

	var body []byte
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = encoded
	}

	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: orgID,
		Role:           role,
	}))
	return req
}

type fakeModelProviderRepo struct {
	providers map[uuid.UUID]repo.ModelProvider
}

func (f *fakeModelProviderRepo) List(context.Context) ([]repo.ModelProvider, error) {
	out := make([]repo.ModelProvider, 0, len(f.providers))
	for _, provider := range f.providers {
		out = append(out, provider)
	}
	return out, nil
}

func (f *fakeModelProviderRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ModelProvider, error) {
	provider, ok := f.providers[id]
	if !ok {
		return repo.ModelProvider{}, repo.ErrNotFound
	}
	return provider, nil
}

func (f *fakeModelProviderRepo) Update(_ context.Context, provider repo.ModelProvider) (repo.ModelProvider, error) {
	if _, ok := f.providers[provider.ID]; !ok {
		return repo.ModelProvider{}, repo.ErrNotFound
	}
	provider.UpdatedAt = time.Now().UTC()
	f.providers[provider.ID] = provider
	return provider, nil
}

type fakeModelProfileRepo struct {
	byLogical map[string][]repo.ModelProfile
}

func newFakeModelProfileRepo() *fakeModelProfileRepo {
	return &fakeModelProfileRepo{byLogical: make(map[string][]repo.ModelProfile)}
}

func (f *fakeModelProfileRepo) Create(_ context.Context, profile repo.ModelProfile) (repo.ModelProfile, error) {
	if strings.TrimSpace(profile.LogicalProfileID) == "" {
		profile.LogicalProfileID = uuid.NewString()
	}
	profile.ID = uuid.New()
	profile.CreatedAt = time.Now().UTC()
	profile.UpdatedAt = profile.CreatedAt
	if profile.Version <= 0 {
		profile.Version = 1
	}
	if !profile.IsCurrent {
		profile.IsCurrent = true
	}
	if profile.InvocationPurpose == "" {
		profile.InvocationPurpose = defaultInvocationPurpose
	}
	if strings.TrimSpace(profile.DisplayName) == "" {
		profile.DisplayName = profile.ModelName
	}
	items := f.byLogical[profile.LogicalProfileID]
	for i := range items {
		items[i].IsCurrent = false
	}
	f.byLogical[profile.LogicalProfileID] = append(items, profile)
	return profile, nil
}

func (f *fakeModelProfileRepo) GetCurrentByLogicalID(_ context.Context, _ uuid.UUID, logicalProfileID string) (repo.ModelProfile, error) {
	items := f.byLogical[strings.TrimSpace(logicalProfileID)]
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].IsCurrent {
			return items[i], nil
		}
	}
	return repo.ModelProfile{}, repo.ErrNotFound
}

func (f *fakeModelProfileRepo) ListCurrent(_ context.Context, _ uuid.UUID) ([]repo.ModelProfile, error) {
	out := make([]repo.ModelProfile, 0)
	for _, items := range f.byLogical {
		for _, item := range items {
			if item.IsCurrent {
				out = append(out, item)
				break
			}
		}
	}
	return out, nil
}

func (f *fakeModelProfileRepo) ListCurrentByProvider(_ context.Context, _ uuid.UUID, providerID uuid.UUID) ([]repo.ModelProfile, error) {
	out := make([]repo.ModelProfile, 0)
	for _, items := range f.byLogical {
		for _, item := range items {
			if item.IsCurrent && item.ProviderID == providerID {
				out = append(out, item)
			}
		}
	}
	return out, nil
}

func (f *fakeModelProfileRepo) ListHistoryByLogicalID(_ context.Context, _ uuid.UUID, logicalProfileID string) ([]repo.ModelProfile, error) {
	items := f.byLogical[strings.TrimSpace(logicalProfileID)]
	out := make([]repo.ModelProfile, len(items))
	copy(out, items)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Version > out[j].Version
	})
	return out, nil
}

func (f *fakeModelProfileRepo) Deprecate(_ context.Context, currentID uuid.UUID, next repo.ModelProfile) (repo.ModelProfile, error) {
	for logicalID, items := range f.byLogical {
		for idx, item := range items {
			if item.ID != currentID {
				continue
			}
			items[idx].IsCurrent = false
			next.ID = uuid.New()
			next.LogicalProfileID = logicalID
			next.OrganizationID = item.OrganizationID
			next.Version = item.Version + 1
			next.IsCurrent = true
			next.CreatedAt = time.Now().UTC()
			next.UpdatedAt = next.CreatedAt
			if next.InvocationPurpose == "" {
				next.InvocationPurpose = item.InvocationPurpose
			}
			f.byLogical[logicalID] = append(items, next)
			return next, nil
		}
	}
	return repo.ModelProfile{}, repo.ErrNotFound
}

type fakeModelAssignmentRepo struct {
	items []repo.ModelProfileAssignment
}

func newFakeModelAssignmentRepo() *fakeModelAssignmentRepo {
	return &fakeModelAssignmentRepo{items: []repo.ModelProfileAssignment{}}
}

func (f *fakeModelAssignmentRepo) Upsert(_ context.Context, assignment repo.ModelProfileAssignment) (repo.ModelProfileAssignment, error) {
	for i := range f.items {
		item := &f.items[i]
		if item.OrganizationID == assignment.OrganizationID &&
			item.ScopeType == assignment.ScopeType &&
			item.ScopeID == assignment.ScopeID &&
			item.InvocationPurpose == assignment.InvocationPurpose {
			item.LogicalProfileID = assignment.LogicalProfileID
			item.UpdatedAt = time.Now().UTC()
			return *item, nil
		}
	}
	assignment.ID = uuid.New()
	assignment.CreatedAt = time.Now().UTC()
	assignment.UpdatedAt = assignment.CreatedAt
	f.items = append(f.items, assignment)
	return assignment, nil
}

func (f *fakeModelAssignmentRepo) GetByScope(_ context.Context, organizationID uuid.UUID, scopeType string, scopeID uuid.UUID, invocationPurpose string) (repo.ModelProfileAssignment, error) {
	for _, item := range f.items {
		if item.OrganizationID == organizationID &&
			item.ScopeType == scopeType &&
			item.ScopeID == scopeID &&
			item.InvocationPurpose == invocationPurpose {
			return item, nil
		}
	}
	return repo.ModelProfileAssignment{}, repo.ErrNotFound
}

func (f *fakeModelAssignmentRepo) ListByOrg(_ context.Context, organizationID uuid.UUID) ([]repo.ModelProfileAssignment, error) {
	out := make([]repo.ModelProfileAssignment, 0, len(f.items))
	for _, item := range f.items {
		if item.OrganizationID == organizationID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (f *fakeModelAssignmentRepo) Delete(_ context.Context, id uuid.UUID) error {
	for i, item := range f.items {
		if item.ID == id {
			f.items = append(f.items[:i], f.items[i+1:]...)
			return nil
		}
	}
	return repo.ErrNotFound
}

func modelJSONPathString(t *testing.T, body []byte, path ...string) string {
	t.Helper()
	value := modelJSONPathValue(t, body, path...)
	typed, _ := value.(string)
	return typed
}

func modelJSONPathValue(t *testing.T, body []byte, path ...string) any {
	t.Helper()

	var current any
	if err := json.Unmarshal(body, &current); err != nil {
		t.Fatalf("unmarshal response body: %v", err)
	}

	for _, segment := range path {
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[segment]
			if !ok {
				t.Fatalf("missing path segment %q in %v body=%s", segment, path, string(body))
			}
			current = next
		case []any:
			if segment != "0" || len(typed) == 0 {
				t.Fatalf("invalid array segment %q in %v body=%s", segment, path, string(body))
			}
			current = typed[0]
		default:
			t.Fatalf("unexpected type %T while resolving %v body=%s", current, path, string(body))
		}
	}

	return current
}
