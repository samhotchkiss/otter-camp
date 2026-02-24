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
)

func TestIdempotencyStoresThenReplaysWithoutInvokingHandler(t *testing.T) {
	store := &fakeIdempotencyStore{}
	orgID := uuid.New()
	now := time.Date(2026, 2, 24, 14, 0, 0, 0, time.UTC)

	calls := 0
	base := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		api.NewResponder(r.Context()).JSON(w, http.StatusCreated, map[string]int{"call": calls})
	})

	handler := Idempotency(IdempotencyOptions{
		Repository: store,
		Now:        func() time.Time { return now },
	})(base)

	req1 := httptest.NewRequest(http.MethodPost, "/v1/projects", strings.NewReader(`{"name":"alpha"}`))
	req1.Header.Set("Idempotency-Key", "idem-1")
	req1 = req1.WithContext(api.WithOrganizationID(api.WithRequestID(req1.Context(), "req-1"), orgID))
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)

	req2 := httptest.NewRequest(http.MethodPost, "/v1/projects", strings.NewReader(`{"name":"alpha"}`))
	req2.Header.Set("Idempotency-Key", "idem-1")
	req2 = req2.WithContext(api.WithOrganizationID(api.WithRequestID(req2.Context(), "req-2"), orgID))
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)

	if calls != 1 {
		t.Fatalf("handler calls = %d, want %d", calls, 1)
	}
	if rr2.Code != http.StatusCreated {
		t.Fatalf("replay status = %d, want %d", rr2.Code, http.StatusCreated)
	}

	firstBody := strings.TrimSpace(rr1.Body.String())
	secondBody := strings.TrimSpace(rr2.Body.String())
	if firstBody != secondBody {
		t.Fatalf("replayed body mismatch: first=%s second=%s", firstBody, secondBody)
	}
}

func TestIdempotencyRejectsKeyReuseWithDifferentBody(t *testing.T) {
	store := &fakeIdempotencyStore{}
	orgID := uuid.New()
	now := time.Date(2026, 2, 24, 14, 0, 0, 0, time.UTC)

	handler := Idempotency(IdempotencyOptions{
		Repository: store,
		Now:        func() time.Time { return now },
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.NewResponder(r.Context()).JSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))

	req1 := httptest.NewRequest(http.MethodPost, "/v1/projects", strings.NewReader(`{"name":"alpha"}`))
	req1.Header.Set("Idempotency-Key", "idem-2")
	req1 = req1.WithContext(api.WithOrganizationID(req1.Context(), orgID))
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)

	req2 := httptest.NewRequest(http.MethodPost, "/v1/projects", strings.NewReader(`{"name":"beta"}`))
	req2.Header.Set("Idempotency-Key", "idem-2")
	req2 = req2.WithContext(api.WithOrganizationID(req2.Context(), orgID))
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rr2.Code, http.StatusConflict)
	}

	var payload map[string]any
	if err := json.Unmarshal(rr2.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	errorObject := payload["error"].(map[string]any)
	if got := errorObject["code"]; got != "idempotency_conflict" {
		t.Fatalf("error.code = %v, want %q", got, "idempotency_conflict")
	}
}

type fakeIdempotencyStore struct {
	rows []repo.IdempotencyKey
}

func (f *fakeIdempotencyStore) Get(_ context.Context, organizationID uuid.UUID, keyHash string) (repo.IdempotencyKey, error) {
	for _, row := range f.rows {
		if row.OrganizationID == organizationID && row.KeyHash == keyHash {
			return row, nil
		}
	}
	return repo.IdempotencyKey{}, repo.ErrNotFound
}

func (f *fakeIdempotencyStore) Store(_ context.Context, key repo.IdempotencyKey) (repo.IdempotencyKey, error) {
	for i := range f.rows {
		if f.rows[i].OrganizationID == key.OrganizationID && f.rows[i].KeyHash == key.KeyHash {
			if f.rows[i].RequestHash != key.RequestHash {
				return repo.IdempotencyKey{}, repo.ErrIdempotencyConflict
			}
			return f.rows[i], nil
		}
	}
	key.ID = uuid.New()
	key.CreatedAt = time.Now().UTC()
	f.rows = append(f.rows, key)
	return key, nil
}

func (f *fakeIdempotencyStore) Delete(_ context.Context, organizationID uuid.UUID, keyHash string) error {
	kept := make([]repo.IdempotencyKey, 0, len(f.rows))
	for _, row := range f.rows {
		if row.OrganizationID == organizationID && row.KeyHash == keyHash {
			continue
		}
		kept = append(kept, row)
	}
	f.rows = kept
	return nil
}
