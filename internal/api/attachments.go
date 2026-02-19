package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxUploadSize  = 20 << 20 // 20 MB
	uploadsDir     = "uploads"
)

// Attachment represents a file attachment on a message.
type Attachment struct {
	ID            string    `json:"id"`
	OrgID         string    `json:"org_id"`
	CommentID     *string   `json:"comment_id,omitempty"`
	ChatMessageID *string   `json:"chat_message_id,omitempty"`
	Filename      string    `json:"filename"`
	SizeBytes     int64     `json:"size_bytes"`
	MimeType      string    `json:"mime_type"`
	StorageKey    string    `json:"storage_key"`
	URL           string    `json:"url"`
	ThumbnailURL  *string   `json:"thumbnail_url,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
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

var ErrAttachmentNotFound = errors.New("attachment not found")

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
			sendJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "file too large (max 20MB)"})
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
		sendJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "file too large (max 20MB)"})
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
	if !isSupportedAttachmentMimeType(mimeType) {
		sendJSON(w, http.StatusUnsupportedMediaType, errorResponse{Error: "unsupported attachment type"})
		return
	}
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
	uploadPath := filepath.Join(getUploadsStorageDir(), orgID)
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

	placeholderURL := "/api/attachments/pending"

	var attachmentID string
	err = db.QueryRowContext(r.Context(), `
		INSERT INTO attachments (org_id, filename, size_bytes, mime_type, storage_key, url, thumbnail_url)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`, orgID, header.Filename, written, mimeType, storageKey, placeholderURL, nil).Scan(&attachmentID)
	if err != nil {
		os.Remove(destPath)
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to save attachment metadata"})
		return
	}

	attachmentURL := fmt.Sprintf("/api/attachments/%s", attachmentID)
	var thumbnailURL *string
	if isImageMimeType(mimeType) {
		thumbnailURL = &attachmentURL
	}
	if _, err := db.ExecContext(
		r.Context(),
		`UPDATE attachments SET url = $1, thumbnail_url = $2 WHERE id = $3`,
		attachmentURL,
		thumbnailURL,
		attachmentID,
	); err != nil {
		os.Remove(destPath)
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to finalize attachment metadata"})
		return
	}

	sendJSON(w, http.StatusOK, UploadResponse{
		Attachment: AttachmentMetadata{
			ID:           attachmentID,
			Filename:     header.Filename,
			SizeBytes:    written,
			MimeType:     mimeType,
			URL:          attachmentURL,
			ThumbnailURL: thumbnailURL,
		},
	})
}

// GetAttachment handles GET /api/attachments/{id} - returns attachment binary when authenticated.
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

	var identityOrgID string
	authenticatedViaSyncToken := false
	identity, err := requireSessionIdentity(r.Context(), db, r)
	if err == nil {
		identityOrgID = identity.OrgID
	} else {
		if _, syncErr := requireOpenClawSyncAuth(r.Context(), db, r); syncErr != nil {
			sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid authentication"})
			return
		}
		authenticatedViaSyncToken = true
	}

	var attachment Attachment
	var thumbnailURL sql.NullString
	var commentID sql.NullString
	var chatMessageID sql.NullString
	if authenticatedViaSyncToken {
		// Sync-token requests come only from the trusted OpenClaw bridge runtime.
		// We intentionally allow attachment lookup by opaque attachment ID without
		// org filtering so dispatch URLs do not need org-specific parameters.
		err = db.QueryRowContext(r.Context(), `
			SELECT id, org_id, comment_id, chat_message_id, filename, size_bytes, mime_type, storage_key, url, thumbnail_url, created_at
			FROM attachments
			WHERE id = $1
		`, attachmentID).Scan(
			&attachment.ID,
			&attachment.OrgID,
			&commentID,
			&chatMessageID,
			&attachment.Filename,
			&attachment.SizeBytes,
			&attachment.MimeType,
			&attachment.StorageKey,
			&attachment.URL,
			&thumbnailURL,
			&attachment.CreatedAt,
		)
	} else {
		err = db.QueryRowContext(r.Context(), `
			SELECT id, org_id, comment_id, chat_message_id, filename, size_bytes, mime_type, storage_key, url, thumbnail_url, created_at
			FROM attachments
			WHERE id = $1 AND org_id = $2
		`, attachmentID, identityOrgID).Scan(
			&attachment.ID,
			&attachment.OrgID,
			&commentID,
			&chatMessageID,
			&attachment.Filename,
			&attachment.SizeBytes,
			&attachment.MimeType,
			&attachment.StorageKey,
			&attachment.URL,
			&thumbnailURL,
			&attachment.CreatedAt,
		)
	}
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
	if chatMessageID.Valid {
		attachment.ChatMessageID = &chatMessageID.String
	}
	if thumbnailURL.Valid {
		attachment.ThumbnailURL = &thumbnailURL.String
	}

	cleanStorageKey := filepath.Clean(strings.TrimSpace(attachment.StorageKey))
	if cleanStorageKey == "." || strings.Contains(cleanStorageKey, "..") || strings.ContainsAny(cleanStorageKey, `/\`) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid attachment storage key"})
		return
	}
	filePath := filepath.Join(getUploadsStorageDir(), attachment.OrgID, cleanStorageKey)
	stat, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "attachment file not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read attachment file"})
		return
	}
	file, err := os.Open(filePath)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read attachment file"})
		return
	}
	defer file.Close()

	contentType := strings.TrimSpace(attachment.MimeType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	dispositionType := "attachment"
	if isImageMimeType(contentType) {
		dispositionType = "inline"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename=%q`, dispositionType, attachment.Filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, attachment.Filename, stat.ModTime(), file)
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

