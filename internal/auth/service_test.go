package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/ratelimit"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestSessionTokenGenerationProducesUniqueValues(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 1000; i++ {
		token, err := newSessionToken()
		if err != nil {
			t.Fatalf("newSessionToken failed: %v", err)
		}
		if _, exists := seen[token]; exists {
			t.Fatalf("duplicate token generated after %d iterations", i+1)
		}
		seen[token] = struct{}{}
	}
}

func TestLoginUsesBcryptCompare(t *testing.T) {
	userID := uuid.New()
	orgID := uuid.New()

	hashBytes, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword failed: %v", err)
	}
	hash := string(hashBytes)

	userRepo := &stubUserRepo{
		getByEmailFn: func(_ context.Context, gotOrgID uuid.UUID, email string) (repo.HumanUser, error) {
			if gotOrgID != orgID {
				t.Fatalf("org id = %s, want %s", gotOrgID, orgID)
			}
			return repo.HumanUser{
				ID:             userID,
				OrganizationID: orgID,
				Email:          email,
				Role:           "admin",
				IsActive:       true,
				PasswordHash:   &hash,
			}, nil
		},
		resetFailedAttemptsFn: func(context.Context, uuid.UUID) (repo.HumanUser, error) {
			return repo.HumanUser{}, nil
		},
	}
	sessionRepo := &stubSessionRepo{
		createFn: func(_ context.Context, session repo.AuthSession) (repo.AuthSession, error) {
			session.ID = uuid.New()
			session.CreatedAt = time.Now().UTC()
			session.LastUsedAt = session.CreatedAt
			session.User = &repo.HumanUser{OrganizationID: orgID}
			return session, nil
		},
	}
	service := mustNewService(t, userRepo, sessionRepo, &stubAPIKeyRepo{}, Config{
		DefaultOrgID: orgID,
		BcryptCost:   bcrypt.MinCost,
	})

	if _, err := service.Login(context.Background(), "sam@example.com", "correct-password", "10.0.0.1", "unit-test"); err != nil {
		t.Fatalf("Login with correct password failed: %v", err)
	}

	_, err = service.Login(context.Background(), "sam@example.com", "wrong-password", "10.0.0.1", "unit-test")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login with wrong password error = %v, want ErrInvalidCredentials", err)
	}
}

func TestRateLimitBlocksTwentyFirstAttempt(t *testing.T) {
	orgID := uuid.New()
	userRepo := &stubUserRepo{
		getByEmailFn: func(context.Context, uuid.UUID, string) (repo.HumanUser, error) {
			return repo.HumanUser{}, repo.ErrNotFound
		},
	}
	limiter := ratelimit.New(20, 15*time.Minute, time.Now)
	service := mustNewService(t, userRepo, &stubSessionRepo{}, &stubAPIKeyRepo{}, Config{
		DefaultOrgID:  orgID,
		IPRateLimiter: limiter,
	})

	for i := 0; i < 20; i++ {
		_, err := service.Login(context.Background(), "sam@example.com", "wrong", "192.168.0.9", "unit-test")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d error = %v, want ErrInvalidCredentials", i+1, err)
		}
	}
	_, err := service.Login(context.Background(), "sam@example.com", "wrong", "192.168.0.9", "unit-test")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("21st attempt error = %v, want ErrRateLimited", err)
	}
}

