package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type SettingsHandler struct {
	DB *sql.DB
}

type settingsProfileResponse struct {
	Name      string  `json:"name"`
	Email     string  `json:"email"`
	AvatarURL *string `json:"avatarUrl"`
}

type settingsWorkspaceMemberResponse struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Email     string  `json:"email"`
	Role      string  `json:"role"`
	AvatarURL *string `json:"avatarUrl,omitempty"`
}

type settingsWorkspaceResponse struct {
	Name    string                            `json:"name"`
	Members []settingsWorkspaceMemberResponse `json:"members"`
}

type settingsIntegrationsAPIKeyResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Prefix    string    `json:"prefix"`
	CreatedAt time.Time `json:"createdAt"`
}

type settingsIntegrationsResponse struct {
	OpenClawWebhookURL string                               `json:"openclawWebhookUrl"`
	APIKeys            []settingsIntegrationsAPIKeyResponse `json:"apiKeys"`
}

type settingsProfileUpdateRequest struct {
	Name      string  `json:"name"`
	Email     string  `json:"email"`
	AvatarURL *string `json:"avatarUrl"`
}

type settingsWorkspaceUpdateRequest struct {
	Name string `json:"name"`
}

type settingsIntegrationsUpdateRequest struct {
	OpenClawWebhookURL string `json:"openclawWebhookUrl"`
}

func (h *SettingsHandler) GetProfile(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.requireSettingsIdentity(w, r)
	if !ok {
		return
	}

	var displayName, email sql.NullString
	err := h.DB.QueryRowContext(
		r.Context(),
		`SELECT display_name, email FROM users WHERE id = $1 AND org_id = $2`,
		identity.UserID,
		identity.OrgID,
	).Scan(&displayName, &email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "user not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load profile settings"})
		return
	}

	name := strings.TrimSpace(displayName.String)
	if name == "" {
		name = strings.TrimSpace(email.String)
	}
	sendJSON(w, http.StatusOK, settingsProfileResponse{
		Name:      name,
		Email:     strings.TrimSpace(email.String),
		AvatarURL: nil,
	})
}

func (h *SettingsHandler) GetWorkspace(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.requireSettingsIdentity(w, r)
	if !ok {
		return
	}

	workspace, err := h.loadWorkspace(r.Context(), identity.OrgID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "workspace not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load workspace settings"})
		return
	}
	sendJSON(w, http.StatusOK, workspace)
}

func (h *SettingsHandler) GetNotifications(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.requireSettingsIdentity(w, r)
	if !ok {
		return
	}

	prefs := defaultSettingsNotificationPreferences()
	var raw json.RawMessage
	err := h.DB.QueryRowContext(
		r.Context(),
		`SELECT notification_preferences FROM users WHERE id = $1 AND org_id = $2`,
		identity.UserID,
		identity.OrgID,
	).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "user not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load notification settings"})
		return
	}

	var stored map[string]map[string]bool
	if len(raw) > 0 && json.Unmarshal(raw, &stored) == nil {
		mergeSettingsNotificationPreferences(prefs, stored)
	}

	sendJSON(w, http.StatusOK, prefs)
}

func (h *SettingsHandler) GetIntegrations(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.requireSettingsIdentity(w, r)
	if !ok {
		return
	}

	integrations, err := h.loadIntegrations(r.Context(), identity.OrgID, identity.UserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "workspace not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load integration settings"})
		return
	}
	sendJSON(w, http.StatusOK, integrations)
}

func (h *SettingsHandler) PutProfile(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.requireSettingsIdentity(w, r)
	if !ok {
		return
	}

	var req settingsProfileUpdateRequest
	if !decodeJSONStrict(w, r, &req, "invalid profile settings payload") {
		return
	}
	name := strings.TrimSpace(req.Name)
	email := strings.TrimSpace(req.Email)

	var displayName, persistedEmail sql.NullString
	err := h.DB.QueryRowContext(
		r.Context(),
		`UPDATE users
		 SET display_name = $1,
		     email = $2,
		     updated_at = NOW()
		 WHERE id = $3
		   AND org_id = $4
		 RETURNING display_name, email`,
		name,
		email,
		identity.UserID,
		identity.OrgID,
	).Scan(&displayName, &persistedEmail)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "user not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update profile settings"})
		return
	}

	responseName := strings.TrimSpace(displayName.String)
	if responseName == "" {
		responseName = strings.TrimSpace(persistedEmail.String)
	}
	sendJSON(w, http.StatusOK, settingsProfileResponse{
		Name:      responseName,
		Email:     strings.TrimSpace(persistedEmail.String),
		AvatarURL: nil,
	})
}

