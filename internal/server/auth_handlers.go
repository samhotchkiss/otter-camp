package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type authHandlers struct {
	service  auth.Service
	users    authUserRepository
	sessions authSessionRepository
}

type authUserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.HumanUser, error)
	GetByEmail(ctx context.Context, organizationID uuid.UUID, email string) (repo.HumanUser, error)
}

type authSessionRepository interface {
	ListActive(ctx context.Context, userID uuid.UUID) ([]repo.AuthSession, error)
	Revoke(ctx context.Context, id uuid.UUID) error
}

func newAuthHandlers(service auth.Service, users authUserRepository, sessions authSessionRepository) authHandlers {
	return authHandlers{service: service, users: users, sessions: sessions}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type issueAPIKeyRequest struct {
	DisplayName string     `json:"display_name"`
	Scopes      []string   `json:"scopes"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

type adminResetPasswordRequest struct {
	NewPassword string `json:"new_password"`
}

type userResponse struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	Email          string    `json:"email"`
	DisplayName    string    `json:"display_name"`
	Role           string    `json:"role"`
}

type apiKeyResponse struct {
	ID          uuid.UUID  `json:"id"`
	Key         string     `json:"key,omitempty"`
	KeyPrefix   string     `json:"key_prefix"`
	DisplayName string     `json:"display_name"`
	Scopes      []string   `json:"scopes"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

type authSessionResponse struct {
	SessionID  uuid.UUID `json:"session_id"`
	ClientType string    `json:"client_type"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	IPAddress  string    `json:"ip_address"`
}

func (h authHandlers) login(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "auth service unavailable")
		return
	}
	// Biometric auth is intentionally client-side only. The server receives normal credentials/API keys.

	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	result, err := h.service.Login(r.Context(), strings.TrimSpace(req.Email), req.Password, requestClientIP(r), r.UserAgent())
	if err != nil {
		if errors.Is(err, auth.ErrRateLimited) {
			// Default auth limiter window is 15 minutes.
			w.Header().Set("Retry-After", "900")
		}
		status, code, message := mapLoginError(err)
		api.Error(w, status, code, message)
		return
	}
	if result == nil || result.Session == nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "login failed")
		return
	}

	api.JSON(w, http.StatusOK, map[string]any{
		"token":         result.SessionToken,
		"session_token": result.SessionToken,
		"expires_at":    result.Session.ExpiresAt,
		"user": userResponse{
			ID:             result.Session.UserID,
			OrganizationID: result.Session.OrganizationID,
			Email:          result.Session.Email,
			DisplayName:    result.Session.DisplayName,
			Role:           result.Session.Role,
		},
	})
}

func (h authHandlers) logout(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "auth service unavailable")
		return
	}

	sessionToken, ok := middleware.SessionTokenFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "session token required")
		return
	}

	if err := h.service.Logout(r.Context(), sessionToken); err != nil {
		status, code, message := mapAuthError(err)
		api.Error(w, status, code, message)
		return
	}

	api.JSON(w, http.StatusOK, map[string]bool{"revoked": true})
}

func (h authHandlers) refresh(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "auth service unavailable")
		return
	}

	sessionToken, ok := middleware.SessionTokenFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "session token required")
		return
	}

	session, err := h.service.RefreshSession(r.Context(), sessionToken)
	if err != nil {
		status, code, message := mapAuthError(err)
		api.Error(w, status, code, message)
		return
	}

	api.JSON(w, http.StatusOK, map[string]time.Time{"expires_at": session.ExpiresAt})
}

func (h authHandlers) me(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	api.JSON(w, http.StatusOK, userResponse{
		ID:             principal.UserID,
		OrganizationID: principal.OrganizationID,
		Email:          principal.Email,
		DisplayName:    principal.DisplayName,
		Role:           principal.Role,
	})
}

func (h authHandlers) listSessions(w http.ResponseWriter, r *http.Request) {
	if h.sessions == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "session repository unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	sessions, err := h.sessions.ListActive(r.Context(), principal.UserID)
	if err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list sessions")
		return
	}

	items := make([]authSessionResponse, 0, len(sessions))
	for _, session := range sessions {
		ipAddress := ""
		if session.IPAddress != nil {
			ipAddress = strings.TrimSpace(*session.IPAddress)
		}
		userAgent := ""
		if session.UserAgent != nil {
			userAgent = strings.TrimSpace(*session.UserAgent)
		}
		items = append(items, authSessionResponse{
			SessionID:  session.ID,
			ClientType: detectClientType(userAgent),
			CreatedAt:  session.CreatedAt,
			LastUsedAt: session.LastUsedAt,
			IPAddress:  ipAddress,
		})
	}

	api.JSON(w, http.StatusOK, map[string]any{"sessions": items})
}

func (h authHandlers) revokeSession(w http.ResponseWriter, r *http.Request) {
	if h.sessions == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "session repository unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	targetSessionID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil || targetSessionID == uuid.Nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid session id")
		return
	}

	sessions, err := h.sessions.ListActive(r.Context(), principal.UserID)
	if err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list sessions")
		return
	}

	found := false
	for _, session := range sessions {
		if session.ID == targetSessionID {
			found = true
			break
		}
	}
	if !found {
		api.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "session not found")
		return
	}

	if err := h.sessions.Revoke(r.Context(), targetSessionID); err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to revoke session")
		return
	}

	api.JSON(w, http.StatusOK, map[string]bool{"revoked": true})
}

func (h authHandlers) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "auth service unavailable")
		return
	}
	if h.sessions == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "session repository unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	currentSessionToken, ok := middleware.SessionTokenFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "session token required")
		return
	}

	current, err := h.service.ValidateSession(r.Context(), currentSessionToken)
	if err != nil {
		status, code, message := mapAuthError(err)
		api.Error(w, status, code, message)
		return
	}

	sessions, err := h.sessions.ListActive(r.Context(), principal.UserID)
	if err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list sessions")
		return
	}

	revokedCount := 0
	for _, session := range sessions {
		if session.ID == current.ID {
			continue
		}
		if err := h.sessions.Revoke(r.Context(), session.ID); err != nil {
			api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to revoke sessions")
			return
		}
		revokedCount++
	}

	api.JSON(w, http.StatusOK, map[string]int{"revoked_count": revokedCount})
}

func (h authHandlers) issueAPIKey(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "auth service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	var req issueAPIKeyRequest
	if err := decodeJSON(r, &req); err != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.DisplayName) == "" {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "display_name is required")
		return
	}

	scopes := normalizeScopes(req.Scopes)
	issued, err := h.service.IssueAPIKey(r.Context(), principal.UserID, strings.TrimSpace(req.DisplayName), scopes, req.ExpiresAt)
	if err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to issue api key")
		return
	}

	api.JSON(w, http.StatusCreated, apiKeyResponse{
		ID:          issued.KeyID,
		Key:         issued.RawKey,
		KeyPrefix:   issued.KeyPrefix,
		DisplayName: issued.DisplayName,
		Scopes:      scopes,
		ExpiresAt:   req.ExpiresAt,
	})
}

func (h authHandlers) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "auth service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	keyID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid api key id")
		return
	}

	err = h.service.RevokeAPIKey(r.Context(), keyID, principal.UserID, principal.Role, principal.OrganizationID)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrForbidden):
			api.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		case errors.Is(err, auth.ErrInvalidAPIKey):
			api.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "api key not found")
		default:
			api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to revoke api key")
		}
		return
	}

	api.JSON(w, http.StatusOK, map[string]bool{"revoked": true})
}

func (h authHandlers) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "auth service unavailable")
		return
	}

	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	keys, err := h.service.ListAPIKeys(r.Context(), principal.UserID)
	if err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list api keys")
		return
	}

	response := make([]apiKeyResponse, 0, len(keys))
	for _, item := range keys {
		if item == nil {
			continue
		}
		createdAt := item.CreatedAt
		response = append(response, apiKeyResponse{
			ID:          item.ID,
			KeyPrefix:   item.KeyPrefix,
			DisplayName: item.DisplayName,
			Scopes:      append([]string{}, item.Scopes...),
			CreatedAt:   &createdAt,
			LastUsedAt:  item.LastUsedAt,
			ExpiresAt:   item.ExpiresAt,
			RevokedAt:   item.RevokedAt,
		})
	}

	api.JSON(w, http.StatusOK, response)
}

func (h authHandlers) listAdminUsers(w http.ResponseWriter, r *http.Request) {
	if h.users == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "user repository unavailable")
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	email := strings.TrimSpace(r.URL.Query().Get("email"))
	if email == "" {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "email query parameter is required")
		return
	}

	user, err := h.users.GetByEmail(r.Context(), principal.OrganizationID, email)
	if errors.Is(err, repo.ErrNotFound) {
		api.JSON(w, http.StatusOK, []userResponse{})
		return
	}
	if err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to fetch user")
		return
	}
	api.JSON(w, http.StatusOK, []userResponse{
		{
			ID:             user.ID,
			OrganizationID: user.OrganizationID,
			Email:          user.Email,
			DisplayName:    user.DisplayName,
			Role:           user.Role,
		},
	})
}

func (h authHandlers) adminResetPassword(w http.ResponseWriter, r *http.Request) {
	target, ok := h.loadAdminTargetUser(w, r)
	if !ok {
		return
	}
	var req adminResetPasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.NewPassword) == "" {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "new_password is required")
		return
	}

	ctx := auth.WithOrganizationID(r.Context(), target.OrganizationID)
	magic, err := h.service.MagicLink(ctx, target.Email)
	if err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to generate reset token")
		return
	}
	if err := h.service.ResetPassword(ctx, magic.Token, req.NewPassword); err != nil {
		status, code, message := mapAuthError(err)
		api.Error(w, status, code, message)
		return
	}
	api.JSON(w, http.StatusOK, map[string]bool{"reset": true})
}

func (h authHandlers) adminMagicLink(w http.ResponseWriter, r *http.Request) {
	target, ok := h.loadAdminTargetUser(w, r)
	if !ok {
		return
	}
	ctx := auth.WithOrganizationID(r.Context(), target.OrganizationID)
	magic, err := h.service.MagicLink(ctx, target.Email)
	if err != nil {
		status, code, message := mapAuthError(err)
		api.Error(w, status, code, message)
		return
	}

	baseURL := requestBaseURL(r)
	magicLinkURL := baseURL + "/auth/magic?token=" + url.QueryEscape(strings.TrimSpace(magic.Token))
	api.JSON(w, http.StatusOK, map[string]string{"magic_link_url": magicLinkURL})
}

func (h authHandlers) adminUnlockAccount(w http.ResponseWriter, r *http.Request) {
	target, ok := h.loadAdminTargetUser(w, r)
	if !ok {
		return
	}
	if err := h.service.UnlockAccount(r.Context(), target.ID); err != nil {
		status, code, message := mapAuthError(err)
		api.Error(w, status, code, message)
		return
	}
	api.JSON(w, http.StatusOK, map[string]bool{"unlocked": true})
}

func (h authHandlers) consumeMagicLink(w http.ResponseWriter, r *http.Request) {
	consumer, ok := h.service.(interface {
		ConsumeMagicLink(ctx context.Context, token, ipAddr, userAgent string) (*auth.LoginResult, error)
	})
	if !ok {
		api.Error(w, http.StatusNotImplemented, api.ErrCodeNotImplemented, "magic link login is unavailable")
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "token is required")
		return
	}
	result, err := consumer.ConsumeMagicLink(r.Context(), token, requestClientIP(r), r.UserAgent())
	if err != nil {
		status, code, message := mapAuthError(err)
		api.Error(w, status, code, message)
		return
	}
	if result == nil || result.Session == nil {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "invalid magic link")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "ottercamp_session",
		Value:    result.SessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		Expires:  result.Session.ExpiresAt,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (h authHandlers) loadAdminTargetUser(w http.ResponseWriter, r *http.Request) (repo.HumanUser, bool) {
	if h.service == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "auth service unavailable")
		return repo.HumanUser{}, false
	}
	if h.users == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "user repository unavailable")
		return repo.HumanUser{}, false
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return repo.HumanUser{}, false
	}
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid user id")
		return repo.HumanUser{}, false
	}
	target, err := h.users.GetByID(r.Context(), userID)
	if errors.Is(err, repo.ErrNotFound) {
		api.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "user not found")
		return repo.HumanUser{}, false
	}
	if err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to fetch user")
		return repo.HumanUser{}, false
	}
	if target.OrganizationID != principal.OrganizationID {
		api.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "user not found")
		return repo.HumanUser{}, false
	}
	return target, true
}

func decodeJSON(r *http.Request, out any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != nil && !errors.Is(err, io.EOF) {
		return errors.New("multiple json values")
	}
	return nil
}

func normalizeScopes(scopes []string) []string {
	seen := make(map[string]struct{}, len(scopes))
	result := make([]string, 0, len(scopes))
	for _, raw := range scopes {
		scope := strings.TrimSpace(raw)
		if scope == "" {
			continue
		}
		if _, exists := seen[scope]; exists {
			continue
		}
		seen[scope] = struct{}{}
		result = append(result, scope)
	}
	return result
}

func mapLoginError(err error) (status int, code, message string) {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return http.StatusUnauthorized, api.ErrCodeInvalidCredentials, "invalid credentials"
	case errors.Is(err, auth.ErrRateLimited):
		return http.StatusTooManyRequests, api.ErrCodeRateLimited, "too many authentication attempts"
	case errors.Is(err, auth.ErrAccountLocked):
		return http.StatusLocked, api.ErrCodeUnauthorized, "account is locked"
	default:
		return http.StatusInternalServerError, api.ErrCodeInternal, "login failed"
	}
}

func mapAuthError(err error) (status int, code, message string) {
	switch {
	case errors.Is(err, auth.ErrSessionExpired):
		return http.StatusUnauthorized, api.ErrCodeSessionExpired, "session expired"
	case errors.Is(err, auth.ErrSessionRevoked):
		return http.StatusUnauthorized, api.ErrCodeSessionRevoked, "session revoked"
	case errors.Is(err, auth.ErrInvalidSession):
		return http.StatusUnauthorized, api.ErrCodeUnauthorized, "invalid session"
	case errors.Is(err, auth.ErrInvalidAPIKey):
		return http.StatusUnauthorized, api.ErrCodeUnauthorized, "invalid api key"
	case errors.Is(err, auth.ErrTokenExpired):
		return http.StatusUnauthorized, api.ErrCodeUnauthorized, "token expired"
	case errors.Is(err, auth.ErrForbidden):
		return http.StatusForbidden, api.ErrCodeForbidden, "forbidden"
	default:
		return http.StatusInternalServerError, api.ErrCodeInternal, "request failed"
	}
}

func requestClientIP(r *http.Request) string {
	if forwardedFor := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwardedFor != "" {
		parts := strings.Split(forwardedFor, ",")
		if len(parts) > 0 {
			candidate := strings.TrimSpace(parts[0])
			if candidate != "" {
				return candidate
			}
		}
	}

	if host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr)); err == nil {
		return strings.TrimSpace(host)
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		scheme = strings.ToLower(proto)
	}
	host := strings.TrimSpace(r.Host)
	if forwardedHost := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); forwardedHost != "" {
		host = forwardedHost
	}
	if host == "" {
		host = "localhost:4110"
	}
	return scheme + "://" + host
}
