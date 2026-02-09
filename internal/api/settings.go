package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

type ProfileResponse struct {
	Name      string  `json:"name"`
	Email     string  `json:"email"`
	AvatarURL *string `json:"avatarUrl"`
}

type NotificationPreferences map[string]map[string]bool

type WorkspaceMember struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Email     string  `json:"email"`
	Role      string  `json:"role"`
	AvatarURL *string `json:"avatarUrl,omitempty"`
}

type WorkspaceResponse struct {
	Name    string            `json:"name"`
	Members []WorkspaceMember `json:"members"`
}

type IntegrationAPIKey struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Prefix    string    `json:"prefix"`
	CreatedAt time.Time `json:"createdAt"`
	Key       string    `json:"key,omitempty"`
}

type IntegrationsResponse struct {
	OpenClawWebhookURL string              `json:"openclawWebhookUrl"`
	APIKeys            []IntegrationAPIKey `json:"apiKeys"`
}

// Backward-compatible aliases used by legacy settings tests and callers.
type settingsProfileResponse = ProfileResponse
type settingsWorkspaceMemberResponse = WorkspaceMember
type settingsWorkspaceResponse = WorkspaceResponse
type settingsIntegrationsResponse = IntegrationsResponse

type apiKeyCreateRequest struct {
	Name string `json:"name"`
}

var validNotificationEvents = map[string]bool{
	"taskAssigned":  true,
	"taskCompleted": true,
	"mentions":      true,
	"comments":      true,
	"agentUpdates":  true,
	"weeklyDigest":  true,
}

var validNotificationChannels = map[string]bool{
	"email": true,
	"push":  true,
	"inApp": true,
}

var defaultNotificationPrefs = NotificationPreferences{
	"taskAssigned":  {"email": true, "push": true, "inApp": true},
	"taskCompleted": {"email": false, "push": true, "inApp": true},
	"mentions":      {"email": true, "push": true, "inApp": true},
	"comments":      {"email": false, "push": false, "inApp": true},
	"agentUpdates":  {"email": false, "push": true, "inApp": true},
	"weeklyDigest":  {"email": true, "push": false, "inApp": false},
}

// GET /api/settings/profile
func HandleSettingsProfileGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	identity, err := requireSessionIdentity(r.Context(), db, r)
	if err != nil {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}

	profile, err := fetchProfile(r.Context(), db, identity.UserID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load profile"})
		return
	}

	sendJSON(w, http.StatusOK, profile)
}

// PUT /api/settings/profile
func HandleSettingsProfilePut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	var req ProfileResponse
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "name is required"})
		return
	}

	email := strings.TrimSpace(req.Email)
	if email == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "email is required"})
		return
	}
	if _, err := mail.ParseAddress(email); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid email"})
		return
	}

	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	identity, err := requireSessionIdentity(r.Context(), db, r)
	if err != nil {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}

	updated, err := updateProfile(r.Context(), db, identity.UserID, name, email, req.AvatarURL)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update profile"})
		return
	}

	sendJSON(w, http.StatusOK, updated)
}

// GET /api/settings/notifications
func HandleSettingsNotificationsGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	identity, err := requireSessionIdentity(r.Context(), db, r)
	if err != nil {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}

	prefs, err := fetchNotificationPrefs(r.Context(), db, identity.UserID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load notifications"})
		return
	}

	sendJSON(w, http.StatusOK, prefs)
}

// PUT /api/settings/notifications
func HandleSettingsNotificationsPut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	var prefs NotificationPreferences
	if err := json.NewDecoder(r.Body).Decode(&prefs); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	if err := validateNotificationPrefs(prefs); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	identity, err := requireSessionIdentity(r.Context(), db, r)
	if err != nil {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}

	updated, err := upsertNotificationPrefs(r.Context(), db, identity.UserID, prefs)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update notifications"})
		return
	}

	sendJSON(w, http.StatusOK, updated)
}

// GET /api/settings/workspace
func HandleSettingsWorkspaceGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	identity, err := requireSessionIdentity(r.Context(), db, r)
	if err != nil {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}

	workspace, err := fetchWorkspace(r.Context(), db, identity.OrgID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load workspace"})
		return
	}

	sendJSON(w, http.StatusOK, workspace)
}

// PUT /api/settings/workspace
func HandleSettingsWorkspacePut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	var req WorkspaceResponse
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "workspace name is required"})
		return
	}

	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	identity, err := requireSessionIdentity(r.Context(), db, r)
	if err != nil {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}

	updated, err := updateWorkspaceName(r.Context(), db, identity.OrgID, name)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update workspace"})
		return
	}

	sendJSON(w, http.StatusOK, updated)
}