func (h *SettingsHandler) PutNotifications(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.requireSettingsIdentity(w, r)
	if !ok {
		return
	}

	var req map[string]map[string]bool
	if !decodeJSONStrict(w, r, &req, "invalid notification settings payload") {
		return
	}

	prefs := defaultSettingsNotificationPreferences()
	mergeSettingsNotificationPreferences(prefs, req)

	raw, err := json.Marshal(prefs)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to encode notification settings"})
		return
	}

	var persisted json.RawMessage
	err = h.DB.QueryRowContext(
		r.Context(),
		`UPDATE users
		 SET notification_preferences = $1::jsonb,
		     updated_at = NOW()
		 WHERE id = $2
		   AND org_id = $3
		 RETURNING notification_preferences`,
		raw,
		identity.UserID,
		identity.OrgID,
	).Scan(&persisted)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "user not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update notification settings"})
		return
	}

	response := defaultSettingsNotificationPreferences()
	var stored map[string]map[string]bool
	if len(persisted) > 0 && json.Unmarshal(persisted, &stored) == nil {
		mergeSettingsNotificationPreferences(response, stored)
	}
	sendJSON(w, http.StatusOK, response)
}

func (h *SettingsHandler) PutWorkspace(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.requireSettingsIdentity(w, r)
	if !ok {
		return
	}

	var req settingsWorkspaceUpdateRequest
	if !decodeJSONStrict(w, r, &req, "invalid workspace settings payload") {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "workspace name is required"})
		return
	}

	result, err := h.DB.ExecContext(
		r.Context(),
		`UPDATE organizations
		 SET name = $1,
		     updated_at = NOW()
		 WHERE id = $2`,
		name,
		identity.OrgID,
	)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update workspace settings"})
		return
	}
	affected, err := result.RowsAffected()
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update workspace settings"})
		return
	}
	if affected == 0 {
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "workspace not found"})
		return
	}

	workspace, err := h.loadWorkspace(r.Context(), identity.OrgID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load workspace settings"})
		return
	}
	sendJSON(w, http.StatusOK, workspace)
}

func (h *SettingsHandler) PutIntegrations(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.requireSettingsIdentity(w, r)
	if !ok {
		return
	}

	var req settingsIntegrationsUpdateRequest
	if !decodeJSONStrict(w, r, &req, "invalid integration settings payload") {
		return
	}

	webhookURL := strings.TrimSpace(req.OpenClawWebhookURL)
	if webhookURL != "" {
		parsed, err := url.ParseRequestURI(webhookURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "openclawWebhookUrl must be a valid http(s) URL"})
			return
		}
	}

	result, err := h.DB.ExecContext(
		r.Context(),
		`UPDATE organizations
		 SET openclaw_webhook_url = $1,
		     updated_at = NOW()
		 WHERE id = $2`,
		webhookURL,
		identity.OrgID,
	)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update integration settings"})
		return
	}
	affected, err := result.RowsAffected()
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update integration settings"})
		return
	}
	if affected == 0 {
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "workspace not found"})
		return
	}

	integrations, err := h.loadIntegrations(r.Context(), identity.OrgID, identity.UserID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load integration settings"})
		return
	}
	sendJSON(w, http.StatusOK, settingsIntegrationsResponse{
		OpenClawWebhookURL: integrations.OpenClawWebhookURL,
		APIKeys:            integrations.APIKeys,
	})
}

func (h *SettingsHandler) requireSettingsIdentity(w http.ResponseWriter, r *http.Request) (sessionIdentity, bool) {
	if h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return sessionIdentity{}, false
	}

	identity, err := requireSessionIdentity(r.Context(), h.DB, r)
	if err != nil {
		switch {
		case errors.Is(err, errMissingAuthentication),
			errors.Is(err, errInvalidSessionToken),
			errors.Is(err, errAuthentication):
			sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		case errors.Is(err, errWorkspaceMismatch):
			sendJSON(w, http.StatusForbidden, errorResponse{Error: err.Error()})
		default:
			sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication error"})
		}
		return sessionIdentity{}, false
	}

	return identity, true
}

