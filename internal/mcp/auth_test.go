package mcp

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestDBAuthenticatorMissingHeader(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	a := NewDBAuthenticator(db)
	req := httptest.NewRequest("POST", "/mcp", nil)

	_, authErr := a.Authenticate(context.Background(), req)
	require.ErrorIs(t, authErr, ErrMissingAuth)
}

func TestDBAuthenticatorSessionToken(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	rows := sqlmock.NewRows([]string{"org_id", "user_id", "role"}).AddRow("org-1", "user-1", "owner")
	mock.ExpectQuery(`SELECT s.org_id::text, s.user_id::text, COALESCE\(u.role, 'owner'\)`).WithArgs("oc_sess_valid_token").WillReturnRows(rows)

	a := NewDBAuthenticator(db)
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer oc_sess_valid_token")

	identity, authErr := a.Authenticate(context.Background(), req)
	require.NoError(t, authErr)
	require.Equal(t, "org-1", identity.OrgID)
	require.Equal(t, "user-1", identity.UserID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDBAuthenticatorInvalidToken(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(`SELECT s.org_id::text, s.user_id::text, COALESCE\(u.role, 'owner'\)`).WithArgs("oc_sess_invalid_token").WillReturnError(sql.ErrNoRows)

	a := NewDBAuthenticator(db)
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer oc_sess_invalid_token")

	_, authErr := a.Authenticate(context.Background(), req)
	require.ErrorIs(t, authErr, ErrInvalidAuth)
}
