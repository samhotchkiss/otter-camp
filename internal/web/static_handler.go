package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

//go:embed web/dist
var webAssets embed.FS

const (
	modeEmbedded  = "embedded"
	modeDirectory = "directory"
)

// ViewStatePersistenceContract: All UI view state (selected filters, column widths,
// panel open/closed state, etc.) MUST be persisted in the browser's localStorage only.
// The API server will never store or return view state. This is a hard design constraint.
// Any request from the front-end team to add view state to the API should be rejected
// and redirected to use localStorage instead.
type StaticFileServer struct {
	fs          http.FileSystem
	mode        string
	assetsBuilt bool
}

type Options struct {
	Mode      string
	Directory string
}

func NewStaticFileServer(opts Options) *StaticFileServer {
	mode := strings.ToLower(strings.TrimSpace(opts.Mode))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(os.Getenv("OTTERCAMP_WEB_MODE")))
	}
	if mode == "" {
		mode = modeEmbedded
	}

	switch mode {
	case modeDirectory:
		directory := strings.TrimSpace(opts.Directory)
		if directory == "" {
			directory = strings.TrimSpace(os.Getenv("OTTERCAMP_WEB_DIR"))
		}
		if directory == "" {
			directory = "./web/dist"
		}
		return &StaticFileServer{
			fs:          http.FS(os.DirFS(directory)),
			mode:        modeDirectory,
			assetsBuilt: fileExistsOnDisk(filepath.Join(directory, "index.html")),
		}
	default:
		sub, err := fs.Sub(webAssets, "web/dist")
		if err != nil {
			sub = emptyFS{}
		}
		return &StaticFileServer{
			fs:          http.FS(sub),
			mode:        modeEmbedded,
			assetsBuilt: fileExists(sub, "index.html"),
		}
	}
}

func (s *StaticFileServer) ServeAsset(w http.ResponseWriter, r *http.Request) {
	if !s.assetsBuilt {
		writeAssetsNotBuilt(w)
		return
	}

	assetPath := strings.TrimPrefix(r.URL.Path, "/assets/")
	assetPath = strings.TrimSpace(assetPath)
	if assetPath == "" || assetPath == r.URL.Path {
		http.NotFound(w, r)
		return
	}
	s.setAssetCacheHeaders(w)
	s.serveFile(w, r, path.Clean("assets/"+assetPath))
}

func (s *StaticFileServer) ServeFavicon(w http.ResponseWriter, r *http.Request) {
	if !s.assetsBuilt {
		writeAssetsNotBuilt(w)
		return
	}
	if s.mode == modeDirectory {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=86400")
	}
	s.serveFile(w, r, "favicon.ico")
}

func (s *StaticFileServer) ServeIndex(w http.ResponseWriter, r *http.Request) {
	if !s.assetsBuilt {
		writeAssetsNotBuilt(w)
		return
	}
	w.Header().Set("Cache-Control", "no-cache, no-store")
	s.serveFile(w, r, "index.html")
}

func (s *StaticFileServer) setAssetCacheHeaders(w http.ResponseWriter) {
	if s.mode == modeDirectory {
		w.Header().Set("Cache-Control", "no-store")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
}

func (s *StaticFileServer) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	if s == nil || s.fs == nil {
		http.NotFound(w, r)
		return
	}

	clean := path.Clean(strings.TrimPrefix(name, "/"))
	if clean == "." || strings.HasPrefix(clean, "../") {
		http.NotFound(w, r)
		return
	}

	file, err := s.fs.Open(clean)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	if seeker, ok := file.(io.ReadSeeker); ok {
		http.ServeContent(w, r, info.Name(), info.ModTime(), seeker)
		return
	}
	data, err := io.ReadAll(file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, info.Name(), time.Time{}, bytes.NewReader(data))
}

func writeAssetsNotBuilt(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": "web_assets_not_built",
		"hint":  "Run 'make build-web' first",
	})
}

func fileExists(fsys fs.FS, name string) bool {
	if fsys == nil {
		return false
	}
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}

func fileExistsOnDisk(name string) bool {
	info, err := os.Stat(strings.TrimSpace(name))
	return err == nil && !info.IsDir()
}

type emptyFS struct{}

func (emptyFS) Open(string) (fs.File, error) {
	return nil, errors.New("not found")
}
