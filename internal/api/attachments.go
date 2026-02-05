package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxUploadSize  = 10 << 20 // 10 MB
	uploadsDir     = "uploads"
	defaultBaseURL = "/uploads"
)

// Attachment represents a file attachment on a message.
type Attachment struct {
	ID           string    `json:"id"`
	OrgID        string    `json:"org_id"`
	CommentID    *string   `json:"comment_id,omitempty"`
	Filename     string    `json:"filename"`
	SizeBytes    int64     `json:"size_bytes"`
	MimeType     string    `json:"mime_type"`
	StorageKey   string    `json:"storage_key"`
	URL          string    `json:"url"`
	ThumbnailURL *string   `json:"thumbnail_url,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// AttachmentMetadata is the minimal attachment info stored in comments.attachments JSONB.
type AttachmentMetadata struct {
	ID           string  `json:"id"`
	Filename     string  `json:"filename"`
	SizeBytes    int64   `json:"size_bytes"`
	MimeType     string  `json:"mime_type"`
	URL          string  `json:"url"`
	ThumbnailURL *string `json:"thumbnail_url,omitempty"`
}

// UploadResponse is returned after successful upload.
type UploadResponse struct {
	Attachment AttachmentMetadata `json:"attachment"`
}

// AttachmentsHandler handles attachment operations.
type AttachmentsHandler struct{}

// Upload handles POST /api/messages/attachments (multipart form upload).
func (h *AttachmentsHandler) Upload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	// Limit request body size
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize+1024) // extra for form overhead

	// Parse multipart form
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			sendJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "file too large (max 10MB)"})
			return
		}
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid multipart form"})
		return
	}

	// Get org_id from form
	orgID := strings.TrimSpace(r.FormValue("org_id"))
	if orgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing org_id"})
		return
	}
	if !uuidRegex.MatchString(orgID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid org_id"})
		return
	}

	// Get uploaded file
	file, header, err := r.FormFile("file")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing file"})
		return
	}
	defer file.Close()

	// Validate file size
	if header.Size > maxUploadSize {
		sendJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "file too large (max 10MB)"})
		return
	}

	db, err := getTasksDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	identity, err := requireSessionIdentity(r.Context(), db, r)
	if err != nil {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}
	if identity.OrgID != orgID {
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "org_id mismatch"})
		return
	}

	// Detect MIME type
	mimeType := detectMimeType(file, header.Filename)
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to process file"})
		return
	}

	// Generate storage key
	storageKey, err := generateStorageKey(header.Filename)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to generate storage key"})
		return
	}

	// Ensure uploads directory exists
	uploadPath := filepath.Join(uploadsDir, orgID)
	if err := os.MkdirAll(uploadPath, 0755); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create upload directory"})
		return
	}

	// Save file to disk
	destPath := filepath.Join(uploadPath, storageKey)
	destFile, err := os.Create(destPath)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to save file"})
		return
	}
	defer destFile.Close()

	written, err := io.Copy(destFile, file)
	if err != nil {
		os.Remove(destPath) // Clean up on failure
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to save file"})
		return
	}

	// Build URL
	baseURL := getUploadBaseURL()
	fileURL := fmt.Sprintf("%s/%s/%s", baseURL, orgID, storageKey)

	// Generate thumbnail URL for images
	var thumbnailURL *string
	if isImageMimeType(mimeType) {
		thumbURL := fileURL + "?thumb=1"
		thumbnailURL = &thumbURL
	}

	var attachmentID string
	err = db.QueryRowContext(r.Context(), `
		INSERT INTO attachments (org_id, filename, size_bytes, mime_type, storage_key, url, thumbnail_url)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`, orgID, header.Filename, written, mimeType, storageKey, fileURL, thumbnailURL).Scan(&attachmentID)
	if err != nil {
		os.Remove(destPath)
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to save attachment metadata"})
		return
	}

	sendJSON(w, http.StatusOK, UploadResponse{
		Attachment: AttachmentMetadata{
			ID:           attachmentID,
			Filename:     header.Filename,
			SizeBytes:    written,
			MimeType:     mimeType,
			URL:          fileURL,
			ThumbnailURL: thumbnailURL,
		},
	})
}

// GetAttachment handles GET /api/attachments/{id} - returns signed URL.
func (h *AttachmentsHandler) GetAttachment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	// Extract ID from path (simple approach)
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/attachments/"), "/")
	if len(pathParts) < 1 || pathParts[0] == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing attachment id"})
		return
	}
	attachmentID := pathParts[0]
	if !uuidRegex.MatchString(attachmentID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid attachment id"})
		return
	}

	db, err := getTasksDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	identity, err := requireSessionIdentity(r.Context(), db, r)
	if err != nil {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}

	var attachment Attachment
	var thumbnailURL sql.NullString
	var commentID sql.NullString
	err = db.QueryRowContext(r.Context(), `
		SELECT id, org_id, comment_id, filename, size_bytes, mime_type, storage_key, url, thumbnail_url, created_at
		FROM attachments
		WHERE id = $1 AND org_id = $2
	`, attachmentID, identity.OrgID).Scan(
		&attachment.ID,
		&attachment.OrgID,
		&commentID,
		&attachment.Filename,
		&attachment.SizeBytes,
		&attachment.MimeType,
		&attachment.StorageKey,
		&attachment.URL,
		&thumbnailURL,
		&attachment.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "attachment not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load attachment"})
		return
	}

	if commentID.Valid {
		attachment.CommentID = &commentID.String
	}
	if thumbnailURL.Valid {
		attachment.ThumbnailURL = &thumbnailURL.String
	}

	sendJSON(w, http.StatusOK, attachment)
}

// detectMimeType detects the MIME type of a file.
func detectMimeType(file multipart.File, filename string) string {
	// Read first 512 bytes for detection
	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	if n > 0 {
		mimeType := http.DetectContentType(buf[:n])
		// Refine for common file extensions
		if mimeType == "application/octet-stream" {
			ext := strings.ToLower(filepath.Ext(filename))
			switch ext {
			case ".pdf":
				return "application/pdf"
			case ".doc":
				return "application/msword"
			case ".docx":
				return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
			case ".xls":
				return "application/vnd.ms-excel"
			case ".xlsx":
				return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
			case ".zip":
				return "application/zip"
			case ".json":
				return "application/json"
			case ".md":
				return "text/markdown"
			}
		}
		return mimeType
	}
	return "application/octet-stream"
}

// generateStorageKey creates a unique storage key for a file.
func generateStorageKey(filename string) (string, error) {
	randomBytes := make([]byte, 16)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	ext := filepath.Ext(filename)
	return hex.EncodeToString(randomBytes) + ext, nil
}

// getUploadBaseURL returns the base URL for uploaded files.
func getUploadBaseURL() string {
	if baseURL := os.Getenv("UPLOAD_BASE_URL"); baseURL != "" {
		return strings.TrimSuffix(baseURL, "/")
	}
	return defaultBaseURL
}

// isImageMimeType checks if a MIME type is an image.
func isImageMimeType(mimeType string) bool {
	return strings.HasPrefix(mimeType, "image/")
}

// UpdateCommentAttachments updates the attachments JSONB array on a comment.
func UpdateCommentAttachments(db *sql.DB, commentID string, attachments []AttachmentMetadata) error {
	attachmentsJSON, err := json.Marshal(attachments)
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		UPDATE comments SET attachments = $1 WHERE id = $2
	`, attachmentsJSON, commentID)
	return err
}

// LinkAttachmentToComment links an attachment to a comment and updates the JSONB array.
func LinkAttachmentToComment(db *sql.DB, attachmentID, commentID string) error {
	// Update attachment with comment_id
	_, err := db.Exec(`
		UPDATE attachments SET comment_id = $1 WHERE id = $2
	`, commentID, attachmentID)
	if err != nil {
		return err
	}

	// Get all attachments for this comment and update JSONB
	rows, err := db.Query(`
		SELECT id, filename, size_bytes, mime_type, url, thumbnail_url
		FROM attachments
		WHERE comment_id = $1
	`, commentID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var attachments []AttachmentMetadata
	for rows.Next() {
		var a AttachmentMetadata
		var thumbnailURL sql.NullString
		if err := rows.Scan(&a.ID, &a.Filename, &a.SizeBytes, &a.MimeType, &a.URL, &thumbnailURL); err != nil {
			return err
		}
		if thumbnailURL.Valid {
			a.ThumbnailURL = &thumbnailURL.String
		}
		attachments = append(attachments, a)
	}

	return UpdateCommentAttachments(db, commentID, attachments)
}
