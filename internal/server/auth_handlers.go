package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/audit"
	"github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"golang.org/x/crypto/bcrypt"
)

type authHandlers struct {
	service       auth.Service
	users         authUserRepository
	sessions      authSessionRepository
	orgs          authOrganizationRepository
	auditRecorder audit.AuditRecorder
}

type authUserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.HumanUser, error)
	GetByEmail(ctx context.Context, organizationID uuid.UUID, email string) (repo.HumanUser, error)
}

type authUserCreator interface {
	Create(ctx context.Context, user repo.HumanUser) (repo.HumanUser, error)
}

type authUserUpdater interface {
	Update(ctx context.Context, user repo.HumanUser) (repo.HumanUser, error)
}

type authSessionRepository interface {
	ListActive(ctx context.Context, userID uuid.UUID) ([]repo.AuthSession, error)
	Revoke(ctx context.Context, id uuid.UUID) error
}

type authOrganizationRepository interface {
	Create(ctx context.Context, org repo.Organization) (repo.Organization, error)
}

type orgLookupByEmailRepository interface {
	GetByEmailAnyOrg(ctx context.Context, email string) (repo.HumanUser, error)
}

type rotatingSessionService interface {
	RefreshSessionToken(ctx context.Context, sessionToken, ipAddr, userAgent string) (*auth.LoginResult, error)
}

