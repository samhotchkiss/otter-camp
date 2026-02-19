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
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
	"github.com/stretchr/testify/require"
)

const testDBURLKey = "OTTER_TEST_DATABASE_URL"

func setupMessageTestDB(t *testing.T) *sql.DB {
	t.Helper()
	connStr := os.Getenv(testDBURLKey)
	if connStr == "" {
		t.Skipf("set %s to a dedicated test database", testDBURLKey)
	}
	t.Setenv("GIT_REPO_ROOT", t.TempDir())

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

type fakeOpenClawDispatcher struct {
	connected bool
	err       error
	calls     []interface{}
}

func (f *fakeOpenClawDispatcher) SendToOpenClaw(event interface{}) error {
	f.calls = append(f.calls, event)
	if f.err == nil && !f.connected {
		return ws.ErrOpenClawNotConnected
	}
	return f.err
}

func (f *fakeOpenClawDispatcher) IsConnected() bool {
	return f.connected
}

func TestDMRoutingExemptAgentSlug(t *testing.T) {
	t.Parallel()

	require.True(t, isDMRoutingExemptAgentSlug("main"))
	require.True(t, isDMRoutingExemptAgentSlug("elephant"))
	require.True(t, isDMRoutingExemptAgentSlug("lori"))
	require.True(t, isDMRoutingExemptAgentSlug("MAIN"))
	require.False(t, isDMRoutingExemptAgentSlug("technonymous"))
	require.False(t, isDMRoutingExemptAgentSlug(""))
}

func TestDMFallbackSessionKeyForAgentSlug(t *testing.T) {
	t.Parallel()

	agentID := "28d27f83-5518-468a-83bf-750f7ec1c9f5"
	tests := []struct {
		name      string
		agentSlug string
		want      string
	}{
		{
			name:      "non exempt slug routes to chameleon",
			agentSlug: "technonymous",
			want:      canonicalChameleonSessionKey(agentID),
		},
		{
			name:      "main slug stays on own session",
			agentSlug: "main",
			want:      "agent:main:main",
		},
		{
			name:      "elephant slug stays on own session",
			agentSlug: "elephant",
			want:      "agent:elephant:main",
		},
		{
			name:      "lori slug stays on own session",
			agentSlug: "lori",
			want:      "agent:lori:main",
		},
			{
				name:      "no slug routes to chameleon",
				agentSlug: "",
				want:      canonicalChameleonSessionKey(agentID),
			},
		}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, dmFallbackSessionKeyForAgentSlug(tt.agentSlug, agentID))
		})
	}
}

func TestNormalizeDMDispatchSessionKeyForAgentSlug(t *testing.T) {
	t.Parallel()

	agentID := "28d27f83-5518-468a-83bf-750f7ec1c9f5"
	tests := []struct {
		name       string
		agentSlug  string
		sessionKey string
		want       string
	}{
		{
			name:       "non exempt chameleon key stays chameleon",
			agentSlug:  "technonymous",
			sessionKey: canonicalChameleonSessionKey(agentID),
			want:       canonicalChameleonSessionKey(agentID),
		},
		{
			name:       "non exempt stale main key migrates to chameleon",
			agentSlug:  "technonymous",
			sessionKey: "agent:technonymous:main",
			want:       canonicalChameleonSessionKey(agentID),
		},
		{
			name:       "exempt main slug rewrites chameleon to dedicated session",
			agentSlug:  "main",
			sessionKey: canonicalChameleonSessionKey(agentID),
			want:       "agent:main:main",
		},
		{
			name:       "exempt elephant slug rewrites chameleon to dedicated session",
			agentSlug:  "elephant",
			sessionKey: canonicalChameleonSessionKey(agentID),
			want:       "agent:elephant:main",
		},
		{
			name:       "exempt lori slug rewrites chameleon to dedicated session",
			agentSlug:  "lori",
			sessionKey: canonicalChameleonSessionKey(agentID),
			want:       "agent:lori:main",
		},
		{
			name:       "non exempt unrelated key is preserved",
			agentSlug:  "technonymous",
			sessionKey: "agent:other:main",
			want:       "agent:other:main",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, normalizeDMDispatchSessionKeyForAgentSlug(tt.agentSlug, agentID, tt.sessionKey))
		})
	}
}

