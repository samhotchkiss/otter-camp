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

func insertTestUser(t *testing.T, db *sql.DB, orgID, subject string) string {
	t.Helper()

	var userID string
	err := db.QueryRow(
		`INSERT INTO users (org_id, subject, issuer, display_name, email)
		 VALUES ($1, $2, 'openclaw', $3, $4)
		 RETURNING id::text`,
		orgID,
		subject,
		"User "+subject,
		subject+"@example.com",
	).Scan(&userID)
	require.NoError(t, err)
	return userID
}

func insertTestSession(t *testing.T, db *sql.DB, orgID, userID, token string, expiresAt time.Time) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO sessions (org_id, user_id, token, expires_at)
		 VALUES ($1, $2, $3, $4)`,
		orgID,
		userID,
		token,
		expiresAt,
	)
	require.NoError(t, err)
}

func TestUserCommandPrefixesRequiresSession(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	router := NewRouter()
	req := httptest.NewRequest(http.MethodGet, "/api/user/prefixes", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	var payload map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "missing authentication", payload["error"])
}

func TestUserCommandPrefixesCRUD(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db := openFeedDatabase(t, connStr)

	orgID := insertFeedOrganization(t, db, "user-prefixes-org")
	userID := insertTestUser(t, db, orgID, "user-1")

	token := "oc_sess_test_user_1"
	insertTestSession(t, db, orgID, userID, token, time.Now().UTC().Add(1*time.Hour))

	router := NewRouter()

	{
		req := httptest.NewRequest(http.MethodGet, "/api/user/prefixes", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		var resp userCommandPrefixesResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		require.Empty(t, resp.Prefixes)
	}

	var created UserCommandPrefix
	{
		body := `{"prefix":"t","command":"/tasks"}`
		req := httptest.NewRequest(http.MethodPost, "/api/user/prefixes", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&created))
		require.NotEmpty(t, created.ID)
		require.Equal(t, "t", created.Prefix)
		require.Equal(t, "/tasks", created.Command)
		require.False(t, created.CreatedAt.IsZero())
		require.False(t, created.UpdatedAt.IsZero())
	}

	{
		req := httptest.NewRequest(http.MethodGet, "/api/user/prefixes", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		var resp userCommandPrefixesResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		require.Len(t, resp.Prefixes, 1)
		require.Equal(t, created.ID, resp.Prefixes[0].ID)
		require.Equal(t, created.Prefix, resp.Prefixes[0].Prefix)
		require.Equal(t, created.Command, resp.Prefixes[0].Command)
	}

	{
		body := `{"prefix":"t","command":"/tasks"}`
		req := httptest.NewRequest(http.MethodPost, "/api/user/prefixes", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusConflict, rec.Code)
		var payload map[string]string
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
		require.Equal(t, "prefix already exists", payload["error"])
	}

	{
		req := httptest.NewRequest(http.MethodDelete, "/api/user/prefixes/"+created.ID, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		var payload map[string]bool
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
		require.True(t, payload["ok"])
	}

	{
		req := httptest.NewRequest(http.MethodGet, "/api/user/prefixes", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		var resp userCommandPrefixesResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		require.Empty(t, resp.Prefixes)
	}
}

func TestUserCommandPrefixesDeleteIsScopedToUser(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db := openFeedDatabase(t, connStr)

	orgID := insertFeedOrganization(t, db, "user-prefixes-scope-org")
	user1ID := insertTestUser(t, db, orgID, "user-1")
	user2ID := insertTestUser(t, db, orgID, "user-2")

	token1 := "oc_sess_test_user_1_scope"
	token2 := "oc_sess_test_user_2_scope"
	insertTestSession(t, db, orgID, user1ID, token1, time.Now().UTC().Add(1*time.Hour))
	insertTestSession(t, db, orgID, user2ID, token2, time.Now().UTC().Add(1*time.Hour))

	router := NewRouter()

	var created UserCommandPrefix
	{
		body := `{"prefix":"feed","command":"/feed"}`
		req := httptest.NewRequest(http.MethodPost, "/api/user/prefixes", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token1)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&created))
		require.NotEmpty(t, created.ID)
	}

	{
		req := httptest.NewRequest(http.MethodDelete, "/api/user/prefixes/"+created.ID, nil)
		req.Header.Set("Authorization", "Bearer "+token2)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusNotFound, rec.Code)
		var payload map[string]string
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
		require.Equal(t, "prefix not found", payload["error"])
	}
}

