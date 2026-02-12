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

func TestAdminAgentsCreateGeneratesSlotFromDisplayNameAndCreatesTemplates(t *testing.T) {
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
		strings.NewReader(`{"displayName":"Riley","model":"gpt-5.2-codex"}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Create(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	createdAgent, err := handler.Store.GetBySlug(
		context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID),
		"riley",
	)
	require.NoError(t, err)
	require.Equal(t, "Riley", createdAgent.DisplayName)

	identityPath := filepath.Join(repoPath, "agents", "riley", "IDENTITY.md")
	soulPath := filepath.Join(repoPath, "agents", "riley", "SOUL.md")
	toolsPath := filepath.Join(repoPath, "agents", "riley", "TOOLS.md")
	memoryPath := filepath.Join(repoPath, "agents", "riley", "MEMORY.md")
	for _, path := range []string{identityPath, soulPath, toolsPath, memoryPath} {
		_, statErr := os.Stat(path)
		require.NoError(t, statErr)
	}
	identityBytes, err := os.ReadFile(identityPath)
	require.NoError(t, err)
	require.Contains(t, string(identityBytes), "**Name:** Riley")

	require.Empty(t, dispatcher.calls)
}

func TestAdminAgentsCreateCollisionSuffixesGeneratedSlot(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agents-create-duplicate")
	_ = seedAdminAgentFilesProjectFixture(t, db, orgID, "main")
	_, err := db.Exec(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'riley', 'Existing Agent', 'active')`,
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
		strings.NewReader(`{"displayName":"Riley","model":"gpt-5.2-codex"}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Create(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	createdAgent, getErr := handler.Store.GetBySlug(
		context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID),
		"riley-2",
	)
	require.NoError(t, getErr)
	require.Equal(t, "Riley", createdAgent.DisplayName)

	require.Empty(t, dispatcher.calls)
}

func TestAdminAgentsCreateAcceptsPreviouslyMissingBuiltInProfiles(t *testing.T) {
	t.Parallel()

	cases := []struct {
		displayName string
		profileID   string
	}{
		{displayName: "Kit", profileID: "kit"},
		{displayName: "Rowan", profileID: "rowan"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.profileID, func(t *testing.T) {
			db := setupMessageTestDB(t)
			orgID := insertMessageTestOrganization(t, db, "admin-agents-create-built-in-profile-"+tc.profileID)
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
				strings.NewReader(`{"displayName":"`+tc.displayName+`","profileId":"`+tc.profileID+`","model":"gpt-5.2-codex"}`),
			)
			req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
			rec := httptest.NewRecorder()
			handler.Create(rec, req)
			require.Equal(t, http.StatusCreated, rec.Code)

			createdAgent, err := handler.Store.GetBySlug(
				context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID),
				tc.profileID,
			)
			require.NoError(t, err)
			require.Equal(t, tc.displayName, createdAgent.DisplayName)
			require.FileExists(t, filepath.Join(repoPath, "agents", tc.profileID, "SOUL.md"))
			require.FileExists(t, filepath.Join(repoPath, "agents", tc.profileID, "IDENTITY.md"))
			require.Empty(t, dispatcher.calls)
		})
	}
}

func TestAdminAgentsCreateDoesNotRequireBridgeAvailability(t *testing.T) {
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
		strings.NewReader(`{"displayName":"Ops Agent","model":"gpt-5.2-codex"}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Create(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	_, err := handler.Store.GetBySlug(
		context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID),
		"ops-agent",
	)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(repoPath, "agents", "ops-agent", "IDENTITY.md"))
	require.NoError(t, err)
	require.Empty(t, dispatcher.calls)
}

func TestAdminAgentsCreateRequiresDisplayName(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agents-create-validates-display-name")
	_ = seedAdminAgentFilesProjectFixture(t, db, orgID, "main")
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
		strings.NewReader(`{"model":"gpt-5.2-codex"}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Create(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "displayName is required", payload.Error)
	require.Empty(t, dispatcher.calls)
}

func TestAdminAgentsCreateAutoCreatesAgentFilesProjectWhenMissing(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agents-create-autoproject")
	dispatcher := &fakeOpenClawConnectionStatus{connected: true}

	handler := &AdminAgentsHandler{
		DB:              db,
		Store:           store.NewAgentStore(db),
		ProjectStore:    store.NewProjectStore(db),
		ProjectRepos:    store.NewProjectRepoStore(db),
		OpenClawHandler: dispatcher,
		EventStore:      store.NewConnectionEventStore(db),
	}

	firstReq := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/agents",
		strings.NewReader(`{"displayName":"Marcus","model":"gpt-5.2-codex"}`),
	)
	firstReq = firstReq.WithContext(context.WithValue(firstReq.Context(), middleware.WorkspaceIDKey, orgID))
	firstRec := httptest.NewRecorder()
	handler.Create(firstRec, firstReq)
	require.Equal(t, http.StatusCreated, firstRec.Code)

	ctx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
	agentFilesProject, err := handler.ProjectStore.GetByName(ctx, agentFilesProjectName)
	require.NoError(t, err)
	require.Equal(t, orgID, agentFilesProject.OrgID)

	binding, err := handler.ProjectRepos.GetBinding(ctx, agentFilesProject.ID)
	require.NoError(t, err)
	require.NotNil(t, binding.LocalRepoPath)
	repoPath := strings.TrimSpace(*binding.LocalRepoPath)
	require.NotEmpty(t, repoPath)
	require.DirExists(t, filepath.Join(repoPath, ".git"))
	require.FileExists(t, filepath.Join(repoPath, "agents", "marcus", "SOUL.md"))
	require.FileExists(t, filepath.Join(repoPath, "agents", "marcus", "IDENTITY.md"))

	secondReq := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/agents",
		strings.NewReader(`{"displayName":"Sage","model":"gpt-5.2-codex"}`),
	)
	secondReq = secondReq.WithContext(context.WithValue(secondReq.Context(), middleware.WorkspaceIDKey, orgID))
	secondRec := httptest.NewRecorder()
	handler.Create(secondRec, secondReq)
	require.Equal(t, http.StatusCreated, secondRec.Code)
	require.FileExists(t, filepath.Join(repoPath, "agents", "sage", "SOUL.md"))
	require.FileExists(t, filepath.Join(repoPath, "agents", "sage", "IDENTITY.md"))

	var projectCount int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM projects WHERE org_id = $1 AND name = $2`,
		orgID,
		agentFilesProjectName,
	).Scan(&projectCount))
	require.Equal(t, 1, projectCount)
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
		writeTemplatesFn: func(context.Context, string, string, adminAgentTemplateInput) error {
			return errors.New("synthetic template write failure")
		},
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/agents",
		strings.NewReader(`{"displayName":"Broken Agent","model":"gpt-5.2-codex"}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Create(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)

	_, err := handler.Store.GetBySlug(
		context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID),
		"broken-agent",
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, store.ErrNotFound))
	require.Empty(t, dispatcher.calls)
}

func TestAdminAgentsCreatePersistsLifecycleMetadata(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agents-create-lifecycle")
	_ = seedAdminAgentFilesProjectFixture(t, db, orgID, "main")
	dispatcher := &fakeOpenClawConnectionStatus{connected: true}

	var projectID string
	err := db.QueryRow(
		`INSERT INTO projects (org_id, name, status) VALUES ($1, 'Lifecycle Project', 'active') RETURNING id`,
		orgID,
	).Scan(&projectID)
	require.NoError(t, err)

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
		strings.NewReader(`{"displayName":"Temp Worker","model":"gpt-5.2-codex","isEphemeral":true,"projectId":"`+projectID+`"}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Create(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	createdAgent, getErr := handler.Store.GetBySlug(
		context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID),
		"temp-worker",
	)
	require.NoError(t, getErr)
	require.True(t, createdAgent.IsEphemeral)
	require.NotNil(t, createdAgent.ProjectID)
	require.Equal(t, projectID, *createdAgent.ProjectID)
}

func TestAdminAgentsCreateRejectsProjectOutsideWorkspace(t *testing.T) {
	db := setupMessageTestDB(t)
	orgA := insertMessageTestOrganization(t, db, "admin-agents-create-project-org-a")
	orgB := insertMessageTestOrganization(t, db, "admin-agents-create-project-org-b")
	_ = seedAdminAgentFilesProjectFixture(t, db, orgA, "main")
	dispatcher := &fakeOpenClawConnectionStatus{connected: true}

	var foreignProjectID string
	err := db.QueryRow(
		`INSERT INTO projects (org_id, name, status) VALUES ($1, 'Foreign Project', 'active') RETURNING id`,
		orgB,
	).Scan(&foreignProjectID)
	require.NoError(t, err)

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
		strings.NewReader(`{"displayName":"Cross Org Temp","model":"gpt-5.2-codex","isEphemeral":true,"projectId":"`+foreignProjectID+`"}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgA))
	rec := httptest.NewRecorder()
	handler.Create(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "projectId must belong to the current workspace", payload.Error)
	require.Empty(t, dispatcher.calls)
}