func newAuthHandlers(service auth.Service, users authUserRepository, sessions authSessionRepository, orgs authOrganizationRepository) authHandlers {
	return authHandlers{
		service:       service,
		users:         users,
		sessions:      sessions,
		orgs:          orgs,
		auditRecorder: audit.NewNoopRecorder(),
	}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type issueAPIKeyRequest struct {
	Name        string     `json:"name,omitempty"`
	DisplayName string     `json:"display_name"`
	Scopes      []string   `json:"scopes"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

type createUserRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
	Role     string `json:"role,omitempty"`
}

type createOrganizationRequest struct {
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	AdminEmail    string `json:"admin_email,omitempty"`
	AdminPassword string `json:"admin_password,omitempty"`
	AdminName     string `json:"admin_name,omitempty"`
}

type adminResetPasswordRequest struct {
	NewPassword string `json:"new_password"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type adminUpdateRoleRequest struct {
	Role string `json:"role"`
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
	Name        string     `json:"name,omitempty"`
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

type organizationResponse struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	Slug       string    `json:"slug"`
	AdminEmail string    `json:"admin_email,omitempty"`
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

	email := strings.ToLower(strings.TrimSpace(req.Email))
	result, err := h.service.Login(r.Context(), email, req.Password, requestClientIP(r), r.UserAgent())
	if errors.Is(err, auth.ErrNoDefaultOrganization) {
		if lookup, ok := h.users.(orgLookupByEmailRepository); ok {
			found, lookupErr := lookup.GetByEmailAnyOrg(r.Context(), email)
			switch {
			case lookupErr == nil:
				ctx := auth.WithOrganizationID(r.Context(), found.OrganizationID)
				result, err = h.service.Login(ctx, email, req.Password, requestClientIP(r), r.UserAgent())
			case errors.Is(lookupErr, repo.ErrNotFound):
				err = auth.ErrInvalidCredentials
			default:
				err = lookupErr
			}
		}
	}
	if err != nil {
		h.recordFailedLoginAuditEvent(r.Context(), r, email, err)
		if errors.Is(err, auth.ErrRateLimited) {
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

	targetType := "auth_session"
	targetID := result.Session.ID
	h.recordAuditEvent(r.Context(), audit.Event{
		OrgID:         result.Session.OrganizationID,
		EventType:     audit.EventAuthLogin,
		PrincipalType: "human",
		PrincipalID:   result.Session.UserID,
		TargetType:    &targetType,
		TargetID:      &targetID,
		IP:            requestClientIP(r),
		Outcome:       "success",
		Metadata: map[string]any{
			"email":      strings.TrimSpace(result.Session.Email),
			"user_agent": strings.TrimSpace(r.UserAgent()),
		},
	})

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

	principal, hasPrincipal := middleware.PrincipalFromContext(r.Context())
	if err := h.service.Logout(r.Context(), sessionToken); err != nil {
		if hasPrincipal {
			h.recordAuditEvent(r.Context(), audit.Event{
				OrgID:         principal.OrganizationID,
				EventType:     audit.EventAuthLogout,
				PrincipalType: "human",
				PrincipalID:   principal.UserID,
				IP:            requestClientIP(r),
				Outcome:       "failure",
				Metadata: map[string]any{
					"error": strings.TrimSpace(err.Error()),
				},
			})
		}
		status, code, message := mapAuthError(err)
		api.Error(w, status, code, message)
		return
	}
	if hasPrincipal {
		h.recordAuditEvent(r.Context(), audit.Event{
			OrgID:         principal.OrganizationID,
			EventType:     audit.EventAuthLogout,
			PrincipalType: "human",
			PrincipalID:   principal.UserID,
			IP:            requestClientIP(r),
			Outcome:       "success",
		})
	}

	api.JSON(w, http.StatusOK, nil)
}

func (h authHandlers) changePassword(w http.ResponseWriter, r *http.Request) {
	updater, ok := h.users.(authUserUpdater)
	if h.users == nil || !ok || updater == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "user repository unavailable")
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

	var req changePasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	currentPassword := strings.TrimSpace(req.CurrentPassword)
	newPassword := strings.TrimSpace(req.NewPassword)
	if currentPassword == "" || newPassword == "" {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "current_password and new_password are required")
		return
	}

	user, err := h.users.GetByID(r.Context(), principal.UserID)
	if err != nil {
		api.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}
	if user.OrganizationID != principal.OrganizationID {
		api.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}
	if user.PasswordHash == nil || bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(currentPassword)) != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "current_password is incorrect")
		return
	}

	hashed, err := hashPasswordForMode(newPassword)
	if err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to update password")
		return
	}
	user.PasswordHash = &hashed
	if _, err := updater.Update(r.Context(), user); err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to update password")
		return
	}

	sessions, err := h.sessions.ListActive(r.Context(), principal.UserID)
	if err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to revoke sessions")
		return
	}
	for _, session := range sessions {
		if err := h.sessions.Revoke(r.Context(), session.ID); err != nil {
			api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to revoke sessions")
			return
		}
	}

	api.JSON(w, http.StatusOK, nil)
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

	if rotator, ok := h.service.(rotatingSessionService); ok {
		login, err := rotator.RefreshSessionToken(r.Context(), sessionToken, requestClientIP(r), r.UserAgent())
		if err != nil {
			status, code, message := mapAuthError(err)
			api.Error(w, status, code, message)
			return
		}
		if login == nil || login.Session == nil {
			api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "refresh failed")
			return
		}
		api.JSON(w, http.StatusOK, map[string]any{
			"token":         login.SessionToken,
			"session_token": login.SessionToken,
			"expires_at":    login.Session.ExpiresAt,
		})
		return
	}

	session, err := h.service.RefreshSession(r.Context(), sessionToken)
	if err != nil {
		status, code, message := mapAuthError(err)
		api.Error(w, status, code, message)
		return
	}

	api.JSON(w, http.StatusOK, map[string]any{
		"token":         sessionToken,
		"session_token": sessionToken,
		"expires_at":    session.ExpiresAt,
	})
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

	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(req.Name)
	}
	if displayName == "" {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "display_name is required")
		return
	}

	scopes := normalizeScopes(req.Scopes)
	issued, err := h.service.IssueAPIKey(r.Context(), principal.UserID, displayName, scopes, req.ExpiresAt)
	if err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to issue api key")
		return
	}

	targetType := "api_key"
	targetID := issued.KeyID
	h.recordAuditEvent(r.Context(), audit.Event{
		OrgID:         principal.OrganizationID,
		EventType:     audit.EventAPIKeyCreated,
		PrincipalType: "human",
		PrincipalID:   principal.UserID,
		TargetType:    &targetType,
		TargetID:      &targetID,
		IP:            requestClientIP(r),
		Outcome:       "success",
		Metadata: map[string]any{
			"display_name": strings.TrimSpace(issued.DisplayName),
			"key_prefix":   strings.TrimSpace(issued.KeyPrefix),
			"scopes":       append([]string{}, scopes...),
		},
	})

	api.JSON(w, http.StatusCreated, apiKeyResponse{
		ID:          issued.KeyID,
		Key:         issued.RawKey,
		KeyPrefix:   issued.KeyPrefix,
		Name:        issued.DisplayName,
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
		h.recordAuditEvent(r.Context(), audit.Event{
			OrgID:         principal.OrganizationID,
			EventType:     audit.EventAPIKeyDeleted,
			PrincipalType: "human",
			PrincipalID:   principal.UserID,
			IP:            requestClientIP(r),
			Outcome:       "failure",
			Metadata: map[string]any{
				"api_key_id": keyID.String(),
				"error":      strings.TrimSpace(err.Error()),
			},
		})
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

	targetType := "api_key"
	h.recordAuditEvent(r.Context(), audit.Event{
		OrgID:         principal.OrganizationID,
		EventType:     audit.EventAPIKeyDeleted,
		PrincipalType: "human",
		PrincipalID:   principal.UserID,
		TargetType:    &targetType,
		TargetID:      &keyID,
		IP:            requestClientIP(r),
		Outcome:       "success",
	})

	w.WriteHeader(http.StatusNoContent)
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
			Name:        item.DisplayName,
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

func (h authHandlers) createUser(w http.ResponseWriter, r *http.Request) {
	creator, ok := h.users.(authUserCreator)
	if !ok || creator == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "user repository unavailable")
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	var req createUserRequest
	if err := decodeJSON(r, &req); err != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	password := strings.TrimSpace(req.Password)
	displayName := strings.TrimSpace(req.Name)
	if email == "" || password == "" || displayName == "" {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "email, password, and name are required")
		return
	}

	role, err := normalizeUserRole(req.Role, "member")
	if err != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, err.Error())
		return
	}

	created, err := createUserInOrganization(r.Context(), creator, principal.OrganizationID, email, password, displayName, role)
	if errors.Is(err, repo.ErrConflict) {
		api.Error(w, http.StatusConflict, api.ErrCodeConflict, "user already exists")
		return
	}
	if err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to create user")
		return
	}

	api.JSON(w, http.StatusCreated, userResponse{
		ID:             created.ID,
		OrganizationID: created.OrganizationID,
		Email:          created.Email,
		DisplayName:    created.DisplayName,
		Role:           created.Role,
	})
}

