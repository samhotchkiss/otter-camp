package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDetectMimeType(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		content  []byte
		want     string
	}{
		{
			name:     "PNG image",
			filename: "test.png",
			content:  []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, // PNG magic bytes
			want:     "image/png",
		},
		{
			name:     "JPEG image",
			filename: "test.jpg",
			content:  []byte{0xFF, 0xD8, 0xFF, 0xE0}, // JPEG magic bytes
			want:     "image/jpeg",
		},
		{
			name:     "GIF image",
			filename: "test.gif",
			content:  []byte("GIF89a"), // GIF magic bytes
			want:     "image/gif",
		},
		{
			name:     "Plain text detected",
			filename: "readme.txt",
			content:  []byte("Hello, World!"),
			want:     "text/plain; charset=utf-8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := bytes.NewReader(tt.content)
			// Create a mock multipart.File
			got := detectMimeType(&mockFile{Reader: reader}, tt.filename)
			if got != tt.want {
				t.Errorf("detectMimeType() = %v, want %v", got, tt.want)
			}
		})
	}
}

// mockFile implements multipart.File for testing
type mockFile struct {
	*bytes.Reader
}

func (m *mockFile) Close() error {
	return nil
}

func TestGenerateStorageKey(t *testing.T) {
	key1, err := generateStorageKey("test.png")
	if err != nil {
		t.Fatalf("generateStorageKey() error = %v", err)
	}

	if !strings.HasSuffix(key1, ".png") {
		t.Errorf("generateStorageKey() should preserve extension, got %v", key1)
	}

	// Test uniqueness
	key2, err := generateStorageKey("test.png")
	if err != nil {
		t.Fatalf("generateStorageKey() error = %v", err)
	}

	if key1 == key2 {
		t.Error("generateStorageKey() should generate unique keys")
	}
}

func TestIsImageMimeType(t *testing.T) {
	tests := []struct {
		mimeType string
		want     bool
	}{
		{"image/png", true},
		{"image/jpeg", true},
		{"image/gif", true},
		{"image/webp", true},
		{"application/pdf", false},
		{"text/plain", false},
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			if got := isImageMimeType(tt.mimeType); got != tt.want {
				t.Errorf("isImageMimeType(%v) = %v, want %v", tt.mimeType, got, tt.want)
			}
		})
	}
}

func TestUploadMissingOrgID(t *testing.T) {
	// Create multipart form without org_id
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("test content"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/messages/attachments", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rr := httptest.NewRecorder()
	handler := &AttachmentsHandler{}
	handler.Upload(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Upload() status = %v, want %v", rr.Code, http.StatusBadRequest)
	}

	if !strings.Contains(rr.Body.String(), "missing org_id") {
		t.Errorf("Upload() body should contain 'missing org_id', got %v", rr.Body.String())
	}
}

func TestUploadMissingFile(t *testing.T) {
	// Create multipart form with org_id but no file
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.WriteField("org_id", "550e8400-e29b-41d4-a716-446655440000")
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/messages/attachments", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rr := httptest.NewRecorder()
	handler := &AttachmentsHandler{}
	handler.Upload(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Upload() status = %v, want %v", rr.Code, http.StatusBadRequest)
	}

	if !strings.Contains(rr.Body.String(), "missing file") {
		t.Errorf("Upload() body should contain 'missing file', got %v", rr.Body.String())
	}
}

func TestUploadMethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/messages/attachments", nil)
	rr := httptest.NewRecorder()

	handler := &AttachmentsHandler{}
	handler.Upload(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("Upload() with GET status = %v, want %v", rr.Code, http.StatusMethodNotAllowed)
	}
}

func TestIsSupportedAttachmentMimeType(t *testing.T) {
	tests := []struct {
		name     string
		mimeType string
		want     bool
	}{
		{name: "png image", mimeType: "image/png", want: true},
		{name: "jpeg image", mimeType: "image/jpeg", want: true},
		{name: "gif image", mimeType: "image/gif", want: true},
		{name: "webp image", mimeType: "image/webp", want: true},
		{name: "pdf document", mimeType: "application/pdf", want: true},
		{name: "plain text", mimeType: "text/plain; charset=utf-8", want: true},
		{name: "json", mimeType: "application/json", want: true},
		{name: "shell script", mimeType: "text/x-shellscript; charset=utf-8", want: true},
		{name: "html markup blocked", mimeType: "text/html; charset=utf-8", want: false},
		{name: "xhtml markup blocked", mimeType: "text/xhtml+xml", want: false},
		{name: "unsupported exe", mimeType: "application/x-msdownload", want: false},
		{name: "octet stream", mimeType: "application/octet-stream", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSupportedAttachmentMimeType(tt.mimeType); got != tt.want {
				t.Fatalf("isSupportedAttachmentMimeType(%q) = %v, want %v", tt.mimeType, got, tt.want)
			}
		})
	}
}

