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

func seedPullRequestTestData(t *testing.T, db *sql.DB, orgID string) string {
	t.Helper()
	projectID := insertProjectTestProject(t, db, orgID, "PR API Project")
	issuePRStore := store.NewGitHubIssuePRStore(db)
	ctx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)

	createdAt := time.Date(2026, 2, 6, 15, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(30 * time.Minute)

	_, err := issuePRStore.UpsertPullRequest(ctx, store.UpsertGitHubPullRequestInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		GitHubNumber:       100,
		Title:              "Draft PR",
		State:              "open",
		Draft:              true,
		HeadRef:            "feature/draft",
		HeadSHA:            "111",
		BaseRef:            "main",
		Merged:             false,
		CreatedAt:          createdAt,
		UpdatedAt:          updatedAt,
	})
	require.NoError(t, err)

	mergedAt := updatedAt.Add(30 * time.Minute)
	_, err = issuePRStore.UpsertPullRequest(ctx, store.UpsertGitHubPullRequestInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		GitHubNumber:       101,
		Title:              "Merged PR",
		State:              "closed",
		Draft:              false,
		HeadRef:            "feature/merged",
		HeadSHA:            "222",
		BaseRef:            "main",
		Merged:             true,
		MergedAt:           &mergedAt,
		MergedCommitSHA:    stringPtr("deadbeef"),
		CreatedAt:          createdAt,
		UpdatedAt:          mergedAt,
		ClosedAt:           &mergedAt,
	})
	require.NoError(t, err)

	_, err = issuePRStore.UpsertPullRequest(ctx, store.UpsertGitHubPullRequestInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		GitHubNumber:       102,
		Title:              "Open PR",
		State:              "open",
		Draft:              false,
		HeadRef:            "feature/open",
		HeadSHA:            "333",
		BaseRef:            "main",
		Merged:             false,
		CreatedAt:          createdAt,
		UpdatedAt:          mergedAt.Add(10 * time.Minute),
	})
	require.NoError(t, err)

	return projectID
}

func TestGitHubPullRequestsListByProjectIncludesPRSpecificBadge(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "pr-api-org")
	projectID := seedPullRequestTestData(t, db, orgID)

	handler := &GitHubPullRequestsHandler{Store: store.NewGitHubIssuePRStore(db)}
	router := chi.NewRouter()
	router.With(middleware.OptionalWorkspace).Get("/api/projects/{id}/pull-requests", handler.ListByProject)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/pull-requests?org_id="+orgID, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp githubPullRequestListResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, 3, resp.Total)

	byNumber := map[int64]githubPullRequestListItem{}
	for _, item := range resp.Items {
		byNumber[item.Number] = item
		require.Equal(t, "pull_request", item.Kind)
	}

	require.Equal(t, "draft", byNumber[100].Badge)
	require.Equal(t, "merged", byNumber[101].Badge)
	require.Equal(t, "open", byNumber[102].Badge)
}

func TestGitHubPullRequestsListByProjectFiltersByState(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "pr-api-filter-org")
	projectID := seedPullRequestTestData(t, db, orgID)

	handler := &GitHubPullRequestsHandler{Store: store.NewGitHubIssuePRStore(db)}
	router := chi.NewRouter()
	router.With(middleware.OptionalWorkspace).Get("/api/projects/{id}/pull-requests", handler.ListByProject)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/pull-requests?org_id="+orgID+"&state=closed", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp githubPullRequestListResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, 1, resp.Total)
	require.Equal(t, int64(101), resp.Items[0].Number)
	require.Equal(t, "merged", resp.Items[0].Badge)
}

func TestGitHubPullRequestsListByProjectRequiresWorkspace(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "pr-api-auth-org")
	projectID := seedPullRequestTestData(t, db, orgID)

	handler := &GitHubPullRequestsHandler{Store: store.NewGitHubIssuePRStore(db)}
	router := chi.NewRouter()
	router.With(middleware.OptionalWorkspace).Get("/api/projects/{id}/pull-requests", handler.ListByProject)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/pull-requests", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func stringPtr(value string) *string {
	return &value
}
