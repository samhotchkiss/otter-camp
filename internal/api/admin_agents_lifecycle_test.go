package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func TestAdminAgentsLifecycleRetireMovesFilesAndDispatchesDisable(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agents-retire")
	_, err := db.Exec(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'main', 'Frank', 'active')`,
		orgID,
	)
	require.NoError(t, err)
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

	req := httptest.NewRequest(http.MethodPost, "/api/admin/agents/main/retire", nil)
	req = addRouteParam(req, "id", "main")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Retire(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminCommandDispatchResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, adminCommandActionConfigPatch, payload.Action)

	_, err = os.Stat(filepath.Join(repoPath, "agents", "main"))
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(repoPath, "agents", "_retired", "main"))
	require.NoError(t, err)

	var status string
	err = db.QueryRow(`SELECT status FROM agents WHERE org_id = $1 AND slug = 'main'`, orgID).Scan(&status)
	require.NoError(t, err)
	require.Equal(t, "retired", status)

	require.Len(t, dispatcher.calls, 1)
	event, ok := dispatcher.calls[0].(openClawAdminCommandEvent)
	require.True(t, ok)
	require.JSONEq(t, `{"agents":{"main":{"enabled":false}}}`, string(event.Data.ConfigPatch))
}

func TestAdminAgentsLifecycleReactivateRestoresFilesAndDispatchesEnable(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agents-reactivate")
	_, err := db.Exec(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'main', 'Frank', 'retired')`,
		orgID,
	)
	require.NoError(t, err)
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

	retireReq := httptest.NewRequest(http.MethodPost, "/api/admin/agents/main/retire", nil)
	retireReq = addRouteParam(retireReq, "id", "main")
	retireReq = retireReq.WithContext(context.WithValue(retireReq.Context(), middleware.WorkspaceIDKey, orgID))
	retireRec := httptest.NewRecorder()
	handler.Retire(retireRec, retireReq)
	require.Equal(t, http.StatusOK, retireRec.Code)
	dispatcher.calls = nil

	req := httptest.NewRequest(http.MethodPost, "/api/admin/agents/main/reactivate", nil)
	req = addRouteParam(req, "id", "main")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Reactivate(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminCommandDispatchResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, adminCommandActionConfigPatch, payload.Action)

	_, err = os.Stat(filepath.Join(repoPath, "agents", "main"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(repoPath, "agents", "_retired", "main"))
	require.Error(t, err)

	var status string
	err = db.QueryRow(`SELECT status FROM agents WHERE org_id = $1 AND slug = 'main'`, orgID).Scan(&status)
	require.NoError(t, err)
	require.Equal(t, "active", status)

	require.Len(t, dispatcher.calls, 1)
	event, ok := dispatcher.calls[0].(openClawAdminCommandEvent)
	require.True(t, ok)
	require.JSONEq(t, `{"agents":{"main":{"enabled":true}}}`, string(event.Data.ConfigPatch))
}

func TestAdminAgentsLifecycleRetireRejectsMissingAgent(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agents-retire-missing")
	handler := &AdminAgentsHandler{
		DB:           db,
		Store:        store.NewAgentStore(db),
		ProjectStore: store.NewProjectStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/agents/missing/retire", nil)
	req = addRouteParam(req, "id", "missing")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Retire(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAdminAgentsLifecycleReactivateRejectsMissingArchive(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agents-reactivate-missing-archive")
	_, err := db.Exec(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'main', 'Frank', 'retired')`,
		orgID,
	)
	require.NoError(t, err)
	_ = seedAdminAgentFilesProjectFixture(t, db, orgID, "main")
	handler := &AdminAgentsHandler{
		DB:              db,
		Store:           store.NewAgentStore(db),
		ProjectStore:    store.NewProjectStore(db),
		ProjectRepos:    store.NewProjectRepoStore(db),
		OpenClawHandler: &fakeOpenClawConnectionStatus{connected: true},
		EventStore:      store.NewConnectionEventStore(db),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/agents/main/reactivate", nil)
	req = addRouteParam(req, "id", "main")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Reactivate(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Contains(t, payload.Error, "retired agent archive is missing")
}
