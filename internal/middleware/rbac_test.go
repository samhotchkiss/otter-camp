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

func TestRequireAnyScopeAcceptsBidirectionalAlias(t *testing.T) {
	handler := RequireAnyScope("write:projects")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/projects", nil)
	req = req.WithContext(WithPrincipal(req.Context(), Principal{
		UserID: uuid.New(),
		Role:   "member",
		APIKey: &auth.APIKeyInfo{
			Scopes: []string{"projects:write"},
		},
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}