func TestDMInjectionDecider(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	tests := []struct {
		name        string
		state       *dmInjectionState
		currentHash string
		want        bool
	}{
		{
			name:        "nil state injects",
			state:       nil,
			currentHash: "hash-a",
			want:        true,
		},
		{
			name: "never injected injects",
			state: &dmInjectionState{
				InjectionHash: "hash-a",
			},
			currentHash: "hash-a",
			want:        true,
		},
		{
			name: "compaction flagged injects",
			state: &dmInjectionState{
				InjectedAt:         sql.NullTime{Time: now, Valid: true},
				InjectionHash:      "hash-a",
				CompactionDetected: true,
			},
			currentHash: "hash-a",
			want:        true,
		},
		{
			name: "hash mismatch injects",
			state: &dmInjectionState{
				InjectedAt:    sql.NullTime{Time: now, Valid: true},
				InjectionHash: "hash-a",
			},
			currentHash: "hash-b",
			want:        true,
		},
		{
			name: "warmed current hash does not inject",
			state: &dmInjectionState{
				InjectedAt:    sql.NullTime{Time: now, Valid: true},
				InjectionHash: "hash-a",
			},
			currentHash: "hash-a",
			want:        false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, shouldInjectDMIdentity(tt.state, tt.currentHash))
		})
	}
}

func TestBuildDMDispatchEvent_InjectionState(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "dm-injection-state-org")
	agentID := insertMessageTestAgent(t, db, orgID, "dm-injection-agent")
	_, err := db.Exec(
		`UPDATE agents
		 SET soul_md = $3,
		     identity_md = $4,
		     instructions_md = $5
		 WHERE org_id = $1 AND id = $2`,
		orgID,
		agentID,
		"Soul v1",
		"Identity v1",
		"Instructions v1",
	)
	require.NoError(t, err)

	handler := &MessageHandler{}
	threadID := "dm_" + agentID
	req := createMessageRequest{
		ThreadID: &threadID,
		Content:  "First turn",
	}
	target := dmDispatchTarget{
		AgentID:    agentID,
		SessionKey: canonicalChameleonSessionKey(agentID),
	}

	event := handler.buildDMDispatchEvent(context.Background(), db, orgID, "msg-1", req, target)
	require.True(t, event.Data.InjectIdentity)

	event = handler.buildDMDispatchEvent(context.Background(), db, orgID, "msg-2", req, target)
	require.False(t, event.Data.InjectIdentity)

	_, err = db.Exec(
		`UPDATE agents
		 SET soul_md = $3
		 WHERE org_id = $1 AND id = $2`,
		orgID,
		agentID,
		"Soul v2",
	)
	require.NoError(t, err)

	event = handler.buildDMDispatchEvent(context.Background(), db, orgID, "msg-3", req, target)
	require.True(t, event.Data.InjectIdentity)

	_, err = db.Exec(
		`UPDATE dm_injection_state
		 SET compaction_detected = TRUE
		 WHERE org_id = $1 AND thread_id = $2`,
		orgID,
		threadID,
	)
	require.NoError(t, err)

	event = handler.buildDMDispatchEvent(context.Background(), db, orgID, "msg-4", req, target)
	require.True(t, event.Data.InjectIdentity)

	var compactionDetected bool
	err = db.QueryRow(
		`SELECT compaction_detected
		 FROM dm_injection_state
		 WHERE org_id = $1 AND thread_id = $2`,
		orgID,
		threadID,
	).Scan(&compactionDetected)
	require.NoError(t, err)
	require.False(t, compactionDetected)
}

func TestBuildDMDispatchEvent_IncrementalContext(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "dm-injection-incremental-org")
	agentID := insertMessageTestAgent(t, db, orgID, "dm-injection-incremental-agent")

	handler := &MessageHandler{}
	threadID := "dm_" + agentID
	incrementalContext := "Updated project status: release candidate approved."
	req := createMessageRequest{
		ThreadID:           &threadID,
		Content:            "Ack",
		IncrementalContext: &incrementalContext,
	}
	target := dmDispatchTarget{
		AgentID:    agentID,
		SessionKey: canonicalChameleonSessionKey(agentID),
	}

	event := handler.buildDMDispatchEvent(context.Background(), db, orgID, "msg-1", req, target)
	require.Equal(t, incrementalContext, event.Data.IncrementalContext)
}