func (h authHandlers) createOrganization(w http.ResponseWriter, r *http.Request) {
	if h.orgs == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "organization repository unavailable")
		return
	}
	creator, ok := h.users.(authUserCreator)
	if !ok || creator == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "user repository unavailable")
		return
	}
	if _, ok := middleware.PrincipalFromContext(r.Context()); !ok {
		api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}

	var req createOrganizationRequest
	if err := decodeJSON(r, &req); err != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	slug := strings.TrimSpace(req.Slug)
	name := strings.TrimSpace(req.Name)
	if slug == "" || name == "" {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "slug and name are required")
		return
	}

	adminEmail := strings.TrimSpace(req.AdminEmail)
	if adminEmail == "" {
		adminEmail = fmt.Sprintf("%s-admin@localhost", slug)
	}
	adminPassword := strings.TrimSpace(req.AdminPassword)
	if adminPassword == "" {
		adminPassword = strings.TrimSpace(os.Getenv("OTTERCAMP_ADMIN_PASSWORD"))
	}
	if adminPassword == "" {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "admin_password is required")
		return
	}
	adminName := strings.TrimSpace(req.AdminName)
	if adminName == "" {
		adminName = "Admin"
	}

	org, err := h.orgs.Create(r.Context(), repo.Organization{
		Slug:        slug,
		DisplayName: name,
	})
	if errors.Is(err, repo.ErrConflict) {
		api.Error(w, http.StatusConflict, api.ErrCodeConflict, "organization already exists")
		return
	}
	if err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to create organization")
		return
	}

	if _, err := createUserInOrganization(r.Context(), creator, org.ID, adminEmail, adminPassword, adminName, "admin"); err != nil {
		if errors.Is(err, repo.ErrConflict) {
			api.Error(w, http.StatusConflict, api.ErrCodeConflict, "admin user already exists")
			return
		}
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to create organization admin")
		return
	}

	api.JSON(w, http.StatusCreated, organizationResponse{
		ID:         org.ID,
		Name:       org.DisplayName,
		Slug:       org.Slug,
		AdminEmail: adminEmail,
	})
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
	api.JSON(w, http.StatusOK, []userResponse{{
		ID:             user.ID,
		OrganizationID: user.OrganizationID,
		Email:          user.Email,
		DisplayName:    user.DisplayName,
		Role:           user.Role,
	}})
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

func (h authHandlers) adminUpdateUserRole(w http.ResponseWriter, r *http.Request) {
	target, ok := h.loadAdminTargetUser(w, r)
	if !ok {
		return
	}

	updater, ok := h.users.(authUserUpdater)
	if !ok || updater == nil {
		api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "user repository unavailable")
		return
	}

	var req adminUpdateRoleRequest
	if err := decodeJSON(r, &req); err != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	role, err := normalizeUserRole(req.Role, "")
	if err != nil {
		api.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, err.Error())
		return
	}

	updated, err := updater.Update(r.Context(), repo.HumanUser{
		ID:                  target.ID,
		OrganizationID:      target.OrganizationID,
		Email:               target.Email,
		DisplayName:         target.DisplayName,
		PasswordHash:        target.PasswordHash,
		Role:                role,
		IsActive:            target.IsActive,
		FailedLoginAttempts: target.FailedLoginAttempts,
		LockedUntil:         target.LockedUntil,
		LastLoginAt:         target.LastLoginAt,
		Settings:            target.Settings,
	})
	if errors.Is(err, repo.ErrNotFound) {
		api.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "user not found")
		return
	}
	if err != nil {
		api.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to update user role")
		return
	}

	principal, hasPrincipal := middleware.PrincipalFromContext(r.Context())
	if hasPrincipal {
		targetType := "human_user"
		targetID := updated.ID
		h.recordAuditEvent(r.Context(), audit.Event{
			OrgID:         principal.OrganizationID,
			EventType:     audit.EventUserRoleChanged,
			PrincipalType: "human",
			PrincipalID:   principal.UserID,
			TargetType:    &targetType,
			TargetID:      &targetID,
			IP:            requestClientIP(r),
			Outcome:       "success",
			Metadata: map[string]any{
				"old_role": strings.ToLower(strings.TrimSpace(target.Role)),
				"new_role": strings.ToLower(strings.TrimSpace(updated.Role)),
			},
		})
	}

	api.JSON(w, http.StatusOK, userResponse{
		ID:             updated.ID,
		OrganizationID: updated.OrganizationID,
		Email:          updated.Email,
		DisplayName:    updated.DisplayName,
		Role:           updated.Role,
	})
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

