package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGetUserProfile(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db := openFeedDatabase(t, connStr)
	orgID := insertFeedOrganization(t, db, "settings-profile")
	userID := insertTestUser(t, db, orgID, "settings-user")
	token := "oc_sess_settings_profile"
	insertTestSession(t, db, orgID, userID, token, time.Now().UTC().Add(time.Hour))

	router := NewRouter()
	req := httptest.NewRequest(http.MethodGet, "/api/settings/profile", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp settingsProfileResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "User settings-user", resp.Name)
	require.Equal(t, "settings-user@example.com", resp.Email)
	require.Nil(t, resp.AvatarURL)
}

func TestGetWorkspaceSettings(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db := openFeedDatabase(t, connStr)
	orgID := insertFeedOrganization(t, db, "settings-workspace")
	ownerID := insertTestUser(t, db, orgID, "settings-owner")
	memberID := insertTestUser(t, db, orgID, "settings-member")
	_, err := db.Exec(`UPDATE users SET role = 'member' WHERE id = $1`, memberID)
	require.NoError(t, err)

	token := "oc_sess_settings_workspace"
	insertTestSession(t, db, orgID, ownerID, token, time.Now().UTC().Add(time.Hour))

	router := NewRouter()
	req := httptest.NewRequest(http.MethodGet, "/api/settings/workspace", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp settingsWorkspaceResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "Org settings-workspace", resp.Name)
	require.Len(t, resp.Members, 2)

	membersByEmail := make(map[string]settingsWorkspaceMemberResponse, len(resp.Members))
	for _, member := range resp.Members {
		membersByEmail[member.Email] = member
	}

	require.Equal(t, "owner", membersByEmail["settings-owner@example.com"].Role)
	require.Equal(t, "member", membersByEmail["settings-member@example.com"].Role)
}

func TestGetNotificationSettings(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db := openFeedDatabase(t, connStr)
	orgID := insertFeedOrganization(t, db, "settings-notifications")
	userID := insertTestUser(t, db, orgID, "settings-notifications-user")
	token := "oc_sess_settings_notifications"
	insertTestSession(t, db, orgID, userID, token, time.Now().UTC().Add(time.Hour))

	router := NewRouter()
	req := httptest.NewRequest(http.MethodGet, "/api/settings/notifications", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]map[string]bool
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, map[string]bool{"email": true, "push": true, "inApp": true}, resp["taskAssigned"])
	require.Equal(t, map[string]bool{"email": false, "push": false, "inApp": true}, resp["comments"])
	require.Equal(t, map[string]bool{"email": true, "push": false, "inApp": false}, resp["weeklyDigest"])
}

