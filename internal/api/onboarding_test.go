package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOnboardingBootstrapCreatesLocalRecords(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)
	resetOnboardingAuthDB(t)

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

func TestOnboardingBootstrapRejectsSecondSetup(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)
	resetOnboardingAuthDB(t)

	first := postOnboardingBootstrap(t, `{"name":"Sam","email":"sam@example.com","organization_name":"My Team"}`)
	require.Equal(t, http.StatusOK, first.Code)

	second := postOnboardingBootstrap(t, `{"name":"Sam","email":"sam@example.com","organization_name":"My Team"}`)
	require.Equal(t, http.StatusConflict, second.Code)
	var payload errorResponse
	require.NoError(t, json.NewDecoder(second.Body).Decode(&payload))
	require.Equal(t, "onboarding already completed", payload.Error)
}

func TestOnboardingBootstrapValidationFailures(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)
	resetOnboardingAuthDB(t)

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

func TestOnboardingBootstrapCanonicalOrganizationFieldOnly(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)
	resetOnboardingAuthDB(t)

	rec := postOnboardingBootstrap(t, `{"name":"Sam","email":"sam@example.com","organization":"My Team"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "organization_name is required", payload.Error)
}

func TestOnboardingBootstrapRouteIsRegistered(t *testing.T) {
	router := NewRouter()
	req := httptest.NewRequest(http.MethodPost, "/api/onboarding/bootstrap", bytes.NewBufferString(`{"name":"Sam"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.NotEqual(t, http.StatusNotFound, rec.Code)
}

func TestOnboardingBootstrapExistingUserRoleUnchanged(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)

	db := openFeedDatabase(t, connStr)
	orgID := insertOnboardingTestOrg(t, db, "Role Test Org", "role-test-org")
	userID := insertOnboardingTestUser(t, db, orgID, "sam@example.com", RoleMember)

	tx, err := db.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	returnedUserID, err := upsertOnboardingUser(t.Context(), tx, orgID, "Sam Updated", "sam@example.com")
	require.NoError(t, err)
	require.Equal(t, userID, returnedUserID)
	require.NoError(t, tx.Commit())

	var role string
	err = db.QueryRow("SELECT role FROM users WHERE id = $1", userID).Scan(&role)
	require.NoError(t, err)
	require.Equal(t, RoleMember, role)
}

func TestOnboardingBootstrapOversizedPayload(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)
	resetOnboardingAuthDB(t)

	largeName := strings.Repeat("A", onboardingMaxBodyBytes)
	body := `{"name":"` + largeName + `","email":"sam@example.com","organization_name":"My Team"}`
	rec := postOnboardingBootstrap(t, body)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "request body too large", payload.Error)
}

func TestOnboardingBootstrapDatabaseUnavailableDoesNotLeakDetails(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	resetOnboardingAuthDB(t)

	rec := postOnboardingBootstrap(t, `{"name":"Sam","email":"sam@example.com","organization_name":"My Team"}`)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "database unavailable", payload.Error)
}

func insertOnboardingTestOrg(t *testing.T, db *sql.DB, name, slug string) string {
	t.Helper()

	var orgID string
	err := db.QueryRow(
		`INSERT INTO organizations (name, slug)
		 VALUES ($1, $2)
		 RETURNING id`,
		name,
		slug,
	).Scan(&orgID)
	require.NoError(t, err)
	return orgID
}

func insertOnboardingTestUser(t *testing.T, db *sql.DB, orgID, email, role string) string {
	t.Helper()

	subject := "local:" + strings.ToLower(strings.TrimSpace(email))
	var userID string
	err := db.QueryRow(
		`INSERT INTO users (org_id, subject, issuer, display_name, email, role)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id`,
		orgID,
		subject,
		onboardingUserIssuer,
		"Existing User",
		email,
		role,
	).Scan(&userID)
	require.NoError(t, err)
	return userID
}

func resetOnboardingAuthDB(t *testing.T) {
	t.Helper()

	if authDB != nil {
		_ = authDB.Close()
	}
	authDB = nil
	authDBErr = nil
	authDBOnce = sync.Once{}
}

func postOnboardingBootstrap(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/onboarding/bootstrap", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	HandleOnboardingBootstrap(rec, req)
	return rec
}
