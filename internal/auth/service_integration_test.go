//go:build integration

package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"golang.org/x/crypto/bcrypt"

	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestAuthServiceLoginValidateRefreshLogoutFlow(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, user := createOrgAndUser(t, pool, "auth-service-flow", "flow@example.com", "flow-password")

	now := time.Date(2026, 2, 24, 15, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)
	service := newIntegrationService(t, pool, fakeClock, Config{
		DefaultOrgID: org.ID,
		SessionTTL:   30 * 24 * time.Hour,
		BcryptCost:   bcrypt.MinCost,
	})

	login, err := service.Login(ctx, user.Email, "flow-password", "10.1.1.5", "integration-test")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	if login.SessionToken == "" {
		t.Fatal("session token should not be empty")
	}

	var (
		tokenHash string
		expiresAt time.Time
	)
	if err := pool.QueryRow(ctx, `
		SELECT token_hash, expires_at
		FROM auth_session
		WHERE id = $1
	`, login.Session.ID).Scan(&tokenHash, &expiresAt); err != nil {
		t.Fatalf("query auth_session row failed: %v", err)
	}
	if tokenHash != hashSHA256(login.SessionToken) {
		t.Fatalf("token_hash = %q, want %q", tokenHash, hashSHA256(login.SessionToken))
	}
	wantInitialExpiry := now.Add(30 * 24 * time.Hour)
	if !expiresAt.Equal(wantInitialExpiry) {
		t.Fatalf("initial expires_at = %s, want %s", expiresAt, wantInitialExpiry)
	}

	validated, err := service.ValidateSession(ctx, login.SessionToken)
	if err != nil {
		t.Fatalf("ValidateSession failed: %v", err)
	}
	if validated.ID != login.Session.ID {
		t.Fatalf("validated session id = %s, want %s", validated.ID, login.Session.ID)
	}

	fakeClock.Advance(2 * time.Hour)
	refreshed, err := service.RefreshSession(ctx, login.SessionToken)
	if err != nil {
		t.Fatalf("RefreshSession failed: %v", err)
	}
	wantRefreshExpiry := fakeClock.Now().UTC().Add(30 * 24 * time.Hour)
	if !refreshed.ExpiresAt.Equal(wantRefreshExpiry) {
		t.Fatalf("refreshed expires_at = %s, want %s", refreshed.ExpiresAt, wantRefreshExpiry)
	}

	if err := service.Logout(ctx, login.SessionToken); err != nil {
		t.Fatalf("Logout failed: %v", err)
	}
	if _, err := service.ValidateSession(ctx, login.SessionToken); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("ValidateSession after logout error = %v, want ErrSessionRevoked", err)
	}
}

func TestAuthServiceLockoutAndUnlock(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, user := createOrgAndUser(t, pool, "auth-service-lock", "lock@example.com", "correct-password")

	now := time.Date(2026, 2, 24, 9, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)
	service := newIntegrationService(t, pool, fakeClock, Config{
		DefaultOrgID: org.ID,
		LockDuration: 30 * time.Minute,
		BcryptCost:   bcrypt.MinCost,
	})

	for i := 0; i < 10; i++ {
		_, err := service.Login(ctx, user.Email, "wrong-password", "203.0.113.10", "integration-test")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d error = %v, want ErrInvalidCredentials", i+1, err)
		}
	}

	lockedUser, err := repo.NewHumanUserRepo(pool).GetByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if lockedUser.LockedUntil == nil {
		t.Fatal("locked_until should be set after 10 failed attempts")
	}
	wantLockedUntil := now.Add(30 * time.Minute)
	if !lockedUser.LockedUntil.Equal(wantLockedUntil) {
		t.Fatalf("locked_until = %s, want %s", lockedUser.LockedUntil, wantLockedUntil)
	}

	_, err = service.Login(ctx, user.Email, "correct-password", "203.0.113.10", "integration-test")
	if !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("login while locked error = %v, want ErrAccountLocked", err)
	}

	if err := service.UnlockAccount(ctx, user.ID); err != nil {
		t.Fatalf("UnlockAccount failed: %v", err)
	}

	_, err = service.Login(ctx, user.Email, "correct-password", "203.0.113.10", "integration-test")
	if err != nil {
		t.Fatalf("login after unlock failed: %v", err)
	}
}

func TestAuthServiceAPIKeyLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, user := createOrgAndUser(t, pool, "auth-service-key", "key@example.com", "irrelevant")

	service := newIntegrationService(t, pool, clock.NewFake(time.Date(2026, 2, 24, 11, 0, 0, 0, time.UTC)), Config{
		DefaultOrgID: org.ID,
		BcryptCost:   bcrypt.MinCost,
	})

	issued, err := service.IssueAPIKey(ctx, user.ID, "CLI Key", []string{"read"}, nil)
	if err != nil {
		t.Fatalf("IssueAPIKey failed: %v", err)
	}
	if len(issued.RawKey) < 4 || issued.RawKey[:4] != "otk_" {
		t.Fatalf("raw key = %q, want otk_ prefix", issued.RawKey)
	}

	if _, err := service.ValidateAPIKey(ctx, issued.RawKey); err != nil {
		t.Fatalf("ValidateAPIKey failed: %v", err)
	}
	if _, err := service.ValidateAPIKey(ctx, "garbage-value"); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("ValidateAPIKey garbage error = %v, want ErrInvalidAPIKey", err)
	}

	if err := service.RevokeAPIKey(ctx, issued.APIKey.ID, user.ID); err != nil {
		t.Fatalf("RevokeAPIKey failed: %v", err)
	}
	if _, err := service.ValidateAPIKey(ctx, issued.RawKey); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("ValidateAPIKey revoked error = %v, want ErrInvalidAPIKey", err)
	}
}

func TestAuthServiceValidateSessionExpired(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, user := createOrgAndUser(t, pool, "auth-service-expired", "expired@example.com", "pw")

	service := newIntegrationService(t, pool, clock.NewFake(time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC)), Config{
		DefaultOrgID: org.ID,
		BcryptCost:   bcrypt.MinCost,
	})

	rawToken := "expired-session-token"
	if _, err := repo.NewAuthSessionRepo(pool).Create(ctx, repo.AuthSession{
		UserID:    user.ID,
		TokenHash: hashSHA256(rawToken),
		ExpiresAt: time.Date(2026, 2, 23, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("create expired session failed: %v", err)
	}

	if _, err := service.ValidateSession(ctx, rawToken); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("ValidateSession expired error = %v, want ErrSessionExpired", err)
	}
}

func TestAuthServiceLocalModeLoopbackAutoLogin(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "auth-service-local",
		DisplayName: "Auth Service Local",
	})
	if err != nil {
		t.Fatalf("create org failed: %v", err)
	}

	hashBytes, err := bcrypt.GenerateFromPassword([]byte("admin-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword failed: %v", err)
	}
	hash := string(hashBytes)

	userRepo := repo.NewHumanUserRepo(pool)
	firstAdmin, err := userRepo.Create(ctx, repo.HumanUser{
		OrganizationID: org.ID,
		Email:          "admin1@example.com",
		DisplayName:    "Admin One",
		PasswordHash:   &hash,
		Role:           "admin",
		IsActive:       true,
	})
	if err != nil {
		t.Fatalf("create first admin failed: %v", err)
	}
	if _, err := userRepo.Create(ctx, repo.HumanUser{
		OrganizationID: org.ID,
		Email:          "admin2@example.com",
		DisplayName:    "Admin Two",
		PasswordHash:   &hash,
		Role:           "admin",
		IsActive:       true,
	}); err != nil {
		t.Fatalf("create second admin failed: %v", err)
	}

	service := newIntegrationService(t, pool, clock.NewFake(time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC)), Config{
		DefaultOrgID: org.ID,
		AuthMode:     "local",
		BcryptCost:   bcrypt.MinCost,
	})

	login, err := service.Login(ctx, "", "", "127.0.0.1", "integration-test")
	if err != nil {
		t.Fatalf("local auto-login failed: %v", err)
	}
	if login.Session.UserID != firstAdmin.ID {
		t.Fatalf("auto-login user id = %s, want first admin %s", login.Session.UserID, firstAdmin.ID)
	}
}

func newIntegrationService(t *testing.T, pool *pgxpool.Pool, clk clock.Clock, cfg Config) Service {
	t.Helper()

	service, err := NewService(
		repo.NewHumanUserRepo(pool),
		repo.NewAuthSessionRepo(pool),
		repo.NewAPIKeyRepo(pool),
		clk,
		cfg,
		nil,
	)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	return service
}

func createOrgAndUser(t *testing.T, pool *pgxpool.Pool, slug, email, password string) (repo.Organization, repo.HumanUser) {
	t.Helper()
	ctx := context.Background()
	orgRepo := repo.NewOrgRepo(pool)
	userRepo := repo.NewHumanUserRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        slug,
		DisplayName: slug,
	})
	if err != nil {
		t.Fatalf("create org failed: %v", err)
	}

	hashBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword failed: %v", err)
	}
	hash := string(hashBytes)

	user, err := userRepo.Create(ctx, repo.HumanUser{
		OrganizationID: org.ID,
		Email:          email,
		DisplayName:    "Integration User",
		PasswordHash:   &hash,
		Role:           "admin",
		IsActive:       true,
	})
	if err != nil {
		t.Fatalf("create user failed: %v", err)
	}

	return org, user
}