func getUploadsStorageDir() string {
	if value := strings.TrimSpace(os.Getenv("UPLOADS_DIR")); value != "" {
		return value
	}
	return uploadsDir
}

// isImageMimeType checks if a MIME type is an image.
func isImageMimeType(mimeType string) bool {
	return strings.HasPrefix(mimeType, "image/")
}

func isSupportedAttachmentMimeType(mimeType string) bool {
	normalized := strings.TrimSpace(strings.ToLower(mimeType))
	if normalized == "" {
		return false
	}
	if parsed, _, err := mime.ParseMediaType(normalized); err == nil {
		normalized = parsed
	}

	// Block markup types that can contain executable scripts.
	switch normalized {
	case "text/html", "text/xhtml+xml", "text/xml":
		return false
	}

	if strings.HasPrefix(normalized, "text/") {
		return true
	}

	switch normalized {
	case "image/png",
		"image/jpeg",
		"image/gif",
		"image/webp",
		"application/pdf",
		"application/json",
		"application/xml",
		"application/yaml",
		"application/x-yaml",
		"application/toml":
		return true
	default:
		return false
	}
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
		UPDATE attachments
		SET comment_id = $1, chat_message_id = NULL
		WHERE id = $2
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

// UpdateProjectChatMessageAttachments updates the attachments JSONB array on a project chat message.
func UpdateProjectChatMessageAttachments(db *sql.DB, chatMessageID string, attachments []AttachmentMetadata) error {
	attachmentsJSON, err := json.Marshal(attachments)
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		UPDATE project_chat_messages SET attachments = $1 WHERE id = $2
	`, attachmentsJSON, chatMessageID)
	return err
}

// LinkAttachmentToChatMessage links an attachment to a project chat message and updates the JSONB array.
func LinkAttachmentToChatMessage(db *sql.DB, orgID, attachmentID, chatMessageID string) error {
	result, err := db.Exec(`
		UPDATE attachments
		SET chat_message_id = $1, comment_id = NULL
		WHERE id = $2 AND org_id = $3
	`, chatMessageID, attachmentID, orgID)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrAttachmentNotFound
	}

	rows, err := db.Query(`
		SELECT id, filename, size_bytes, mime_type, url, thumbnail_url
		FROM attachments
		WHERE chat_message_id = $1 AND org_id = $2
		ORDER BY created_at ASC, id ASC
	`, chatMessageID, orgID)
	if err != nil {
		return err
	}
	defer rows.Close()

	attachments := make([]AttachmentMetadata, 0, 4)
	for rows.Next() {
		var attachment AttachmentMetadata
		var thumbnailURL sql.NullString
		if err := rows.Scan(
			&attachment.ID,
			&attachment.Filename,
			&attachment.SizeBytes,
			&attachment.MimeType,
			&attachment.URL,
			&thumbnailURL,
		); err != nil {
			return err
		}
		if thumbnailURL.Valid {
			attachment.ThumbnailURL = &thumbnailURL.String
		}
		attachments = append(attachments, attachment)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	return UpdateProjectChatMessageAttachments(db, chatMessageID, attachments)
}
