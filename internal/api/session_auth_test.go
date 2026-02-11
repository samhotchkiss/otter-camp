package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRequireSessionIdentityAcceptsMagicToken(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)

	db := openFeedDatabase(t, connStr)
	orgID := insertFeedOrganization(t, db, "session-auth-magic")
	userID := insertFeedUser(t, db, orgID, "magic-user", "Magic User")

	token := "oc_magic_validMagic123"
	insertSessionIdentityToken(t, db, orgID, userID, token)

	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	identity, err := requireSessionIdentity(context.Background(), db, req)
	require.NoError(t, err)
	require.Equal(t, orgID, identity.OrgID)
	require.Equal(t, userID, identity.UserID)
	require.Equal(t, "owner", identity.Role)
}

func TestRequireSessionIdentityRejectsMalformedMagicToken(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)

	db := openFeedDatabase(t, connStr)
	orgID := insertFeedOrganization(t, db, "session-auth-magic-malformed")
	userID := insertFeedUser(t, db, orgID, "malformed-user", "Malformed User")

	// Insert an intentionally malformed token to ensure runtime validation fails closed.
	insertSessionIdentityToken(t, db, orgID, userID, "oc_magic_")

	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req.Header.Set("Authorization", "Bearer oc_magic_")

	identity, err := requireSessionIdentity(context.Background(), db, req)
	require.ErrorIs(t, err, errInvalidSessionToken)
	require.Empty(t, identity.OrgID)
	require.Empty(t, identity.UserID)
}

func insertSessionIdentityToken(t *testing.T, db *sql.DB, orgID, userID, token string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO sessions (org_id, user_id, token, expires_at)
		 VALUES ($1, $2, $3, $4)`,
		orgID,
		userID,
		token,
		time.Now().UTC().Add(24*time.Hour),
	)
	require.NoError(t, err)
}
