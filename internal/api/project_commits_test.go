package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func newProjectCommitsTestRouter(handler *ProjectCommitsHandler) http.Handler {
	router := chi.NewRouter()
	router.With(middleware.OptionalWorkspace).Get("/api/projects/{id}/commits", handler.List)
	router.With(middleware.OptionalWorkspace).Get("/api/projects/{id}/commits/{sha}", handler.Get)
	router.With(middleware.OptionalWorkspace).Get("/api/projects/{id}/commits/{sha}/diff", handler.Diff)
	return router
}

func seedProjectCommitTestData(t *testing.T, db *sql.DB, orgID string) (string, string, string, string) {
	t.Helper()
	projectID := insertProjectTestProject(t, db, orgID, "Project Commits API")
	commitStore := store.NewProjectCommitStore(db)
	ctx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)

	older := time.Date(2026, 2, 6, 10, 0, 0, 0, time.UTC)
	oldMeta := json.RawMessage(`{
		"files": [
			{"filename":"docs/old.md","status":"modified","patch":"@@ -1 +1 @@\n-old\n+old"}
		]
	}`)
	_, _, err := commitStore.UpsertCommit(ctx, store.UpsertProjectCommitInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		BranchName:         "main",
		SHA:                "sha-main-old",
		AuthorName:         "Sam",
		AuthorEmail:        commitTestStringPtr("sam@example.com"),
		AuthoredAt:         &older,
		Subject:            "Old main commit",
		Message:            "Old main commit",
		Metadata:           oldMeta,
	})
	require.NoError(t, err)

	newer := older.Add(1 * time.Hour)
	newMeta := json.RawMessage(`{
		"added": [" docs/new.md "],
		"modified": ["src/main.go", " src/helper.go "],
		"removed": [" tmp/unused.txt "]
	}`)
	_, _, err = commitStore.UpsertCommit(ctx, store.UpsertProjectCommitInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		BranchName:         "main",
		SHA:                "sha-main-new",
		AuthorName:         "Stone",
		AuthorEmail:        commitTestStringPtr("stone@example.com"),
		AuthoredAt:         &newer,
		Subject:            "New main commit",
		Body:               commitTestStringPtr("Verbose body for main commit"),
		Message:            "New main commit\n\nVerbose body for main commit",
		Metadata:           newMeta,
	})
	require.NoError(t, err)

	featureTime := newer.Add(10 * time.Minute)
	featureMeta := json.RawMessage(`{
		"files": [
			{"filename":"src/new_file.go","status":"added","patch":"@@ -0,0 +1 @@\n+package main"},
			{"filename":"src/main.go","status":"modified","patch":"@@ -1 +1 @@\n-old\n+new"},
			{"filename":"src/legacy.go","status":"removed","patch":"@@ -1 +0,0 @@\n-package legacy"},
			{"filename":"src/renamed.go","status":"renamed","patch":"@@ -1 +1 @@\n-old-name\n+new-name"}
		]
	}`)
	_, _, err = commitStore.UpsertCommit(ctx, store.UpsertProjectCommitInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		BranchName:         "feature/review",
		SHA:                "sha-feature",
		AuthorName:         "Kam",
		AuthoredAt:         &featureTime,
		Subject:            "Feature commit",
		Message:            "Feature commit",
		Metadata:           featureMeta,
	})
	require.NoError(t, err)

	return projectID, "sha-main-new", "sha-main-old", "sha-feature"
}

func TestProjectCommitsHandlerListReturnsOrderedBranchFilteredPagination(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-commits-list-org")
	projectID, newestSHA, oldestSHA, _ := seedProjectCommitTestData(t, db, orgID)

	handler := &ProjectCommitsHandler{
		ProjectStore: store.NewProjectStore(db),
		CommitStore:  store.NewProjectCommitStore(db),
	}
	router := newProjectCommitsTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/commits?org_id="+orgID+"&branch=main&limit=1&offset=0",
		nil,
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var firstPage projectCommitListResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&firstPage))
	require.Len(t, firstPage.Items, 1)
	require.Equal(t, newestSHA, firstPage.Items[0].SHA)
	require.Equal(t, "main", firstPage.Items[0].BranchName)
	require.True(t, firstPage.HasMore)
	require.NotNil(t, firstPage.NextOffset)
	require.Equal(t, 1, *firstPage.NextOffset)
	require.Equal(t, 1, firstPage.Limit)
	require.Equal(t, 0, firstPage.Offset)

	req = httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/commits?org_id="+orgID+"&branch=main&limit=1&offset=1",
		nil,
	)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var secondPage projectCommitListResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&secondPage))
	require.Len(t, secondPage.Items, 1)
	require.Equal(t, oldestSHA, secondPage.Items[0].SHA)
	require.False(t, secondPage.HasMore)
	require.Nil(t, secondPage.NextOffset)
	require.Equal(t, 1, secondPage.Offset)
}

