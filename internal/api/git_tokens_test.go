package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func insertTestProject(t *testing.T, db *sql.DB, orgID, name string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		`INSERT INTO projects (org_id, name, status) VALUES ($1, $2, 'active') RETURNING id`,
		orgID,
		name,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestGitTokensCRUD(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db := openFeedDatabase(t, connStr)

	orgID := insertFeedOrganization(t, db, "git-token-org")
	userID := insertTestUser(t, db, orgID, "git-token-user")
	projectID := insertTestProject(t, db, orgID, "git-token-project")

	token := "oc_sess_test_git_token"
	insertTestSession(t, db, orgID, userID, token, time.Now().UTC().Add(time.Hour))

	router := NewRouter()

	{
		req := httptest.NewRequest(http.MethodGet, "/api/git/tokens", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		var resp gitTokensListResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		require.Empty(t, resp.Tokens)
	}

	var created gitTokenResponse
	{
		body := `{"name":"CI token","projects":[{"project_id":"` + projectID + `","permission":"write"}]}`
		req := httptest.NewRequest(http.MethodPost, "/api/git/tokens", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&created))
		require.NotEmpty(t, created.ID)
		require.NotEmpty(t, created.Token)
		require.Equal(t, "CI token", created.Name)
		require.Equal(t, projectID, created.Projects[0].ProjectID)
		require.Equal(t, "write", created.Projects[0].Permission)
	}

	{
		req := httptest.NewRequest(http.MethodGet, "/api/git/tokens", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		var resp gitTokensListResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		require.Len(t, resp.Tokens, 1)
		require.Equal(t, created.ID, resp.Tokens[0].ID)
		require.Equal(t, created.Name, resp.Tokens[0].Name)
		require.Empty(t, resp.Tokens[0].Token)
	}

	{
		req := httptest.NewRequest(http.MethodPost, "/api/git/tokens/"+created.ID+"/revoke", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		var resp gitTokenResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		require.Equal(t, created.ID, resp.ID)
		require.NotNil(t, resp.RevokedAt)
	}
}

func TestGitKeysCRUD(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db := openFeedDatabase(t, connStr)

	orgID := insertFeedOrganization(t, db, "git-key-org")
	userID := insertTestUser(t, db, orgID, "git-key-user")
	projectID := insertTestProject(t, db, orgID, "git-key-project")

	token := "oc_sess_test_git_key"
	insertTestSession(t, db, orgID, userID, token, time.Now().UTC().Add(time.Hour))

	router := NewRouter()

	publicKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEb0dC3sVtPXk7x1YgnPZXoqBYwygJyI072QtdgQXl3k test@example.com"

	{
		req := httptest.NewRequest(http.MethodGet, "/api/git/keys", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		var resp gitKeysListResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		require.Empty(t, resp.Keys)
	}

	var created gitKeyResponse
	{
		body := `{"name":"Laptop","public_key":"` + publicKey + `","projects":[{"project_id":"` + projectID + `","permission":"read"}]}`
		req := httptest.NewRequest(http.MethodPost, "/api/git/keys", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&created))
		require.NotEmpty(t, created.ID)
		require.NotEmpty(t, created.Fingerprint)
		require.Equal(t, "Laptop", created.Name)
		require.Equal(t, projectID, created.Projects[0].ProjectID)
		require.Equal(t, "read", created.Projects[0].Permission)
	}

	{
		req := httptest.NewRequest(http.MethodGet, "/api/git/keys", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		var resp gitKeysListResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		require.Len(t, resp.Keys, 1)
		require.Equal(t, created.ID, resp.Keys[0].ID)
		require.Equal(t, created.Fingerprint, resp.Keys[0].Fingerprint)
	}

	{
		req := httptest.NewRequest(http.MethodPost, "/api/git/keys/"+created.ID+"/revoke", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		var resp gitKeyResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		require.Equal(t, created.ID, resp.ID)
		require.NotNil(t, resp.RevokedAt)
	}
}
