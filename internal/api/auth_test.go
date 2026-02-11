package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestHandleLoginCreatesAuthRequest(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db := openFeedDatabase(t, connStr)
	orgID := insertFeedOrganization(t, db, "auth-login-org")

	body := `{"org_id":"` + orgID + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	HandleLogin(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp AuthRequestResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.NotEmpty(t, resp.RequestID)
	require.NotEmpty(t, resp.State)
	require.True(t, resp.ExpiresAt.After(time.Now().UTC()))
}

func TestAuthExchangeCreatesSession(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)
	t.Setenv("OPENCLAW_AUTH_SECRET", "test-secret")

	db := openFeedDatabase(t, connStr)
	orgID := insertFeedOrganization(t, db, "auth-exchange-org")

	body := `{"org_id":"` + orgID + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	HandleLogin(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var authResp AuthRequestResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&authResp))

	token := buildOpenClawToken(t, openClawClaims{
		Sub: "user-123",
		Iss: openClawAuthIssuer(),
		Iat: time.Now().Add(-1 * time.Minute).Unix(),
		Exp: time.Now().Add(5 * time.Minute).Unix(),
	}, "test-secret")

	exchangeBody := `{"request_id":"` + authResp.RequestID + `","token":"` + token + `"}`
	exchangeReq := httptest.NewRequest(http.MethodPost, "/api/auth/exchange", bytes.NewBufferString(exchangeBody))
	exchangeReq.Header.Set("Content-Type", "application/json")
	exchangeRec := httptest.NewRecorder()

	HandleAuthExchange(exchangeRec, exchangeReq)
	require.Equal(t, http.StatusOK, exchangeRec.Code)

	var loginResp LoginResponse
	require.NoError(t, json.NewDecoder(exchangeRec.Body).Decode(&loginResp))
	require.NotEmpty(t, loginResp.Token)
	require.NotEmpty(t, loginResp.User.ID)

	var sessionCount int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM sessions WHERE token = $1 AND org_id = $2",
		loginResp.Token,
		orgID,
	).Scan(&sessionCount)
	require.NoError(t, err)
	require.Equal(t, 1, sessionCount)
}

func buildOpenClawToken(t *testing.T, claims openClawClaims, secret string) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	sig := hmac.New(sha256.New, []byte(secret))
	_, _ = sig.Write([]byte(payloadB64))
	signatureB64 := base64.RawURLEncoding.EncodeToString(sig.Sum(nil))
	return openClawTokenPrefix + payloadB64 + "." + signatureB64
}

func setAuthDBMock(t *testing.T, db *sql.DB) {
	t.Helper()
	prevDB := authDB
	prevErr := authDBErr
	prevOnce := authDBOnce

	authDB = db
	authDBErr = nil
	authDBOnce = sync.Once{}
	authDBOnce.Do(func() {})

	t.Cleanup(func() {
		authDB = prevDB
		authDBErr = prevErr
		authDBOnce = prevOnce
	})
}

func TestAllowInsecureMagicTokenValidationEnvAliases(t *testing.T) {
	t.Setenv("OTTERCAMP_ALLOW_INSECURE_MAGIC_AUTH", "true")
	t.Setenv("ALLOW_INSECURE_MAGIC_TOKEN_VALIDATION", "")
	require.True(t, allowInsecureMagicTokenValidation())

	t.Setenv("OTTERCAMP_ALLOW_INSECURE_MAGIC_AUTH", "")
	t.Setenv("ALLOW_INSECURE_MAGIC_TOKEN_VALIDATION", "yes")
	require.True(t, allowInsecureMagicTokenValidation())

	t.Setenv("OTTERCAMP_ALLOW_INSECURE_MAGIC_AUTH", "false")
	t.Setenv("ALLOW_INSECURE_MAGIC_TOKEN_VALIDATION", "")
	require.False(t, allowInsecureMagicTokenValidation())
}

func TestShouldBypassDBTokenValidation(t *testing.T) {
	t.Setenv("OTTERCAMP_ALLOW_INSECURE_MAGIC_AUTH", "true")
	t.Setenv("ALLOW_INSECURE_MAGIC_TOKEN_VALIDATION", "")
	require.True(t, shouldBypassDBTokenValidation("oc_magic_abc123"))
	require.False(t, shouldBypassDBTokenValidation("oc_local_abc123"))

	t.Setenv("OTTERCAMP_ALLOW_INSECURE_MAGIC_AUTH", "false")
	t.Setenv("ALLOW_INSECURE_MAGIC_TOKEN_VALIDATION", "")
	require.False(t, shouldBypassDBTokenValidation("oc_magic_abc123"))
}

