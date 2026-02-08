package api

import (
	"context"
	"database/sql"
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

func seedAdminAgentFilesProjectFixture(
	t *testing.T,
	db *sql.DB,
	orgID string,
	agentSlug string,
) string {
	t.Helper()
	projectID := insertProjectTestProject(t, db, orgID, "Agent Files")
	fixture := newPublishRepoFixture(t)

	require.NoError(t, os.MkdirAll(filepath.Join(fixture.LocalPath, "agents", agentSlug, "memory"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(fixture.LocalPath, "agents", agentSlug, "SOUL.md"),
		[]byte("# SOUL\nYou are calm and practical.\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(fixture.LocalPath, "agents", agentSlug, "IDENTITY.md"),
		[]byte("# IDENTITY\n- Name: Frank\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(fixture.LocalPath, "agents", agentSlug, "memory", "2026-02-07.md"),
		[]byte("# Memory 2026-02-07\n- Reviewed queue\n"),
		0o644,
	))
	require.NoError(t, os.MkdirAll(filepath.Join(fixture.LocalPath, "agents", "other-agent"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(fixture.LocalPath, "agents", "other-agent", "SOUL.md"),
		[]byte("# SOUL\nOther agent\n"),
		0o644,
	))
	runGitTest(t, fixture.LocalPath, "add", ".")
	runGitTest(t, fixture.LocalPath, "commit", "-m", "seed agent files fixture")

	seedProjectTreeRepoBinding(t, db, orgID, projectID, fixture.LocalPath)
	return projectID
}

func TestAdminAgentFilesListIdentityFiles(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agent-files-list")
	_, err := db.Exec(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'main', 'Frank', 'active')`,
		orgID,
	)
	require.NoError(t, err)

	_ = seedAdminAgentFilesProjectFixture(t, db, orgID, "main")

	handler := &AdminAgentsHandler{
		DB:           db,
		Store:        store.NewAgentStore(db),
		ProjectStore: store.NewProjectStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/agents/main/files", nil)
	req = addRouteParam(req, "id", "main")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))

	rec := httptest.NewRecorder()
	handler.ListFiles(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminAgentFilesListResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "/", payload.Path)
	require.Equal(t, "HEAD", payload.Ref)
	require.NotEmpty(t, payload.Entries)

	paths := make([]string, 0, len(payload.Entries))
	for _, entry := range payload.Entries {
		paths = append(paths, entry.Path)
	}
	require.Contains(t, paths, "SOUL.md")
	require.Contains(t, paths, "IDENTITY.md")
	require.Contains(t, paths, "memory/")
	require.NotContains(t, paths, "../")
}

func TestAdminAgentFilesGetIdentityFile(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agent-files-get")
	_, err := db.Exec(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'main', 'Frank', 'active')`,
		orgID,
	)
	require.NoError(t, err)
	_ = seedAdminAgentFilesProjectFixture(t, db, orgID, "main")

	handler := &AdminAgentsHandler{
		DB:           db,
		Store:        store.NewAgentStore(db),
		ProjectStore: store.NewProjectStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/agents/main/files/SOUL.md", nil)
	req = addRouteParam(req, "id", "main")
	req = addRouteParam(req, "path", "SOUL.md")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))

	rec := httptest.NewRecorder()
	handler.GetFile(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload projectBlobResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "/SOUL.md", payload.Path)
	require.Equal(t, "utf-8", payload.Encoding)
	require.Contains(t, payload.Content, "calm and practical")
}

func TestAdminAgentFilesListMemoryFiles(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agent-memory-list")
	_, err := db.Exec(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'main', 'Frank', 'active')`,
		orgID,
	)
	require.NoError(t, err)
	_ = seedAdminAgentFilesProjectFixture(t, db, orgID, "main")

	handler := &AdminAgentsHandler{
		DB:           db,
		Store:        store.NewAgentStore(db),
		ProjectStore: store.NewProjectStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/agents/main/memory", nil)
	req = addRouteParam(req, "id", "main")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))

	rec := httptest.NewRecorder()
	handler.ListMemoryFiles(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminAgentFilesListResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "/memory", payload.Path)
	require.Len(t, payload.Entries, 1)
	require.Equal(t, "2026-02-07.md", payload.Entries[0].Path)
}

func TestAdminAgentFilesGetMemoryFileByDate(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agent-memory-date")
	_, err := db.Exec(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'main', 'Frank', 'active')`,
		orgID,
	)
	require.NoError(t, err)
	_ = seedAdminAgentFilesProjectFixture(t, db, orgID, "main")

	handler := &AdminAgentsHandler{
		DB:           db,
		Store:        store.NewAgentStore(db),
		ProjectStore: store.NewProjectStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/agents/main/memory/2026-02-07", nil)
	req = addRouteParam(req, "id", "main")
	req = addRouteParam(req, "date", "2026-02-07")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))

	rec := httptest.NewRecorder()
	handler.GetMemoryFileByDate(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload projectBlobResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "/memory/2026-02-07.md", payload.Path)
	require.Contains(t, payload.Content, "Reviewed queue")
}

func TestAdminAgentFilesRejectsPathTraversal(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agent-files-traversal")
	_, err := db.Exec(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'main', 'Frank', 'active')`,
		orgID,
	)
	require.NoError(t, err)
	_ = seedAdminAgentFilesProjectFixture(t, db, orgID, "main")

	handler := &AdminAgentsHandler{
		DB:           db,
		Store:        store.NewAgentStore(db),
		ProjectStore: store.NewProjectStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/agents/main/files/../secret", nil)
	req = addRouteParam(req, "id", "main")
	req = addRouteParam(req, "path", "../secret")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))

	rec := httptest.NewRecorder()
	handler.GetFile(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAdminAgentFilesReturnsConflictWhenAgentFilesProjectUnbound(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-agent-files-unbound")
	_, err := db.Exec(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'main', 'Frank', 'active')`,
		orgID,
	)
	require.NoError(t, err)

	handler := &AdminAgentsHandler{
		DB:           db,
		Store:        store.NewAgentStore(db),
		ProjectStore: store.NewProjectStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/agents/main/files", nil)
	req = addRouteParam(req, "id", "main")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))

	rec := httptest.NewRecorder()
	handler.ListFiles(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Contains(t, payload.Error, "Agent Files project")
}
