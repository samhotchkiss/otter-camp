package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func writeProjectContentTestFile(t *testing.T, path, content string, modifiedAt time.Time) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	require.NoError(t, os.Chtimes(path, modifiedAt, modifiedAt))
}

func TestProjectContentSearchReturnsPostsAndNotesOnly(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-content-search-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Content Search")

	root := t.TempDir()
	t.Setenv("OTTER_CONTENT_ROOT", root)

	postPath := filepath.Join(root, projectID, "posts", "launch-plan.md")
	notePath := filepath.Join(root, projectID, "notes", "research.md")
	assetPath := filepath.Join(root, projectID, "assets", "ignored.md")

	postTime := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	noteTime := time.Date(2026, 2, 3, 15, 0, 0, 0, time.UTC)

	writeProjectContentTestFile(t, postPath, "---\nauthor: Sam\n---\n# Launch Plan\nLaunch plan draft body.", postTime)
	writeProjectContentTestFile(t, notePath, "<!-- ottercamp_project_chat_source author=Stone -->\nLaunch notes and checklist.", noteTime)
	writeProjectContentTestFile(t, assetPath, "Launch term in assets should be ignored.", noteTime)

	handler := &ProjectChatHandler{ProjectStore: store.NewProjectStore(db)}
	router := newProjectChatTestRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/content/search?org_id="+orgID+"&q=launch", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp projectContentSearchResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, 2, resp.Total)
	paths := []string{resp.Items[0].Path, resp.Items[1].Path}
	require.Contains(t, paths, "/posts/launch-plan.md")
	require.Contains(t, paths, "/notes/research.md")
	for _, item := range resp.Items {
		require.NotContains(t, item.Path, "/assets/")
	}

	postsOnlyReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/content/search?org_id="+orgID+"&q=launch&scope=posts", nil)
	postsOnlyRec := httptest.NewRecorder()
	router.ServeHTTP(postsOnlyRec, postsOnlyReq)
	require.Equal(t, http.StatusOK, postsOnlyRec.Code)
	require.NoError(t, json.NewDecoder(postsOnlyRec.Body).Decode(&resp))
	require.Equal(t, 1, resp.Total)
	require.Equal(t, "/posts/launch-plan.md", resp.Items[0].Path)

	authorReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/content/search?org_id="+orgID+"&q=launch&author=Sam", nil)
	authorRec := httptest.NewRecorder()
	router.ServeHTTP(authorRec, authorReq)
	require.Equal(t, http.StatusOK, authorRec.Code)
	require.NoError(t, json.NewDecoder(authorRec.Body).Decode(&resp))
	require.Equal(t, 1, resp.Total)
	require.Equal(t, "/posts/launch-plan.md", resp.Items[0].Path)

	fromReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/content/search?org_id="+orgID+"&q=launch&from=2026-02-02", nil)
	fromRec := httptest.NewRecorder()
	router.ServeHTTP(fromRec, fromReq)
	require.Equal(t, http.StatusOK, fromRec.Code)
	require.NoError(t, json.NewDecoder(fromRec.Body).Decode(&resp))
	require.Equal(t, 1, resp.Total)
	require.Equal(t, "/notes/research.md", resp.Items[0].Path)
}

func TestProjectContentSearchValidatesInputs(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-content-search-validate-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Content Search Validate")

	handler := &ProjectChatHandler{ProjectStore: store.NewProjectStore(db)}
	router := newProjectChatTestRouter(handler)

	missingQuery := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/content/search?org_id="+orgID, nil)
	missingQueryRec := httptest.NewRecorder()
	router.ServeHTTP(missingQueryRec, missingQuery)
	require.Equal(t, http.StatusBadRequest, missingQueryRec.Code)

	invalidScope := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/content/search?org_id="+orgID+"&q=test&scope=assets", nil)
	invalidScopeRec := httptest.NewRecorder()
	router.ServeHTTP(invalidScopeRec, invalidScope)
	require.Equal(t, http.StatusBadRequest, invalidScopeRec.Code)

	invalidDates := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/content/search?org_id="+orgID+"&q=test&from=2026-02-07&to=2026-02-01", nil)
	invalidDatesRec := httptest.NewRecorder()
	router.ServeHTTP(invalidDatesRec, invalidDates)
	require.Equal(t, http.StatusBadRequest, invalidDatesRec.Code)
}
