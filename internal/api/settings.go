package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
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

	var workspaceName sql.NullString
	err := h.DB.QueryRowContext(
		r.Context(),
		`SELECT name FROM organizations WHERE id = $1`,
		identity.OrgID,
	).Scan(&workspaceName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "workspace not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load workspace settings"})
		return
	}

	rows, err := h.DB.QueryContext(
		r.Context(),
		`SELECT id::text, COALESCE(display_name, ''), COALESCE(email, ''), COALESCE(role, 'owner')
		 FROM users
		 WHERE org_id = $1
		 ORDER BY LOWER(COALESCE(NULLIF(display_name, ''), NULLIF(email, ''), id::text)) ASC`,
		identity.OrgID,
	)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load workspace members"})
		return
	}
	defer rows.Close()

	members := make([]settingsWorkspaceMemberResponse, 0)
	for rows.Next() {
		var member settingsWorkspaceMemberResponse
		var role string
		if err := rows.Scan(&member.ID, &member.Name, &member.Email, &role); err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read workspace member"})
			return
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
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load workspace members"})
		return
	}

	sendJSON(w, http.StatusOK, settingsWorkspaceResponse{
		Name:    strings.TrimSpace(workspaceName.String),
		Members: members,
	})
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

	var openClawWebhookURL sql.NullString
	err := h.DB.QueryRowContext(
		r.Context(),
		`SELECT COALESCE(openclaw_webhook_url, '') FROM organizations WHERE id = $1`,
		identity.OrgID,
	).Scan(&openClawWebhookURL)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "workspace not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load integration settings"})
		return
	}

	rows, err := h.DB.QueryContext(
		r.Context(),
		`SELECT id::text, name, token_prefix, created_at
		 FROM git_access_tokens
		 WHERE org_id = $1
		   AND user_id = $2
		   AND revoked_at IS NULL
		 ORDER BY created_at DESC`,
		identity.OrgID,
		identity.UserID,
	)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load API keys"})
		return
	}
	defer rows.Close()

	apiKeys := make([]settingsIntegrationsAPIKeyResponse, 0)
	for rows.Next() {
		var key settingsIntegrationsAPIKeyResponse
		if err := rows.Scan(&key.ID, &key.Name, &key.Prefix, &key.CreatedAt); err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load API keys"})
			return
		}
		apiKeys = append(apiKeys, key)
	}
	if err := rows.Err(); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load API keys"})
		return
	}

	sendJSON(w, http.StatusOK, settingsIntegrationsResponse{
		OpenClawWebhookURL: strings.TrimSpace(openClawWebhookURL.String),
		APIKeys:            apiKeys,
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
