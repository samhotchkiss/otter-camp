package api

import (
	"bytes"
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
	router.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/commits", handler.Create)
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

func TestProjectCommitsHandlerCreateSupportsExplicitAndGeneratedMessages(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-commits-create-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Commits Create API")
	fixture := newPublishRepoFixture(t)
	seedProjectCommitRepoBinding(t, db, orgID, projectID, fixture.LocalPath)

	handler := &ProjectCommitsHandler{
		ProjectStore: store.NewProjectStore(db),
		CommitStore:  store.NewProjectCommitStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	}
	router := newProjectCommitsTestRouter(handler)

	explicitBody := []byte(`{
		"path": "/posts/2026-02-06-launch-plan.md",
		"content": "# Launch Plan\n\nFirst draft copy.",
		"commit_subject": "Draft launch plan post",
		"commit_body": "Adds the first markdown draft for review."
	}`)
	explicitReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/commits?org_id="+orgID,
		bytes.NewReader(explicitBody),
	)
	explicitRec := httptest.NewRecorder()
	router.ServeHTTP(explicitRec, explicitReq)
	require.Equal(t, http.StatusCreated, explicitRec.Code)

	var explicitResp projectCommitCreateResponse
	require.NoError(t, json.NewDecoder(explicitRec.Body).Decode(&explicitResp))
	require.Equal(t, "/posts/2026-02-06-launch-plan.md", explicitResp.Path)
	require.False(t, explicitResp.AutoGeneratedMessage)
	require.Equal(t, "Draft launch plan post", explicitResp.Commit.Subject)
	require.NotNil(t, explicitResp.Commit.Body)
	require.Equal(t, "Adds the first markdown draft for review.", *explicitResp.Commit.Body)

	generatedBody := []byte(`{
		"path": "/posts/2026-02-06-launch-plan.md",
		"content": "# Launch Plan\n\nSecond draft copy with edits."
	}`)
	generatedReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/commits?org_id="+orgID,
		bytes.NewReader(generatedBody),
	)
	generatedRec := httptest.NewRecorder()
	router.ServeHTTP(generatedRec, generatedReq)
	require.Equal(t, http.StatusCreated, generatedRec.Code)

	var generatedResp projectCommitCreateResponse
	require.NoError(t, json.NewDecoder(generatedRec.Body).Decode(&generatedResp))
	require.Equal(t, "/posts/2026-02-06-launch-plan.md", generatedResp.Path)
	require.True(t, generatedResp.AutoGeneratedMessage)
	require.NotEmpty(t, generatedResp.Commit.Subject)

	listReq := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/commits?org_id="+orgID,
		nil,
	)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)

	var listResp projectCommitListResponse
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listResp))
	require.Len(t, listResp.Items, 2)
	require.Equal(t, generatedResp.Commit.SHA, listResp.Items[0].SHA)
	require.Equal(t, explicitResp.Commit.SHA, listResp.Items[1].SHA)
}

func TestProjectCommitsHandlerCreateRejectsInvalidPathAndNoOp(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-commits-create-validate-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Commits Create Validate")
	fixture := newPublishRepoFixture(t)
	seedProjectCommitRepoBinding(t, db, orgID, projectID, fixture.LocalPath)

	handler := &ProjectCommitsHandler{
		ProjectStore: store.NewProjectStore(db),
		CommitStore:  store.NewProjectCommitStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	}
	router := newProjectCommitsTestRouter(handler)

	invalidPathReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/commits?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"path": "/etc/passwd",
			"content": "oops"
		}`)),
	)
	invalidPathRec := httptest.NewRecorder()
	router.ServeHTTP(invalidPathRec, invalidPathReq)
	require.Equal(t, http.StatusBadRequest, invalidPathRec.Code)

	firstReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/commits?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"path": "/notes/research.md",
			"content": "alpha",
			"commit_subject": "Add notes draft",
			"commit_body": "Capture initial research notes for the writing workflow."
		}`)),
	)
	firstRec := httptest.NewRecorder()
	router.ServeHTTP(firstRec, firstReq)
	require.Equal(t, http.StatusCreated, firstRec.Code)

	noOpReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/commits?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"path": "/notes/research.md",
			"content": "alpha",
			"commit_subject": "No-op notes draft"
		}`)),
	)
	noOpRec := httptest.NewRecorder()
	router.ServeHTTP(noOpRec, noOpReq)
	require.Equal(t, http.StatusConflict, noOpRec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(noOpRec.Body).Decode(&payload))
	require.Contains(t, payload.Error, "no changes")
}

func TestProjectCommitsHandlerCreateEnforcesWritingCommitBodyPolicy(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-commits-policy-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Commits Policy")
	fixture := newPublishRepoFixture(t)
	seedProjectCommitRepoBinding(t, db, orgID, projectID, fixture.LocalPath)

	handler := &ProjectCommitsHandler{
		ProjectStore: store.NewProjectStore(db),
		CommitStore:  store.NewProjectCommitStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	}
	router := newProjectCommitsTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/commits?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"path": "/notes/policy.md",
			"content": "alpha",
			"commit_subject": "Policy check commit"
		}`)),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Contains(t, payload.Error, "commit_body is required")
}

