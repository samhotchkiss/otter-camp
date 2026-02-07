package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func TestProjectContentRenameReconcilesLinkedIssuePathAndLogsHistory(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-content-rename-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Content Rename")

	root := t.TempDir()
	t.Setenv("OTTER_CONTENT_ROOT", root)

	fromPath := "/posts/2026-02-06-old-title.md"
	toPath := "/posts/2026-02-07-new-title.md"
	fromAbsolute := filepath.Join(root, projectID, "posts", "2026-02-06-old-title.md")
	writeProjectContentTestFile(t, fromAbsolute, "# old", time.Now().UTC())

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(testCtxWithWorkspace(orgID), store.CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "Linked issue",
		Origin:       "local",
		DocumentPath: &fromPath,
	})
	require.NoError(t, err)

	handler := &ProjectChatHandler{
		ProjectStore: store.NewProjectStore(db),
		IssueStore:   issueStore,
		DB:           db,
	}
	router := newProjectChatTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/content/rename?org_id="+orgID,
		bytes.NewReader([]byte(`{"from_path":"`+fromPath+`","to_path":"`+toPath+`"}`)),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp projectContentRenameResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, fromPath, resp.FromPath)
	require.Equal(t, toPath, resp.ToPath)
	require.Equal(t, 1, resp.LinkedIssuesUpdated)

	_, err = os.Stat(fromAbsolute)
	require.True(t, errors.Is(err, os.ErrNotExist))
	_, err = os.Stat(filepath.Join(root, projectID, "posts", "2026-02-07-new-title.md"))
	require.NoError(t, err)

	updated, err := issueStore.GetIssueByID(testCtxWithWorkspace(orgID), issue.ID)
	require.NoError(t, err)
	require.NotNil(t, updated.DocumentPath)
	require.Equal(t, toPath, *updated.DocumentPath)

	var historyCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM activity_log WHERE org_id = $1 AND project_id = $2 AND action = 'issue.document_link_renamed'`,
		orgID,
		projectID,
	).Scan(&historyCount)
	require.NoError(t, err)
	require.Equal(t, 1, historyCount)
}

func TestProjectContentDeleteDetachesLinkedIssueWithWarningMetadata(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-content-delete-detach-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Content Delete")

	root := t.TempDir()
	t.Setenv("OTTER_CONTENT_ROOT", root)

	path := "/posts/2026-02-08-delete-me.md"
	absolute := filepath.Join(root, projectID, "posts", "2026-02-08-delete-me.md")
	writeProjectContentTestFile(t, absolute, "# delete me", time.Now().UTC())

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(testCtxWithWorkspace(orgID), store.CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "Delete-linked issue",
		Origin:       "local",
		DocumentPath: &path,
	})
	require.NoError(t, err)

	handler := &ProjectChatHandler{
		ProjectStore: store.NewProjectStore(db),
		IssueStore:   issueStore,
		DB:           db,
	}
	router := newProjectChatTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/content/delete?org_id="+orgID,
		bytes.NewReader([]byte(`{"path":"`+path+`","hard_delete":false}`)),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp projectContentDeleteResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, path, resp.Path)
	require.True(t, resp.Deleted)
	require.Equal(t, 1, resp.DetachedIssues)
	require.NotNil(t, resp.Warning)
	require.NotEmpty(t, *resp.Warning)

	updated, err := issueStore.GetIssueByID(testCtxWithWorkspace(orgID), issue.ID)
	require.NoError(t, err)
	require.Nil(t, updated.DocumentPath)

	var warning string
	err = db.QueryRow(
		`SELECT metadata->>'warning' FROM activity_log
			WHERE org_id = $1 AND project_id = $2 AND action = 'issue.document_link_detached'
			ORDER BY created_at DESC LIMIT 1`,
		orgID,
		projectID,
	).Scan(&warning)
	require.NoError(t, err)
	require.Contains(t, warning, "detached")
}

func TestProjectContentDeleteRejectsHardDeleteForActiveLinkedIssue(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-content-delete-hard-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Content Hard Delete")

	root := t.TempDir()
	t.Setenv("OTTER_CONTENT_ROOT", root)

	path := "/posts/2026-02-09-hard-delete.md"
	absolute := filepath.Join(root, projectID, "posts", "2026-02-09-hard-delete.md")
	writeProjectContentTestFile(t, absolute, "# hard delete", time.Now().UTC())

	issueStore := store.NewProjectIssueStore(db)
	_, err := issueStore.CreateIssue(testCtxWithWorkspace(orgID), store.CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "Active linked issue",
		Origin:       "local",
		DocumentPath: &path,
	})
	require.NoError(t, err)

	handler := &ProjectChatHandler{
		ProjectStore: store.NewProjectStore(db),
		IssueStore:   issueStore,
		DB:           db,
	}
	router := newProjectChatTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/content/delete?org_id="+orgID,
		bytes.NewReader([]byte(`{"path":"`+path+`","hard_delete":true}`)),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)

	_, err = os.Stat(absolute)
	require.NoError(t, err)
}

func TestProjectContentDeleteWorksForUnlinkedFile(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-content-delete-unlinked-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Content Unlinked Delete")

	root := t.TempDir()
	t.Setenv("OTTER_CONTENT_ROOT", root)

	path := "/posts/2026-02-10-unlinked.md"
	absolute := filepath.Join(root, projectID, "posts", "2026-02-10-unlinked.md")
	writeProjectContentTestFile(t, absolute, "# unlinked", time.Now().UTC())

	handler := &ProjectChatHandler{
		ProjectStore: store.NewProjectStore(db),
		IssueStore:   store.NewProjectIssueStore(db),
		DB:           db,
	}
	router := newProjectChatTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/content/delete?org_id="+orgID,
		bytes.NewReader([]byte(`{"path":"`+path+`"}`)),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp projectContentDeleteResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.True(t, resp.Deleted)
	require.Equal(t, 0, resp.DetachedIssues)
	require.Nil(t, resp.Warning)

	_, err := os.Stat(absolute)
	require.True(t, errors.Is(err, os.ErrNotExist))
}
