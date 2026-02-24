//go:build integration

package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	oclock "github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestAuthServiceLoginValidateRefreshLogoutFlow(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, user := createAuthIntegrationUser(t, pool, "auth-flow-org", "flow@example.com", "correct-password-123", "admin")
	fakeClock := oclock.NewFake(time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC))

	svc := newAuthServiceForIntegration(t, pool, Options{
		Clock:        fakeClock,
		DefaultOrgID: org.ID,
	})

	login, err := svc.Login(ctx, user.Email, "correct-password-123", "203.0.113.10", "integration-test")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if strings.TrimSpace(login.SessionToken) == "" {
		t.Fatal("expected session token")
	}

	if _, err := svc.ValidateSession(ctx, login.SessionToken); err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}

	refreshed, err := svc.RefreshSession(ctx, login.SessionToken)
	if err != nil {
		t.Fatalf("RefreshSession: %v", err)
	}
	wantExpiry := fakeClock.Now().UTC().Add(30 * 24 * time.Hour)
	if !refreshed.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("expires_at = %s, want %s", refreshed.ExpiresAt, wantExpiry)
	}

	if err := svc.Logout(ctx, login.SessionToken); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := svc.ValidateSession(ctx, login.SessionToken); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("ValidateSession after logout err = %v, want ErrSessionRevoked", err)
	}
}

func TestAuthServiceFailedLoginLockoutAndUnlock(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, user := createAuthIntegrationUser(t, pool, "auth-lockout-org", "lockout@example.com", "correct-password-123", "admin")
	svc := newAuthServiceForIntegration(t, pool, Options{
		DefaultOrgID: org.ID,
	})

	for i := 0; i < 10; i++ {
		_, err := svc.Login(ctx, user.Email, "wrong-password", "198.51.100.42", "integration-test")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("login attempt %d err = %v, want ErrInvalidCredentials", i+1, err)
		}
	}

	_, err := svc.Login(ctx, user.Email, "correct-password-123", "198.51.100.42", "integration-test")
	if !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("login while locked err = %v, want ErrAccountLocked", err)
	}

	userRepo := repo.NewHumanUserRepo(pool)
	updated, err := userRepo.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.LockedUntil == nil {
		t.Fatal("expected locked_until to be set")
	}
	if updated.FailedLoginAttempts < 10 {
		t.Fatalf("failed_login_attempts = %d, want >= 10", updated.FailedLoginAttempts)
	}

	if err := svc.UnlockAccount(ctx, user.ID); err != nil {
		t.Fatalf("UnlockAccount: %v", err)
	}

	unlocked, err := userRepo.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetByID after unlock: %v", err)
	}
	if unlocked.FailedLoginAttempts != 0 {
		t.Fatalf("failed_login_attempts = %d, want 0", unlocked.FailedLoginAttempts)
	}
	if unlocked.LockedUntil != nil {
		t.Fatalf("locked_until = %v, want nil", unlocked.LockedUntil)
	}
}

func TestAuthServiceAPIKeyIssueValidateRevoke(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, user := createAuthIntegrationUser(t, pool, "auth-api-key-org", "apikey@example.com", "correct-password-123", "admin")
	svc := newAuthServiceForIntegration(t, pool, Options{
		DefaultOrgID: org.ID,
	})

	issued, err := svc.IssueAPIKey(ctx, user.ID, "Primary Key", []string{"full"}, nil)
	if err != nil {
		t.Fatalf("IssueAPIKey first: %v", err)
	}
	if !strings.HasPrefix(issued.RawKey, "otk_") {
		t.Fatalf("raw key prefix = %q, want otk_", issued.RawKey)
	}

	issuedAgain, err := svc.IssueAPIKey(ctx, user.ID, "Primary Key", []string{"full"}, nil)
	if err != nil {
		t.Fatalf("IssueAPIKey second: %v", err)
	}
	if issuedAgain.RawKey == issued.RawKey {
		t.Fatal("expected second issued key to differ from first")
	}

	if _, err := svc.ValidateAPIKey(ctx, issued.RawKey); err != nil {
		t.Fatalf("ValidateAPIKey valid: %v", err)
	}
	if _, err := svc.ValidateAPIKey(ctx, "garbage"); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("ValidateAPIKey garbage err = %v, want ErrInvalidAPIKey", err)
	}

	if err := svc.RevokeAPIKey(ctx, issued.APIKey.ID, user.ID); err != nil {
		t.Fatalf("RevokeAPIKey: %v", err)
	}
	if _, err := svc.ValidateAPIKey(ctx, issued.RawKey); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("ValidateAPIKey revoked err = %v, want ErrInvalidAPIKey", err)
	}
}

func TestAuthServiceValidateSessionExpired(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, user := createAuthIntegrationUser(t, pool, "auth-expired-org", "expired@example.com", "correct-password-123", "admin")
	svc := newAuthServiceForIntegration(t, pool, Options{
		DefaultOrgID: org.ID,
	})

	rawToken := "expired-session-token"
	_, err := repo.NewAuthSessionRepo(pool).Create(ctx, repo.AuthSession{
		UserID:    user.ID,
		TokenHash: sha256Hex(rawToken),
		ExpiresAt: time.Now().UTC().Add(-1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create expired session: %v", err)
	}

	if _, err := svc.ValidateSession(ctx, rawToken); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("ValidateSession expired err = %v, want ErrSessionExpired", err)
	}
}

func TestAuthServiceLocalModeAutoLogin(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, _ := createAuthIntegrationUser(t, pool, "auth-local-org", "local@example.com", "correct-password-123", "admin")
	svc := newAuthServiceForIntegration(t, pool, Options{
		DefaultOrgID: org.ID,
		AuthMode:     "local",
	})

	login, err := svc.Login(ctx, "", "", "127.0.0.1", "integration-test")
	if err != nil {
		t.Fatalf("Login local mode: %v", err)
	}
	if strings.TrimSpace(login.SessionToken) == "" {
		t.Fatal("expected session token")
	}
}

func newAuthServiceForIntegration(t *testing.T, pool *pgxpool.Pool, opts Options) Service {
	t.Helper()

	svc, err := NewService(
		repo.NewHumanUserRepo(pool),
		repo.NewAuthSessionRepo(pool),
		repo.NewAPIKeyRepo(pool),
		opts,
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func createAuthIntegrationUser(t *testing.T, pool *pgxpool.Pool, orgSlug, email, password, role string) (repo.Organization, repo.HumanUser) {
	t.Helper()
	ctx := context.Background()

	orgRepo := repo.NewOrgRepo(pool)
	userRepo := repo.NewHumanUserRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        orgSlug,
		DisplayName: orgSlug,
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	hashBytes, err := bcrypt.GenerateFromPassword([]byte(password), defaultBcryptCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	hash := string(hashBytes)

	user, err := userRepo.Create(ctx, repo.HumanUser{
		OrganizationID: org.ID,
		Email:          email,
		DisplayName:    "Integration User",
		PasswordHash:   &hash,
		Role:           role,
		IsActive:       true,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	return org, user
}