// GET /api/settings/integrations
func HandleSettingsIntegrationsGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	identity, err := requireSessionIdentity(r.Context(), db, r)
	if err != nil {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}

	integrations, err := fetchIntegrations(r.Context(), db, identity.OrgID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load integrations"})
		return
	}

	sendJSON(w, http.StatusOK, integrations)
}

// PUT /api/settings/integrations
func HandleSettingsIntegrationsPut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	var req IntegrationsResponse
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	identity, err := requireSessionIdentity(r.Context(), db, r)
	if err != nil {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}

	updated, err := upsertIntegrations(r.Context(), db, identity.OrgID, strings.TrimSpace(req.OpenClawWebhookURL))
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update integrations"})
		return
	}

	sendJSON(w, http.StatusOK, updated)
}

// POST /api/settings/integrations/api-keys
func HandleSettingsAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	var req apiKeyCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "name is required"})
		return
	}

	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	identity, err := requireSessionIdentity(r.Context(), db, r)
	if err != nil {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}

	key, err := createAPIKey(r.Context(), db, identity.OrgID, name)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create api key"})
		return
	}

	sendJSON(w, http.StatusOK, key)
}

// DELETE /api/settings/integrations/api-keys/{id}
func HandleSettingsAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing id"})
		return
	}
	if !uuidRegex.MatchString(id) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid id"})
		return
	}

	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	identity, err := requireSessionIdentity(r.Context(), db, r)
	if err != nil {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}

	deleted, err := deleteAPIKey(r.Context(), db, identity.OrgID, id)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to delete api key"})
		return
	}
	if !deleted {
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "api key not found"})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func fetchProfile(ctx context.Context, db *sql.DB, userID string) (ProfileResponse, error) {
	var profile ProfileResponse
	var avatar sql.NullString
	var name sql.NullString
	var email sql.NullString

	err := db.QueryRowContext(
		ctx,
		`SELECT display_name, email, avatar_url
		 FROM users
		 WHERE id = $1`,
		userID,
	).Scan(&name, &email, &avatar)
	if err != nil {
		return profile, err
	}

	if name.Valid {
		profile.Name = name.String
	}
	if email.Valid {
		profile.Email = email.String
	}
	if avatar.Valid {
		profile.AvatarURL = &avatar.String
	}

	return profile, nil
}

func updateProfile(ctx context.Context, db *sql.DB, userID, name, email string, avatarURL *string) (ProfileResponse, error) {
	_, err := db.ExecContext(
		ctx,
		`UPDATE users
		 SET display_name = $1,
		     email = $2,
		     avatar_url = $3,
		     updated_at = NOW()
		 WHERE id = $4`,
		name,
		email,
		avatarURL,
		userID,
	)
	if err != nil {
		return ProfileResponse{}, err
	}

	return fetchProfile(ctx, db, userID)
}

func fetchNotificationPrefs(ctx context.Context, db *sql.DB, userID string) (NotificationPreferences, error) {
	var prefsJSON []byte
	if err := db.QueryRowContext(
		ctx,
		`SELECT preferences
		 FROM user_notification_settings
		 WHERE user_id = $1`,
		userID,
	).Scan(&prefsJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return defaultNotificationPrefs, nil
		}
		return nil, err
	}

	var prefs NotificationPreferences
	if err := json.Unmarshal(prefsJSON, &prefs); err != nil {
		return nil, err
	}

	if err := validateNotificationPrefs(prefs); err != nil {
		return defaultNotificationPrefs, nil
	}

	return prefs, nil
}

func upsertNotificationPrefs(ctx context.Context, db *sql.DB, userID string, prefs NotificationPreferences) (NotificationPreferences, error) {
	prefsJSON, err := json.Marshal(prefs)
	if err != nil {
		return nil, err
	}

	_, err = db.ExecContext(
		ctx,
		`INSERT INTO user_notification_settings (user_id, preferences)
		 VALUES ($1, $2)
		 ON CONFLICT (user_id)
		 DO UPDATE SET preferences = EXCLUDED.preferences, updated_at = NOW()`,
		userID,
		prefsJSON,
	)
	if err != nil {
		return nil, err
	}

	return prefs, nil
}

func validateNotificationPrefs(prefs NotificationPreferences) error {
	if prefs == nil {
		return errors.New("preferences are required")
	}

	for event, channels := range prefs {
		if !validNotificationEvents[event] {
			return errors.New("invalid notification event")
		}
		for channel := range channels {
			if !validNotificationChannels[channel] {
				return errors.New("invalid notification channel")
			}
		}
	}

	for event := range validNotificationEvents {
		channels, ok := prefs[event]
		if !ok {
			return errors.New("missing notification event")
		}
		for channel := range validNotificationChannels {
			if _, ok := channels[channel]; !ok {
				return errors.New("missing notification channel")
			}
		}
	}

	return nil
}