func (h *SettingsHandler) loadWorkspace(ctx context.Context, orgID string) (settingsWorkspaceResponse, error) {
	var workspaceName sql.NullString
	err := h.DB.QueryRowContext(
		ctx,
		`SELECT name FROM organizations WHERE id = $1`,
		orgID,
	).Scan(&workspaceName)
	if err != nil {
		return settingsWorkspaceResponse{}, err
	}

	rows, err := h.DB.QueryContext(
		ctx,
		`SELECT id::text, COALESCE(display_name, ''), COALESCE(email, ''), COALESCE(role, 'owner')
		 FROM users
		 WHERE org_id = $1
		 ORDER BY LOWER(COALESCE(NULLIF(display_name, ''), NULLIF(email, ''), id::text)) ASC`,
		orgID,
	)
	if err != nil {
		return settingsWorkspaceResponse{}, err
	}
	defer rows.Close()

	members := make([]settingsWorkspaceMemberResponse, 0)
	for rows.Next() {
		var member settingsWorkspaceMemberResponse
		var role string
		if err := rows.Scan(&member.ID, &member.Name, &member.Email, &role); err != nil {
			return settingsWorkspaceResponse{}, err
		}
		member.Name = strings.TrimSpace(member.Name)
		member.Email = strings.TrimSpace(member.Email)
		if member.Name == "" {
			member.Name = member.Email
		}
		member.Role = settingsRoleFromUserRole(role)
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return settingsWorkspaceResponse{}, err
	}

	return settingsWorkspaceResponse{
		Name:    strings.TrimSpace(workspaceName.String),
		Members: members,
	}, nil
}

func (h *SettingsHandler) loadIntegrations(
	ctx context.Context,
	orgID string,
	userID string,
) (settingsIntegrationsResponse, error) {
	var openClawWebhookURL sql.NullString
	err := h.DB.QueryRowContext(
		ctx,
		`SELECT COALESCE(openclaw_webhook_url, '') FROM organizations WHERE id = $1`,
		orgID,
	).Scan(&openClawWebhookURL)
	if err != nil {
		return settingsIntegrationsResponse{}, err
	}

	rows, err := h.DB.QueryContext(
		ctx,
		`SELECT id::text, name, token_prefix, created_at
		 FROM git_access_tokens
		 WHERE org_id = $1
		   AND user_id = $2
		   AND revoked_at IS NULL
		 ORDER BY created_at DESC`,
		orgID,
		userID,
	)
	if err != nil {
		return settingsIntegrationsResponse{}, err
	}
	defer rows.Close()

	apiKeys := make([]settingsIntegrationsAPIKeyResponse, 0)
	for rows.Next() {
		var key settingsIntegrationsAPIKeyResponse
		if err := rows.Scan(&key.ID, &key.Name, &key.Prefix, &key.CreatedAt); err != nil {
			return settingsIntegrationsResponse{}, err
		}
		apiKeys = append(apiKeys, key)
	}
	if err := rows.Err(); err != nil {
		return settingsIntegrationsResponse{}, err
	}

	return settingsIntegrationsResponse{
		OpenClawWebhookURL: strings.TrimSpace(openClawWebhookURL.String),
		APIKeys:            apiKeys,
	}, nil
}

func settingsRoleFromUserRole(role string) string {
	switch normalizeRole(role) {
	case RoleOwner:
		return "owner"
	case RoleMaintainer:
		return "admin"
	default:
		return "member"
	}
}

func defaultSettingsNotificationPreferences() map[string]map[string]bool {
	return map[string]map[string]bool{
		"taskAssigned":  {"email": true, "push": true, "inApp": true},
		"taskCompleted": {"email": false, "push": true, "inApp": true},
		"mentions":      {"email": true, "push": true, "inApp": true},
		"comments":      {"email": false, "push": false, "inApp": true},
		"agentUpdates":  {"email": false, "push": true, "inApp": true},
		"weeklyDigest":  {"email": true, "push": false, "inApp": false},
	}
}

func mergeSettingsNotificationPreferences(base, stored map[string]map[string]bool) {
	for event, channels := range base {
		storedChannels, ok := stored[event]
		if !ok {
			continue
		}
		for channel := range channels {
			value, ok := storedChannels[channel]
			if !ok {
				continue
			}
			base[event][channel] = value
		}
	}
}

func decodeJSONStrict(w http.ResponseWriter, r *http.Request, target any, invalidMessage string) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: invalidMessage})
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: invalidMessage})
		return false
	}
	return true
}
