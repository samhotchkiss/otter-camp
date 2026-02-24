package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestCapabilityPolicyRoutesRegistered(t *testing.T) {
	registrar := NewCapabilityPolicyRouteRegistrar(CapabilityPolicyRouteOptions{})
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
		"GET /control/policies",
		"POST /control/policies",
		"PUT /control/policies/{id}",
		"DELETE /control/policies/{id}",
		"POST /control/policies/evaluate",
	}
	for _, route := range required {
		if _, ok := routes[route]; !ok {
			t.Fatalf("missing route %q", route)
		}
	}
}

func TestCapabilityPolicyInstanceWriteProtection(t *testing.T) {
	orgID := uuid.New()
	policyID := uuid.New()
	handler := capabilityPolicyHandlers{
		policies: &fakeCapabilityPolicyRepo{
			listByOrganizationFn: func(context.Context, uuid.UUID, repo.CapabilityPolicyFilters) ([]repo.CapabilityPolicy, error) {
				return []repo.CapabilityPolicy{
					{
						ID:          policyID,
						PolicyLayer: "instance",
						Capability:  "system.file.write",
						Effect:      "deny",
						CreatedAt:   time.Now().UTC(),
					},
				}, nil
			},
			getByIDFn: func(context.Context, uuid.UUID) (repo.CapabilityPolicy, error) {
				return repo.CapabilityPolicy{
					ID:          policyID,
					PolicyLayer: "instance",
					Capability:  "system.file.write",
					Effect:      "deny",
				}, nil
			},
		},
	}

	t.Run("post instance returns 403", func(t *testing.T) {
		req := newCapabilityPolicyRequest(t, http.MethodPost, "/v1/control/policies", map[string]any{
			"policy_layer": "instance",
			"capability":   "system.file.write",
			"effect":       "deny",
		}, orgID, "admin", nil)
		rr := httptest.NewRecorder()

		handler.createPolicy(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
		}
	})

	t.Run("put instance returns 403", func(t *testing.T) {
		handler.policies.(*fakeCapabilityPolicyRepo).getByIDFn = func(context.Context, uuid.UUID) (repo.CapabilityPolicy, error) {
			return repo.CapabilityPolicy{
				ID:             policyID,
				PolicyLayer:    "org",
				OrganizationID: &orgID,
				Capability:     "system.file.write",
				Effect:         "deny",
			}, nil
		}
		req := newCapabilityPolicyRequest(t, http.MethodPut, "/v1/control/policies/"+policyID.String(), map[string]any{
			"policy_layer": "instance",
			"capability":   "system.file.write",
			"effect":       "deny",
		}, orgID, "admin", map[string]string{"id": policyID.String()})
		rr := httptest.NewRecorder()

		handler.updatePolicy(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
		}
	})

	t.Run("delete instance returns 403", func(t *testing.T) {
		handler.policies.(*fakeCapabilityPolicyRepo).getByIDFn = func(context.Context, uuid.UUID) (repo.CapabilityPolicy, error) {
			return repo.CapabilityPolicy{
				ID:          policyID,
				PolicyLayer: "instance",
				Capability:  "system.file.write",
				Effect:      "deny",
			}, nil
		}
		req := newCapabilityPolicyRequest(t, http.MethodDelete, "/v1/control/policies/"+policyID.String(), nil, orgID, "admin", map[string]string{"id": policyID.String()})
		rr := httptest.NewRecorder()

		handler.deletePolicy(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
		}
	})

	t.Run("get instance returns 200", func(t *testing.T) {
		req := newCapabilityPolicyRequest(t, http.MethodGet, "/v1/control/policies?policy_layer=instance", nil, orgID, "admin", nil)
		rr := httptest.NewRecorder()

		handler.listPolicies(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
		}
	})
}

func TestCapabilityPolicyValidation(t *testing.T) {
	orgID := uuid.New()
	fakeRepo := &fakeCapabilityPolicyRepo{}
	handler := capabilityPolicyHandlers{policies: fakeRepo}

	projectMissing := newCapabilityPolicyRequest(t, http.MethodPost, "/v1/control/policies", map[string]any{
		"policy_layer": "project",
		"capability":   "system.file.write",
		"effect":       "deny",
	}, orgID, "admin", nil)
	projectMissingResp := httptest.NewRecorder()
	handler.createPolicy(projectMissingResp, projectMissing)
	if projectMissingResp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("project missing status = %d, want %d body=%s", projectMissingResp.Code, http.StatusUnprocessableEntity, projectMissingResp.Body.String())
	}

	invalidEffect := newCapabilityPolicyRequest(t, http.MethodPost, "/v1/control/policies", map[string]any{
		"policy_layer": "org",
		"capability":   "system.file.write",
		"effect":       "maybe",
	}, orgID, "admin", nil)
	invalidEffectResp := httptest.NewRecorder()
	handler.createPolicy(invalidEffectResp, invalidEffect)
	if invalidEffectResp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid effect status = %d, want %d body=%s", invalidEffectResp.Code, http.StatusUnprocessableEntity, invalidEffectResp.Body.String())
	}
}

func newCapabilityPolicyRequest(t *testing.T, method, path string, payload any, orgID uuid.UUID, role string, urlParams map[string]string) *http.Request {
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
	rctx := chi.NewRouteContext()
	for key, value := range urlParams {
		rctx.URLParams.Add(key, value)
	}
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: orgID,
		Role:           role,
	}))
	return req
}

type fakeCapabilityPolicyRepo struct {
	createCalls          int
	updateCalls          int
	deleteCalls          int
	createFn             func(context.Context, repo.CapabilityPolicy) (repo.CapabilityPolicy, error)
	getByIDFn            func(context.Context, uuid.UUID) (repo.CapabilityPolicy, error)
	listByOrganizationFn func(context.Context, uuid.UUID, repo.CapabilityPolicyFilters) ([]repo.CapabilityPolicy, error)
	updateFn             func(context.Context, repo.CapabilityPolicy) (repo.CapabilityPolicy, error)
	deleteFn             func(context.Context, uuid.UUID) error
}

func (f *fakeCapabilityPolicyRepo) Create(ctx context.Context, item repo.CapabilityPolicy) (repo.CapabilityPolicy, error) {
	f.createCalls++
	if f.createFn != nil {
		return f.createFn(ctx, item)
	}
	return item, nil
}

func (f *fakeCapabilityPolicyRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.CapabilityPolicy, error) {
	if f.getByIDFn != nil {
		return f.getByIDFn(ctx, id)
	}
	return repo.CapabilityPolicy{}, repo.ErrNotFound
}

func (f *fakeCapabilityPolicyRepo) ListByOrganization(ctx context.Context, organizationID uuid.UUID, filters repo.CapabilityPolicyFilters) ([]repo.CapabilityPolicy, error) {
	if f.listByOrganizationFn != nil {
		return f.listByOrganizationFn(ctx, organizationID, filters)
	}
	return []repo.CapabilityPolicy{}, nil
}

func (f *fakeCapabilityPolicyRepo) Update(ctx context.Context, item repo.CapabilityPolicy) (repo.CapabilityPolicy, error) {
	f.updateCalls++
	if f.updateFn != nil {
		return f.updateFn(ctx, item)
	}
	return item, nil
}

func (f *fakeCapabilityPolicyRepo) Delete(ctx context.Context, id uuid.UUID) error {
	f.deleteCalls++
	if f.deleteFn != nil {
		return f.deleteFn(ctx, id)
	}
	return nil
}