func TestProjectCommitsHandlerCreateBypassesPolicyForReviewAndSystemCommitTypes(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-commits-policy-bypass-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Commits Policy Bypass")
	fixture := newPublishRepoFixture(t)
	seedProjectCommitRepoBinding(t, db, orgID, projectID, fixture.LocalPath)

	handler := &ProjectCommitsHandler{
		ProjectStore: store.NewProjectStore(db),
		CommitStore:  store.NewProjectCommitStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	}
	router := newProjectCommitsTestRouter(handler)

	reviewReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/commits?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"path": "/posts/2026-02-06-review.md",
			"content": "review alpha",
			"commit_subject": "Save review markup",
			"commit_type": "review"
		}`)),
	)
	reviewRec := httptest.NewRecorder()
	router.ServeHTTP(reviewRec, reviewReq)
	require.Equal(t, http.StatusCreated, reviewRec.Code)

	systemReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/commits?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"path": "/notes/system.md",
			"content": "system alpha",
			"commit_subject": "System automation update",
			"commit_type": "system"
		}`)),
	)
	systemRec := httptest.NewRecorder()
	router.ServeHTTP(systemRec, systemReq)
	require.Equal(t, http.StatusCreated, systemRec.Code)
}

func TestProjectCommitsHandlerCreateAllowsMissingBodyWhenPolicyDisabled(t *testing.T) {
	t.Setenv("OTTER_WRITING_COMMIT_POLICY_ENABLED", "false")

	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-commits-policy-disabled-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Commits Policy Disabled")
	fixture := newPublishRepoFixture(t)
	seedProjectCommitRepoBinding(t, db, orgID, projectID, fixture.LocalPath)

	handler := &ProjectCommitsHandler{
		ProjectStore: store.NewProjectStore(db),
		CommitStore:  store.NewProjectCommitStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	}
	router := newProjectCommitsTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/commits?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"path": "/notes/policy-disabled.md",
			"content": "alpha",
			"commit_subject": "No body needed"
		}`)),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestValidateBrowserCommitPolicy(t *testing.T) {
	policy := browserCommitPolicy{
		Enabled:      true,
		MinBodyChars: 20,
	}
	require.Error(t, validateBrowserCommitPolicy(browserCommitTypeWriting, nil, policy))

	tooShort := "too short"
	require.Error(t, validateBrowserCommitPolicy(browserCommitTypeWriting, &tooShort, policy))

	valid := "Provide context, rationale, and expected review scope."
	require.NoError(t, validateBrowserCommitPolicy(browserCommitTypeWriting, &valid, policy))
	require.NoError(t, validateBrowserCommitPolicy(browserCommitTypeReview, nil, policy))
	require.NoError(t, validateBrowserCommitPolicy(browserCommitTypeSystem, nil, policy))
	require.NoError(t, validateBrowserCommitPolicy(browserCommitTypeWriting, nil, browserCommitPolicy{
		Enabled:      false,
		MinBodyChars: 20,
	}))
}

func TestGenerateBrowserCommitMessageDeterministic(t *testing.T) {
	summary := browserCommitDiffSummary{
		Path:       "/posts/2026-02-06-launch-plan.md",
		ChangeType: "modified",
		Added:      12,
		Deleted:    3,
	}

	firstSubject, firstBody := generateBrowserCommitMessage(summary)
	secondSubject, secondBody := generateBrowserCommitMessage(summary)
	require.Equal(t, firstSubject, secondSubject)
	require.NotNil(t, firstBody)
	require.NotNil(t, secondBody)
	require.Equal(t, *firstBody, *secondBody)
	require.Contains(t, firstSubject, "Update")
	require.Contains(t, *firstBody, "+12")
	require.Contains(t, *firstBody, "-3")
}

func seedProjectCommitRepoBinding(
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

func commitTestStringPtr(value string) *string {
	return &value
}