func TestDMInjectionState_InvalidatesOnAgentIdentityUpdate(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "dm-injection-invalidate-org")
	agentID := insertMessageTestAgent(t, db, orgID, "dm-injection-invalidate-agent")
	threadID := "dm_" + agentID
	sessionKey := canonicalChameleonSessionKey(agentID)

	_, err := db.Exec(
		`UPDATE agents
		 SET soul_md = $3,
		     identity_md = $4,
		     instructions_md = $5
		 WHERE org_id = $1 AND id = $2`,
		orgID,
		agentID,
		"Soul v1",
		"Identity v1",
		"Instructions v1",
	)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO dm_injection_state (
			org_id,
			thread_id,
			session_key,
			agent_id,
			injected_at,
			injection_hash,
			compaction_detected
		) VALUES ($1, $2, $3, $4, NOW(), $5, FALSE)`,
		orgID,
		threadID,
		sessionKey,
		agentID,
		"hash-v1",
	)
	require.NoError(t, err)

	_, err = db.Exec(
		`UPDATE agents
		 SET soul_md = $3
		 WHERE org_id = $1 AND id = $2`,
		orgID,
		agentID,
		"Soul v2",
	)
	require.NoError(t, err)

	var injectionHash sql.NullString
	err = db.QueryRow(
		`SELECT injection_hash
		 FROM dm_injection_state
		 WHERE org_id = $1 AND thread_id = $2`,
		orgID,
		threadID,
	).Scan(&injectionHash)
	require.NoError(t, err)
	require.False(t, injectionHash.Valid)

	handler := &MessageHandler{}
	req := createMessageRequest{
		ThreadID: &threadID,
		Content:  "Need refreshed identity",
	}
	target := dmDispatchTarget{
		AgentID:    agentID,
		SessionKey: sessionKey,
	}

	event := handler.buildDMDispatchEvent(context.Background(), db, orgID, "msg-refresh", req, target)
	require.True(t, event.Data.InjectIdentity)
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

func TestCreateMessageDMBridgeOfflinePersistsMessageWithDeliveryWarning(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "dm-dispatch-offline-org")
	_, err := db.Exec(
		`INSERT INTO agent_sync_state (org_id, id, name, status, session_key, updated_at)
		 VALUES ($1, $2, $3, $4, $5, NOW())`,
		orgID,
		"itsalive",
		"Ivy",
		"online",
		"agent:itsalive:main",
	)
	require.NoError(t, err)

	payload := map[string]interface{}{
		"org_id":      orgID,
		"thread_id":   "dm_itsalive",
		"content":     "Hello Ivy",
		"sender_type": "user",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	handler := &MessageHandler{
		OpenClawDispatcher: &fakeOpenClawDispatcher{connected: false},
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewReader(body))
	handler.CreateMessage(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var createResp struct {
		Message  Message          `json:"message"`
		Delivery dmDeliveryStatus `json:"delivery"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &createResp))
	require.NotEmpty(t, createResp.Message.ID)
	require.True(t, createResp.Delivery.Attempted)
	require.False(t, createResp.Delivery.Delivered)
	require.Equal(t, openClawDispatchQueuedWarning, createResp.Delivery.Error)
	require.Len(t, handler.OpenClawDispatcher.(*fakeOpenClawDispatcher).calls, 1)

	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM comments WHERE thread_id = 'dm_itsalive'`).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	var queued int
	err = db.QueryRow(`SELECT COUNT(*) FROM openclaw_dispatch_queue WHERE event_type = 'dm.message'`).Scan(&queued)
	require.NoError(t, err)
	require.Equal(t, 1, queued)
}

func TestCreateMessageDMDispatchesToOpenClaw(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "dm-dispatch-success-org")

	_, err := db.Exec(
		`INSERT INTO agent_sync_state (org_id, id, name, status, session_key, updated_at)
		 VALUES ($1, $2, $3, $4, $5, NOW())`,
		orgID,
		"itsalive",
		"Ivy",
		"online",
		"agent:itsalive:main",
	)
	require.NoError(t, err)

	dispatcher := &fakeOpenClawDispatcher{connected: true}
	handler := &MessageHandler{OpenClawDispatcher: dispatcher}

	payload := map[string]interface{}{
		"org_id":      orgID,
		"thread_id":   "dm_itsalive",
		"content":     "Please create a file",
		"sender_id":   "sam-user",
		"sender_type": "user",
		"sender_name": "Sam",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewReader(body))
	handler.CreateMessage(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, dispatcher.calls, 1)

	event, ok := dispatcher.calls[0].(openClawDMDispatchEvent)
	require.True(t, ok)
	require.Equal(t, "dm.message", event.Type)
	require.Equal(t, orgID, event.OrgID)
	require.Equal(t, "dm_itsalive", event.Data.ThreadID)
	require.Equal(t, "itsalive", event.Data.AgentID)
	require.Equal(t, "agent:itsalive:main", event.Data.SessionKey)
	require.Equal(t, "Please create a file", event.Data.Content)
	require.Equal(t, "sam-user", event.Data.SenderID)
	require.Equal(t, "user", event.Data.SenderType)
	require.Equal(t, "Sam", event.Data.SenderName)

	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM comments WHERE thread_id = 'dm_itsalive'`).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestCreateMessageDMUsesChameleonFallbackWhenSyncStateMissingByUUID(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "dm-dispatch-fallback-uuid-org")
	agentID := "28d27f83-5518-468a-83bf-750f7ec1c9f5"
	_, err := db.Exec(
		`INSERT INTO agents (id, org_id, slug, display_name, status)
		 VALUES ($1, $2, $3, $4, 'active')`,
		agentID,
		orgID,
		"marcus",
		"Marcus",
	)
	require.NoError(t, err)

	dispatcher := &fakeOpenClawDispatcher{connected: true}
	handler := &MessageHandler{OpenClawDispatcher: dispatcher}

	payload := map[string]interface{}{
		"org_id":      orgID,
		"thread_id":   "dm_" + agentID,
		"content":     "Hey Marcus",
		"sender_type": "user",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewReader(body))
	handler.CreateMessage(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, dispatcher.calls, 1)

	event, ok := dispatcher.calls[0].(openClawDMDispatchEvent)
	require.True(t, ok)
	require.Equal(t, agentID, event.Data.AgentID)
	require.Equal(t, "agent:chameleon:oc:"+agentID, event.Data.SessionKey)
}