func fetchWorkspace(ctx context.Context, db *sql.DB, orgID string) (WorkspaceResponse, error) {
	var name string
	if err := db.QueryRowContext(
		ctx,
		`SELECT name
		 FROM organizations
		 WHERE id = $1`,
		orgID,
	).Scan(&name); err != nil {
		return WorkspaceResponse{}, err
	}

	rows, err := db.QueryContext(
		ctx,
		`SELECT id::text, display_name, email, role, avatar_url
		 FROM users
		 WHERE org_id = $1
		 ORDER BY created_at ASC`,
		orgID,
	)
	if err != nil {
		return WorkspaceResponse{}, err
	}
	defer rows.Close()

	members := make([]WorkspaceMember, 0)
	for rows.Next() {
		var member WorkspaceMember
		var name sql.NullString
		var email sql.NullString
		var role sql.NullString
		var avatar sql.NullString
		if err := rows.Scan(&member.ID, &name, &email, &role, &avatar); err != nil {
			return WorkspaceResponse{}, err
		}
		member.Name = name.String
		member.Email = email.String
		if role.Valid {
			member.Role = role.String
		} else {
			member.Role = "member"
		}
		if avatar.Valid {
			member.AvatarURL = &avatar.String
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return WorkspaceResponse{}, err
	}

	return WorkspaceResponse{Name: name, Members: members}, nil
}

func updateWorkspaceName(ctx context.Context, db *sql.DB, orgID, name string) (WorkspaceResponse, error) {
	_, err := db.ExecContext(
		ctx,
		`UPDATE organizations
		 SET name = $1
		 WHERE id = $2`,
		name,
		orgID,
	)
	if err != nil {
		return WorkspaceResponse{}, err
	}

	return fetchWorkspace(ctx, db, orgID)
}

func fetchIntegrations(ctx context.Context, db *sql.DB, orgID string) (IntegrationsResponse, error) {
	var webhook sql.NullString
	if err := db.QueryRowContext(
		ctx,
		`SELECT openclaw_webhook_url
		 FROM org_settings
		 WHERE org_id = $1`,
		orgID,
	).Scan(&webhook); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return IntegrationsResponse{}, err
		}
	}

	rows, err := db.QueryContext(
		ctx,
		`SELECT id::text, name, prefix, created_at
		 FROM api_keys
		 WHERE org_id = $1
		 ORDER BY created_at DESC`,
		orgID,
	)
	if err != nil {
		return IntegrationsResponse{}, err
	}
	defer rows.Close()

	keys := make([]IntegrationAPIKey, 0)
	for rows.Next() {
		var key IntegrationAPIKey
		if err := rows.Scan(&key.ID, &key.Name, &key.Prefix, &key.CreatedAt); err != nil {
			return IntegrationsResponse{}, err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return IntegrationsResponse{}, err
	}

	resp := IntegrationsResponse{
		OpenClawWebhookURL: "",
		APIKeys:            keys,
	}
	if webhook.Valid {
		resp.OpenClawWebhookURL = webhook.String
	}
	return resp, nil
}

func upsertIntegrations(ctx context.Context, db *sql.DB, orgID, webhook string) (IntegrationsResponse, error) {
	_, err := db.ExecContext(
		ctx,
		`INSERT INTO org_settings (org_id, openclaw_webhook_url)
		 VALUES ($1, $2)
		 ON CONFLICT (org_id)
		 DO UPDATE SET openclaw_webhook_url = EXCLUDED.openclaw_webhook_url, updated_at = NOW()`,
		orgID,
		webhook,
	)
	if err != nil {
		return IntegrationsResponse{}, err
	}

	return fetchIntegrations(ctx, db, orgID)
}

func createAPIKey(ctx context.Context, db *sql.DB, orgID, name string) (IntegrationAPIKey, error) {
	rawKey, err := generateAPIKey()
	if err != nil {
		return IntegrationAPIKey{}, err
	}

	hash := sha256.Sum256([]byte(rawKey))
	keyHash := hex.EncodeToString(hash[:])

	prefix := rawKey
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}

	var key IntegrationAPIKey
	key.Name = name
	key.Prefix = prefix
	key.Key = rawKey

	err = db.QueryRowContext(
		ctx,
		`INSERT INTO api_keys (org_id, name, prefix, key_hash)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id::text, created_at`,
		orgID,
		name,
		prefix,
		keyHash,
	).Scan(&key.ID, &key.CreatedAt)
	if err != nil {
		return IntegrationAPIKey{}, err
	}

	return key, nil
}

func deleteAPIKey(ctx context.Context, db *sql.DB, orgID, id string) (bool, error) {
	var deletedID string
	err := db.QueryRowContext(
		ctx,
		`DELETE FROM api_keys
		 WHERE id = $1 AND org_id = $2
		 RETURNING id::text`,
		id,
		orgID,
	).Scan(&deletedID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func generateAPIKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}

	return "oc_key_" + hex.EncodeToString(buf), nil
}
