package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func newChatsTestRouter(handler *ChatsHandler) http.Handler {
	router := chi.NewRouter()
	router.With(middleware.OptionalWorkspace).Get("/api/chats", handler.List)
	router.With(middleware.OptionalWorkspace).Get("/api/chats/{id}", handler.Get)
	router.With(middleware.OptionalWorkspace).Post("/api/chats/{id}/archive", handler.Archive)
	router.With(middleware.OptionalWorkspace).Post("/api/chats/{id}/unarchive", handler.Unarchive)
	return router
}

func chatsTestCtx(orgID string) context.Context {
	return context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
}

func TestChatsHandlerListArchiveUnarchive(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "chats-handler-org")
	userID := insertTestUser(t, db, orgID, "chats-handler-user")
	token := "oc_sess_chats_handler_user"
	insertTestSession(t, db, orgID, userID, token, time.Now().UTC().Add(1*time.Hour))

	chatStore := store.NewChatThreadStore(db)
	ctx := chatsTestCtx(orgID)
	base := time.Date(2026, 2, 11, 11, 0, 0, 0, time.UTC)

	first, err := chatStore.TouchThread(ctx, store.TouchChatThreadInput{
		UserID:             userID,
		ThreadKey:          "dm:dm_alpha",
		ThreadType:         store.ChatThreadTypeDM,
		Title:              "Alpha",
		LastMessagePreview: "alpha",
		LastMessageAt:      base,
	})
	require.NoError(t, err)
	second, err := chatStore.TouchThread(ctx, store.TouchChatThreadInput{
		UserID:             userID,
		ThreadKey:          "project:proj_beta",
		ThreadType:         store.ChatThreadTypeProject,
		Title:              "Beta",
		LastMessagePreview: "beta",
		LastMessageAt:      base.Add(1 * time.Minute),
	})
	require.NoError(t, err)

	handler := &ChatsHandler{ChatThreadStore: chatStore, DB: db}
	router := newChatsTestRouter(handler)

	listReq := httptest.NewRequest(http.MethodGet, "/api/chats?org_id="+orgID, nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)

	var listResp chatsListResponse
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listResp))
	require.Len(t, listResp.Chats, 2)
	require.Equal(t, second.ID, listResp.Chats[0].ID)
	require.Equal(t, first.ID, listResp.Chats[1].ID)

	archiveReq := httptest.NewRequest(http.MethodPost, "/api/chats/"+first.ID+"/archive?org_id="+orgID, nil)
	archiveReq.Header.Set("Authorization", "Bearer "+token)
	archiveRec := httptest.NewRecorder()
	router.ServeHTTP(archiveRec, archiveReq)
	require.Equal(t, http.StatusOK, archiveRec.Code)

	activeReq := httptest.NewRequest(http.MethodGet, "/api/chats?org_id="+orgID, nil)
	activeReq.Header.Set("Authorization", "Bearer "+token)
	activeRec := httptest.NewRecorder()
	router.ServeHTTP(activeRec, activeReq)
	require.Equal(t, http.StatusOK, activeRec.Code)
	var activeResp chatsListResponse
	require.NoError(t, json.NewDecoder(activeRec.Body).Decode(&activeResp))
	require.Len(t, activeResp.Chats, 1)
	require.Equal(t, second.ID, activeResp.Chats[0].ID)

	archivedReq := httptest.NewRequest(http.MethodGet, "/api/chats?org_id="+orgID+"&archived=true", nil)
	archivedReq.Header.Set("Authorization", "Bearer "+token)
	archivedRec := httptest.NewRecorder()
	router.ServeHTTP(archivedRec, archivedReq)
	require.Equal(t, http.StatusOK, archivedRec.Code)
	var archivedResp chatsListResponse
	require.NoError(t, json.NewDecoder(archivedRec.Body).Decode(&archivedResp))
	require.Len(t, archivedResp.Chats, 1)
	require.Equal(t, first.ID, archivedResp.Chats[0].ID)

	unarchiveReq := httptest.NewRequest(http.MethodPost, "/api/chats/"+first.ID+"/unarchive?org_id="+orgID, nil)
	unarchiveReq.Header.Set("Authorization", "Bearer "+token)
	unarchiveRec := httptest.NewRecorder()
	router.ServeHTTP(unarchiveRec, unarchiveReq)
	require.Equal(t, http.StatusOK, unarchiveRec.Code)

	activeAgainReq := httptest.NewRequest(http.MethodGet, "/api/chats?org_id="+orgID, nil)
	activeAgainReq.Header.Set("Authorization", "Bearer "+token)
	activeAgainRec := httptest.NewRecorder()
	router.ServeHTTP(activeAgainRec, activeAgainReq)
	require.Equal(t, http.StatusOK, activeAgainRec.Code)
	var activeAgainResp chatsListResponse
	require.NoError(t, json.NewDecoder(activeAgainRec.Body).Decode(&activeAgainResp))
	require.Len(t, activeAgainResp.Chats, 2)
}

