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

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func newProjectTreeTestRouter(handler *ProjectTreeHandler) http.Handler {
	router := chi.NewRouter()
	router.With(middleware.OptionalWorkspace).Get("/api/projects/{id}/tree", handler.GetTree)
	return router
}

func seedProjectTreeRepoBinding(
	t *testing.T,
	db *sql.DB,
	orgID string,
	projectID string,
	localRepoPath string,
) {
	t.Helper()
	repoStore := store.NewProjectRepoStore(db)
	ctx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
	_, err := repoStore.UpsertBinding(ctx, store.UpsertProjectRepoBindingInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		DefaultBranch:      "main",
		LocalRepoPath:      &localRepoPath,
		Enabled:            true,
		SyncMode:           store.RepoSyncModeSync,
		AutoSync:           true,
		ConflictState:      store.RepoConflictNone,
	})
	require.NoError(t, err)
}

func seedProjectTreeFixtureRepo(t *testing.T, localRepoPath string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(localRepoPath, "posts", "archive"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(localRepoPath, "posts", "2026-02-07-tree-test.md"),
		[]byte("# Tree test\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(localRepoPath, "posts", "archive", "2025-12-31-old.md"),
		[]byte("# Old post\n"),
		0o644,
	))
	require.NoError(t, os.MkdirAll(filepath.Join(localRepoPath, "notes"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(localRepoPath, "notes", "todo.txt"), []byte("todo"), 0o644))
	runGitTest(t, localRepoPath, "add", ".")
	runGitTest(t, localRepoPath, "commit", "-m", "seed file browser tree fixtures")
}

func TestProjectTreeHandlerListsRootAndSubdirectories(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-tree-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Tree")
	fixture := newPublishRepoFixture(t)
	seedProjectTreeFixtureRepo(t, fixture.LocalPath)
	seedProjectTreeRepoBinding(t, db, orgID, projectID, fixture.LocalPath)

	handler := &ProjectTreeHandler{
		ProjectStore: store.NewProjectStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	}
	router := newProjectTreeTestRouter(handler)

	rootReq := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/tree?org_id="+orgID+"&ref=main&path=/",
		nil,
	)
	rootRec := httptest.NewRecorder()
	router.ServeHTTP(rootRec, rootReq)
	require.Equal(t, http.StatusOK, rootRec.Code)

	var rootPayload projectTreeResponse
	require.NoError(t, json.NewDecoder(rootRec.Body).Decode(&rootPayload))
	require.Equal(t, "main", rootPayload.Ref)
	require.Equal(t, "/", rootPayload.Path)
	require.NotEmpty(t, rootPayload.Entries)
	require.Contains(t, rootPayload.Entries, projectTreeEntry{Name: "posts", Type: "dir", Path: "posts/"})
	require.Contains(t, rootPayload.Entries, projectTreeEntry{Name: "notes", Type: "dir", Path: "notes/"})

	subReq := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/tree?org_id="+orgID+"&ref=main&path=/posts",
		nil,
	)
	subRec := httptest.NewRecorder()
	router.ServeHTTP(subRec, subReq)
	require.Equal(t, http.StatusOK, subRec.Code)

	var subPayload projectTreeResponse
	require.NoError(t, json.NewDecoder(subRec.Body).Decode(&subPayload))
	require.Equal(t, "/posts", subPayload.Path)

	paths := make([]string, 0, len(subPayload.Entries))
	for _, entry := range subPayload.Entries {
		paths = append(paths, entry.Path)
	}
	require.Contains(t, paths, "posts/2026-02-07-tree-test.md")
	require.Contains(t, paths, "posts/archive/")
	require.NotContains(t, paths, "posts/archive/2025-12-31-old.md")
}

func TestProjectTreeHandlerRejectsTraversalPath(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-tree-traversal-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Tree Traversal")
	fixture := newPublishRepoFixture(t)
	seedProjectTreeRepoBinding(t, db, orgID, projectID, fixture.LocalPath)

	handler := &ProjectTreeHandler{
		ProjectStore: store.NewProjectStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	}
	router := newProjectTreeTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/tree?org_id="+orgID+"&path=../../etc/passwd",
		nil,
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Contains(t, payload.Error, "traversal")
}

func TestProjectTreeHandlerReturnsConflictForInvalidRepoPath(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-tree-invalid-repo-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Tree Invalid Repo")
	missingRepoPath := filepath.Join(t.TempDir(), "does-not-exist")
	seedProjectTreeRepoBinding(t, db, orgID, projectID, missingRepoPath)

	handler := &ProjectTreeHandler{
		ProjectStore: store.NewProjectStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	}
	router := newProjectTreeTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/tree?org_id="+orgID,
		nil,
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Contains(t, payload.Error, "does not exist")
}

func TestProjectTreeHandlerRouteIsRegistered(t *testing.T) {
	router := newProjectTreeTestRouter(&ProjectTreeHandler{})
	req := httptest.NewRequest(http.MethodGet, "/api/projects/project-1/tree?org_id=org-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.NotEqual(t, http.StatusNotFound, rec.Code)
}
