package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
	"github.com/stretchr/testify/require"
)

func newProjectChatTestRouter(handler *ProjectChatHandler) http.Handler {
	router := chi.NewRouter()
	router.With(middleware.OptionalWorkspace).Get("/api/projects/{id}/chat", handler.List)
	router.With(middleware.OptionalWorkspace).Get("/api/projects/{id}/chat/search", handler.Search)
	router.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/chat/messages", handler.Create)
	router.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/chat/messages/{messageID}/save-to-notes", handler.SaveToNotes)
	router.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/content/bootstrap", handler.BootstrapContent)
	router.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/content/assets", handler.UploadContentAsset)
	router.With(middleware.OptionalWorkspace).Get("/api/projects/{id}/content/metadata", handler.GetContentMetadata)
	router.With(middleware.OptionalWorkspace).Get("/api/projects/{id}/content/search", handler.SearchContent)
	return router
}

func TestProjectChatHandlerCreateAndList(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-chat-api-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Chat API")

	handler := &ProjectChatHandler{
		ProjectStore: store.NewProjectStore(db),
		ChatStore:    store.NewProjectChatStore(db),
	}
	router := newProjectChatTestRouter(handler)

	createBody := []byte(`{"author":"Sam","body":"First project chat message"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/chat/messages?org_id="+orgID, bytes.NewReader(createBody))
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)

	require.Equal(t, http.StatusCreated, createRec.Code)
	var createResp struct {
		Message projectChatMessagePayload `json:"message"`
	}
	require.NoError(t, json.NewDecoder(createRec.Body).Decode(&createResp))
	require.NotEmpty(t, createResp.Message.ID)
	require.Equal(t, projectID, createResp.Message.ProjectID)
	require.Equal(t, "Sam", createResp.Message.Author)
	require.Equal(t, "First project chat message", createResp.Message.Body)

	listReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/chat?org_id="+orgID, nil)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)

	require.Equal(t, http.StatusOK, listRec.Code)
	var listResp projectChatListResponse
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listResp))
	require.Len(t, listResp.Messages, 1)
	require.Equal(t, createResp.Message.ID, listResp.Messages[0].ID)
	require.False(t, listResp.HasMore)
}

func TestProjectChatHandlerCreateValidatesPayload(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-chat-validate-org")
	projectID := insertProjectTestProject(t, db, orgID, "Validation Project")

	handler := &ProjectChatHandler{
		ProjectStore: store.NewProjectStore(db),
		ChatStore:    store.NewProjectChatStore(db),
	}
	router := newProjectChatTestRouter(handler)

	badReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/chat/messages?org_id="+orgID, bytes.NewReader([]byte(`{"author":"Sam"}`)))
	badRec := httptest.NewRecorder()
	router.ServeHTTP(badRec, badReq)
	require.Equal(t, http.StatusBadRequest, badRec.Code)

	missingWorkspaceReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/chat/messages", bytes.NewReader([]byte(`{"author":"Sam","body":"x"}`)))
	missingWorkspaceRec := httptest.NewRecorder()
	router.ServeHTTP(missingWorkspaceRec, missingWorkspaceReq)
	require.Equal(t, http.StatusUnauthorized, missingWorkspaceRec.Code)
}

func TestProjectChatHandlerSearchSupportsFilters(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-chat-search-api-org")
	projectID := insertProjectTestProject(t, db, orgID, "Search API Project")

	chatStore := store.NewProjectChatStore(db)
	ctx := testCtxWithWorkspace(orgID)
	first, err := chatStore.Create(ctx, store.CreateProjectChatMessageInput{
		ProjectID: projectID,
		Author:    "Sam",
		Body:      "Launch plan draft for next week",
	})
	require.NoError(t, err)
	second, err := chatStore.Create(ctx, store.CreateProjectChatMessageInput{
		ProjectID: projectID,
		Author:    "Stone",
		Body:      "Launch retrospective and edits",
	})
	require.NoError(t, err)

	base := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	_, err = db.Exec("UPDATE project_chat_messages SET created_at = $1, updated_at = $1 WHERE id = $2", base, first.ID)
	require.NoError(t, err)
	_, err = db.Exec("UPDATE project_chat_messages SET created_at = $1, updated_at = $1 WHERE id = $2", base.Add(5*time.Minute), second.ID)
	require.NoError(t, err)

	handler := &ProjectChatHandler{
		ProjectStore: store.NewProjectStore(db),
		ChatStore:    chatStore,
	}
	router := newProjectChatTestRouter(handler)

	searchReq := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/chat/search?org_id="+orgID+"&q=launch&author=Stone&from=2026-02-07T12:03:00Z",
		nil,
	)
	searchRec := httptest.NewRecorder()
	router.ServeHTTP(searchRec, searchReq)

	require.Equal(t, http.StatusOK, searchRec.Code)
	var searchResp projectChatSearchResponse
	require.NoError(t, json.NewDecoder(searchRec.Body).Decode(&searchResp))
	require.Equal(t, 1, searchResp.Total)
	require.Equal(t, second.ID, searchResp.Items[0].Message.ID)
	require.NotEmpty(t, searchResp.Items[0].Snippet)

	invalidReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/chat/search?org_id="+orgID+"&q=launch&from=bad-date", nil)
	invalidRec := httptest.NewRecorder()
	router.ServeHTTP(invalidRec, invalidReq)
	require.Equal(t, http.StatusBadRequest, invalidRec.Code)

	emptyQueryReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/chat/search?org_id="+orgID+"&q=", nil)
	emptyQueryRec := httptest.NewRecorder()
	router.ServeHTTP(emptyQueryRec, emptyQueryReq)
	require.Equal(t, http.StatusBadRequest, emptyQueryRec.Code)
}

func TestProjectChatHandlerWebSocketBroadcastProjectChannel(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-chat-ws-org")
	projectA := insertProjectTestProject(t, db, orgID, "Project A")
	projectB := insertProjectTestProject(t, db, orgID, "Project B")

	hub := ws.NewHub()
	go hub.Run()

	handler := &ProjectChatHandler{
		ProjectStore: store.NewProjectStore(db),
		ChatStore:    store.NewProjectChatStore(db),
		Hub:          hub,
	}
	router := newProjectChatTestRouter(handler)

	clientA := ws.NewClient(hub, nil)
	clientA.SetOrgID(orgID)
	clientA.SubscribeTopic(projectChatChannel(projectA))
	hub.Register(clientA)
	t.Cleanup(func() { hub.Unregister(clientA) })

	clientB := ws.NewClient(hub, nil)
	clientB.SetOrgID(orgID)
	clientB.SubscribeTopic(projectChatChannel(projectB))
	hub.Register(clientB)
	t.Cleanup(func() { hub.Unregister(clientB) })

	time.Sleep(25 * time.Millisecond)

	postReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectA+"/chat/messages?org_id="+orgID, bytes.NewReader([]byte(`{"author":"Sam","body":"Realtime hello"}`)))
	postRec := httptest.NewRecorder()
	router.ServeHTTP(postRec, postReq)
	require.Equal(t, http.StatusCreated, postRec.Code)

	select {
	case payload := <-clientA.Send:
		var event projectChatMessageCreatedEvent
		require.NoError(t, json.Unmarshal(payload, &event))
		require.Equal(t, ws.MessageProjectChatMessageCreated, event.Type)
		require.Equal(t, projectChatChannel(projectA), event.Channel)
		require.Equal(t, "Realtime hello", event.Message.Body)
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected project A websocket subscriber to receive chat event")
	}

	select {
	case payload := <-clientB.Send:
		t.Fatalf("did not expect project B subscriber payload: %s", string(payload))
	case <-time.After(120 * time.Millisecond):
	}
}

func TestProjectChatHandlerSaveToNotesWritesProvenanceAndIsIdempotent(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-chat-notes-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Notes")

	tempRoot := t.TempDir()
	t.Setenv("OTTER_CONTENT_ROOT", tempRoot)

	chatStore := store.NewProjectChatStore(db)
	ctx := testCtxWithWorkspace(orgID)
	message, err := chatStore.Create(ctx, store.CreateProjectChatMessageInput{
		ProjectID: projectID,
		Author:    "Sam",
		Body:      "Save this to notes",
	})
	require.NoError(t, err)

	handler := &ProjectChatHandler{
		ProjectStore: store.NewProjectStore(db),
		ChatStore:    chatStore,
	}
	router := newProjectChatTestRouter(handler)

	savePath := "/api/projects/" + projectID + "/chat/messages/" + message.ID + "/save-to-notes?org_id=" + orgID
	saveReq := httptest.NewRequest(http.MethodPost, savePath, nil)
	saveRec := httptest.NewRecorder()
	router.ServeHTTP(saveRec, saveReq)
	require.Equal(t, http.StatusOK, saveRec.Code)

	var saveResp projectChatSaveToNotesResponse
	require.NoError(t, json.NewDecoder(saveRec.Body).Decode(&saveResp))
	require.True(t, saveResp.Saved)
	require.Equal(t, "/notes/project-chat.md", saveResp.Path)

	noteFile := filepath.Join(tempRoot, projectID, "notes", "project-chat.md")
	contentBytes, err := os.ReadFile(noteFile)
	require.NoError(t, err)
	content := string(contentBytes)
	require.Contains(t, content, "Save this to notes")
	require.Contains(t, content, "ottercamp_project_chat_source")
	require.Contains(t, content, "message_id="+message.ID)

	// Saving the same message twice should be idempotent.
	saveReq = httptest.NewRequest(http.MethodPost, savePath, nil)
	saveRec = httptest.NewRecorder()
	router.ServeHTTP(saveRec, saveReq)
	require.Equal(t, http.StatusOK, saveRec.Code)
	require.NoError(t, json.NewDecoder(saveRec.Body).Decode(&saveResp))
	require.False(t, saveResp.Saved)

	contentBytes, err = os.ReadFile(noteFile)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(contentBytes), "message_id="+message.ID))
}

func TestProjectChatHandlerCreateDoesNotAutoWriteNotes(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-chat-notes-regression-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Notes Regression")

	tempRoot := t.TempDir()
	t.Setenv("OTTER_CONTENT_ROOT", tempRoot)

	handler := &ProjectChatHandler{
		ProjectStore: store.NewProjectStore(db),
		ChatStore:    store.NewProjectChatStore(db),
	}
	router := newProjectChatTestRouter(handler)

	createReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/chat/messages?org_id="+orgID,
		bytes.NewReader([]byte(`{"author":"Sam","body":"Normal chat message"}`)),
	)
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	require.Equal(t, http.StatusCreated, createRec.Code)

	noteFile := filepath.Join(tempRoot, projectID, "notes", "project-chat.md")
	_, err := os.Stat(noteFile)
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))
}

func TestProjectContentBootstrapCreatesDirectorySkeleton(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-content-bootstrap-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Bootstrap")

	root := t.TempDir()
	t.Setenv("OTTER_CONTENT_ROOT", root)

	handler := &ProjectChatHandler{
		ProjectStore: store.NewProjectStore(db),
		ChatStore:    store.NewProjectChatStore(db),
	}
	router := newProjectChatTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/content/bootstrap?org_id="+orgID, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp projectContentBootstrapResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.ElementsMatch(t, []string{"/notes", "/posts", "/assets"}, resp.Created)

	for _, dir := range []string{"notes", "posts", "assets"} {
		require.DirExists(t, filepath.Join(root, projectID, dir))
	}
}

func testCtxWithWorkspace(orgID string) context.Context {
	return context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
}
