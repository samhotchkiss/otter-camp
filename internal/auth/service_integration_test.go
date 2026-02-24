//go:build integration

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

func TestLoginValidateRefreshLogoutFlow(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fakeClock := clock.NewFake(time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC))

	org, user := createOrgAndUser(t, pool, "auth-flow-org", "flow@example.com", "admin", "secret-123")
	service := newServiceForTest(t, pool, org.ID, authModeStandard, fakeClock)
	sessionRepo := repo.NewAuthSessionRepo(pool)

	login, err := service.Login(ctx, user.Email, "secret-123", "203.0.113.1", "integration-test")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if strings.TrimSpace(login.SessionToken) == "" {
		t.Fatal("expected session token")
	}

	sessionRow, err := sessionRepo.GetByTokenHash(ctx, hashToken(login.SessionToken))
	if err != nil {
		t.Fatalf("GetByTokenHash: %v", err)
	}
	if sessionRow.TokenHash != hashToken(login.SessionToken) {
		t.Fatalf("token_hash mismatch")
	}
	if !sessionRow.ExpiresAt.Equal(fakeClock.Now().Add(30 * 24 * time.Hour)) {
		t.Fatalf("expires_at = %s, want %s", sessionRow.ExpiresAt, fakeClock.Now().Add(30*24*time.Hour))
	}

	validated, err := service.ValidateSession(ctx, login.SessionToken)
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}
	if validated.UserID != user.ID {
		t.Fatalf("validated user id = %s, want %s", validated.UserID, user.ID)
	}

	fakeClock.Advance(2 * time.Hour)
	refreshed, err := service.RefreshSession(ctx, login.SessionToken)
	if err != nil {
		t.Fatalf("RefreshSession: %v", err)
	}
	if !refreshed.ExpiresAt.Equal(fakeClock.Now().Add(30 * 24 * time.Hour)) {
		t.Fatalf("refreshed expires_at = %s, want %s", refreshed.ExpiresAt, fakeClock.Now().Add(30*24*time.Hour))
	}

	if err := service.Logout(ctx, login.SessionToken); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := service.ValidateSession(ctx, login.SessionToken); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("ValidateSession after logout err = %v, want ErrSessionRevoked", err)
	}
}

func TestFailedLoginLockoutAndUnlock(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fakeClock := clock.NewFake(time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC))

	org, user := createOrgAndUser(t, pool, "auth-lock-org", "lock@example.com", "admin", "correct-password")
	service := newServiceForTest(t, pool, org.ID, authModeStandard, fakeClock)
	userRepo := repo.NewHumanUserRepo(pool)

	for i := 0; i < 10; i++ {
		_, err := service.Login(ctx, user.Email, "wrong-password", "198.51.100.2", "integration-test")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d err = %v, want ErrInvalidCredentials", i+1, err)
		}
	}

	afterFailures, err := userRepo.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetByID after failures: %v", err)
	}
	if afterFailures.LockedUntil == nil {
		t.Fatal("expected locked_until to be set")
	}

	_, err = service.Login(ctx, user.Email, "correct-password", "198.51.100.2", "integration-test")
	if !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("login while locked err = %v, want ErrAccountLocked", err)
	}

	if err := service.UnlockAccount(ctx, user.ID); err != nil {
		t.Fatalf("UnlockAccount: %v", err)
	}

	login, err := service.Login(ctx, user.Email, "correct-password", "198.51.100.2", "integration-test")
	if err != nil {
		t.Fatalf("Login after unlock: %v", err)
	}
	if login.Session == nil || login.Session.UserID != user.ID {
		t.Fatalf("unexpected login result after unlock: %+v", login.Session)
	}
}

