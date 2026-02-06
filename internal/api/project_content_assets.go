package api

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

const maxProjectContentAssetBytes = 8 << 20 // 8 MiB

var (
	allowedAssetMimeToExt = map[string]string{
		"image/png":  ".png",
		"image/jpeg": ".jpg",
		"image/gif":  ".gif",
	}
	assetFilenameCleaner = regexp.MustCompile(`[^a-z0-9]+`)
)

type projectContentAssetUploadResponse struct {
	Path     string `json:"path"`
	Markdown string `json:"markdown"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size_bytes"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

func (h *ProjectChatHandler) UploadContentAsset(w http.ResponseWriter, r *http.Request) {
	if h.ProjectStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}
	if err := h.requireProjectAccess(r.Context(), projectID); err != nil {
		handleProjectChatStoreError(w, err)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxProjectContentAssetBytes+2048)
	if err := r.ParseMultipartForm(maxProjectContentAssetBytes); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "too large") {
			sendJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "asset exceeds 8MB limit"})
			return
		}
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid multipart form"})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "file is required"})
		return
	}
	defer file.Close()

	content, err := readUploadedAsset(file)
	if err != nil {
		sendJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "asset exceeds 8MB limit"})
		return
	}

	detectedMime := http.DetectContentType(content[:minInt(len(content), 512)])
	extension, ok := allowedAssetMimeToExt[detectedMime]
	if !ok {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "only PNG, JPG, and GIF are supported"})
		return
	}

	config, _, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid image payload"})
		return
	}

	assetName := normalizeAssetFilename(header.Filename, extension)
	relativePath, absolutePath, err := resolveProjectContentWritePath(projectID, "/assets/"+assetName)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	absolutePath, relativePath, err = chooseUniqueAssetPath(absolutePath, relativePath)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to resolve asset path"})
		return
	}

	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to prepare asset directory"})
		return
	}
	if err := os.WriteFile(absolutePath, content, 0o644); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to write asset"})
		return
	}

	alt := strings.TrimSuffix(filepath.Base(relativePath), filepath.Ext(relativePath))
	sendJSON(w, http.StatusCreated, projectContentAssetUploadResponse{
		Path:     relativePath,
		Markdown: fmt.Sprintf("![%s](%s)", alt, relativePath),
		MimeType: detectedMime,
		Size:     int64(len(content)),
		Width:    config.Width,
		Height:   config.Height,
	})
}

func readUploadedAsset(file multipart.File) ([]byte, error) {
	limited := io.LimitReader(file, maxProjectContentAssetBytes+1)
	content, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(content) > maxProjectContentAssetBytes {
		return nil, fmt.Errorf("file too large")
	}
	if len(content) == 0 {
		return nil, fmt.Errorf("file is empty")
	}
	return content, nil
}

func normalizeAssetFilename(original, ext string) string {
	base := strings.TrimSpace(strings.TrimSuffix(strings.ToLower(filepath.Base(original)), filepath.Ext(original)))
	base = assetFilenameCleaner.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "asset-" + time.Now().UTC().Format("20060102-150405")
	}
	return base + ext
}

func chooseUniqueAssetPath(absolutePath, relativePath string) (string, string, error) {
	if _, err := os.Stat(absolutePath); err != nil {
		if os.IsNotExist(err) {
			return absolutePath, relativePath, nil
		}
		return "", "", err
	}

	base := strings.TrimSuffix(relativePath, filepath.Ext(relativePath))
	ext := filepath.Ext(relativePath)
	for index := 1; index <= 1000; index++ {
		nextRelative := fmt.Sprintf("%s-%d%s", base, index, ext)
		nextAbsolute := filepath.Join(filepath.Dir(absolutePath), filepath.Base(nextRelative))
		if _, err := os.Stat(nextAbsolute); err != nil {
			if os.IsNotExist(err) {
				return nextAbsolute, nextRelative, nil
			}
			return "", "", err
		}
	}

	return "", "", fmt.Errorf("failed to allocate unique asset path")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
