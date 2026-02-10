package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOnboardingBootstrapCreatesLocalRecords(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db := openFeedDatabase(t, connStr)

	rec := postOnboardingBootstrap(t, `{"name":"Sam","email":"sam@example.com","organization_name":"My Team"}`)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp OnboardingBootstrapResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.NotEmpty(t, resp.OrgID)
	require.Equal(t, "my-team", resp.OrgSlug)
	require.NotEmpty(t, resp.UserID)
	require.True(t, strings.HasPrefix(resp.Token, "oc_sess_"))
	require.NotEmpty(t, resp.ProjectID)
	require.NotEmpty(t, resp.IssueID)
	require.EqualValues(t, 1, resp.IssueNumber)
	require.True(t, resp.ExpiresAt.After(time.Now().UTC()))

	var orgName string
	err := db.QueryRow("SELECT name FROM organizations WHERE id = $1", resp.OrgID).Scan(&orgName)
	require.NoError(t, err)
	require.Equal(t, "My Team", orgName)

	var displayName, email, role string
	err = db.QueryRow("SELECT display_name, email, role FROM users WHERE id = $1", resp.UserID).Scan(&displayName, &email, &role)
	require.NoError(t, err)
	require.Equal(t, "Sam", displayName)
	require.Equal(t, "sam@example.com", email)
	require.Equal(t, "owner", role)

	var sessionCount int
	err = db.QueryRow("SELECT COUNT(*) FROM sessions WHERE token = $1 AND org_id = $2 AND user_id = $3", resp.Token, resp.OrgID, resp.UserID).Scan(&sessionCount)
	require.NoError(t, err)
	require.Equal(t, 1, sessionCount)

	var projectName string
	err = db.QueryRow("SELECT name FROM projects WHERE id = $1 AND org_id = $2", resp.ProjectID, resp.OrgID).Scan(&projectName)
	require.NoError(t, err)
	require.Equal(t, "Getting Started", projectName)

	var issueTitle, issueState, issueOrigin string
	err = db.QueryRow("SELECT title, state, origin FROM project_issues WHERE id = $1 AND project_id = $2 AND org_id = $3", resp.IssueID, resp.ProjectID, resp.OrgID).Scan(&issueTitle, &issueState, &issueOrigin)
	require.NoError(t, err)
	require.Equal(t, "Welcome to Otter Camp", issueTitle)
	require.Equal(t, "open", issueState)
	require.Equal(t, "local", issueOrigin)
}

func TestOnboardingBootstrapHandlesDuplicates(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db := openFeedDatabase(t, connStr)

	first := postOnboardingBootstrap(t, `{"name":"Sam","email":"sam@example.com","organization_name":"My Team"}`)
	require.Equal(t, http.StatusOK, first.Code)
	var firstResp OnboardingBootstrapResponse
	require.NoError(t, json.NewDecoder(first.Body).Decode(&firstResp))

	second := postOnboardingBootstrap(t, `{"name":"Sam","email":"sam@example.com","organization_name":"My Team"}`)
	require.Equal(t, http.StatusOK, second.Code)
	var secondResp OnboardingBootstrapResponse
	require.NoError(t, json.NewDecoder(second.Body).Decode(&secondResp))

	require.Equal(t, firstResp.OrgID, secondResp.OrgID)
	require.Equal(t, firstResp.ProjectID, secondResp.ProjectID)
	require.Equal(t, firstResp.IssueID, secondResp.IssueID)
	require.NotEqual(t, firstResp.Token, secondResp.Token)

	var orgCount int
	err := db.QueryRow("SELECT COUNT(*) FROM organizations WHERE slug = 'my-team'").Scan(&orgCount)
	require.NoError(t, err)
	require.Equal(t, 1, orgCount)

	var userCount int
	err = db.QueryRow("SELECT COUNT(*) FROM users WHERE org_id = $1", firstResp.OrgID).Scan(&userCount)
	require.NoError(t, err)
	require.Equal(t, 1, userCount)

	var projectCount int
	err = db.QueryRow("SELECT COUNT(*) FROM projects WHERE org_id = $1 AND name = 'Getting Started'", firstResp.OrgID).Scan(&projectCount)
	require.NoError(t, err)
	require.Equal(t, 1, projectCount)

	var issueCount int
	err = db.QueryRow("SELECT COUNT(*) FROM project_issues WHERE project_id = $1", firstResp.ProjectID).Scan(&issueCount)
	require.NoError(t, err)
	require.Equal(t, 1, issueCount)
}

func TestOnboardingBootstrapValidationFailures(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	cases := []struct {
		name       string
		body       string
		errorMatch string
	}{
		{
			name:       "missing name",
			body:       `{"email":"sam@example.com","organization_name":"My Team"}`,
			errorMatch: "name is required",
		},
		{
			name:       "missing email",
			body:       `{"name":"Sam","organization_name":"My Team"}`,
			errorMatch: "email is required",
		},
		{
			name:       "invalid email",
			body:       `{"name":"Sam","email":"sam.example.com","organization_name":"My Team"}`,
			errorMatch: "invalid email",
		},
		{
			name:       "missing organization",
			body:       `{"name":"Sam","email":"sam@example.com"}`,
			errorMatch: "organization_name is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postOnboardingBootstrap(t, tc.body)
			require.Equal(t, http.StatusBadRequest, rec.Code)

			var payload errorResponse
			require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
			require.Contains(t, payload.Error, tc.errorMatch)
		})
	}
}

func TestOnboardingBootstrapRouteIsRegistered(t *testing.T) {
	router := NewRouter()
	req := httptest.NewRequest(http.MethodPost, "/api/onboarding/bootstrap", bytes.NewBufferString(`{"name":"Sam"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.NotEqual(t, http.StatusNotFound, rec.Code)
}

func postOnboardingBootstrap(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/onboarding/bootstrap", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	HandleOnboardingBootstrap(rec, req)
	return rec
}
