package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func buildMultipartAssetRequest(t *testing.T, filename string, payload []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write(payload)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	return body, writer.FormDataContentType()
}

func createPNGFixture(t *testing.T) []byte {
	t.Helper()
	buffer := &bytes.Buffer{}
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 1, color.RGBA{B: 255, A: 255})
	require.NoError(t, png.Encode(buffer, img))
	return buffer.Bytes()
}

func TestProjectContentAssetUploadStoresUnderAssetsAndReturnsMetadata(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-content-assets-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Content Assets")

	root := t.TempDir()
	t.Setenv("OTTER_CONTENT_ROOT", root)

	handler := &ProjectChatHandler{
		ProjectStore: store.NewProjectStore(db),
		ChatStore:    store.NewProjectChatStore(db),
	}
	router := newProjectChatTestRouter(handler)

	body, contentType := buildMultipartAssetRequest(t, "../../My Launch Image.PNG", createPNGFixture(t))
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/content/assets?org_id="+orgID, body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var resp projectContentAssetUploadResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.True(t, strings.HasPrefix(resp.Path, "/assets/my-launch-image"))
	require.True(t, strings.HasSuffix(resp.Path, ".png"))
	require.Contains(t, resp.Markdown, resp.Path)
	require.Equal(t, "image/png", resp.MimeType)
	require.Greater(t, resp.Size, int64(0))
	require.Equal(t, 2, resp.Width)
	require.Equal(t, 2, resp.Height)

	savedPath := filepath.Join(root, projectID, filepath.FromSlash(strings.TrimPrefix(resp.Path, "/")))
	info, err := os.Stat(savedPath)
	require.NoError(t, err)
	require.False(t, info.IsDir())
}

func TestProjectContentAssetUploadRejectsUnsupportedAndOversizePayloads(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-content-assets-validate-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Content Assets Validate")

	root := t.TempDir()
	t.Setenv("OTTER_CONTENT_ROOT", root)

	handler := &ProjectChatHandler{
		ProjectStore: store.NewProjectStore(db),
		ChatStore:    store.NewProjectChatStore(db),
	}
	router := newProjectChatTestRouter(handler)

	unsupportedBody, unsupportedType := buildMultipartAssetRequest(t, "notes.txt", []byte("not an image"))
	unsupportedReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/content/assets?org_id="+orgID, unsupportedBody)
	unsupportedReq.Header.Set("Content-Type", unsupportedType)
	unsupportedRec := httptest.NewRecorder()
	router.ServeHTTP(unsupportedRec, unsupportedReq)
	require.Equal(t, http.StatusBadRequest, unsupportedRec.Code)

	oversizePayload := bytes.Repeat([]byte("x"), maxProjectContentAssetBytes+1)
	oversizeBody, oversizeType := buildMultipartAssetRequest(t, "too-big.png", oversizePayload)
	oversizeReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/content/assets?org_id="+orgID, oversizeBody)
	oversizeReq.Header.Set("Content-Type", oversizeType)
	oversizeRec := httptest.NewRecorder()
	router.ServeHTTP(oversizeRec, oversizeReq)
	require.Equal(t, http.StatusRequestEntityTooLarge, oversizeRec.Code)
}
