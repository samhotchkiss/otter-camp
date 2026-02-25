package health

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLivenessReturnsOKWithoutDependencies(t *testing.T) {
	h := NewHandler(Options{})
	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	rec := httptest.NewRecorder()

	h.Liveness(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestReadinessReturnsDegradedWhenDBUnavailable(t *testing.T) {
	h := NewHandler(Options{})
	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()

	h.Readiness(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if got := rec.Body.String(); !strings.Contains(got, `"db":false`) {
		t.Fatalf("body missing db=false: %s", got)
	}
}