func TestRefreshSessionExtendsExactlyThirtyDaysFromClockNow(t *testing.T) {
	orgID := uuid.New()
	userID := uuid.New()
	now := time.Date(2026, 2, 24, 18, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)

	sessionID := uuid.New()
	sessionRepo := &stubSessionRepo{
		getByTokenHashFn: func(context.Context, string) (repo.AuthSession, error) {
			return repo.AuthSession{
				ID:         sessionID,
				UserID:     userID,
				TokenHash:  "hash",
				ExpiresAt:  now.Add(5 * time.Minute),
				CreatedAt:  now.Add(-2 * time.Hour),
				LastUsedAt: now.Add(-1 * time.Minute),
				User:       &repo.HumanUser{ID: userID, OrganizationID: orgID},
			}, nil
		},
		extendExpiryFn: func(_ context.Context, gotID uuid.UUID, gotExpiresAt time.Time) (repo.AuthSession, error) {
			if gotID != sessionID {
				t.Fatalf("session id = %s, want %s", gotID, sessionID)
			}
			want := now.Add(30 * 24 * time.Hour)
			if !gotExpiresAt.Equal(want) {
				t.Fatalf("expires_at = %s, want %s", gotExpiresAt, want)
			}
			return repo.AuthSession{
				ID:         gotID,
				UserID:     userID,
				ExpiresAt:  gotExpiresAt,
				CreatedAt:  now.Add(-2 * time.Hour),
				LastUsedAt: now,
				User:       &repo.HumanUser{ID: userID, OrganizationID: orgID},
			}, nil
		},
		touchLastUsedFn: func(context.Context, uuid.UUID) (repo.AuthSession, error) {
			return repo.AuthSession{
				ID:         sessionID,
				UserID:     userID,
				LastUsedAt: now,
				User:       &repo.HumanUser{ID: userID, OrganizationID: orgID},
			}, nil
		},
	}

	service := mustNewService(t, &stubUserRepo{}, sessionRepo, &stubAPIKeyRepo{}, Config{
		DefaultOrgID: orgID,
		SessionTTL:   30 * 24 * time.Hour,
	}, fakeClock)

	rawToken := "refresh-token"
	_, err := service.RefreshSession(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("RefreshSession failed: %v", err)
	}
}

func TestMagicLinkTTLAndSingleUse(t *testing.T) {
	orgID := uuid.New()
	userID := uuid.New()
	start := time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(start)

	passwordHash := "$2a$04$abcdefghijklmnopqrstuvwxyzABCDEuvwxyzaBCDEFGHIJK"
	userRepo := &stubUserRepo{
		getByEmailFn: func(_ context.Context, gotOrgID uuid.UUID, email string) (repo.HumanUser, error) {
			if gotOrgID != orgID {
				t.Fatalf("org id = %s, want %s", gotOrgID, orgID)
			}
			return repo.HumanUser{
				ID:             userID,
				OrganizationID: orgID,
				Email:          email,
				DisplayName:    "Sam",
				Role:           "admin",
				IsActive:       true,
				PasswordHash:   &passwordHash,
			}, nil
		},
		getByIDFn: func(context.Context, uuid.UUID) (repo.HumanUser, error) {
			return repo.HumanUser{
				ID:             userID,
				OrganizationID: orgID,
				Email:          "sam@example.com",
				DisplayName:    "Sam",
				Role:           "admin",
				IsActive:       true,
				PasswordHash:   &passwordHash,
			}, nil
		},
		updateFn: func(_ context.Context, updated repo.HumanUser) (repo.HumanUser, error) {
			if updated.PasswordHash == nil || *updated.PasswordHash == "" {
				t.Fatal("password hash should be updated")
			}
			return updated, nil
		},
		resetFailedAttemptsFn: func(context.Context, uuid.UUID) (repo.HumanUser, error) {
			return repo.HumanUser{}, nil
		},
	}

	service := mustNewService(t, userRepo, &stubSessionRepo{}, &stubAPIKeyRepo{}, Config{
		DefaultOrgID: orgID,
		MagicLinkTTL: 15 * time.Minute,
		BcryptCost:   bcrypt.MinCost,
	}, fakeClock)

	link, err := service.MagicLink(context.Background(), "sam@example.com")
	if err != nil {
		t.Fatalf("MagicLink failed: %v", err)
	}
	if link.Token == "" {
		t.Fatal("MagicLink token should not be empty")
	}

	if err := service.ResetPassword(context.Background(), link.Token, "new-password"); err != nil {
		t.Fatalf("ResetPassword first use failed: %v", err)
	}

	err = service.ResetPassword(context.Background(), link.Token, "another-password")
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("ResetPassword second use error = %v, want ErrTokenExpired", err)
	}

	expiringLink, err := service.MagicLink(context.Background(), "sam@example.com")
	if err != nil {
		t.Fatalf("MagicLink second token failed: %v", err)
	}
	fakeClock.Advance(16 * time.Minute)
	err = service.ResetPassword(context.Background(), expiringLink.Token, "new-password")
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("ResetPassword expired token error = %v, want ErrTokenExpired", err)
	}
}

