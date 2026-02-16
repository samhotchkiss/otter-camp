package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
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

func TestOnboardingBootstrapSeedsStarterTrioAgents(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)
	resetOnboardingAuthDB(t)

	db := openFeedDatabase(t, connStr)

	rec := postOnboardingBootstrap(t, `{"name":"Sam","email":"sam@example.com","organization_name":"My Team"}`)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp OnboardingBootstrapResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(t, resp.Agents, 3)

	expected := []OnboardingAgent{
		{Slug: "frank", DisplayName: "Frank"},
		{Slug: "lori", DisplayName: "Lori"},
		{Slug: "ellie", DisplayName: "Ellie"},
	}
	for idx, want := range expected {
		got := resp.Agents[idx]
		require.NotEmpty(t, got.ID)
		require.Equal(t, want.Slug, got.Slug)
		require.Equal(t, want.DisplayName, got.DisplayName)
	}

	rows, err := db.Query(
		`SELECT slug, display_name, status
		 FROM agents
		 WHERE org_id = $1
		 ORDER BY CASE slug
			WHEN 'frank' THEN 1
			WHEN 'lori' THEN 2
			WHEN 'ellie' THEN 3
			ELSE 99
		 END`,
		resp.OrgID,
	)
	require.NoError(t, err)
	defer rows.Close()

	type agentRecord struct {
		Slug        string
		DisplayName string
		Status      string
	}
	var gotRows []agentRecord
	for rows.Next() {
		var row agentRecord
		require.NoError(t, rows.Scan(&row.Slug, &row.DisplayName, &row.Status))
		gotRows = append(gotRows, row)
	}
	require.NoError(t, rows.Err())
	require.Len(t, gotRows, 3)
	for idx, want := range expected {
		require.Equal(t, want.Slug, gotRows[idx].Slug)
		require.Equal(t, want.DisplayName, gotRows[idx].DisplayName)
		require.Equal(t, "active", gotRows[idx].Status)
	}

	handler := &AgentsHandler{
		Store: store.NewAgentStore(db),
		DB:    db,
	}
	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, resp.OrgID))
	recList := httptest.NewRecorder()
	handler.List(recList, req)
	require.Equal(t, http.StatusOK, recList.Code)

	var payload struct {
		Agents []AgentResponse `json:"agents"`
		Total  int             `json:"total"`
	}
	require.NoError(t, json.NewDecoder(recList.Body).Decode(&payload))
	require.GreaterOrEqual(t, payload.Total, 3)

	agentNamesByRole := make(map[string]string, len(payload.Agents))
	for _, agent := range payload.Agents {
		agentNamesByRole[agent.Role] = agent.Name
	}
	require.Equal(t, "Frank", agentNamesByRole["frank"])
	require.Equal(t, "Lori", agentNamesByRole["lori"])
	require.Equal(t, "Ellie", agentNamesByRole["ellie"])
}

func TestOnboardingBootstrapSeedsDefaultTaxonomyRoots(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)
	resetOnboardingAuthDB(t)

	db := openFeedDatabase(t, connStr)

	rec := postOnboardingBootstrap(t, `{"name":"Sam","email":"sam@example.com","organization_name":"My Team"}`)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp OnboardingBootstrapResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))

	rows, err := db.Query(
		`SELECT slug
		 FROM ellie_taxonomy_nodes
		 WHERE org_id = $1
		   AND parent_id IS NULL
		 ORDER BY slug ASC`,
		resp.OrgID,
	)
	require.NoError(t, err)
	defer rows.Close()

	var got []string
	for rows.Next() {
		var slug string
		require.NoError(t, rows.Scan(&slug))
		got = append(got, slug)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{"agents", "personal", "process", "projects", "technical"}, got)
}

