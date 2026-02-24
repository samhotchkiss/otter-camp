package server

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

type authHandlers struct {
	service auth.Service
}

func newAuthHandlers(service auth.Service) authHandlers {
	return authHandlers{service: service}
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

func (h authHandlers) login(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "auth service unavailable")
		return
	}

	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	result, err := h.service.Login(r.Context(), strings.TrimSpace(req.Email), req.Password, requestClientIP(r), r.UserAgent())
	if err != nil {
		status, code, message := mapLoginError(err)
		api.Error(w, status, code, message)
		return
	}
	if result == nil || result.Session == nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "login failed")
		return
	}

	api.JSON(w, http.StatusOK, map[string]any{
		"token":      result.SessionToken,
		"expires_at": result.Session.ExpiresAt,
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
		return http.StatusUnauthorized, api.ErrCodeUnauthorized, "account is locked"
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
