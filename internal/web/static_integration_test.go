//go:build integration

package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"
)

func TestEmbeddedModeServesIndexAndPreservesAPIPriority(t *testing.T) {
	static := &StaticFileServer{
		fs: http.FS(fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("<html>embedded</html>")},
		}),
		mode:        modeEmbedded,
		assetsBuilt: true,
	}

	router := chi.NewRouter()
	router.Route("/v1", func(v1 chi.Router) {
		v1.Get("/version", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":{"version":"test"}}`)
		})
	})
	router.Get("/", SPAFallbackHandler(static))
	router.Get("/*", SPAFallbackHandler(static))

	rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
	rootRec := httptest.NewRecorder()
	router.ServeHTTP(rootRec, rootReq)
	if rootRec.Code != http.StatusOK {
		t.Fatalf("root status = %d, want %d", rootRec.Code, http.StatusOK)
	}
	if !strings.Contains(rootRec.Body.String(), "embedded") {
		t.Fatalf("root body = %q", rootRec.Body.String())
	}

	apiReq := httptest.NewRequest(http.MethodGet, "/v1/version", nil)
	apiRec := httptest.NewRecorder()
	router.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusOK {
		t.Fatalf("api status = %d, want %d", apiRec.Code, http.StatusOK)
	}
	if !strings.Contains(apiRec.Body.String(), `"version":"test"`) {
		t.Fatalf("api body = %q", apiRec.Body.String())
	}
}

func TestDirectoryModeServesFromDisk(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>directory</html>"), 0o600); err != nil {
		t.Fatalf("write index.html: %v", err)
	}

	static := NewStaticFileServer(Options{Mode: modeDirectory, Directory: dir})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	static.ServeIndex(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "directory") {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache, no-store" {
		t.Fatalf("cache-control = %q", got)
	}
}