func TestHandleMagicLinkPrefersLocalAuthToken(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)
	t.Setenv("LOCAL_AUTH_TOKEN", "oc_local_shared_token")

	db := openFeedDatabase(t, connStr)
	orgID := insertFeedOrganization(t, db, "magic-local-org")

	var userID string
	err := db.QueryRow(
		`INSERT INTO users (org_id, display_name, email, subject, issuer)
		 VALUES ($1, 'Admin', 'admin@localhost', 'magic', 'otter.camp')
		 RETURNING id`,
		orgID,
	).Scan(&userID)
	require.NoError(t, err)

	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	_, err = db.Exec(
		`INSERT INTO sessions (org_id, user_id, token, expires_at)
		 VALUES ($1, $2, $3, $4)`,
		orgID,
		userID,
		"oc_local_shared_token",
		expiresAt,
	)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/magic", bytes.NewBufferString(`{"name":"Admin","email":"admin@localhost"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	HandleMagicLink(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp MagicLinkResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "oc_local_shared_token", resp.Token)
	require.Contains(t, resp.URL, "auth=oc_local_shared_token")
	require.WithinDuration(t, expiresAt, resp.ExpiresAt, time.Second)

	var magicSessions int
	err = db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token LIKE 'oc_magic_%'`).Scan(&magicSessions)
	require.NoError(t, err)
	require.Zero(t, magicSessions)
}

func TestHandleMagicLinkCreatesMagicTokenWhenLocalTokenMissing(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)
	t.Setenv("LOCAL_AUTH_TOKEN", "oc_local_missing_token")

	req := httptest.NewRequest(http.MethodPost, "/api/auth/magic", bytes.NewBufferString(`{"name":"Admin","email":"admin@localhost"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	HandleMagicLink(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp MagicLinkResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Contains(t, resp.Token, "oc_magic_")
	require.Contains(t, resp.URL, fmt.Sprintf("auth=%s", resp.Token))
}

func TestHandleMagicLinkSupportsCustomOrgSlugAndEmail(t *testing.T) {
	t.Setenv("LOCAL_AUTH_TOKEN", "oc_local_shared_token")

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	setAuthDBMock(t, db)

	mock.ExpectQuery(`INSERT INTO organizations`).
		WithArgs("Acme Labs", "acme-sandbox").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("org-1"))
	mock.ExpectQuery(`INSERT INTO users`).
		WithArgs("org-1", "Casey", "casey@example.com", "magic:casey@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("user-1"))
	mock.ExpectExec(`INSERT INTO sessions`).
		WithArgs("org-1", "user-1", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/magic",
		bytes.NewBufferString(`{"name":"Casey","email":"casey@example.com","organization_name":"Acme Labs","org_slug":"acme-sandbox"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	HandleMagicLink(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp MagicLinkResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Contains(t, resp.Token, "oc_magic_")
	require.NotEqual(t, "oc_local_shared_token", resp.Token)
	require.Contains(t, resp.URL, fmt.Sprintf("auth=%s", resp.Token))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHandleMagicLinkRejectsInvalidCustomEmail(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/magic",
		bytes.NewBufferString(`{"name":"Casey","email":"not-an-email","organization_name":"Acme Labs","org_slug":"acme-sandbox"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	HandleMagicLink(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "invalid email", payload.Error)
}

func TestHandleMagicLinkRejectsInvalidCustomOrgSlug(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/magic",
		bytes.NewBufferString(`{"name":"Casey","email":"casey@example.com","organization_name":"Acme Labs","org_slug":"!!!"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	HandleMagicLink(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "invalid org_slug", payload.Error)
}

func TestHandleValidateTokenReconcilesLocalAgentsByEmail(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)
	t.Setenv("LOCAL_AUTH_TOKEN", "oc_local_reconcile_token")

	db := openFeedDatabase(t, connStr)
	targetOrgID := insertFeedOrganization(t, db, "target-org")
	sourceOrgID := insertFeedOrganization(t, db, "source-org")

	var targetUserID string
	err := db.QueryRow(
		`INSERT INTO users (org_id, display_name, email, subject, issuer)
		 VALUES ($1, 'Admin', 'admin@localhost', 'magic', 'otter.camp')
		 RETURNING id`,
		targetOrgID,
	).Scan(&targetUserID)
	require.NoError(t, err)

	targetExpiry := time.Now().UTC().Add(24 * time.Hour)
	_, err = db.Exec(
		`INSERT INTO sessions (org_id, user_id, token, expires_at)
		 VALUES ($1, $2, $3, $4)`,
		targetOrgID,
		targetUserID,
		"oc_local_reconcile_token",
		targetExpiry,
	)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO users (org_id, display_name, email, subject, issuer)
		 VALUES ($1, 'Admin', 'admin@localhost', 'magic', 'otter.camp')`,
		sourceOrgID,
	)
	require.NoError(t, err)

	var sourceAgentID string
	err = db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status, role, emoji, soul_md, identity_md, instructions_md)
		 VALUES ($1, 'marcus', 'Marcus', 'active', 'Ops', '🦦', 'soul text', 'identity text', 'instructions text')
		 RETURNING id`,
		sourceOrgID,
	).Scan(&sourceAgentID)
	require.NoError(t, err)
	require.NotEmpty(t, sourceAgentID)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/validate?token=oc_local_reconcile_token", nil)
	rec := httptest.NewRecorder()
	HandleValidateToken(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var copiedCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM agents WHERE org_id = $1 AND slug = 'marcus'`,
		targetOrgID,
	).Scan(&copiedCount)
	require.NoError(t, err)
	require.Equal(t, 1, copiedCount)

	var copiedRole, copiedEmoji, copiedSoul string
	err = db.QueryRow(
		`SELECT COALESCE(role, ''), COALESCE(emoji, ''), COALESCE(soul_md, '')
		 FROM agents
		 WHERE org_id = $1 AND slug = 'marcus'`,
		targetOrgID,
	).Scan(&copiedRole, &copiedEmoji, &copiedSoul)
	require.NoError(t, err)
	require.Equal(t, "Ops", copiedRole)
	require.Equal(t, "🦦", copiedEmoji)
	require.Equal(t, "soul text", copiedSoul)

	// Idempotent on repeated validate
	rec2 := httptest.NewRecorder()
	HandleValidateToken(rec2, req)
	require.Equal(t, http.StatusOK, rec2.Code)

	err = db.QueryRow(
		`SELECT COUNT(*) FROM agents WHERE org_id = $1 AND slug = 'marcus'`,
		targetOrgID,
	).Scan(&copiedCount)
	require.NoError(t, err)
	require.Equal(t, 1, copiedCount)
}