func TestUploadRejectsUnsupportedContentType(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "attachment-upload-unsupported")
	userID := insertTestUser(t, db, orgID, "attachment-upload-unsupported-user")
	token := "oc_sess_attachment_upload_unsupported"
	insertTestSession(t, db, orgID, userID, token, time.Now().UTC().Add(1*time.Hour))

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("org_id", orgID); err != nil {
		t.Fatalf("WriteField() error = %v", err)
	}
	part, err := writer.CreateFormFile("file", "payload.exe")
	if err != nil {
		t.Fatalf("CreateFormFile() error = %v", err)
	}
	if _, err := part.Write([]byte("MZ fake executable payload")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/messages/attachments", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	handler := &AttachmentsHandler{}
	handler.Upload(rr, req)

	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("Upload() status = %v, want %v (body=%s)", rr.Code, http.StatusUnsupportedMediaType, rr.Body.String())
	}
	if !strings.Contains(strings.ToLower(rr.Body.String()), "unsupported") {
		t.Fatalf("Upload() body should mention unsupported type, got %v", rr.Body.String())
	}
}

func TestUploadRejectsOversizedFile(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("org_id", "550e8400-e29b-41d4-a716-446655440000"); err != nil {
		t.Fatalf("WriteField() error = %v", err)
	}
	part, err := writer.CreateFormFile("file", "too-large.txt")
	if err != nil {
		t.Fatalf("CreateFormFile() error = %v", err)
	}
	oversizePayload := bytes.Repeat([]byte("a"), maxUploadSize+1024)
	if _, err := part.Write(oversizePayload); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/messages/attachments", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rr := httptest.NewRecorder()
	handler := &AttachmentsHandler{}
	handler.Upload(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"Upload() status = %v, want %v (payload=%s, body=%s)",
			rr.Code,
			http.StatusRequestEntityTooLarge,
			fmt.Sprintf("%d bytes", len(oversizePayload)),
			rr.Body.String(),
		)
	}
}

func TestUploadReturnsAuthenticatedAttachmentURL(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "attachment-upload-url")
	userID := insertTestUser(t, db, orgID, "attachment-upload-url-user")
	token := "oc_sess_attachment_upload_url"
	insertTestSession(t, db, orgID, userID, token, time.Now().UTC().Add(1*time.Hour))

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("org_id", orgID); err != nil {
		t.Fatalf("WriteField() error = %v", err)
	}
	part, err := writer.CreateFormFile("file", "readme.txt")
	if err != nil {
		t.Fatalf("CreateFormFile() error = %v", err)
	}
	if _, err := part.Write([]byte("readme contents")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/messages/attachments", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler := &AttachmentsHandler{}
	handler.Upload(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Upload() status = %v, want %v (body=%s)", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp UploadResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	wantPrefix := "/api/attachments/"
	if !strings.HasPrefix(resp.Attachment.URL, wantPrefix) {
		t.Fatalf("Upload() attachment URL = %q, want prefix %q", resp.Attachment.URL, wantPrefix)
	}
}

func TestGetAttachmentRejectsMissingAuth(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "attachment-get-auth-required")
	attachmentID, _ := insertStoredAttachmentFixture(t, db, orgID, "private.txt", []byte("private body"), "text/plain")

	req := httptest.NewRequest(http.MethodGet, "/api/attachments/"+attachmentID, nil)
	rr := httptest.NewRecorder()
	handler := &AttachmentsHandler{}
	handler.GetAttachment(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("GetAttachment() status = %v, want %v (body=%s)", rr.Code, http.StatusUnauthorized, rr.Body.String())
	}
}

func TestGetAttachmentStreamsFileWithSyncToken(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "attachment-get-sync-token")
	attachmentID, content := insertStoredAttachmentFixture(t, db, orgID, "doc.txt", []byte("sync token body"), "text/plain")

	req := httptest.NewRequest(http.MethodGet, "/api/attachments/"+attachmentID, nil)
	req.Header.Set("X-OpenClaw-Token", "sync-secret")
	rr := httptest.NewRecorder()
	handler := &AttachmentsHandler{}
	handler.GetAttachment(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GetAttachment() status = %v, want %v (body=%s)", rr.Code, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != string(content) {
		t.Fatalf("GetAttachment() body = %q, want %q", got, string(content))
	}
}

func insertStoredAttachmentFixture(
	t *testing.T,
	db *sql.DB,
	orgID string,
	filename string,
	content []byte,
	mimeType string,
) (string, []byte) {
	t.Helper()

	storageKey, err := generateStorageKey(filename)
	if err != nil {
		t.Fatalf("generateStorageKey() error = %v", err)
	}
	uploadDir := filepath.Join(getUploadsStorageDir(), orgID)
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	filePath := filepath.Join(uploadDir, storageKey)
	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(filePath)
	})

	url := "/api/attachments/pending"
	var attachmentID string
	if err := db.QueryRow(
		`INSERT INTO attachments (org_id, filename, size_bytes, mime_type, storage_key, url)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id`,
		orgID,
		filename,
		len(content),
		mimeType,
		storageKey,
		url,
	).Scan(&attachmentID); err != nil {
		t.Fatalf("insert attachment error = %v", err)
	}

	return attachmentID, content
}
