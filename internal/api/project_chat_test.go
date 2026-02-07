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
	router.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/chat/reset", handler.ResetSession)
	router.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/chat/messages/{messageID}/save-to-notes", handler.SaveToNotes)
	router.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/content/bootstrap", handler.BootstrapContent)
	router.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/content/assets", handler.UploadContentAsset)
	router.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/content/rename", handler.RenameContent)
	router.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/content/delete", handler.DeleteContent)
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

	invalidSenderTypeReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/chat/messages?org_id="+orgID,
		bytes.NewReader([]byte(`{"author":"Sam","body":"x","sender_type":"robot"}`)),
	)
	invalidSenderTypeRec := httptest.NewRecorder()
	router.ServeHTTP(invalidSenderTypeRec, invalidSenderTypeReq)
	require.Equal(t, http.StatusBadRequest, invalidSenderTypeRec.Code)

	missingWorkspaceReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/chat/messages", bytes.NewReader([]byte(`{"author":"Sam","body":"x"}`)))
	missingWorkspaceRec := httptest.NewRecorder()
	router.ServeHTTP(missingWorkspaceRec, missingWorkspaceReq)
	require.Equal(t, http.StatusUnauthorized, missingWorkspaceRec.Code)

	reservedAuthorReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/chat/messages?org_id="+orgID,
		bytes.NewReader([]byte(`{"author":"__otter_session__","body":"x"}`)),
	)
	reservedAuthorRec := httptest.NewRecorder()
	router.ServeHTTP(reservedAuthorRec, reservedAuthorReq)
	require.Equal(t, http.StatusBadRequest, reservedAuthorRec.Code)
}

func TestProjectChatHandlerResetSessionCreatesMarkerAndDispatchesUsingNewSessionKey(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-chat-reset-session-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Reset Session")
	ownerID := insertMessageTestAgent(t, db, orgID, "stone")

	issueStore := store.NewProjectIssueStore(db)
	ctx := testCtxWithWorkspace(orgID)
	issue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Dispatch routing issue",
		Origin:    "local",
	})
	require.NoError(t, err)
	_, err = issueStore.AddParticipant(ctx, store.AddProjectIssueParticipantInput{
		IssueID: issue.ID,
		AgentID: ownerID,
		Role:    "owner",
	})
	require.NoError(t, err)

	dispatcher := &fakeOpenClawDispatcher{connected: true}
	handler := &ProjectChatHandler{
		ProjectStore:       store.NewProjectStore(db),
		ChatStore:          store.NewProjectChatStore(db),
		DB:                 db,
		OpenClawDispatcher: dispatcher,
	}
	router := newProjectChatTestRouter(handler)

	resetReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/chat/reset?org_id="+orgID, nil)
	resetRec := httptest.NewRecorder()
	router.ServeHTTP(resetRec, resetReq)
	require.Equal(t, http.StatusCreated, resetRec.Code)

	var resetResp struct {
		Message   projectChatMessagePayload `json:"message"`
		SessionID string                    `json:"session_id"`
	}
	require.NoError(t, json.NewDecoder(resetRec.Body).Decode(&resetResp))
	require.Equal(t, projectChatSessionResetAuthor, resetResp.Message.Author)
	require.Equal(t, buildProjectChatSessionResetBody(resetResp.SessionID), resetResp.Message.Body)
	require.NotEmpty(t, resetResp.SessionID)

	createReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/chat/messages?org_id="+orgID,
		bytes.NewReader([]byte(`{"author":"Sam","body":"Message after reset"}`)),
	)
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	require.Equal(t, http.StatusCreated, createRec.Code)
	require.Len(t, dispatcher.calls, 1)

	event, ok := dispatcher.calls[0].(openClawProjectChatDispatchEvent)
	require.True(t, ok)
	require.Equal(t, projectChatSessionKey("stone", projectID, resetResp.SessionID), event.Data.SessionKey)
}

