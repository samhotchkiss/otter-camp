package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const messageTestDatabaseURLKey = "OTTER_TEST_DATABASE_URL"

func messageTestDatabaseURL(t *testing.T) string {
	t.Helper()
	connStr := os.Getenv(messageTestDatabaseURLKey)
	if connStr == "" {
		t.Skipf("set %s to a dedicated test database", messageTestDatabaseURLKey)
	}
	return connStr
}

func messageMigrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	require.NoError(t, err)
	return dir
}

func resetMessageDatabase(t *testing.T, connStr string) {
	t.Helper()
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer func() {
		_ = db.Close()
	}()

	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS pgcrypto")
	require.NoError(t, err)

	m, err := migrate.New("file://"+messageMigrationsDir(t), connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = m.Close()
	})

	err = m.Down()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}
}

func insertMessageOrganization(t *testing.T, db *sql.DB, slug string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		"INSERT INTO organizations (name, slug, tier) VALUES ($1, $2, 'free') RETURNING id",
		"Org "+slug,
		slug,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func insertMessageTask(t *testing.T, db *sql.DB, orgID, title string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		"INSERT INTO tasks (org_id, title, status, priority) VALUES ($1, $2, 'queued', 'P2') RETURNING id",
		orgID,
		title,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestListMessagesRequiresOrgID(t *testing.T) {
	handler := &MessageHandler{}
	req := httptest.NewRequest(http.MethodGet, "/api/messages", nil)
	rec := httptest.NewRecorder()

	handler.ListMessages(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "missing query parameter: org_id", resp.Error)
}

func TestListMessagesRequiresTaskOrThreadID(t *testing.T) {
	handler := &MessageHandler{}
	req := httptest.NewRequest(http.MethodGet, "/api/messages?org_id=00000000-0000-0000-0000-000000000000", nil)
	rec := httptest.NewRecorder()

	handler.ListMessages(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "missing query parameter: task_id or thread_id", resp.Error)
}

func TestListMessagesRejectsBothTaskAndThread(t *testing.T) {
	handler := &MessageHandler{}
	req := httptest.NewRequest(http.MethodGet, "/api/messages?org_id=00000000-0000-0000-0000-000000000000&task_id=00000000-0000-0000-0000-000000000001&thread_id=00000000-0000-0000-0000-000000000002", nil)
	rec := httptest.NewRecorder()

	handler.ListMessages(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "provide either task_id or thread_id, not both", resp.Error)
}

func TestCreateMessageRequiresContent(t *testing.T) {
	handler := &MessageHandler{}
	body := `{"org_id":"00000000-0000-0000-0000-000000000000","task_id":"00000000-0000-0000-0000-000000000001"}`
	req := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.CreateMessage(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "missing content", resp.Error)
}

func TestCreateMessageRequiresTaskOrThread(t *testing.T) {
	handler := &MessageHandler{}
	body := `{"org_id":"00000000-0000-0000-0000-000000000000","content":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.CreateMessage(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "missing task_id or thread_id", resp.Error)
}

func TestCreateMessageInvalidRole(t *testing.T) {
	handler := &MessageHandler{}
	body := `{"org_id":"00000000-0000-0000-0000-000000000000","task_id":"00000000-0000-0000-0000-000000000001","content":"hello","role":"invalid"}`
	req := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.CreateMessage(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "invalid role", resp.Error)
}

func TestUpdateMessageInvalidID(t *testing.T) {
	handler := &MessageHandler{}
	body := `{"content":"updated"}`

	r := chi.NewRouter()
	r.Patch("/api/messages/{id}", handler.UpdateMessage)

	req := httptest.NewRequest(http.MethodPatch, "/api/messages/not-a-uuid", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "invalid message id", resp.Error)
}

func TestDeleteMessageInvalidID(t *testing.T) {
	handler := &MessageHandler{}

	r := chi.NewRouter()
	r.Delete("/api/messages/{id}", handler.DeleteMessage)

	req := httptest.NewRequest(http.MethodDelete, "/api/messages/not-a-uuid", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "invalid message id", resp.Error)
}

func TestMessageCRUDIntegration(t *testing.T) {
	connStr := messageTestDatabaseURL(t)
	resetMessageDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})

	orgID := insertMessageOrganization(t, db, "msg-org")
	taskID := insertMessageTask(t, db, orgID, "Test Task")

	handler := &MessageHandler{}

	r := chi.NewRouter()
	r.Get("/api/messages", handler.ListMessages)
	r.Post("/api/messages", handler.CreateMessage)
	r.Patch("/api/messages/{id}", handler.UpdateMessage)
	r.Delete("/api/messages/{id}", handler.DeleteMessage)

	// Create a message
	createBody := map[string]interface{}{
		"org_id":  orgID,
		"task_id": taskID,
		"content": "Hello, world!",
		"role":    "user",
	}
	createJSON, _ := json.Marshal(createBody)
	createReq := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewBuffer(createJSON))
	createRec := httptest.NewRecorder()

	r.ServeHTTP(createRec, createReq)

	require.Equal(t, http.StatusOK, createRec.Code)
	var created Message
	require.NoError(t, json.NewDecoder(createRec.Body).Decode(&created))
	require.Equal(t, "Hello, world!", created.Content)
	require.Equal(t, "user", created.Role)
	require.NotEmpty(t, created.ID)

	// List messages by task_id
	listReq := httptest.NewRequest(http.MethodGet, "/api/messages?org_id="+orgID+"&task_id="+taskID, nil)
	listRec := httptest.NewRecorder()

	r.ServeHTTP(listRec, listReq)

	require.Equal(t, http.StatusOK, listRec.Code)
	var listResp MessagesResponse
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listResp))
	require.Len(t, listResp.Messages, 1)
	require.Equal(t, "Hello, world!", listResp.Messages[0].Content)

	// Update the message
	updateBody := map[string]interface{}{
		"content": "Updated content",
	}
	updateJSON, _ := json.Marshal(updateBody)
	updateReq := httptest.NewRequest(http.MethodPatch, "/api/messages/"+created.ID, bytes.NewBuffer(updateJSON))
	updateRec := httptest.NewRecorder()

	r.ServeHTTP(updateRec, updateReq)

	require.Equal(t, http.StatusOK, updateRec.Code)
	var updated Message
	require.NoError(t, json.NewDecoder(updateRec.Body).Decode(&updated))
	require.Equal(t, "Updated content", updated.Content)

	// Delete the message
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/messages/"+created.ID, nil)
	deleteRec := httptest.NewRecorder()

	r.ServeHTTP(deleteRec, deleteReq)

	require.Equal(t, http.StatusOK, deleteRec.Code)
	var deleteResp map[string]string
	require.NoError(t, json.NewDecoder(deleteRec.Body).Decode(&deleteResp))
	require.Equal(t, "deleted", deleteResp["status"])

	// Verify deletion
	listReq2 := httptest.NewRequest(http.MethodGet, "/api/messages?org_id="+orgID+"&task_id="+taskID, nil)
	listRec2 := httptest.NewRecorder()

	r.ServeHTTP(listRec2, listReq2)

	require.Equal(t, http.StatusOK, listRec2.Code)
	var listResp2 MessagesResponse
	require.NoError(t, json.NewDecoder(listRec2.Body).Decode(&listResp2))
	require.Len(t, listResp2.Messages, 0)
}

func TestMessageWithThreadID(t *testing.T) {
	connStr := messageTestDatabaseURL(t)
	resetMessageDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})

	orgID := insertMessageOrganization(t, db, "thread-org")
	threadID := "00000000-0000-0000-0000-000000000099"

	handler := &MessageHandler{}

	r := chi.NewRouter()
	r.Get("/api/messages", handler.ListMessages)
	r.Post("/api/messages", handler.CreateMessage)

	// Create a message with thread_id
	createBody := map[string]interface{}{
		"org_id":    orgID,
		"thread_id": threadID,
		"content":   "Thread message",
		"role":      "assistant",
	}
	createJSON, _ := json.Marshal(createBody)
	createReq := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewBuffer(createJSON))
	createRec := httptest.NewRecorder()

	r.ServeHTTP(createRec, createReq)

	require.Equal(t, http.StatusOK, createRec.Code)
	var created Message
	require.NoError(t, json.NewDecoder(createRec.Body).Decode(&created))
	require.Equal(t, "Thread message", created.Content)
	require.Equal(t, "assistant", created.Role)
	require.NotNil(t, created.ThreadID)
	require.Equal(t, threadID, *created.ThreadID)

	// List messages by thread_id
	listReq := httptest.NewRequest(http.MethodGet, "/api/messages?org_id="+orgID+"&thread_id="+threadID, nil)
	listRec := httptest.NewRecorder()

	r.ServeHTTP(listRec, listReq)

	require.Equal(t, http.StatusOK, listRec.Code)
	var listResp MessagesResponse
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listResp))
	require.Len(t, listResp.Messages, 1)
	require.Equal(t, threadID, *listResp.ThreadID)
}
