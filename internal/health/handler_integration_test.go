//go:build integration

package health

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/storage"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

type healthyStore struct{}

func (healthyStore) Put(context.Context, string, io.Reader, storage.PutOptions) error { return nil }
func (healthyStore) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (healthyStore) Delete(context.Context, string) error         { return nil }
func (healthyStore) Exists(context.Context, string) (bool, error) { return false, nil }
func (healthyStore) List(context.Context, string) ([]storage.ObjectMeta, error) {
	return []storage.ObjectMeta{}, nil
}

func TestReadinessHealthyAndDBFailure(t *testing.T) {
	pool := testdb.New(t)
	h := NewHandler(Options{Pool: pool, Store: healthyStore{}})

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()
	h.Readiness(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthy status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	pool.Close()
	rec = httptest.NewRecorder()
	h.Readiness(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("db failure status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if got := rec.Body.String(); !strings.Contains(got, `"db":false`) {
		t.Fatalf("expected checks.db=false body=%s", got)
	}
}