func TestCreateMessageDMUsesChameleonFallbackWhenSyncStateMissingBySlug(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "dm-dispatch-fallback-slug-org")
	agentID := "28d27f83-5518-468a-83bf-750f7ec1c9f5"
	_, err := db.Exec(
		`INSERT INTO agents (id, org_id, slug, display_name, status)
		 VALUES ($1, $2, $3, $4, 'active')`,
		agentID,
		orgID,
		"marcus",
		"Marcus",
	)
	require.NoError(t, err)

	dispatcher := &fakeOpenClawDispatcher{connected: true}
	handler := &MessageHandler{OpenClawDispatcher: dispatcher}

	payload := map[string]interface{}{
		"org_id":      orgID,
		"thread_id":   "dm_marcus",
		"content":     "Hey Marcus",
		"sender_type": "user",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewReader(body))
	handler.CreateMessage(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, dispatcher.calls, 1)

	event, ok := dispatcher.calls[0].(openClawDMDispatchEvent)
	require.True(t, ok)
	require.Equal(t, agentID, event.Data.AgentID)
	require.Equal(t, "agent:chameleon:oc:"+agentID, event.Data.SessionKey)
}

func TestCreateMessageDMUsesElephantMainFallbackWhenSyncStateMissing(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "dm-dispatch-fallback-elephant-org")
	agentID := insertMessageTestAgent(t, db, orgID, "elephant")

	dispatcher := &fakeOpenClawDispatcher{connected: true}
	handler := &MessageHandler{OpenClawDispatcher: dispatcher}

	payload := map[string]interface{}{
		"org_id":      orgID,
		"thread_id":   "dm_elephant",
		"content":     "Hey Elephant",
		"sender_type": "user",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewReader(body))
	handler.CreateMessage(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, dispatcher.calls, 1)

	event, ok := dispatcher.calls[0].(openClawDMDispatchEvent)
	require.True(t, ok)
	require.Equal(t, agentID, event.Data.AgentID)
	require.Equal(t, "agent:elephant:main", event.Data.SessionKey)
}

