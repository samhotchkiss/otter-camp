package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCommandSearchValidation(t *testing.T) {
	t.Parallel()

	router := NewRouter()

	for _, tc := range []struct {
		name       string
		target     string
		wantStatus int
		wantError  string
	}{
		{
			name:       "missing query",
			target:     "/api/commands/search?org_id=00000000-0000-0000-0000-000000000000",
			wantStatus: http.StatusBadRequest,
			wantError:  "missing query parameter: q",
		},
		{
			name:       "missing org_id",
			target:     "/api/commands/search?q=agent",
			wantStatus: http.StatusBadRequest,
			wantError:  "missing query parameter: org_id",
		},
		{
			name:       "invalid org_id",
			target:     "/api/commands/search?q=agent&org_id=not-a-uuid",
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid org_id",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, rec.Code)
			}

			var payload map[string]string
			if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if payload["error"] != tc.wantError {
				t.Fatalf("expected error %q, got %q", tc.wantError, payload["error"])
			}
		})
	}
}

func TestCommandSearchMethodNotAllowed(t *testing.T) {
	t.Parallel()

	router := NewRouter()

	req := httptest.NewRequest(http.MethodPost, "/api/commands/search?q=agent&org_id=00000000-0000-0000-0000-000000000000", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status %d, got %d", http.StatusMethodNotAllowed, rec.Code)
	}
}