func TestBootstrapOnboardingIsIdempotentForStarterAgents(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	db := openFeedDatabase(t, connStr)

	first, err := bootstrapOnboarding(context.Background(), db, "Sam", "sam@example.com", "My Team", "my-team")
	require.NoError(t, err)
	require.Len(t, first.Agents, 3)

	second, err := bootstrapOnboarding(context.Background(), db, "Sam", "sam@example.com", "My Team", "my-team")
	require.NoError(t, err)
	require.Len(t, second.Agents, 3)
	require.Equal(t, first.OrgID, second.OrgID)

	var total int
	err = db.QueryRow(
		`SELECT COUNT(*)
		 FROM agents
		 WHERE org_id = $1
		   AND slug IN ('frank', 'lori', 'ellie')`,
		first.OrgID,
	).Scan(&total)
	require.NoError(t, err)
	require.Equal(t, 3, total)

	for _, slug := range []string{"frank", "lori", "ellie"} {
		var count int
		err = db.QueryRow(
			`SELECT COUNT(*)
			 FROM agents
			 WHERE org_id = $1
			   AND slug = $2`,
			first.OrgID,
			slug,
		).Scan(&count)
		require.NoError(t, err)
		require.Equalf(t, 1, count, "expected exactly one %s agent", slug)
	}
}

func TestBootstrapOnboardingTaxonomySeedIsIdempotent(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	db := openFeedDatabase(t, connStr)

	first, err := bootstrapOnboarding(context.Background(), db, "Sam", "sam@example.com", "My Team", "my-team")
	require.NoError(t, err)
	second, err := bootstrapOnboarding(context.Background(), db, "Sam", "sam@example.com", "My Team", "my-team")
	require.NoError(t, err)
	require.Equal(t, first.OrgID, second.OrgID)

	rows, err := db.Query(
		`SELECT slug, COUNT(*)
		 FROM ellie_taxonomy_nodes
		 WHERE org_id = $1
		   AND parent_id IS NULL
		 GROUP BY slug
		 ORDER BY slug ASC`,
		first.OrgID,
	)
	require.NoError(t, err)
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var slug string
		var count int
		require.NoError(t, rows.Scan(&slug, &count))
		counts[slug] = count
	}
	require.NoError(t, rows.Err())
	require.Equal(t, 5, len(counts))
	for _, slug := range []string{"agents", "personal", "process", "projects", "technical"} {
		require.Equalf(t, 1, counts[slug], "expected exactly one %s root", slug)
	}
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

func TestOnboardingBootstrapAllowsSecondSetupWhenMultiOrgEnabled(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)
	t.Setenv("ONBOARDING_ALLOW_MULTI_ORG", "true")
	resetOnboardingAuthDB(t)

	first := postOnboardingBootstrap(t, `{"name":"Sam","email":"sam@example.com","organization_name":"First Team"}`)
	require.Equal(t, http.StatusOK, first.Code)
	var firstPayload OnboardingBootstrapResponse
	require.NoError(t, json.NewDecoder(first.Body).Decode(&firstPayload))

	second := postOnboardingBootstrap(t, `{"name":"Ari","email":"ari@example.com","organization_name":"Second Team"}`)
	require.Equal(t, http.StatusOK, second.Code)
	var secondPayload OnboardingBootstrapResponse
	require.NoError(t, json.NewDecoder(second.Body).Decode(&secondPayload))

	require.NotEqual(t, firstPayload.OrgID, secondPayload.OrgID)
	require.Equal(t, "second-team", secondPayload.OrgSlug)
}

func TestOnboardingSetupLockEnabled(t *testing.T) {
	t.Run("enabled by default", func(t *testing.T) {
		t.Setenv("ONBOARDING_ALLOW_MULTI_ORG", "")
		t.Setenv("ONBOARDING_SETUP_LOCK_DISABLED", "")
		require.True(t, onboardingSetupLockEnabled())
	})

	t.Run("disabled when multi-org flag enabled", func(t *testing.T) {
		t.Setenv("ONBOARDING_ALLOW_MULTI_ORG", "true")
		require.False(t, onboardingSetupLockEnabled())
	})

	t.Run("disabled when explicit lock disable flag enabled", func(t *testing.T) {
		t.Setenv("ONBOARDING_SETUP_LOCK_DISABLED", "1")
		require.False(t, onboardingSetupLockEnabled())
	})
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
