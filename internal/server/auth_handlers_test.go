package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestLoginFallsBackToEmailOrganizationWhenDefaultMissing(t *testing.T) {
	orgID := uuid.New()
	handler := authHandlers{
		service: &fallbackAuthService{
			orgID: orgID,
		},
		users: fallbackUserRepo{
			user: repo.HumanUser{
				ID:             uuid.New(),
				OrganizationID: orgID,
				Email:          "admin@localhost",
				DisplayName:    "Admin",
				Role:           "admin",
			},
		},
	}

	body, err := json.Marshal(map[string]any{
		"email":    "admin@localhost",
		"password": "test-bootstrap-password",
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.login(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestRevokeOtherSessionsKeepsCurrentSession(t *testing.T) {
	userID := uuid.New()
	currentSessionID := uuid.New()
	otherA := uuid.New()
	otherB := uuid.New()

	sessionRepo := &fakeAuthSessionRepo{
		active: []repo.AuthSession{
			{ID: currentSessionID, UserID: userID, ExpiresAt: time.Now().Add(time.Hour)},
			{ID: otherA, UserID: userID, ExpiresAt: time.Now().Add(time.Hour)},
			{ID: otherB, UserID: userID, ExpiresAt: time.Now().Add(time.Hour)},
		},
		revoked: make(map[uuid.UUID]bool),
	}

	handler := authHandlers{
		service: &fakeAuthService{
			validateSessionResult: &authsvc.SessionInfo{ID: currentSessionID, UserID: userID, ExpiresAt: time.Now().Add(time.Hour)},
		},
		sessions: sessionRepo,
	}

	req := httptest.NewRequest(http.MethodDelete, "/v1/auth/sessions", nil)
	ctx := middleware.WithPrincipal(req.Context(), middleware.Principal{UserID: userID, OrganizationID: uuid.New()})
	ctx = middleware.WithSessionToken(ctx, "current-session-token")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.revokeOtherSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if sessionRepo.revoked[currentSessionID] {
		t.Fatal("current session was revoked")
	}
	if !sessionRepo.revoked[otherA] || !sessionRepo.revoked[otherB] {
		t.Fatalf("expected other sessions revoked, got revoked=%v", sessionRepo.revoked)
	}
}

type fakeAuthService struct {
	validateSessionResult *authsvc.SessionInfo
	validateSessionErr    error
}

func (f *fakeAuthService) Login(context.Context, string, string, string, string) (*authsvc.LoginResult, error) {
	return nil, nil
}

func (f *fakeAuthService) Logout(context.Context, string) error { return nil }

func (f *fakeAuthService) RefreshSession(context.Context, string) (*authsvc.SessionInfo, error) {
	return nil, nil
}

func (f *fakeAuthService) ValidateSession(context.Context, string) (*authsvc.SessionInfo, error) {
	return f.validateSessionResult, f.validateSessionErr
}

func (f *fakeAuthService) ValidateAPIKey(context.Context, string) (*authsvc.APIKeyInfo, error) {
	return nil, nil
}

func (f *fakeAuthService) IssueAPIKey(context.Context, uuid.UUID, string, []string, *time.Time) (*authsvc.IssueResult, error) {
	return nil, nil
}

func (f *fakeAuthService) RevokeAPIKey(context.Context, uuid.UUID, uuid.UUID, string, uuid.UUID) error {
	return nil
}

func (f *fakeAuthService) ListAPIKeys(context.Context, uuid.UUID) ([]*authsvc.APIKeyInfo, error) {
	return nil, nil
}

func (f *fakeAuthService) MagicLink(context.Context, string) (*authsvc.MagicLinkResult, error) {
	return nil, nil
}

func (f *fakeAuthService) ResetPassword(context.Context, string, string) error { return nil }

func (f *fakeAuthService) UnlockAccount(context.Context, uuid.UUID) error { return nil }

type fakeAuthSessionRepo struct {
	active  []repo.AuthSession
	revoked map[uuid.UUID]bool
}

func (f *fakeAuthSessionRepo) ListActive(context.Context, uuid.UUID) ([]repo.AuthSession, error) {
	return append([]repo.AuthSession{}, f.active...), nil
}

func (f *fakeAuthSessionRepo) Revoke(_ context.Context, id uuid.UUID) error {
	f.revoked[id] = true
	return nil
}

type fallbackAuthService struct {
	orgID uuid.UUID
}

func (f *fallbackAuthService) Login(ctx context.Context, _, _, _, _ string) (*authsvc.LoginResult, error) {
	if _, ok := authsvc.OrganizationIDFromContext(ctx); !ok {
		return nil, authsvc.ErrNoDefaultOrganization
	}
	return &authsvc.LoginResult{
		SessionToken: "token",
		Session: &authsvc.SessionInfo{
			ID:             uuid.New(),
			UserID:         uuid.New(),
			OrganizationID: f.orgID,
			Email:          "admin@localhost",
			DisplayName:    "Admin",
			Role:           "admin",
			ExpiresAt:      time.Now().Add(time.Hour),
		},
	}, nil
}

func (f *fallbackAuthService) Logout(context.Context, string) error { return nil }

func (f *fallbackAuthService) RefreshSession(context.Context, string) (*authsvc.SessionInfo, error) {
	return nil, nil
}

func (f *fallbackAuthService) ValidateSession(context.Context, string) (*authsvc.SessionInfo, error) {
	return nil, nil
}

func (f *fallbackAuthService) ValidateAPIKey(context.Context, string) (*authsvc.APIKeyInfo, error) {
	return nil, nil
}

func (f *fallbackAuthService) IssueAPIKey(context.Context, uuid.UUID, string, []string, *time.Time) (*authsvc.IssueResult, error) {
	return nil, nil
}

func (f *fallbackAuthService) RevokeAPIKey(context.Context, uuid.UUID, uuid.UUID, string, uuid.UUID) error {
	return nil
}

func (f *fallbackAuthService) ListAPIKeys(context.Context, uuid.UUID) ([]*authsvc.APIKeyInfo, error) {
	return nil, nil
}

func (f *fallbackAuthService) MagicLink(context.Context, string) (*authsvc.MagicLinkResult, error) {
	return nil, nil
}

func (f *fallbackAuthService) ResetPassword(context.Context, string, string) error { return nil }

func (f *fallbackAuthService) UnlockAccount(context.Context, uuid.UUID) error { return nil }

type fallbackUserRepo struct {
	user repo.HumanUser
}

func (f fallbackUserRepo) GetByID(context.Context, uuid.UUID) (repo.HumanUser, error) {
	return repo.HumanUser{}, repo.ErrNotFound
}

func (f fallbackUserRepo) GetByEmail(context.Context, uuid.UUID, string) (repo.HumanUser, error) {
	return repo.HumanUser{}, repo.ErrNotFound
}

func (f fallbackUserRepo) GetByEmailAnyOrg(context.Context, string) (repo.HumanUser, error) {
	return f.user, nil
}
