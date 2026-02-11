package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestRequireSessionIdentityAcceptsMagicToken(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	token := "oc_magic_validMagic123"
	mock.ExpectQuery(`SELECT s.org_id::text, s.user_id::text, COALESCE\(u.role, 'owner'\)`).
		WithArgs(token).
		WillReturnRows(sqlmock.NewRows([]string{"org_id", "user_id", "role"}).AddRow("org-1", "user-1", "owner"))

	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	identity, err := requireSessionIdentity(context.Background(), db, req)
	require.NoError(t, err)
	require.Equal(t, "org-1", identity.OrgID)
	require.Equal(t, "user-1", identity.UserID)
	require.Equal(t, "owner", identity.Role)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRequireSessionIdentityRejectsMalformedMagicToken(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req.Header.Set("Authorization", "Bearer oc_magic_")

	identity, err := requireSessionIdentity(context.Background(), db, req)
	require.ErrorIs(t, err, errInvalidSessionToken)
	require.Empty(t, identity.OrgID)
	require.Empty(t, identity.UserID)
	require.NoError(t, mock.ExpectationsWereMet())
}