func (h authHandlers) recordAuditEvent(ctx context.Context, event audit.Event) {
	if h.auditRecorder == nil {
		return
	}
	h.auditRecorder.RecordAsync(ctx, event)
}

func (h authHandlers) recordFailedLoginAuditEvent(ctx context.Context, r *http.Request, email string, loginErr error) {
	if h.auditRecorder == nil {
		return
	}
	if !errors.Is(loginErr, auth.ErrInvalidCredentials) && !errors.Is(loginErr, auth.ErrAccountLocked) {
		return
	}

	lookup, ok := h.users.(orgLookupByEmailRepository)
	if !ok || lookup == nil {
		return
	}

	user, err := lookup.GetByEmailAnyOrg(ctx, email)
	if err != nil {
		return
	}

	reason := "invalid_credentials"
	if errors.Is(loginErr, auth.ErrAccountLocked) {
		reason = "account_locked"
	}

	h.auditRecorder.RecordAsync(ctx, audit.Event{
		OrgID:         user.OrganizationID,
		EventType:     audit.EventAuthLoginFailed,
		PrincipalType: "human",
		PrincipalID:   user.ID,
		IP:            requestClientIP(r),
		Outcome:       "failure",
		Metadata: map[string]any{
			"email":      strings.TrimSpace(email),
			"user_agent": strings.TrimSpace(r.UserAgent()),
			"reason":     reason,
		},
	})
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

func normalizeUserRole(rawRole, defaultRole string) (string, error) {
	role := strings.ToLower(strings.TrimSpace(rawRole))
	if role == "" {
		role = strings.ToLower(strings.TrimSpace(defaultRole))
	}
	switch role {
	case "owner", "admin", "member", "viewer":
		return role, nil
	default:
		return "", fmt.Errorf("invalid role")
	}
}

func createUserInOrganization(ctx context.Context, creator authUserCreator, orgID uuid.UUID, email, password, displayName, role string) (repo.HumanUser, error) {
	passwordHash, err := hashPasswordForMode(password)
	if err != nil {
		return repo.HumanUser{}, err
	}
	return creator.Create(ctx, repo.HumanUser{
		OrganizationID: orgID,
		Email:          strings.TrimSpace(email),
		DisplayName:    strings.TrimSpace(displayName),
		PasswordHash:   &passwordHash,
		Role:           role,
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
}

func hashPasswordForMode(password string) (string, error) {
	cost := 12
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OTTERCAMP_MODE")), "test") {
		cost = bcrypt.MinCost
	}
	hashBytes, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", err
	}
	return string(hashBytes), nil
}

func mapLoginError(err error) (status int, code, message string) {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return http.StatusUnauthorized, api.ErrCodeInvalidCredentials, "invalid credentials"
	case errors.Is(err, auth.ErrRateLimited):
		return http.StatusTooManyRequests, api.ErrCodeRateLimited, "too many authentication attempts"
	case errors.Is(err, auth.ErrAccountLocked):
		// Return the same message as invalid credentials to prevent user enumeration
		// via account lockout state disclosure.
		return http.StatusUnauthorized, api.ErrCodeInvalidCredentials, "invalid credentials"
	default:
		return http.StatusInternalServerError, api.ErrCodeInternal, "login failed"
	}
}

func mapAuthError(err error) (status int, code, message string) {
	switch {
	case errors.Is(err, auth.ErrSessionExpired):
		return http.StatusUnauthorized, api.ErrCodeSessionExpired, "session expired"
	case errors.Is(err, auth.ErrSessionRevoked):
		return http.StatusUnauthorized, api.ErrCodeUnauthorized, "session invalid"
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
