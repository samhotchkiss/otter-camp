package api

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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
