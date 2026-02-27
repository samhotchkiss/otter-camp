package server

import (
	"context"
	"encoding/json"
	"errors"
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

func TestOrgAuditRoutesRegistered(t *testing.T) {
	registrar := &OrgAuditRouteRegistrar{}
	router := chi.NewRouter()
	registrar.RegisterRoutes(router)

	routes := map[string]struct{}{}
	if err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[strings.ToUpper(method)+" "+route] = struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("walk routes: %v", err)
	}

	required := []string{
		"GET /orgs/current",
		"GET /audit",
		"GET /audit-events",
	}
	for _, route := range required {
		if _, ok := routes[route]; !ok {
			t.Fatalf("missing route %q", route)
		}
	}
}

func TestGetCurrentOrg(t *testing.T) {
	orgID := uuid.New()
	h := orgAuditHandlers{
		orgs: fakeOrgRepo{
			getByID: func(_ context.Context, id uuid.UUID) (repo.Organization, error) {
				if id != orgID {
					t.Fatalf("GetByID id=%s want=%s", id, orgID)
				}
				return repo.Organization{ID: orgID, DisplayName: "OtterCamp", Slug: "default"}, nil
			},
		},
	}

	req := withPrincipal(t, httptest.NewRequest(http.MethodGet, "/v1/orgs/current", nil), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: orgID,
		Role:           "admin",
	})
	rr := httptest.NewRecorder()
	h.getCurrentOrg(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if got := stringJSONPath(t, rr.Body.Bytes(), "data", "id"); got != orgID.String() {
		t.Fatalf("data.id=%q want=%q body=%s", got, orgID, rr.Body.String())
	}
	if got := stringJSONPath(t, rr.Body.Bytes(), "data", "name"); got != "OtterCamp" {
		t.Fatalf("data.name=%q want=%q body=%s", got, "OtterCamp", rr.Body.String())
	}
}

func TestGetCurrentOrgNotFound(t *testing.T) {
	h := orgAuditHandlers{
		orgs: fakeOrgRepo{
			getByID: func(context.Context, uuid.UUID) (repo.Organization, error) {
				return repo.Organization{}, repo.ErrNotFound
			},
		},
	}

	req := withPrincipal(t, httptest.NewRequest(http.MethodGet, "/v1/orgs/current", nil), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: uuid.New(),
		Role:           "admin",
	})
	rr := httptest.NewRecorder()
	h.getCurrentOrg(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d want=%d body=%s", rr.Code, http.StatusNotFound, rr.Body.String())
	}
}

func TestListAuditMapsBootstrapAction(t *testing.T) {
	orgID := uuid.New()
	now := time.Now().UTC()
	repo := &fakeAuditRepo{
		events: []repo.AuditEvent{
			{
				ID:             uuid.New(),
				OrganizationID: orgID,
				EventType:      bootstrapAuditEventType,
				PrincipalType:  "system",
				PrincipalID:    uuid.Nil,
				IP:             "203.0.113.200",
				Outcome:        "success",
				CreatedAt:      now,
			},
		},
	}
	h := orgAuditHandlers{audits: repo}

	req := withPrincipal(t, httptest.NewRequest(http.MethodGet, "/v1/audit?action=bootstrap_complete", nil), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: orgID,
		Role:           "admin",
	})
	rr := httptest.NewRecorder()
	h.listAudit(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}

	if repo.filters.EventType == nil || *repo.filters.EventType != bootstrapAuditEventType {
		t.Fatalf("event type filter=%v want=%q", repo.filters.EventType, bootstrapAuditEventType)
	}
	if got := stringJSONPath(t, rr.Body.Bytes(), "data", "0", "action"); got != bootstrapAuditAction {
		t.Fatalf("data[0].action=%q want=%q body=%s", got, bootstrapAuditAction, rr.Body.String())
	}
	if got := stringJSONPath(t, rr.Body.Bytes(), "data", "0", "ip"); got != "203.0.113.200" {
		t.Fatalf("data[0].ip=%q want=%q body=%s", got, "203.0.113.200", rr.Body.String())
	}
	if got := stringJSONPath(t, rr.Body.Bytes(), "data", "0", "outcome"); got != "success" {
		t.Fatalf("data[0].outcome=%q want=%q body=%s", got, "success", rr.Body.String())
	}
}

func TestListAuditRepositoryError(t *testing.T) {
	h := orgAuditHandlers{
		audits: &fakeAuditRepo{err: errors.New("boom")},
	}

	req := withPrincipal(t, httptest.NewRequest(http.MethodGet, "/v1/audit", nil), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: uuid.New(),
		Role:           "admin",
	})
	rr := httptest.NewRecorder()
	h.listAudit(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want=%d body=%s", rr.Code, http.StatusInternalServerError, rr.Body.String())
	}
}

func TestListAuditEventsRequiresAdminOrOwner(t *testing.T) {
	h := orgAuditHandlers{
		audits: &fakeAuditRepo{},
	}

	req := withPrincipal(t, httptest.NewRequest(http.MethodGet, "/v1/audit-events", nil), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: uuid.New(),
		Role:           "member",
	})
	rr := httptest.NewRecorder()
	h.listAuditEvents(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d want=%d body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
	}
}

