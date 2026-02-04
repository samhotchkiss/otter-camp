package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const testDBURLKey = "OTTER_TEST_DATABASE_URL"

func setupMessageTestDB(t *testing.T) *sql.DB {
	t.Helper()
	connStr := os.Getenv(testDBURLKey)
	if connStr == "" {
		t.Skipf("set %s to a dedicated test database", testDBURLKey)
	}

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)

	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS pgcrypto")
	require.NoError(t, err)

	migrationsDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	require.NoError(t, err)

	m, err := migrate.New("file://"+migrationsDir, connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = m.Close()
		_ = db.Close()
	})

	err = m.Down()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	require.NoError(t, os.Setenv("DATABASE_URL", connStr))
	tasksDBOnce = sync.Once{}
	tasksDBErr = nil
	if tasksDB != nil {
		_ = tasksDB.Close()
		tasksDB = nil
	}

	return db
}

func insertMessageTestOrganization(t *testing.T, db *sql.DB, slug string) string {
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

func insertMessageTestAgent(t *testing.T, db *sql.DB, orgID, slug string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		"INSERT INTO agents (org_id, slug, display_name, status) VALUES ($1, $2, $3, 'active') RETURNING id",
		orgID,
		slug,
		"Agent "+slug,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func insertMessageTestTask(t *testing.T, db *sql.DB, orgID, title string) string {
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

func insertMessageTestAttachment(t *testing.T, db *sql.DB, orgID, filename string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		`INSERT INTO attachments (org_id, filename, size_bytes, mime_type, storage_key, url)
		 VALUES ($1, $2, 12, 'text/plain', $3, $4)
		 RETURNING id`,
		orgID,
		filename,
		filename+"-key",
		"/uploads/"+filename,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func addRouteParam(req *http.Request, key, value string) *http.Request {
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add(key, value)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx)
	return req.WithContext(ctx)
}

func TestMessageCRUDTaskThread(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "messages-org")
	agentID := insertMessageTestAgent(t, db, orgID, "agent-1")
	taskID := insertMessageTestTask(t, db, orgID, "Message Task")
	attachmentID := insertMessageTestAttachment(t, db, orgID, "note.txt")

	payload := map[string]interface{}{
		"task_id":   taskID,
		"author_id": agentID,
		"content":   "Hello from task",
		"attachments": []map[string]interface{}{
			{
				"id":         attachmentID,
				"filename":   "note.txt",
				"size_bytes": 12,
				"mime_type":  "text/plain",
				"url":        "/uploads/note.txt",
			},
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	handler := &MessageHandler{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewReader(body))
	handler.CreateMessage(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var createResp struct {
		Message Message `json:"message"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &createResp))
	require.NotEmpty(t, createResp.Message.ID)
	require.NotNil(t, createResp.Message.TaskID)
	require.Equal(t, taskID, *createResp.Message.TaskID)
	require.Equal(t, "Hello from task", createResp.Message.Content)

	var linkedCommentID string
	err = db.QueryRow(`SELECT comment_id FROM attachments WHERE id = $1`, attachmentID).Scan(&linkedCommentID)
	require.NoError(t, err)
	require.Equal(t, createResp.Message.ID, linkedCommentID)

	getReq := httptest.NewRequest(http.MethodGet, "/api/messages/"+createResp.Message.ID, nil)
	getReq = addRouteParam(getReq, "id", createResp.Message.ID)
	getRR := httptest.NewRecorder()
	handler.GetMessage(getRR, getReq)
	require.Equal(t, http.StatusOK, getRR.Code)

	updatePayload := map[string]interface{}{
		"content": "Updated content",
	}
	updateBody, err := json.Marshal(updatePayload)
	require.NoError(t, err)
	updateReq := httptest.NewRequest(http.MethodPut, "/api/messages/"+createResp.Message.ID, bytes.NewReader(updateBody))
	updateReq = addRouteParam(updateReq, "id", createResp.Message.ID)
	updateRR := httptest.NewRecorder()
	handler.UpdateMessage(updateRR, updateReq)
	require.Equal(t, http.StatusOK, updateRR.Code)

	var updateResp struct {
		Message Message `json:"message"`
	}
	require.NoError(t, json.Unmarshal(updateRR.Body.Bytes(), &updateResp))
	require.Equal(t, "Updated content", updateResp.Message.Content)

	listReq := httptest.NewRequest(http.MethodGet, "/api/messages?taskId="+taskID, nil)
	listRR := httptest.NewRecorder()
	handler.ListMessages(listRR, listReq)
	require.Equal(t, http.StatusOK, listRR.Code)
	var listResp messageListResponse
	require.NoError(t, json.Unmarshal(listRR.Body.Bytes(), &listResp))
	require.Len(t, listResp.Messages, 1)

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/messages/"+createResp.Message.ID, nil)
	deleteReq = addRouteParam(deleteReq, "id", createResp.Message.ID)
	deleteRR := httptest.NewRecorder()
	handler.DeleteMessage(deleteRR, deleteReq)
	require.Equal(t, http.StatusOK, deleteRR.Code)

	getReq = httptest.NewRequest(http.MethodGet, "/api/messages/"+createResp.Message.ID, nil)
	getReq = addRouteParam(getReq, "id", createResp.Message.ID)
	getRR = httptest.NewRecorder()
	handler.GetMessage(getRR, getReq)
	require.Equal(t, http.StatusNotFound, getRR.Code)
}

func TestMessageListDMThreadPagination(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "dm-org")

	threadID := "dm_agent_1"
	now := time.Now().UTC()
	msgs := []struct {
		content string
		created time.Time
	}{
		{"first", now.Add(-3 * time.Hour)},
		{"second", now.Add(-2 * time.Hour)},
		{"third", now.Add(-1 * time.Hour)},
	}

	for _, msg := range msgs {
		_, err := db.Exec(
			`INSERT INTO comments (org_id, thread_id, content, sender_name, sender_type, created_at, updated_at)
			 VALUES ($1, $2, $3, 'User', 'user', $4, $4)`,
			orgID,
			threadID,
			msg.content,
			msg.created,
		)
		require.NoError(t, err)
	}

	handler := &MessageHandler{}
	listReq := httptest.NewRequest(http.MethodGet, "/api/messages?thread_id="+threadID+"&limit=2", nil)
	listRR := httptest.NewRecorder()
	handler.ListMessages(listRR, listReq)
	require.Equal(t, http.StatusOK, listRR.Code)

	var listResp messageListResponse
	require.NoError(t, json.Unmarshal(listRR.Body.Bytes(), &listResp))
	require.Len(t, listResp.Messages, 2)
	require.True(t, listResp.HasMore)
	require.NotEmpty(t, listResp.NextCursor)
	require.Equal(t, "second", listResp.Messages[0].Content)
	require.Equal(t, "third", listResp.Messages[1].Content)

	olderReq := httptest.NewRequest(http.MethodGet, "/api/messages?thread_id="+threadID+"&limit=2&cursor="+listResp.NextCursor, nil)
	olderRR := httptest.NewRecorder()
	handler.ListMessages(olderRR, olderReq)
	require.Equal(t, http.StatusOK, olderRR.Code)

	var olderResp messageListResponse
	require.NoError(t, json.Unmarshal(olderRR.Body.Bytes(), &olderResp))
	require.Len(t, olderResp.Messages, 1)
	require.False(t, olderResp.HasMore)
	require.Equal(t, "first", olderResp.Messages[0].Content)

	threadReq := httptest.NewRequest(http.MethodGet, "/api/threads/"+threadID+"/messages?limit=2", nil)
	threadReq = addRouteParam(threadReq, "id", threadID)
	threadRR := httptest.NewRecorder()
	handler.ListThreadMessages(threadRR, threadReq)
	require.Equal(t, http.StatusOK, threadRR.Code)
}
