package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/auth"
)

func TestAuthMiddlewareCredentialExtraction(t *testing.T) {
	testCases := []struct {
		name          string
		headerName    string
		headerValue   string
		requestPath   string
		wantMethod    string
		wantToken     string
		wantAuthField string
	}{
		{
			name:          "authorization bearer",
			headerName:    "Authorization",
			headerValue:   "Bearer session-token",
			requestPath:   "/protected",
			wantMethod:    AuthMethodSession,
			wantToken:     "session-token",
			wantAuthField: "session",
		},
		{
			name:          "x-api-key header",
			headerName:    "X-API-Key",
			headerValue:   "otk_header",
			requestPath:   "/protected",
			wantMethod:    AuthMethodAPIKey,
			wantToken:     "otk_header",
			wantAuthField: "api_key",
		},
		{
			name:          "api key query param",
			headerName:    "",
			headerValue:   "",
			requestPath:   "/protected?api_key=otk_query",
			wantMethod:    AuthMethodAPIKey,
			wantToken:     "otk_query",
			wantAuthField: "api_key",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeAuthService{}
			svc.validateSessionFn = func(_ context.Context, token string) (*auth.SessionInfo, error) {
				if tc.wantAuthField != "session" {
					t.Fatalf("ValidateSession should not have been called")
				}
				if token != tc.wantToken {
					t.Fatalf("ValidateSession token = %q, want %q", token, tc.wantToken)
				}
				return &auth.SessionInfo{UserID: uuid.New(), OrganizationID: uuid.New(), Role: "admin", Email: "sam@example.com"}, nil
			}
			svc.validateAPIKeyFn = func(_ context.Context, token string) (*auth.APIKeyInfo, error) {
				if tc.wantAuthField != "api_key" {
					t.Fatalf("ValidateAPIKey should not have been called")
				}
				if token != tc.wantToken {
					t.Fatalf("ValidateAPIKey token = %q, want %q", token, tc.wantToken)
				}
				return &auth.APIKeyInfo{ID: uuid.New(), UserID: uuid.New(), OrganizationID: uuid.New(), Role: "member", Email: "member@example.com"}, nil
			}
			handler := Auth(AuthOptions{Service: svc, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				principal, ok := PrincipalFromContext(r.Context())
				if !ok {
					t.Fatal("expected principal in context")
				}
				if principal.AuthMethod != tc.wantMethod {
					t.Fatalf("principal.AuthMethod = %q, want %q", principal.AuthMethod, tc.wantMethod)
				}
				w.WriteHeader(http.StatusNoContent)
			}))

			req := httptest.NewRequest(http.MethodGet, tc.requestPath, nil)
			if tc.headerName != "" {
				req.Header.Set(tc.headerName, tc.headerValue)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
			}
		})
	}
}

func TestAuthMiddlewareUnauthorizedOnMissingCredentials(t *testing.T) {
	handler := Auth(AuthOptions{Service: &fakeAuthService{}})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run")
	}))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
	if got := errorCodeFromResponse(t, rr); got != "unauthorized" {
		t.Fatalf("error.code = %q, want %q", got, "unauthorized")
	}
}

func TestAuthMiddlewareSessionExpired(t *testing.T) {
	handler := Auth(AuthOptions{Service: &fakeAuthService{
		validateSessionFn: func(context.Context, string) (*auth.SessionInfo, error) {
			return nil, auth.ErrSessionExpired
		},
	}})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run")
	}))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer expired-token")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
	if got := errorCodeFromResponse(t, rr); got != "session_expired" {
		t.Fatalf("error.code = %q, want %q", got, "session_expired")
	}
}

func errorCodeFromResponse(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal error body: %v", err)
	}
	errorValue, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("error body missing error object: %+v", body)
	}
	code, _ := errorValue["code"].(string)
	return code
}

type fakeAuthService struct {
	loginFn           func(ctx context.Context, email, password, ipAddr, userAgent string) (*auth.LoginResult, error)
	logoutFn          func(ctx context.Context, sessionToken string) error
	refreshFn         func(ctx context.Context, sessionToken string) (*auth.SessionInfo, error)
	validateSessionFn func(ctx context.Context, sessionToken string) (*auth.SessionInfo, error)
	validateAPIKeyFn  func(ctx context.Context, rawKey string) (*auth.APIKeyInfo, error)
	issueFn           func(ctx context.Context, userID uuid.UUID, displayName string, scopes []string, expiresAt *time.Time) (*auth.IssueResult, error)
	revokeFn          func(ctx context.Context, keyID, requestingUserID uuid.UUID, requestingRole string, requestingOrgID uuid.UUID) error
	listFn            func(ctx context.Context, userID uuid.UUID) ([]*auth.APIKeyInfo, error)
}

func (f *fakeAuthService) Login(ctx context.Context, email, password, ipAddr, userAgent string) (*auth.LoginResult, error) {
	if f.loginFn != nil {
		return f.loginFn(ctx, email, password, ipAddr, userAgent)
	}
	return nil, auth.ErrInvalidCredentials
}

func (f *fakeAuthService) Logout(ctx context.Context, sessionToken string) error {
	if f.logoutFn != nil {
		return f.logoutFn(ctx, sessionToken)
	}
	return nil
}

func (f *fakeAuthService) RefreshSession(ctx context.Context, sessionToken string) (*auth.SessionInfo, error) {
	if f.refreshFn != nil {
		return f.refreshFn(ctx, sessionToken)
	}
	return &auth.SessionInfo{}, nil
}

func (f *fakeAuthService) ValidateSession(ctx context.Context, sessionToken string) (*auth.SessionInfo, error) {
	if f.validateSessionFn != nil {
		return f.validateSessionFn(ctx, sessionToken)
	}
	return nil, auth.ErrInvalidSession
}

func (f *fakeAuthService) ValidateAPIKey(ctx context.Context, rawKey string) (*auth.APIKeyInfo, error) {
	if f.validateAPIKeyFn != nil {
		return f.validateAPIKeyFn(ctx, rawKey)
	}
	return nil, auth.ErrInvalidAPIKey
}

func (f *fakeAuthService) IssueAPIKey(ctx context.Context, userID uuid.UUID, displayName string, scopes []string, expiresAt *time.Time) (*auth.IssueResult, error) {
	if f.issueFn != nil {
		return f.issueFn(ctx, userID, displayName, scopes, expiresAt)
	}
	return nil, errors.New("not implemented")
}

func (f *fakeAuthService) RevokeAPIKey(ctx context.Context, keyID, requestingUserID uuid.UUID, requestingRole string, requestingOrgID uuid.UUID) error {
	if f.revokeFn != nil {
		return f.revokeFn(ctx, keyID, requestingUserID, requestingRole, requestingOrgID)
	}
	return nil
}

func (f *fakeAuthService) ListAPIKeys(ctx context.Context, userID uuid.UUID) ([]*auth.APIKeyInfo, error) {
	if f.listFn != nil {
		return f.listFn(ctx, userID)
	}
	return []*auth.APIKeyInfo{}, nil
}

func (f *fakeAuthService) MagicLink(context.Context, string) (*auth.MagicLinkResult, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeAuthService) ResetPassword(context.Context, string, string) error {
	return errors.New("not implemented")
}

func (f *fakeAuthService) UnlockAccount(context.Context, uuid.UUID) error {
	return errors.New("not implemented")
}

func (f *fakeAuthService) ChangePassword(context.Context, uuid.UUID, uuid.UUID, string, string) error {
	return errors.New("not implemented")
}