func TestIssueValidateRevokeAPIKeyAndRateLimit(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fakeClock := clock.NewFake(time.Date(2026, 2, 1, 14, 0, 0, 0, time.UTC))

	org, user := createOrgAndUser(t, pool, "auth-key-org", "keys@example.com", "admin", "secret-123")
	service := newServiceForTest(t, pool, org.ID, authModeStandard, fakeClock)

	first, err := service.IssueAPIKey(ctx, user.ID, "CLI key", []string{"read"}, nil)
	if err != nil {
		t.Fatalf("IssueAPIKey first: %v", err)
	}
	second, err := service.IssueAPIKey(ctx, user.ID, "CLI key", []string{"read"}, nil)
	if err != nil {
		t.Fatalf("IssueAPIKey second: %v", err)
	}
	if !strings.HasPrefix(first.RawKey, "otk_") || !strings.HasPrefix(second.RawKey, "otk_") {
		t.Fatalf("raw key prefix mismatch: %q / %q", first.RawKey, second.RawKey)
	}
	if first.RawKey == second.RawKey {
		t.Fatal("expected unique raw keys per issuance")
	}

	validated, err := service.ValidateAPIKey(ctx, first.RawKey)
	if err != nil {
		t.Fatalf("ValidateAPIKey: %v", err)
	}
	if validated.ID != first.KeyID {
		t.Fatalf("validated key id = %s, want %s", validated.ID, first.KeyID)
	}

	if _, err := service.ValidateAPIKey(ctx, "garbage"); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("ValidateAPIKey garbage err = %v, want ErrInvalidAPIKey", err)
	}

	if err := service.RevokeAPIKey(ctx, first.KeyID, user.ID); err != nil {
		t.Fatalf("RevokeAPIKey: %v", err)
	}
	if _, err := service.ValidateAPIKey(ctx, first.RawKey); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("ValidateAPIKey revoked err = %v, want ErrInvalidAPIKey", err)
	}

	for i := 0; i < 20; i++ {
		_, err := service.Login(ctx, "missing@example.com", "wrong", "192.0.2.44", "integration-test")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("rate-limit attempt %d err = %v, want ErrInvalidCredentials", i+1, err)
		}
	}
	if _, err := service.Login(ctx, "missing@example.com", "wrong", "192.0.2.44", "integration-test"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("21st attempt err = %v, want ErrRateLimited", err)
	}
}

func TestValidateSessionExpiredAndLocalAuthMode(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fakeClock := clock.NewFake(time.Date(2026, 2, 1, 16, 0, 0, 0, time.UTC))

	org, admin := createOrgAndUser(t, pool, "auth-local-org", "admin@example.com", "admin", "admin-password")

	standard := newServiceForTest(t, pool, org.ID, authModeStandard, fakeClock)
	sessionRepo := repo.NewAuthSessionRepo(pool)

	expiredToken := "expired-session-token"
	if _, err := sessionRepo.Create(ctx, repo.AuthSession{
		UserID:    admin.ID,
		TokenHash: hashToken(expiredToken),
		ExpiresAt: fakeClock.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("create expired session: %v", err)
	}
	if _, err := standard.ValidateSession(ctx, expiredToken); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("ValidateSession expired err = %v, want ErrSessionExpired", err)
	}

	local := newServiceForTest(t, pool, org.ID, authModeLocal, fakeClock)
	auto, err := local.Login(ctx, "", "", "127.0.0.1", "cli")
	if err != nil {
		t.Fatalf("local auto-login: %v", err)
	}
	if auto.Session == nil || auto.Session.UserID == uuid.Nil {
		t.Fatalf("unexpected local auto-login session: %+v", auto.Session)
	}

	_, err = local.Login(ctx, "", "", "203.0.113.55", "cli")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("non-loopback local login err = %v, want ErrInvalidCredentials", err)
	}
}

func createOrgAndUser(t *testing.T, pool *pgxpool.Pool, slug, email, role, password string) (repo.Organization, repo.HumanUser) {
	t.Helper()
	ctx := context.Background()

	orgRepo := repo.NewOrgRepo(pool)
	userRepo := repo.NewHumanUserRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{Slug: slug, DisplayName: slug})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	passwordHashBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	passwordHash := string(passwordHashBytes)

	user, err := userRepo.Create(ctx, repo.HumanUser{
		OrganizationID: org.ID,
		Email:          email,
		DisplayName:    email,
		PasswordHash:   &passwordHash,
		Role:           role,
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return org, user
}

func newServiceForTest(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, mode string, clk clock.Clock) Service {
	t.Helper()

	service, err := NewService(Options{
		Users:        repo.NewHumanUserRepo(pool),
		Sessions:     repo.NewAuthSessionRepo(pool),
		APIKeys:      repo.NewAPIKeyRepo(pool),
		Clock:        clk,
		AuthMode:     mode,
		DefaultOrgID: orgID,
		BcryptCost:   bcrypt.MinCost,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service
}
