package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
