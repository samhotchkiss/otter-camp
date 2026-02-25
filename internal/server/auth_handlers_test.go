package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

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
