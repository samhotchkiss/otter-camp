package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