func TestChatsHandlerRequiresAuthentication(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "chats-handler-auth-org")

	handler := &ChatsHandler{
		ChatThreadStore: store.NewChatThreadStore(db),
		DB:              db,
	}
	router := newChatsTestRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/chats?org_id="+orgID, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestChatsHandlerEnforcesUserOwnership(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "chats-handler-scope-org")
	userA := insertTestUser(t, db, orgID, "chats-handler-user-a")
	userB := insertTestUser(t, db, orgID, "chats-handler-user-b")
	tokenB := "oc_sess_chats_handler_user_b"
	insertTestSession(t, db, orgID, userB, tokenB, time.Now().UTC().Add(1*time.Hour))

	chatStore := store.NewChatThreadStore(db)
	ctx := chatsTestCtx(orgID)
	thread, err := chatStore.TouchThread(ctx, store.TouchChatThreadInput{
		UserID:             userA,
		ThreadKey:          "dm:dm_private",
		ThreadType:         store.ChatThreadTypeDM,
		Title:              "Private",
		LastMessagePreview: "private",
		LastMessageAt:      time.Now().UTC(),
	})
	require.NoError(t, err)

	handler := &ChatsHandler{ChatThreadStore: chatStore, DB: db}
	router := newChatsTestRouter(handler)

	archiveReq := httptest.NewRequest(http.MethodPost, "/api/chats/"+thread.ID+"/archive?org_id="+orgID, nil)
	archiveReq.Header.Set("Authorization", "Bearer "+tokenB)
	archiveRec := httptest.NewRecorder()
	router.ServeHTTP(archiveRec, archiveReq)
	require.Equal(t, http.StatusNotFound, archiveRec.Code)
}

func TestChatsHandlerGetReturnsMessagesForDMThread(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "chats-handler-get-org")
	userID := insertTestUser(t, db, orgID, "chats-handler-get-user")
	token := "oc_sess_chats_handler_get_user"
	insertTestSession(t, db, orgID, userID, token, time.Now().UTC().Add(1*time.Hour))
	agentID := insertMessageTestAgent(t, db, orgID, "chats-get-agent")

	threadID := "dm_" + agentID
	chatStore := store.NewChatThreadStore(db)
	ctx := chatsTestCtx(orgID)
	thread, err := chatStore.TouchThread(ctx, store.TouchChatThreadInput{
		UserID:             userID,
		AgentID:            &agentID,
		ThreadKey:          "dm:" + threadID,
		ThreadType:         store.ChatThreadTypeDM,
		Title:              "Agent chat",
		LastMessagePreview: "hello",
		LastMessageAt:      time.Now().UTC(),
	})
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO comments (org_id, thread_id, content, sender_name, sender_type)
		 VALUES ($1, $2, $3, $4, $5)`,
		orgID,
		threadID,
		"hello from dm",
		"Agent",
		"agent",
	)
	require.NoError(t, err)

	handler := &ChatsHandler{ChatThreadStore: chatStore, DB: db}
	router := newChatsTestRouter(handler)

	getReq := httptest.NewRequest(http.MethodGet, "/api/chats/"+thread.ID+"?org_id="+orgID, nil)
	getReq.Header.Set("Authorization", "Bearer "+token)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)

	var resp chatDetailResponse
	require.NoError(t, json.NewDecoder(getRec.Body).Decode(&resp))
	require.Equal(t, thread.ID, resp.Chat.ID)
	require.Len(t, resp.Messages, 1)
	require.Equal(t, "hello from dm", resp.Messages[0].Content)
	require.Equal(t, "agent", resp.Messages[0].SenderType)
}
