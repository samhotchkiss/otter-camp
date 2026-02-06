package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func insertGitAccessToken(t *testing.T, db *sql.DB, orgID, userID, name, token, projectID, permission string) string {
	t.Helper()
	tokenHash := hashGitSecret(token)
	tokenPrefix := token
	if len(token) > tokenPrefixLength {
		tokenPrefix = token[:tokenPrefixLength]
	}

	var tokenID string
	err := db.QueryRow(
		`INSERT INTO git_access_tokens (org_id, user_id, name, token_hash, token_prefix)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id::text`,
		orgID,
		userID,
		name,
		tokenHash,
		tokenPrefix,
	).Scan(&tokenID)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO git_access_token_projects (org_id, token_id, project_id, permission)
		 VALUES ($1, $2, $3, $4)`,
		orgID,
		tokenID,
		projectID,
		permission,
	)
	require.NoError(t, err)

	return tokenID
}

func TestGitAuthIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db := openFeedDatabase(t, connStr)

	orgID := insertFeedOrganization(t, db, "git-auth-org")
	userID := insertTestUser(t, db, orgID, "git-auth-user")
	projectID := insertTestProject(t, db, orgID, "git-auth-project")

	repoDir := t.TempDir()
	repoPath := filepath.Join(repoDir, "repo.git")
	require.NoError(t, exec.Command("git", "init", "--bare", repoPath).Run())

	_, err := db.Exec(`UPDATE projects SET local_repo_path = $1 WHERE id = $2`, repoPath, projectID)
	require.NoError(t, err)

	readToken, _, _, err := generateGitToken()
	require.NoError(t, err)
	insertGitAccessToken(t, db, orgID, userID, "read-token", readToken, projectID, "read")

	router := NewRouter()

	{
		req := httptest.NewRequest(http.MethodGet, "/git/"+orgID+"/"+projectID+".git/info/refs?service=git-upload-pack", nil)
		req.SetBasicAuth("user", readToken)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
	}

	{
		req := httptest.NewRequest(http.MethodPost, "/git/"+orgID+"/"+projectID+".git/git-receive-pack", nil)
		req.SetBasicAuth("user", readToken)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusForbidden, rec.Code)
	}

	_, err = db.Exec(`UPDATE git_access_tokens SET revoked_at = NOW() WHERE token_hash = $1`, hashGitSecret(readToken))
	require.NoError(t, err)

	{
		req := httptest.NewRequest(http.MethodGet, "/git/"+orgID+"/"+projectID+".git/info/refs?service=git-upload-pack", nil)
		req.SetBasicAuth("user", readToken)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusUnauthorized, rec.Code)
	}
}