func TestCreateMessageDMUsesMainFallbackWhenSyncStateMissing(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "dm-dispatch-fallback-main-org")
	agentID := insertMessageTestAgent(t, db, orgID, "main")

	dispatcher := &fakeOpenClawDispatcher{connected: true}
	handler := &MessageHandler{OpenClawDispatcher: dispatcher}

	payload := map[string]interface{}{
		"org_id":      orgID,
		"thread_id":   "dm_main",
		"content":     "Hey Frank",
		"sender_type": "user",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewReader(body))
	handler.CreateMessage(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, dispatcher.calls, 1)

	event, ok := dispatcher.calls[0].(openClawDMDispatchEvent)
	require.True(t, ok)
	require.Equal(t, agentID, event.Data.AgentID)
	require.Equal(t, "agent:main:main", event.Data.SessionKey)
}

func TestCreateMessageDMUsesLoriFallbackWhenSyncStateMissing(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "dm-dispatch-fallback-lori-org")
	agentID := insertMessageTestAgent(t, db, orgID, "lori")

	dispatcher := &fakeOpenClawDispatcher{connected: true}
	handler := &MessageHandler{OpenClawDispatcher: dispatcher}

	payload := map[string]interface{}{
		"org_id":      orgID,
		"thread_id":   "dm_lori",
		"content":     "Hey Lori",
		"sender_type": "user",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewReader(body))
	handler.CreateMessage(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, dispatcher.calls, 1)

	event, ok := dispatcher.calls[0].(openClawDMDispatchEvent)
	require.True(t, ok)
	require.Equal(t, agentID, event.Data.AgentID)
	require.Equal(t, "agent:lori:main", event.Data.SessionKey)
}

func TestCreateMessageDMNormalizesElephantSyncStateFromChameleonKey(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "dm-dispatch-elephant-normalize-org")
	agentID := insertMessageTestAgent(t, db, orgID, "elephant")

	_, err := db.Exec(
		`INSERT INTO agent_sync_state (org_id, id, name, status, session_key, updated_at)
		 VALUES ($1, $2, $3, 'online', $4, NOW())`,
		orgID,
		agentID,
		"Elephant",
		canonicalChameleonSessionKey(agentID),
	)
	require.NoError(t, err)

	dispatcher := &fakeOpenClawDispatcher{connected: true}
	handler := &MessageHandler{OpenClawDispatcher: dispatcher}

	payload := map[string]interface{}{
		"org_id":      orgID,
		"thread_id":   "dm_" + agentID,
		"content":     "Still there?",
		"sender_type": "user",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewReader(body))
	handler.CreateMessage(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, dispatcher.calls, 1)

	event, ok := dispatcher.calls[0].(openClawDMDispatchEvent)
	require.True(t, ok)
	require.Equal(t, agentID, event.Data.AgentID)
	require.Equal(t, "agent:elephant:main", event.Data.SessionKey)
}

func TestCreateMessageDMNormalizesNonExemptSyncStateMainKeyToChameleonAndPersists(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "dm-dispatch-nonexempt-normalize-org")
	agentID := insertMessageTestAgent(t, db, orgID, "technonymous")

	_, err := db.Exec(
		`INSERT INTO agent_sync_state (org_id, id, name, status, session_key, updated_at)
		 VALUES ($1, $2, $3, 'online', $4, NOW())`,
		orgID,
		agentID,
		"Technonymous",
		"agent:technonymous:main",
	)
	require.NoError(t, err)

	dispatcher := &fakeOpenClawDispatcher{connected: true}
	handler := &MessageHandler{OpenClawDispatcher: dispatcher}

	payload := map[string]interface{}{
		"org_id":      orgID,
		"thread_id":   "dm_" + agentID,
		"content":     "Check route",
		"sender_type": "user",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewReader(body))
	handler.CreateMessage(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, dispatcher.calls, 1)

	event, ok := dispatcher.calls[0].(openClawDMDispatchEvent)
	require.True(t, ok)
	require.Equal(t, agentID, event.Data.AgentID)
	require.Equal(t, canonicalChameleonSessionKey(agentID), event.Data.SessionKey)

	var persistedSessionKey string
	err = db.QueryRow(
		`SELECT session_key FROM agent_sync_state WHERE org_id = $1 AND id = $2`,
		orgID,
		agentID,
	).Scan(&persistedSessionKey)
	require.NoError(t, err)
	require.Equal(t, canonicalChameleonSessionKey(agentID), persistedSessionKey)
}