func TestProjectChatHandlerCreateDispatchesToOpenClaw(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-chat-dispatch-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Dispatch")
	ownerID := insertMessageTestAgent(t, db, orgID, "stone")

	issueStore := store.NewProjectIssueStore(db)
	ctx := testCtxWithWorkspace(orgID)
	issue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Dispatch routing issue",
		Origin:    "local",
	})
	require.NoError(t, err)
	_, err = issueStore.AddParticipant(ctx, store.AddProjectIssueParticipantInput{
		IssueID: issue.ID,
		AgentID: ownerID,
		Role:    "owner",
	})
	require.NoError(t, err)

	dispatcher := &fakeOpenClawDispatcher{connected: true}
	handler := &ProjectChatHandler{
		ProjectStore:       store.NewProjectStore(db),
		ChatStore:          store.NewProjectChatStore(db),
		DB:                 db,
		OpenClawDispatcher: dispatcher,
	}
	router := newProjectChatTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/chat/messages?org_id="+orgID,
		bytes.NewReader([]byte(`{"author":"Sam","body":"Please review this project update"}`)),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Len(t, dispatcher.calls, 1)

	event, ok := dispatcher.calls[0].(openClawProjectChatDispatchEvent)
	require.True(t, ok)
	require.Equal(t, "project.chat.message", event.Type)
	require.Equal(t, orgID, event.OrgID)
	require.Equal(t, projectID, event.Data.ProjectID)
	require.Equal(t, "Please review this project update", event.Data.Content)
	require.Equal(t, "Sam", event.Data.Author)
	require.Equal(t, "stone", event.Data.AgentID)
	require.Equal(t, "Agent stone", event.Data.AgentName)
	require.Equal(t, projectChatSessionKey("stone", projectID, ""), event.Data.SessionKey)

	var resp struct {
		Message  projectChatMessagePayload `json:"message"`
		Delivery dmDeliveryStatus          `json:"delivery"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.True(t, resp.Delivery.Attempted)
	require.True(t, resp.Delivery.Delivered)
	require.NotEmpty(t, resp.Message.ID)
}

func TestProjectChatHandlerCreateAgentSenderSkipsOpenClawDispatch(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-chat-agent-sender-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Agent Sender")

	dispatcher := &fakeOpenClawDispatcher{connected: true}
	handler := &ProjectChatHandler{
		ProjectStore:       store.NewProjectStore(db),
		ChatStore:          store.NewProjectChatStore(db),
		OpenClawDispatcher: dispatcher,
	}
	router := newProjectChatTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/chat/messages?org_id="+orgID,
		bytes.NewReader([]byte(`{"author":"Stone","body":"Assistant reply","sender_type":"agent"}`)),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Len(t, dispatcher.calls, 0)

	var resp struct {
		Message  projectChatMessagePayload `json:"message"`
		Delivery dmDeliveryStatus          `json:"delivery"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.False(t, resp.Delivery.Attempted)
	require.False(t, resp.Delivery.Delivered)
}

func TestProjectChatHandlerCreateDispatchesToTaskAssigneeWhenNoOwner(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-chat-dispatch-assignee-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Dispatch Assignee")
	assigneeID := insertMessageTestAgent(t, db, orgID, "stone")

	_, err := db.Exec(
		`INSERT INTO tasks (org_id, project_id, assigned_agent_id, title, status, priority)
		 VALUES ($1, $2, $3, $4, 'queued', 'P2')`,
		orgID,
		projectID,
		assigneeID,
		"Assignee routing task",
	)
	require.NoError(t, err)

	dispatcher := &fakeOpenClawDispatcher{connected: true}
	handler := &ProjectChatHandler{
		ProjectStore:       store.NewProjectStore(db),
		ChatStore:          store.NewProjectChatStore(db),
		DB:                 db,
		OpenClawDispatcher: dispatcher,
	}
	router := newProjectChatTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/chat/messages?org_id="+orgID,
		bytes.NewReader([]byte(`{"author":"Sam","body":"Route this to assigned lead"}`)),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Len(t, dispatcher.calls, 1)

	event, ok := dispatcher.calls[0].(openClawProjectChatDispatchEvent)
	require.True(t, ok)
	require.Equal(t, "stone", event.Data.AgentID)
	require.Equal(t, projectChatSessionKey("stone", projectID, ""), event.Data.SessionKey)
}

func TestProjectChatHandlerCreateWarnsWhenProjectLeadUnavailable(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-chat-dispatch-unavailable-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Dispatch Unavailable")

	dispatcher := &fakeOpenClawDispatcher{connected: true}
	handler := &ProjectChatHandler{
		ProjectStore:       store.NewProjectStore(db),
		ChatStore:          store.NewProjectChatStore(db),
		DB:                 db,
		OpenClawDispatcher: dispatcher,
	}
	router := newProjectChatTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/chat/messages?org_id="+orgID,
		bytes.NewReader([]byte(`{"author":"Sam","body":"Hello?"}`)),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Len(t, dispatcher.calls, 0)

	var resp struct {
		Delivery dmDeliveryStatus `json:"delivery"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.False(t, resp.Delivery.Attempted)
	require.False(t, resp.Delivery.Delivered)
	require.Equal(t, "project agent unavailable; message was saved but not delivered", resp.Delivery.Error)
}

func TestProjectChatHandlerCreateDispatchesToPrimaryProjectAgent(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-chat-primary-agent-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Primary Agent")
	primaryAgentID := insertMessageTestAgent(t, db, orgID, "stone-primary")

	_, err := db.Exec("UPDATE projects SET primary_agent_id = $1 WHERE id = $2", primaryAgentID, projectID)
	require.NoError(t, err)

	dispatcher := &fakeOpenClawDispatcher{connected: true}
	handler := &ProjectChatHandler{
		ProjectStore:       store.NewProjectStore(db),
		ChatStore:          store.NewProjectChatStore(db),
		DB:                 db,
		OpenClawDispatcher: dispatcher,
	}
	router := newProjectChatTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/chat/messages?org_id="+orgID,
		bytes.NewReader([]byte(`{"author":"Sam","body":"Use the configured primary agent"}`)),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Len(t, dispatcher.calls, 1)

	event, ok := dispatcher.calls[0].(openClawProjectChatDispatchEvent)
	require.True(t, ok)
	require.Equal(t, "stone-primary", event.Data.AgentID)
	require.Equal(t, projectChatSessionKey("stone-primary", projectID, ""), event.Data.SessionKey)
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
