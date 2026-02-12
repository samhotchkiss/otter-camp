package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRouterRegistersMCPRoute(t *testing.T) {
	t.Parallel()

	router := NewRouter()
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotFound {
		t.Fatalf("expected /mcp route to be registered, got status %d", rec.Code)
	}
}