func TestCreateMessageDMDispatchFailurePersistsMessageWithDeliveryWarning(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "dm-dispatch-failure-org")

	_, err := db.Exec(
		`INSERT INTO agent_sync_state (org_id, id, name, status, session_key, updated_at)
		 VALUES ($1, $2, $3, $4, $5, NOW())`,
		orgID,
		"itsalive",
		"Ivy",
		"online",
		"agent:itsalive:main",
	)
	require.NoError(t, err)

	handler := &MessageHandler{
		OpenClawDispatcher: &fakeOpenClawDispatcher{
			connected: true,
			err:       errors.New("send failed"),
		},
	}

	payload := map[string]interface{}{
		"org_id":      orgID,
		"thread_id":   "dm_itsalive",
		"content":     "Can you review this?",
		"sender_type": "user",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewReader(body))
	handler.CreateMessage(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var createResp struct {
		Message  Message          `json:"message"`
		Delivery dmDeliveryStatus `json:"delivery"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &createResp))
	require.NotEmpty(t, createResp.Message.ID)
	require.True(t, createResp.Delivery.Attempted)
	require.False(t, createResp.Delivery.Delivered)
	require.Equal(t, openClawDispatchQueuedWarning, createResp.Delivery.Error)

	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM comments WHERE thread_id = 'dm_itsalive'`).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestCreateMessageDMTouchesChatThreadForAuthenticatedUser(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "messages-chat-thread-org")
	agentID := insertMessageTestAgent(t, db, orgID, "messages-chat-thread-agent")
	userID := insertTestUser(t, db, orgID, "messages-chat-thread-user")
	token := "oc_sess_messages_chat_thread_user"
	insertTestSession(t, db, orgID, userID, token, time.Now().UTC().Add(1*time.Hour))

	handler := &MessageHandler{
		ChatThreadStore: store.NewChatThreadStore(db),
	}

	body, err := json.Marshal(map[string]any{
		"org_id":      orgID,
		"thread_id":   "dm_" + agentID,
		"content":     "Thread touch DM message",
		"sender_type": "user",
		"sender_name": "Sam",
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.CreateMessage(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var (
		threadType string
		preview    string
	)
	err = db.QueryRow(
		`SELECT thread_type, last_message_preview
		 FROM chat_threads
		 WHERE org_id = $1 AND user_id = $2 AND thread_key = $3`,
		orgID,
		userID,
		"dm:dm_"+agentID,
	).Scan(&threadType, &preview)
	require.NoError(t, err)
	require.Equal(t, store.ChatThreadTypeDM, threadType)
	require.Equal(t, "Thread touch DM message", preview)
}

func TestDMChatThreadTitleStabilityAcrossMultipleSenders(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "messages-dm-title-org")
	agentID := insertMessageTestAgent(t, db, orgID, "messages-dm-title-agent")
	userID := insertTestUser(t, db, orgID, "messages-dm-title-user")
	token := "oc_sess_messages_dm_title_user"
	insertTestSession(t, db, orgID, userID, token, time.Now().UTC().Add(1*time.Hour))

	handler := &MessageHandler{
		ChatThreadStore: store.NewChatThreadStore(db),
	}

	send := func(payload map[string]any) {
		payload["org_id"] = orgID
		payload["thread_id"] = "dm_" + agentID

		body, err := json.Marshal(payload)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.CreateMessage(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	}

	send(map[string]any{
		"content":     "first",
		"sender_type": "user",
	})

	send(map[string]any{
		"content":     "second",
		"sender_type": "agent",
		"sender_name": "Different Agent Name",
	})

	send(map[string]any{
		"content":     "third",
		"sender_type": "user",
	})

	var title string
	err := db.QueryRow(
		`SELECT title
		 FROM chat_threads
		 WHERE org_id = $1 AND user_id = $2 AND thread_key = $3`,
		orgID,
		userID,
		"dm:dm_"+agentID,
	).Scan(&title)
	require.NoError(t, err)
	require.Equal(t, "Agent messages-dm-title-agent", title)
}
