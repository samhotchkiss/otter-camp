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

func seedLifecycleAgentFilesDir(t *testing.T, repoPath, slug string) {
	t.Helper()
	baseDir := filepath.Join(repoPath, "agents", slug)
	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, "memory"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "SOUL.md"), []byte("# SOUL\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "IDENTITY.md"), []byte("# IDENTITY\n"), 0o644))
}

func TestAdminAgentsLifecycleRetireMovesFilesWithoutOpenClawPatch(t *testing.T) {
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

	var payload map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, true, payload["ok"])
	require.Equal(t, "main", payload["agent"])
	require.Equal(t, "retired", payload["status"])
	require.Equal(t, false, payload["openclaw_config_modified"])

	_, err = os.Stat(filepath.Join(repoPath, "agents", "main"))
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(repoPath, "agents", "_retired", "main"))
	require.NoError(t, err)

	var status string
	err = db.QueryRow(`SELECT status FROM agents WHERE org_id = $1 AND slug = 'main'`, orgID).Scan(&status)
	require.NoError(t, err)
	require.Equal(t, "retired", status)

	require.Empty(t, dispatcher.calls)
}

func TestAdminAgentsLifecycleReactivateRestoresFilesWithoutOpenClawPatch(t *testing.T) {
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

	var payload map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, true, payload["ok"])
	require.Equal(t, "main", payload["agent"])
	require.Equal(t, "active", payload["status"])
	require.Equal(t, false, payload["openclaw_config_modified"])

	_, err = os.Stat(filepath.Join(repoPath, "agents", "main"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(repoPath, "agents", "_retired", "main"))
	require.Error(t, err)

	var status string
	err = db.QueryRow(`SELECT status FROM agents WHERE org_id = $1 AND slug = 'main'`, orgID).Scan(&status)
	require.NoError(t, err)
	require.Equal(t, "active", status)

	require.Empty(t, dispatcher.calls)
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

func TestAdminAgentsLifecycleRetireRejectsProtectedElephant(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agents-retire-protected-elephant")
	_, err := db.Exec(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'elephant', 'Elephant', 'active')`,
		orgID,
	)
	require.NoError(t, err)

	handler := &AdminAgentsHandler{
		DB:           db,
		Store:        store.NewAgentStore(db),
		ProjectStore: store.NewProjectStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/agents/elephant/retire", nil)
	req = addRouteParam(req, "id", "elephant")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Retire(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Contains(t, payload.Error, "protected system agents cannot be retired")
}

func TestAdminAgentsBulkRetireByProjectRetiresOnlyEphemeralTemps(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agents-retire-by-project")

	var projectID string
	err := db.QueryRow(
		`INSERT INTO projects (org_id, name, status) VALUES ($1, 'Temps Project', 'active') RETURNING id`,
		orgID,
	).Scan(&projectID)
	require.NoError(t, err)

	var otherProjectID string
	err = db.QueryRow(
		`INSERT INTO projects (org_id, name, status) VALUES ($1, 'Other Project', 'active') RETURNING id`,
		orgID,
	).Scan(&otherProjectID)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO agents (org_id, slug, display_name, status, is_ephemeral, project_id)
		 VALUES
		   ($1, 'temp-a', 'Temp A', 'active', true, $2),
		   ($1, 'temp-b', 'Temp B', 'active', true, $2),
		   ($1, 'perm-a', 'Perm A', 'active', false, $2),
		   ($1, 'temp-c', 'Temp C', 'active', true, $3)`,
		orgID,
		projectID,
		otherProjectID,
	)
	require.NoError(t, err)

	agentFilesProjectID := seedAdminAgentFilesProjectFixture(t, db, orgID, "temp-a")
	repoPath := agentFilesRepoPathForProject(t, db, orgID, agentFilesProjectID)
	seedLifecycleAgentFilesDir(t, repoPath, "temp-b")
	seedLifecycleAgentFilesDir(t, repoPath, "perm-a")
	seedLifecycleAgentFilesDir(t, repoPath, "temp-c")
	runGitTest(t, repoPath, "add", ".")
	runGitTest(t, repoPath, "commit", "-m", "seed lifecycle agent dirs")

	handler := &AdminAgentsHandler{
		DB:              db,
		Store:           store.NewAgentStore(db),
		ProjectStore:    store.NewProjectStore(db),
		ProjectRepos:    store.NewProjectRepoStore(db),
		OpenClawHandler: &fakeOpenClawConnectionStatus{connected: true},
		EventStore:      store.NewConnectionEventStore(db),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/agents/retire/project/"+projectID, nil)
	req = addRouteParam(req, "projectID", projectID)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.RetireByProject(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload struct {
		OK        bool   `json:"ok"`
		Total     int    `json:"total"`
		Retired   int    `json:"retired"`
		Failed    int    `json:"failed"`
		ProjectID string `json:"project_id"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.True(t, payload.OK)
	require.Equal(t, projectID, payload.ProjectID)
	require.Equal(t, 2, payload.Total)
	require.Equal(t, 2, payload.Retired)
	require.Equal(t, 0, payload.Failed)

	for _, slug := range []string{"temp-a", "temp-b"} {
		var status string
		require.NoError(t, db.QueryRow(`SELECT status FROM agents WHERE org_id = $1 AND slug = $2`, orgID, slug).Scan(&status))
		require.Equal(t, "retired", status)
		_, statErr := os.Stat(filepath.Join(repoPath, "agents", "_retired", slug))
		require.NoError(t, statErr)
	}

	var permStatus string
	require.NoError(t, db.QueryRow(`SELECT status FROM agents WHERE org_id = $1 AND slug = 'perm-a'`, orgID).Scan(&permStatus))
	require.Equal(t, "active", permStatus)

	var otherStatus string
	require.NoError(t, db.QueryRow(`SELECT status FROM agents WHERE org_id = $1 AND slug = 'temp-c'`, orgID).Scan(&otherStatus))
	require.Equal(t, "active", otherStatus)
}