func mustNewService(
	t *testing.T,
	userRepo HumanUserRepository,
	sessionRepo AuthSessionRepository,
	apiKeyRepo APIKeyRepository,
	cfg Config,
	overrides ...clock.Clock,
) Service {
	t.Helper()

	clk := clock.Clock(clock.Real{})
	if len(overrides) > 0 {
		clk = overrides[0]
	}

	svc, err := NewService(userRepo, sessionRepo, apiKeyRepo, clk, cfg, nil)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	return svc
}

type stubUserRepo struct {
	getByIDFn             func(ctx context.Context, id uuid.UUID) (repo.HumanUser, error)
	getByEmailFn          func(ctx context.Context, organizationID uuid.UUID, email string) (repo.HumanUser, error)
	listFn                func(ctx context.Context, organizationID uuid.UUID) ([]repo.HumanUser, error)
	updateFn              func(ctx context.Context, user repo.HumanUser) (repo.HumanUser, error)
	incrFailedAttemptsFn  func(ctx context.Context, id uuid.UUID) (repo.HumanUser, error)
	resetFailedAttemptsFn func(ctx context.Context, id uuid.UUID) (repo.HumanUser, error)
	setLockedUntilFn      func(ctx context.Context, id uuid.UUID, lockedUntil *time.Time) (repo.HumanUser, error)
}

func (s *stubUserRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.HumanUser, error) {
	if s.getByIDFn == nil {
		return repo.HumanUser{}, repo.ErrNotFound
	}
	return s.getByIDFn(ctx, id)
}

func (s *stubUserRepo) GetByEmail(ctx context.Context, organizationID uuid.UUID, email string) (repo.HumanUser, error) {
	if s.getByEmailFn == nil {
		return repo.HumanUser{}, repo.ErrNotFound
	}
	return s.getByEmailFn(ctx, organizationID, email)
}

func (s *stubUserRepo) List(ctx context.Context, organizationID uuid.UUID) ([]repo.HumanUser, error) {
	if s.listFn == nil {
		return nil, nil
	}
	return s.listFn(ctx, organizationID)
}

func (s *stubUserRepo) Update(ctx context.Context, user repo.HumanUser) (repo.HumanUser, error) {
	if s.updateFn == nil {
		return repo.HumanUser{}, repo.ErrNotFound
	}
	return s.updateFn(ctx, user)
}

func (s *stubUserRepo) IncrFailedAttempts(ctx context.Context, id uuid.UUID) (repo.HumanUser, error) {
	if s.incrFailedAttemptsFn == nil {
		return repo.HumanUser{}, nil
	}
	return s.incrFailedAttemptsFn(ctx, id)
}

func (s *stubUserRepo) ResetFailedAttempts(ctx context.Context, id uuid.UUID) (repo.HumanUser, error) {
	if s.resetFailedAttemptsFn == nil {
		return repo.HumanUser{}, nil
	}
	return s.resetFailedAttemptsFn(ctx, id)
}

func (s *stubUserRepo) SetLockedUntil(ctx context.Context, id uuid.UUID, lockedUntil *time.Time) (repo.HumanUser, error) {
	if s.setLockedUntilFn == nil {
		return repo.HumanUser{}, nil
	}
	return s.setLockedUntilFn(ctx, id, lockedUntil)
}

