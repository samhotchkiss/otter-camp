package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func agentFilesRepoPathForProject(t *testing.T, db *sql.DB, orgID string, projectID string) string {
	t.Helper()
	binding, err := store.NewProjectRepoStore(db).GetBinding(
		context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID),
		projectID,
	)
	require.NoError(t, err)
	require.NotNil(t, binding.LocalRepoPath)
	return strings.TrimSpace(*binding.LocalRepoPath)
}

func TestAdminAgentsCreateValidRequestCreatesAgentAndTemplates(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agents-create-valid")
	projectID := seedAdminAgentFilesProjectFixture(t, db, orgID, "main")
	repoPath := agentFilesRepoPathForProject(t, db, orgID, projectID)
	dispatcher := &fakeOpenClawConnectionStatus{connected: true}

	handler := &AdminAgentsHandler{
		DB:              db,
		Store:           store.NewAgentStore(db),
		ProjectStore:    store.NewProjectStore(db),
		ProjectRepos:    store.NewProjectRepoStore(db),
		OpenClawHandler: dispatcher,
		EventStore:      store.NewConnectionEventStore(db),
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/agents",
		strings.NewReader(`{"slot":"research","display_name":"Riley","model":"gpt-5.2-codex","heartbeat_every":"15m"}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Create(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminCommandDispatchResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.True(t, payload.OK)
	require.False(t, payload.Queued)
	require.Equal(t, adminCommandActionConfigPatch, payload.Action)

	createdAgent, err := handler.Store.GetBySlug(
		context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID),
		"research",
	)
	require.NoError(t, err)
	require.Equal(t, "Riley", createdAgent.DisplayName)

	identityPath := filepath.Join(repoPath, "agents", "research", "IDENTITY.md")
	soulPath := filepath.Join(repoPath, "agents", "research", "SOUL.md")
	toolsPath := filepath.Join(repoPath, "agents", "research", "TOOLS.md")
	memoryPath := filepath.Join(repoPath, "agents", "research", "MEMORY.md")
	for _, path := range []string{identityPath, soulPath, toolsPath, memoryPath} {
		_, statErr := os.Stat(path)
		require.NoError(t, statErr)
	}
	identityBytes, err := os.ReadFile(identityPath)
	require.NoError(t, err)
	require.Contains(t, string(identityBytes), "**Name:** Riley")

	require.Len(t, dispatcher.calls, 1)
	event, ok := dispatcher.calls[0].(openClawAdminCommandEvent)
	require.True(t, ok)
	require.Equal(t, adminCommandActionConfigPatch, event.Data.Action)
	require.JSONEq(
		t,
		`{"agents":{"research":{"enabled":true,"heartbeat":{"every":"15m"},"model":{"primary":"gpt-5.2-codex"}}}}`,
		string(event.Data.ConfigPatch),
	)
}

func TestAdminAgentsCreateRejectsDuplicateSlot(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agents-create-duplicate")
	_ = seedAdminAgentFilesProjectFixture(t, db, orgID, "main")
	_, err := db.Exec(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'research', 'Existing Agent', 'active')`,
		orgID,
	)
	require.NoError(t, err)

	dispatcher := &fakeOpenClawConnectionStatus{connected: true}
	handler := &AdminAgentsHandler{
		DB:              db,
		Store:           store.NewAgentStore(db),
		ProjectStore:    store.NewProjectStore(db),
		ProjectRepos:    store.NewProjectRepoStore(db),
		OpenClawHandler: dispatcher,
		EventStore:      store.NewConnectionEventStore(db),
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/agents",
		strings.NewReader(`{"slot":"research","display_name":"Riley","model":"gpt-5.2-codex"}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Create(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Empty(t, dispatcher.calls)
}

func TestAdminAgentsCreateQueuesConfigMutationWhenBridgeUnavailable(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agents-create-queued")
	projectID := seedAdminAgentFilesProjectFixture(t, db, orgID, "main")
	repoPath := agentFilesRepoPathForProject(t, db, orgID, projectID)
	dispatcher := &fakeOpenClawConnectionStatus{connected: false}

	handler := &AdminAgentsHandler{
		DB:              db,
		Store:           store.NewAgentStore(db),
		ProjectStore:    store.NewProjectStore(db),
		ProjectRepos:    store.NewProjectRepoStore(db),
		OpenClawHandler: dispatcher,
		EventStore:      store.NewConnectionEventStore(db),
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/agents",
		strings.NewReader(`{"slot":"ops","display_name":"Ops Agent","model":"gpt-5.2-codex"}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Create(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	var payload adminCommandDispatchResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.True(t, payload.OK)
	require.True(t, payload.Queued)
	require.Equal(t, adminCommandActionConfigPatch, payload.Action)

	_, err := handler.Store.GetBySlug(
		context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID),
		"ops",
	)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(repoPath, "agents", "ops", "IDENTITY.md"))
	require.NoError(t, err)
}

func TestAdminAgentsCreateRollsBackOnTemplateWriteFailure(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agents-create-rollback")
	_ = seedAdminAgentFilesProjectFixture(t, db, orgID, "main")
	dispatcher := &fakeOpenClawConnectionStatus{connected: true}

	handler := &AdminAgentsHandler{
		DB:              db,
		Store:           store.NewAgentStore(db),
		ProjectStore:    store.NewProjectStore(db),
		ProjectRepos:    store.NewProjectRepoStore(db),
		OpenClawHandler: dispatcher,
		EventStore:      store.NewConnectionEventStore(db),
		writeTemplatesFn: func(context.Context, string, string, string) error {
			return errors.New("synthetic template write failure")
		},
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/agents",
		strings.NewReader(`{"slot":"broken","display_name":"Broken Agent","model":"gpt-5.2-codex"}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Create(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)

	_, err := handler.Store.GetBySlug(
		context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID),
		"broken",
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, store.ErrNotFound))
	require.Empty(t, dispatcher.calls)
}