func TestGetIntegrationsSettings(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db := openFeedDatabase(t, connStr)
	orgID := insertFeedOrganization(t, db, "settings-integrations")
	userID := insertTestUser(t, db, orgID, "settings-integrations-user")
	token := "oc_sess_settings_integrations"
	insertTestSession(t, db, orgID, userID, token, time.Now().UTC().Add(time.Hour))

	createdAt := time.Now().UTC().Add(-5 * time.Minute)
	_, err := db.Exec(
		`INSERT INTO git_access_tokens (org_id, user_id, name, token_hash, token_prefix, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		orgID,
		userID,
		"CLI Key",
		"hash-settings-integrations",
		"oc_test_",
		createdAt,
	)
	require.NoError(t, err)

	router := NewRouter()
	req := httptest.NewRequest(http.MethodGet, "/api/settings/integrations", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp settingsIntegrationsResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "", resp.OpenClawWebhookURL)
	require.Len(t, resp.APIKeys, 1)
	require.Equal(t, "CLI Key", resp.APIKeys[0].Name)
	require.Equal(t, "oc_test_", resp.APIKeys[0].Prefix)
	require.False(t, resp.APIKeys[0].CreatedAt.IsZero())
}

func TestPatchUserProfile(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db := openFeedDatabase(t, connStr)
	orgID := insertFeedOrganization(t, db, "settings-profile-patch")
	userID := insertTestUser(t, db, orgID, "settings-user-patch")
	token := "oc_sess_settings_profile_patch"
	insertTestSession(t, db, orgID, userID, token, time.Now().UTC().Add(time.Hour))

	router := NewRouter()
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/settings/profile",
		bytes.NewBufferString(`{"name":"Sam Patch","email":"sam.patch@example.com","avatarUrl":null}`),
	)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp settingsProfileResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "Sam Patch", resp.Name)
	require.Equal(t, "sam.patch@example.com", resp.Email)

	var displayName, email string
	err := db.QueryRow(
		`SELECT display_name, email FROM users WHERE id = $1 AND org_id = $2`,
		userID,
		orgID,
	).Scan(&displayName, &email)
	require.NoError(t, err)
	require.Equal(t, "Sam Patch", displayName)
	require.Equal(t, "sam.patch@example.com", email)
}

func TestPatchWorkspaceSettings(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db := openFeedDatabase(t, connStr)
	orgID := insertFeedOrganization(t, db, "settings-workspace-patch")
	userID := insertTestUser(t, db, orgID, "workspace-owner-patch")
	token := "oc_sess_settings_workspace_patch"
	insertTestSession(t, db, orgID, userID, token, time.Now().UTC().Add(time.Hour))

	router := NewRouter()
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/settings/workspace",
		bytes.NewBufferString(`{"name":"Ops Workspace","members":[]}`),
	)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp settingsWorkspaceResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "Ops Workspace", resp.Name)

	var orgName string
	err := db.QueryRow(`SELECT name FROM organizations WHERE id = $1`, orgID).Scan(&orgName)
	require.NoError(t, err)
	require.Equal(t, "Ops Workspace", orgName)
}

func TestPatchNotificationSettings(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db := openFeedDatabase(t, connStr)
	orgID := insertFeedOrganization(t, db, "settings-notifications-patch")
	userID := insertTestUser(t, db, orgID, "notifications-user-patch")
	token := "oc_sess_settings_notifications_patch"
	insertTestSession(t, db, orgID, userID, token, time.Now().UTC().Add(time.Hour))

	router := NewRouter()
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/settings/notifications",
		bytes.NewBufferString(`{"comments":{"email":true,"push":true,"inApp":false},"weeklyDigest":{"email":false,"push":false,"inApp":false}}`),
	)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]map[string]bool
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, map[string]bool{"email": true, "push": true, "inApp": false}, resp["comments"])
	require.Equal(t, map[string]bool{"email": true, "push": true, "inApp": true}, resp["taskAssigned"])

	var raw json.RawMessage
	err := db.QueryRow(
		`SELECT notification_preferences FROM users WHERE id = $1 AND org_id = $2`,
		userID,
		orgID,
	).Scan(&raw)
	require.NoError(t, err)
	var stored map[string]map[string]bool
	require.NoError(t, json.Unmarshal(raw, &stored))
	require.Equal(t, map[string]bool{"email": true, "push": true, "inApp": false}, stored["comments"])
}

func TestPatchIntegrationsSettings(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db := openFeedDatabase(t, connStr)
	orgID := insertFeedOrganization(t, db, "settings-integrations-patch")
	userID := insertTestUser(t, db, orgID, "integrations-user-patch")
	token := "oc_sess_settings_integrations_patch"
	insertTestSession(t, db, orgID, userID, token, time.Now().UTC().Add(time.Hour))

	router := NewRouter()
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/settings/integrations",
		bytes.NewBufferString(`{"openclawWebhookUrl":"https://example.com/openclaw/webhook"}`),
	)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp settingsIntegrationsResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "https://example.com/openclaw/webhook", resp.OpenClawWebhookURL)

	var webhookURL string
	err := db.QueryRow(`SELECT openclaw_webhook_url FROM organizations WHERE id = $1`, orgID).Scan(&webhookURL)
	require.NoError(t, err)
	require.Equal(t, "https://example.com/openclaw/webhook", webhookURL)
}
