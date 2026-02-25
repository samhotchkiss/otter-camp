package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"
)

func TestSPAFallbackRouting(t *testing.T) {
	static := &StaticFileServer{
		fs:          http.FS(fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>spa</html>")}}),
		mode:        modeEmbedded,
		assetsBuilt: true,
	}

	router := chi.NewRouter()
	router.Route("/v1", func(v1 chi.Router) {
		v1.Get("/projects", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		})
	})
	router.Get("/", SPAFallbackHandler(static))
	router.Get("/*", SPAFallbackHandler(static))

	spaReq := httptest.NewRequest(http.MethodGet, "/dashboard/projects", nil)
	spaRec := httptest.NewRecorder()
	router.ServeHTTP(spaRec, spaReq)
	if spaRec.Code != http.StatusOK {
		t.Fatalf("spa fallback status = %d, want %d", spaRec.Code, http.StatusOK)
	}
	if !strings.Contains(spaRec.Body.String(), "<html>spa</html>") {
		t.Fatalf("spa fallback body = %q", spaRec.Body.String())
	}

	apiReq := httptest.NewRequest(http.MethodGet, "/v1/projects", nil)
	apiRec := httptest.NewRecorder()
	router.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusUnauthorized {
		t.Fatalf("api route status = %d, want %d", apiRec.Code, http.StatusUnauthorized)
	}
	if strings.Contains(apiRec.Body.String(), "<html>spa</html>") {
		t.Fatalf("api route unexpectedly served index.html: %q", apiRec.Body.String())
	}
}

func TestAssetAndIndexCacheHeaders(t *testing.T) {
	static := &StaticFileServer{
		fs: http.FS(fstest.MapFS{
			"index.html":        &fstest.MapFile{Data: []byte("<html>spa</html>")},
			"assets/app.abc.js": &fstest.MapFile{Data: []byte("console.log('ok')")},
		}),
		mode:        modeEmbedded,
		assetsBuilt: true,
	}

	assetReq := httptest.NewRequest(http.MethodGet, "/assets/app.abc.js", nil)
	assetRec := httptest.NewRecorder()
	static.ServeAsset(assetRec, assetReq)
	if assetRec.Code != http.StatusOK {
		t.Fatalf("asset status = %d, want %d", assetRec.Code, http.StatusOK)
	}
	if got := assetRec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("asset cache-control = %q", got)
	}

	indexReq := httptest.NewRequest(http.MethodGet, "/", nil)
	indexRec := httptest.NewRecorder()
	static.ServeIndex(indexRec, indexReq)
	if indexRec.Code != http.StatusOK {
		t.Fatalf("index status = %d, want %d", indexRec.Code, http.StatusOK)
	}
	if got := indexRec.Header().Get("Cache-Control"); got != "no-cache, no-store" {
		t.Fatalf("index cache-control = %q", got)
	}
}

func TestDirectoryModeDisablesAssetCaching(t *testing.T) {
	static := &StaticFileServer{
		fs: http.FS(fstest.MapFS{
			"assets/main.js": &fstest.MapFile{Data: []byte("alert(1)")},
		}),
		mode:        modeDirectory,
		assetsBuilt: true,
	}

	req := httptest.NewRequest(http.MethodGet, "/assets/main.js", nil)
	rec := httptest.NewRecorder()
	static.ServeAsset(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control = %q, want %q", got, "no-store")
	}
}

func TestEmptyEmbeddedFSReturnsHint(t *testing.T) {
	static := &StaticFileServer{
		fs:          http.FS(fstest.MapFS{}),
		mode:        modeEmbedded,
		assetsBuilt: false,
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	static.ServeIndex(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "web_assets_not_built") {
		t.Fatalf("body = %q", string(body))
	}
}