func TestListAuditEventsParsesFiltersAndReturnsData(t *testing.T) {
	orgID := uuid.New()
	principalID := uuid.New()
	from := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	to := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Second)
	now := time.Now().UTC()

	repo := &fakeAuditRepo{
		events: []repo.AuditEvent{
			{
				ID:             uuid.New(),
				OrganizationID: orgID,
				EventType:      "auth.login",
				PrincipalType:  "agent",
				PrincipalID:    principalID,
				Metadata: map[string]any{
					"ip_address": "127.0.0.1",
				},
				CreatedAt: now,
			},
		},
	}
	h := orgAuditHandlers{audits: repo}

	req := withPrincipal(t, httptest.NewRequest(
		http.MethodGet,
		"/v1/audit-events?action=auth.login&principal_type=agent&principal_id="+principalID.String()+"&from="+from.Format(time.RFC3339)+"&to="+to.Format(time.RFC3339)+"&limit=20",
		nil,
	), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: orgID,
		Role:           "owner",
	})
	rr := httptest.NewRecorder()
	h.listAuditEvents(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if repo.filters.EventType == nil || *repo.filters.EventType != "auth.login" {
		t.Fatalf("event type filter=%v want=%q", repo.filters.EventType, "auth.login")
	}
	if repo.filters.PrincipalType == nil || *repo.filters.PrincipalType != "agent" {
		t.Fatalf("principal type filter=%v want=%q", repo.filters.PrincipalType, "agent")
	}
	if repo.filters.PrincipalID == nil || *repo.filters.PrincipalID != principalID {
		t.Fatalf("principal id filter=%v want=%s", repo.filters.PrincipalID, principalID)
	}
	if repo.filters.CreatedAfter == nil || !repo.filters.CreatedAfter.Equal(from) {
		t.Fatalf("created_after=%v want=%s", repo.filters.CreatedAfter, from)
	}
	if repo.filters.CreatedBefore == nil || !repo.filters.CreatedBefore.Equal(to) {
		t.Fatalf("created_before=%v want=%s", repo.filters.CreatedBefore, to)
	}
	if repo.page.Limit != 20 {
		t.Fatalf("limit=%d want=%d", repo.page.Limit, 20)
	}
	if got := stringJSONPath(t, rr.Body.Bytes(), "data", "0", "action"); got != "auth.login" {
		t.Fatalf("data[0].action=%q want=%q body=%s", got, "auth.login", rr.Body.String())
	}
	if got := stringJSONPath(t, rr.Body.Bytes(), "data", "0", "principal_type"); got != "agent" {
		t.Fatalf("data[0].principal_type=%q want=%q body=%s", got, "agent", rr.Body.String())
	}
	if got := stringJSONPath(t, rr.Body.Bytes(), "data", "0", "context", "ip_address"); got != "127.0.0.1" {
		t.Fatalf("data[0].context.ip_address=%q want=%q body=%s", got, "127.0.0.1", rr.Body.String())
	}
}

func TestListAuditEventsRejectsInvalidPrincipalType(t *testing.T) {
	h := orgAuditHandlers{
		audits: &fakeAuditRepo{},
	}

	req := withPrincipal(t, httptest.NewRequest(http.MethodGet, "/v1/audit-events?principal_type=robot", nil), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: uuid.New(),
		Role:           "admin",
	})
	rr := httptest.NewRecorder()
	h.listAuditEvents(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want=%d body=%s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
}

type fakeOrgRepo struct {
	getByID func(ctx context.Context, id uuid.UUID) (repo.Organization, error)
}

func (f fakeOrgRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.Organization, error) {
	if f.getByID == nil {
		return repo.Organization{}, repo.ErrNotFound
	}
	return f.getByID(ctx, id)
}

type fakeAuditRepo struct {
	events   []repo.AuditEvent
	err      error
	filters  repo.AuditEventFilters
	page     repo.Pagination
	called   bool
	calledID uuid.UUID
}

func (f *fakeAuditRepo) ListByOrg(_ context.Context, organizationID uuid.UUID, filters repo.AuditEventFilters, pagination repo.Pagination) ([]repo.AuditEvent, error) {
	f.called = true
	f.calledID = organizationID
	f.filters = filters
	f.page = pagination
	if f.err != nil {
		return nil, f.err
	}
	return f.events, nil
}

func withPrincipal(t *testing.T, req *http.Request, principal middleware.Principal) *http.Request {
	t.Helper()
	return req.WithContext(middleware.WithPrincipal(req.Context(), principal))
}

func stringJSONPath(t *testing.T, body []byte, path ...string) string {
	t.Helper()
	value := jsonPath(t, body, path...)
	str, _ := value.(string)
	return str
}

func jsonPath(t *testing.T, body []byte, path ...string) any {
	t.Helper()
	var current any
	if err := json.Unmarshal(body, &current); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	for _, segment := range path {
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[segment]
			if !ok {
				t.Fatalf("path %v missing segment %q in body=%s", path, segment, string(body))
			}
			current = next
		case []any:
			if segment != "0" || len(typed) == 0 {
				t.Fatalf("path %v invalid array segment %q in body=%s", path, segment, string(body))
			}
			current = typed[0]
		default:
			t.Fatalf("path %v invalid segment %q on type %T in body=%s", path, segment, current, string(body))
		}
	}
	return current
}