type stubSessionRepo struct {
	createFn         func(ctx context.Context, session repo.AuthSession) (repo.AuthSession, error)
	getByTokenHashFn func(ctx context.Context, tokenHash string) (repo.AuthSession, error)
	revokeFn         func(ctx context.Context, id uuid.UUID) error
	touchLastUsedFn  func(ctx context.Context, id uuid.UUID) (repo.AuthSession, error)
	extendExpiryFn   func(ctx context.Context, id uuid.UUID, expiresAt time.Time) (repo.AuthSession, error)
}

func (s *stubSessionRepo) Create(ctx context.Context, session repo.AuthSession) (repo.AuthSession, error) {
	if s.createFn == nil {
		return repo.AuthSession{}, fmt.Errorf("not implemented")
	}
	return s.createFn(ctx, session)
}

func (s *stubSessionRepo) GetByTokenHash(ctx context.Context, tokenHash string) (repo.AuthSession, error) {
	if s.getByTokenHashFn == nil {
		return repo.AuthSession{}, repo.ErrNotFound
	}
	return s.getByTokenHashFn(ctx, tokenHash)
}

func (s *stubSessionRepo) Revoke(ctx context.Context, id uuid.UUID) error {
	if s.revokeFn == nil {
		return nil
	}
	return s.revokeFn(ctx, id)
}

func (s *stubSessionRepo) TouchLastUsed(ctx context.Context, id uuid.UUID) (repo.AuthSession, error) {
	if s.touchLastUsedFn == nil {
		return repo.AuthSession{}, nil
	}
	return s.touchLastUsedFn(ctx, id)
}

func (s *stubSessionRepo) ExtendExpiry(ctx context.Context, id uuid.UUID, expiresAt time.Time) (repo.AuthSession, error) {
	if s.extendExpiryFn == nil {
		return repo.AuthSession{}, nil
	}
	return s.extendExpiryFn(ctx, id, expiresAt)
}

type stubAPIKeyRepo struct {
	createFn        func(ctx context.Context, apiKey repo.APIKey) (repo.APIKey, error)
	getByKeyHashFn  func(ctx context.Context, keyHash string) (repo.APIKey, error)
	listByUserFn    func(ctx context.Context, userID uuid.UUID) ([]repo.APIKey, error)
	revokeFn        func(ctx context.Context, id uuid.UUID) (repo.APIKey, error)
	touchLastUsedFn func(ctx context.Context, id uuid.UUID) (repo.APIKey, error)
}

func (s *stubAPIKeyRepo) Create(ctx context.Context, apiKey repo.APIKey) (repo.APIKey, error) {
	if s.createFn == nil {
		return repo.APIKey{}, nil
	}
	return s.createFn(ctx, apiKey)
}

func (s *stubAPIKeyRepo) GetByKeyHash(ctx context.Context, keyHash string) (repo.APIKey, error) {
	if s.getByKeyHashFn == nil {
		return repo.APIKey{}, repo.ErrNotFound
	}
	return s.getByKeyHashFn(ctx, keyHash)
}

func (s *stubAPIKeyRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]repo.APIKey, error) {
	if s.listByUserFn == nil {
		return nil, nil
	}
	return s.listByUserFn(ctx, userID)
}

func (s *stubAPIKeyRepo) Revoke(ctx context.Context, id uuid.UUID) (repo.APIKey, error) {
	if s.revokeFn == nil {
		return repo.APIKey{}, nil
	}
	return s.revokeFn(ctx, id)
}

func (s *stubAPIKeyRepo) TouchLastUsed(ctx context.Context, id uuid.UUID) (repo.APIKey, error) {
	if s.touchLastUsedFn == nil {
		return repo.APIKey{}, nil
	}
	return s.touchLastUsedFn(ctx, id)
}
