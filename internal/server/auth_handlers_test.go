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
	"github.com/samhotchkiss/otter-camp/internal/audit"
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

func TestLoginRecordsAuditEventWithIPAndOutcome(t *testing.T) {
	orgID := uuid.New()
	userID := uuid.New()
	sessionID := uuid.New()
	recorder := &capturedAuditRecorder{}
	handler := authHandlers{
		service: &fakeAuthService{
			loginResult: &authsvc.LoginResult{
				SessionToken: "session-token",
				Session: &authsvc.SessionInfo{
					ID:             sessionID,
					UserID:         userID,
					OrganizationID: orgID,
					Email:          "audit@example.com",
					DisplayName:    "Audit User",
					Role:           "admin",
					ExpiresAt:      time.Now().Add(time.Hour),
				},
			},
		},
		auditRecorder: recorder,
	}

	body, err := json.Marshal(map[string]any{
		"email":    "audit@example.com",
		"password": "pw",
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	rr := httptest.NewRecorder()

	handler.login(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}

	events := recorder.Events()
	if len(events) != 1 {
		t.Fatalf("audit event count=%d want=1", len(events))
	}
	event := events[0]
	if event.EventType != audit.EventAuthLogin {
		t.Fatalf("event type=%q want=%q", event.EventType, audit.EventAuthLogin)
	}
	if event.OrgID != orgID || event.PrincipalID != userID {
		t.Fatalf("event principal/org mismatch: org=%s user=%s", event.OrgID, event.PrincipalID)
	}
	if event.IP != "203.0.113.5" {
		t.Fatalf("event ip=%q want=%q", event.IP, "203.0.113.5")
	}
	if event.Outcome != "success" {
		t.Fatalf("event outcome=%q want=%q", event.Outcome, "success")
	}
	if event.TargetID == nil || *event.TargetID != sessionID {
		t.Fatalf("target id=%v want=%s", event.TargetID, sessionID)
	}
}

func TestAPIKeyIssueAndRevokeRecordAuditEvents(t *testing.T) {
	orgID := uuid.New()
	userID := uuid.New()
	keyID := uuid.New()
	recorder := &capturedAuditRecorder{}
	handler := authHandlers{
		service: &fakeAuthService{
			issueAPIKeyResult: &authsvc.IssueResult{
				KeyID:       keyID,
				RawKey:      "otk_123",
				KeyPrefix:   "otk_123",
				DisplayName: "CI",
				CreatedAt:   time.Now().UTC(),
			},
		},
		auditRecorder: recorder,
	}

	issueBody, err := json.Marshal(map[string]any{
		"display_name": "CI",
		"scopes":       []string{"read:audit"},
	})
	if err != nil {
		t.Fatalf("marshal issue body: %v", err)
	}
	issueReq := httptest.NewRequest(http.MethodPost, "/v1/api-keys", bytes.NewReader(issueBody))
	issueReq.Header.Set("Content-Type", "application/json")
	issueReq.Header.Set("X-Forwarded-For", "198.51.100.10")
	issueReq = issueReq.WithContext(middleware.WithPrincipal(issueReq.Context(), middleware.Principal{
		UserID:         userID,
		OrganizationID: orgID,
		Role:           "admin",
	}))
	issueRR := httptest.NewRecorder()
	handler.issueAPIKey(issueRR, issueReq)
	if issueRR.Code != http.StatusCreated {
		t.Fatalf("issue status=%d want=%d body=%s", issueRR.Code, http.StatusCreated, issueRR.Body.String())
	}

	revokeReq := httptest.NewRequest(http.MethodDelete, "/v1/api-keys/"+keyID.String(), nil)
	revokeReq.Header.Set("X-Forwarded-For", "198.51.100.10")
	revokeReq = revokeReq.WithContext(middleware.WithPrincipal(revokeReq.Context(), middleware.Principal{
		UserID:         userID,
		OrganizationID: orgID,
		Role:           "admin",
	}))
	revokeReq = withRouteParams(revokeReq, map[string]string{"id": keyID.String()})
	revokeRR := httptest.NewRecorder()
	handler.revokeAPIKey(revokeRR, revokeReq)
	if revokeRR.Code != http.StatusNoContent {
		t.Fatalf("revoke status=%d want=%d body=%s", revokeRR.Code, http.StatusNoContent, revokeRR.Body.String())
	}

	events := recorder.Events()
	if len(events) != 2 {
		t.Fatalf("audit event count=%d want=2", len(events))
	}
	if events[0].EventType != audit.EventAPIKeyCreated {
		t.Fatalf("event[0] type=%q want=%q", events[0].EventType, audit.EventAPIKeyCreated)
	}
	if events[1].EventType != audit.EventAPIKeyDeleted {
		t.Fatalf("event[1] type=%q want=%q", events[1].EventType, audit.EventAPIKeyDeleted)
	}
	if events[1].Outcome != "success" {
		t.Fatalf("event[1] outcome=%q want=%q", events[1].Outcome, "success")
	}
	if events[1].TargetID == nil || *events[1].TargetID != keyID {
		t.Fatalf("event[1] target id=%v want=%s", events[1].TargetID, keyID)
	}
}

func TestAdminUpdateUserRoleRecordsRoleChangedAuditEvent(t *testing.T) {
	orgID := uuid.New()
	adminID := uuid.New()
	userID := uuid.New()
	recorder := &capturedAuditRecorder{}
	users := &fakeRoleUpdateUserRepo{
		getByIDFn: func(_ context.Context, id uuid.UUID) (repo.HumanUser, error) {
			if id != userID {
				return repo.HumanUser{}, repo.ErrNotFound
			}
			return repo.HumanUser{
				ID:             userID,
				OrganizationID: orgID,
				Email:          "member@example.com",
				DisplayName:    "Member",
				Role:           "member",
				Settings:       []byte(`{}`),
			}, nil
		},
		updateFn: func(_ context.Context, user repo.HumanUser) (repo.HumanUser, error) {
			user.Role = "viewer"
			return user, nil
		},
	}
	handler := authHandlers{
		service:       &fakeAuthService{},
		users:         users,
		auditRecorder: recorder,
	}

	body, err := json.Marshal(map[string]any{"role": "viewer"})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/v1/admin/users/"+userID.String()+"/role", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "203.0.113.90")
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{
		UserID:         adminID,
		OrganizationID: orgID,
		Role:           "admin",
	}))
	req = withRouteParams(req, map[string]string{"id": userID.String()})
	rr := httptest.NewRecorder()

	handler.adminUpdateUserRole(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	events := recorder.Events()
	if len(events) != 1 {
		t.Fatalf("audit event count=%d want=1", len(events))
	}
	if events[0].EventType != audit.EventUserRoleChanged {
		t.Fatalf("event type=%q want=%q", events[0].EventType, audit.EventUserRoleChanged)
	}
	if events[0].Metadata["old_role"] != "member" || events[0].Metadata["new_role"] != "viewer" {
		t.Fatalf("role metadata=%v", events[0].Metadata)
	}
	if events[0].IP != "203.0.113.90" || events[0].Outcome != "success" {
		t.Fatalf("ip/outcome=(%q,%q) want=(%q,%q)", events[0].IP, events[0].Outcome, "203.0.113.90", "success")
	}
}

type fakeAuthService struct {
	loginResult           *authsvc.LoginResult
	loginErr              error
	logoutErr             error
	issueAPIKeyResult     *authsvc.IssueResult
	issueAPIKeyErr        error
	revokeAPIKeyErr       error
	validateSessionResult *authsvc.SessionInfo
	validateSessionErr    error
}

func (f *fakeAuthService) Login(context.Context, string, string, string, string) (*authsvc.LoginResult, error) {
	return f.loginResult, f.loginErr
}

func (f *fakeAuthService) Logout(context.Context, string) error { return f.logoutErr }

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
	return f.issueAPIKeyResult, f.issueAPIKeyErr
}

func (f *fakeAuthService) RevokeAPIKey(context.Context, uuid.UUID, uuid.UUID, string, uuid.UUID) error {
	return f.revokeAPIKeyErr
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

type fakeRoleUpdateUserRepo struct {
	getByIDFn func(ctx context.Context, id uuid.UUID) (repo.HumanUser, error)
	updateFn  func(ctx context.Context, user repo.HumanUser) (repo.HumanUser, error)
}

func (f *fakeRoleUpdateUserRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.HumanUser, error) {
	if f.getByIDFn != nil {
		return f.getByIDFn(ctx, id)
	}
	return repo.HumanUser{}, repo.ErrNotFound
}

func (f *fakeRoleUpdateUserRepo) GetByEmail(context.Context, uuid.UUID, string) (repo.HumanUser, error) {
	return repo.HumanUser{}, repo.ErrNotFound
}

func (f *fakeRoleUpdateUserRepo) Update(ctx context.Context, user repo.HumanUser) (repo.HumanUser, error) {
	if f.updateFn != nil {
		return f.updateFn(ctx, user)
	}
	return user, nil
}
