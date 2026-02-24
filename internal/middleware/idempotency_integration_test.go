//go:build integration

package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestIdempotencyRoundTripAndExpiry(t *testing.T) {
	pool := testdb.New(t)
	orgRepo := repo.NewOrgRepo(pool)
	keyRepo := repo.NewIdempotencyKeyRepo(pool)
	org, err := orgRepo.Create(context.Background(), repo.Organization{
		Slug:        "idem-mw-org",
		DisplayName: "idem-mw-org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	baseTime := time.Date(2026, 2, 24, 15, 0, 0, 0, time.UTC)
	callCount := 0
	handler := Idempotency(IdempotencyOptions{
		Repository: keyRepo,
		Now:        func() time.Time { return baseTime },
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		api.NewResponder(r.Context()).JSON(w, http.StatusCreated, map[string]int{"call": callCount})
	}))

	req1 := httptest.NewRequest(http.MethodPost, "/v1/projects", strings.NewReader(`{"name":"alpha"}`))
	req1.Header.Set("Idempotency-Key", "idem-roundtrip")
	req1 = req1.WithContext(api.WithOrganizationID(req1.Context(), org.ID))
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusCreated {
		t.Fatalf("first status = %d, want %d body=%s", rr1.Code, http.StatusCreated, rr1.Body.String())
	}

	keyHash := hashRequest("idem-roundtrip")
	stored, err := keyRepo.Get(context.Background(), org.ID, keyHash)
	if err != nil {
		t.Fatalf("idempotency row missing after first request: %v", err)
	}
	if stored.ResponseStatus != http.StatusCreated {
		t.Fatalf("stored response status = %d, want %d", stored.ResponseStatus, http.StatusCreated)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/v1/projects", strings.NewReader(`{"name":"alpha"}`))
	req2.Header.Set("Idempotency-Key", "idem-roundtrip")
	req2 = req2.WithContext(api.WithOrganizationID(req2.Context(), org.ID))
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusCreated {
		t.Fatalf("replay status = %d, want %d body=%s", rr2.Code, http.StatusCreated, rr2.Body.String())
	}
	if callCount != 1 {
		t.Fatalf("handler callCount = %d, want %d", callCount, 1)
	}

	if _, err := pool.Exec(context.Background(), `
		UPDATE idempotency_key
		SET expires_at = $1
		WHERE organization_id = $2
		  AND key_hash = $3
	`, baseTime.Add(-25*time.Hour), org.ID, keyHash); err != nil {
		t.Fatalf("expire idempotency key: %v", err)
	}

	req3 := httptest.NewRequest(http.MethodPost, "/v1/projects", strings.NewReader(`{"name":"alpha"}`))
	req3.Header.Set("Idempotency-Key", "idem-roundtrip")
	req3 = req3.WithContext(api.WithOrganizationID(req3.Context(), org.ID))
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusCreated {
		t.Fatalf("expired-key status = %d, want %d body=%s", rr3.Code, http.StatusCreated, rr3.Body.String())
	}
	if callCount != 2 {
		t.Fatalf("handler callCount after expired key = %d, want %d", callCount, 2)
	}

	var payload map[string]any
	if err := json.Unmarshal(rr3.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal final response: %v", err)
	}
	data := payload["data"].(map[string]any)
	if got := int(data["call"].(float64)); got != 2 {
		t.Fatalf("final call marker = %d, want %d", got, 2)
	}
}

func TestIdempotencyConflictOnDifferentBodyIntegration(t *testing.T) {
	pool := testdb.New(t)
	orgRepo := repo.NewOrgRepo(pool)
	keyRepo := repo.NewIdempotencyKeyRepo(pool)
	org, err := orgRepo.Create(context.Background(), repo.Organization{
		Slug:        "idem-mw-org-conflict-" + uuid.NewString()[:8],
		DisplayName: "idem-mw-org-conflict",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	handler := Idempotency(IdempotencyOptions{
		Repository: keyRepo,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.NewResponder(r.Context()).JSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))

	first := httptest.NewRequest(http.MethodPost, "/v1/projects", strings.NewReader(`{"name":"alpha"}`))
	first.Header.Set("Idempotency-Key", "idem-conflict")
	first = first.WithContext(api.WithOrganizationID(first.Context(), org.ID))
	handler.ServeHTTP(httptest.NewRecorder(), first)

	second := httptest.NewRequest(http.MethodPost, "/v1/projects", strings.NewReader(`{"name":"beta"}`))
	second.Header.Set("Idempotency-Key", "idem-conflict")
	second = second.WithContext(api.WithOrganizationID(second.Context(), org.ID))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, second)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusConflict, rr.Body.String())
	}
}
