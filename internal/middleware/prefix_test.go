package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPrefixEnforcementRejectsNonV1PathForNonReadMethods(t *testing.T) {
	handler := PrefixEnforcement()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run")
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/projects", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}

	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	errorObject, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("error envelope missing: %+v", payload)
	}
	if got := errorObject["message"]; got != "This API uses the /v1/ prefix. See docs." {
		t.Fatalf("error.message = %v", got)
	}
}

func TestPrefixEnforcementAllowsRoot(t *testing.T) {
	handler := PrefixEnforcement()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestPrefixEnforcementAllowsTestResetRoute(t *testing.T) {
	called := false
	handler := PrefixEnforcement()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/test/reset", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
	if !called {
		t.Fatal("expected wrapped handler to be called")
	}
}
