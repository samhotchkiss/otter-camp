package api

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func TestParseProjectWorkflowTriggerCron(t *testing.T) {
	trigger := parseProjectWorkflowTrigger(json.RawMessage(`{"kind":"cron","expr":"0 6 * * *","tz":"America/Denver"}`))
	require.Equal(t, "cron", trigger.Type)
	require.Equal(t, "0 6 * * *", trigger.Cron)
	require.Contains(t, trigger.Label, "Daily at 6:00")
	require.Contains(t, trigger.Label, "America/Denver")
}

func TestParseProjectWorkflowTriggerEvery(t *testing.T) {
	trigger := parseProjectWorkflowTrigger(json.RawMessage(`{"kind":"every","everyMs":900000}`))
	require.Equal(t, "interval", trigger.Type)
	require.Equal(t, "15m0s", trigger.Every)
	require.Equal(t, "Every 15m0s", trigger.Label)
}

func TestParseProjectWorkflowTriggerDefaultManual(t *testing.T) {
	trigger := parseProjectWorkflowTrigger(nil)
	require.Equal(t, "manual", trigger.Type)
	require.Equal(t, "Manual", trigger.Label)
}

func TestDeriveLegacyWorkflowLastStatus(t *testing.T) {
	require.Equal(t, "", deriveLegacyWorkflowLastStatus(nil))
	now := time.Now()
	require.Equal(t, "ok", deriveLegacyWorkflowLastStatus(&now))
}

func TestWorkflowListJWTWorkspaceRequiresOptionalMiddleware(t *testing.T) {
	t.Setenv("TRUST_UNVERIFIED_JWT_WORKSPACE_CLAIMS", "true")
	const orgID = "00000000-0000-0000-0000-000000000123"

	db, err := sql.Open("postgres", "postgres://invalid:invalid@127.0.0.1:1/invalid?sslmode=disable")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	handler := &WorkflowsHandler{
		DB:          db,
		ProjectStore: store.NewProjectStore(db),
	}

	withOptional := chi.NewRouter()
	withOptional.With(middleware.OptionalWorkspace).Get("/api/workflows", handler.List)

	reqWithOptional := httptest.NewRequest(http.MethodGet, "/api/workflows", nil)
	reqWithOptional.Header.Set("Authorization", "Bearer "+unsignedJWT(orgID))
	recWithOptional := httptest.NewRecorder()
	withOptional.ServeHTTP(recWithOptional, reqWithOptional)
	require.NotEqual(t, http.StatusUnauthorized, recWithOptional.Code)

	withoutOptional := chi.NewRouter()
	withoutOptional.Get("/api/workflows", handler.List)

	reqWithoutOptional := httptest.NewRequest(http.MethodGet, "/api/workflows", nil)
	reqWithoutOptional.Header.Set("Authorization", "Bearer "+unsignedJWT(orgID))
	recWithoutOptional := httptest.NewRecorder()
	withoutOptional.ServeHTTP(recWithoutOptional, reqWithoutOptional)
	require.Equal(t, http.StatusUnauthorized, recWithoutOptional.Code)
}

func unsignedJWT(orgID string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"org_id":"` + orgID + `","sub":"test-user"}`))
	return strings.Join([]string{header, payload, "signature"}, ".")
}