func TestProjectCommitsHandlerGetReturnsCommitDetail(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-commits-get-org")
	projectID, newestSHA, _, _ := seedProjectCommitTestData(t, db, orgID)

	handler := &ProjectCommitsHandler{
		ProjectStore: store.NewProjectStore(db),
		CommitStore:  store.NewProjectCommitStore(db),
	}
	router := newProjectCommitsTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/commits/"+newestSHA+"?org_id="+orgID,
		nil,
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var payload projectCommitPayload
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, newestSHA, payload.SHA)
	require.Equal(t, "New main commit", payload.Subject)
	require.Equal(t, "Stone", payload.AuthorName)
	require.NotNil(t, payload.AuthorEmail)
	require.Equal(t, "stone@example.com", *payload.AuthorEmail)
	require.NotNil(t, payload.Body)
	require.Equal(t, "Verbose body for main commit", *payload.Body)
	require.NotEmpty(t, payload.AuthoredAt)
	require.NotEmpty(t, payload.CreatedAt)
	require.NotEmpty(t, payload.UpdatedAt)
	_, err := time.Parse(time.RFC3339, payload.AuthoredAt)
	require.NoError(t, err)
}

func TestProjectCommitsHandlerDiffReturnsNormalizedPatchPayload(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-commits-diff-org")
	projectID, _, _, featureSHA := seedProjectCommitTestData(t, db, orgID)

	handler := &ProjectCommitsHandler{
		ProjectStore: store.NewProjectStore(db),
		CommitStore:  store.NewProjectCommitStore(db),
	}
	router := newProjectCommitsTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/commits/"+featureSHA+"/diff?org_id="+orgID,
		nil,
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var payload projectCommitDiffResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, featureSHA, payload.SHA)
	require.Equal(t, 4, payload.Total)
	require.Len(t, payload.Files, 4)

	require.Equal(t, "src/new_file.go", payload.Files[0].Path)
	require.Equal(t, "added", payload.Files[0].ChangeType)
	require.NotNil(t, payload.Files[0].Patch)
	require.Contains(t, *payload.Files[0].Patch, "+package main")

	require.Equal(t, "src/main.go", payload.Files[1].Path)
	require.Equal(t, "modified", payload.Files[1].ChangeType)
	require.NotNil(t, payload.Files[1].Patch)
	require.Contains(t, *payload.Files[1].Patch, "+new")

	require.Equal(t, "src/legacy.go", payload.Files[2].Path)
	require.Equal(t, "removed", payload.Files[2].ChangeType)
	require.NotNil(t, payload.Files[2].Patch)
	require.Contains(t, *payload.Files[2].Patch, "-package legacy")

	require.Equal(t, "src/renamed.go", payload.Files[3].Path)
	require.Equal(t, "renamed", payload.Files[3].ChangeType)
	require.NotNil(t, payload.Files[3].Patch)
	require.Contains(t, *payload.Files[3].Patch, "+new-name")
}

func TestProjectCommitsHandlerBlocksCrossOrgProjectAccess(t *testing.T) {
	db := setupMessageTestDB(t)
	orgA := insertMessageTestOrganization(t, db, "project-commits-iso-a")
	orgB := insertMessageTestOrganization(t, db, "project-commits-iso-b")
	projectB, _, _, featureSHA := seedProjectCommitTestData(t, db, orgB)

	handler := &ProjectCommitsHandler{
		ProjectStore: store.NewProjectStore(db),
		CommitStore:  store.NewProjectCommitStore(db),
	}
	router := newProjectCommitsTestRouter(handler)

	listReq := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectB+"/commits?org_id="+orgA,
		nil,
	)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	require.Contains(t, []int{http.StatusForbidden, http.StatusNotFound}, listRec.Code)

	getReq := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectB+"/commits/"+featureSHA+"?org_id="+orgA,
		nil,
	)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	require.Contains(t, []int{http.StatusForbidden, http.StatusNotFound}, getRec.Code)

	diffReq := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectB+"/commits/"+featureSHA+"/diff?org_id="+orgA,
		nil,
	)
	diffRec := httptest.NewRecorder()
	router.ServeHTTP(diffRec, diffReq)
	require.Contains(t, []int{http.StatusForbidden, http.StatusNotFound}, diffRec.Code)
}

func commitTestStringPtr(value string) *string {
	return &value
}
