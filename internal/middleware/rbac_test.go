package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/auth"
)

func TestRequireRoleAdminAllowsAdmin(t *testing.T) {
	handler := RequireRole("admin")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req = req.WithContext(WithPrincipal(req.Context(), Principal{UserID: uuid.New(), Role: "admin"}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestRequireRoleAdminRejectsMember(t *testing.T) {
	handler := RequireRole("admin")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run")
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req = req.WithContext(WithPrincipal(req.Context(), Principal{UserID: uuid.New(), Role: "member"}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestRequireScopeAllowsMatchingAPIKeyScope(t *testing.T) {
	handler := RequireScope("write:projects")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/projects", nil)
	req = req.WithContext(WithPrincipal(req.Context(), Principal{
		UserID: uuid.New(),
		APIKey: &auth.APIKeyInfo{Scopes: []string{"write:projects"}},
	}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestRequireAnyScopeAllowsAdminWildcard(t *testing.T) {
	handler := RequireAnyScope("admin:agents")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/agents", nil)
	req = req.WithContext(WithPrincipal(req.Context(), Principal{
		UserID: uuid.New(),
		APIKey: &auth.APIKeyInfo{Scopes: []string{"admin:*"}},
	}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestRequireAnyScopeRejectsMissingScope(t *testing.T) {
	handler := RequireAnyScope("write:projects")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run")
	}))

	req := httptest.NewRequest(http.MethodPost, "/projects", nil)
	req = req.WithContext(WithPrincipal(req.Context(), Principal{
		UserID: uuid.New(),
		APIKey: &auth.APIKeyInfo{Scopes: []string{"read:projects"}},
	}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestRequireAnyScopeSkipsScopeCheckForSessionAuth(t *testing.T) {
	handler := RequireAnyScope("write:projects")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/projects", nil)
	req = req.WithContext(WithPrincipal(req.Context(), Principal{
		UserID: uuid.New(),
	}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}
