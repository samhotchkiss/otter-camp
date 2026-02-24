package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	oclock "github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestGenerateSessionTokenUnique(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		raw, hash, err := generateSessionToken()
		if err != nil {
			t.Fatalf("generateSessionToken: %v", err)
		}
		if raw == "" || hash == "" {
			t.Fatal("expected non-empty token and hash")
		}
		if _, exists := seen[raw]; exists {
			t.Fatalf("duplicate token generated on iteration %d", i)
		}
		seen[raw] = struct{}{}
	}
}

func TestValidatePassword(t *testing.T) {
	// Precomputed bcrypt hash for plaintext "password".
	const hash = "$2a$12$E5oxhOX945Pq5JxkuQNzROhIORN3GdzHQSqqI3ubgNRGbbDZlNAYC"

	if err := validatePassword(stringPtr(hash), "password"); err != nil {
		t.Fatalf("validatePassword correct password error: %v", err)
	}
	if err := validatePassword(stringPtr(hash), "wrong-password"); err == nil {
		t.Fatal("validatePassword should fail for wrong password")
	}
}

func TestMagicLinkTokenTTLAndSingleUse(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	userID := uuid.New()
	fakeClock := oclock.NewFake(time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC))

	humanUsers := &stubHumanUsers{
		usersByEmail: map[string]repo.HumanUser{
			"user@example.com": {
				ID:             userID,
				OrganizationID: orgID,
				Email:          "user@example.com",
				DisplayName:    "User",
				Role:           "admin",
				IsActive:       true,
			},
		},
		usersByID: map[uuid.UUID]repo.HumanUser{
			userID: {
				ID:             userID,
				OrganizationID: orgID,
				Email:          "user@example.com",
				DisplayName:    "User",
				Role:           "admin",
				IsActive:       true,
			},
		},
	}
	sessions := &stubSessions{}
	service, err := NewService(humanUsers, sessions, &stubAPIKeys{}, Options{
		Clock:        fakeClock,
		DefaultOrgID: orgID,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	link, err := service.MagicLink(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("MagicLink: %v", err)
	}

	if err := service.ResetPassword(ctx, link.Token, "new-password-123"); err != nil {
		t.Fatalf("ResetPassword first use: %v", err)
	}
	if err := service.ResetPassword(ctx, link.Token, "new-password-456"); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("ResetPassword second use err = %v, want ErrTokenExpired", err)
	}

	link2, err := service.MagicLink(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("MagicLink second token: %v", err)
	}
	fakeClock.Advance(16 * time.Minute)
	if err := service.ResetPassword(ctx, link2.Token, "new-password-789"); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("ResetPassword expired token err = %v, want ErrTokenExpired", err)
	}
}

func TestRefreshSessionExtendsExactlyThirtyDaysFromClock(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)
	fakeClock := oclock.NewFake(now)
	userID := uuid.New()

	stub := &stubSessions{
		sessionByHash: map[string]repo.AuthSession{
			sha256Hex("session-token"): {
				ID:         uuid.New(),
				UserID:     userID,
				TokenHash:  sha256Hex("session-token"),
				ExpiresAt:  now.Add(5 * time.Minute),
				LastUsedAt: now,
				User: &repo.HumanUser{
					ID:             userID,
					OrganizationID: uuid.New(),
					Email:          "user@example.com",
					Role:           "admin",
				},
			},
		},
	}

	service, err := NewService(&stubHumanUsers{}, stub, &stubAPIKeys{}, Options{Clock: fakeClock, DefaultOrgID: uuid.New()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	info, err := service.RefreshSession(ctx, "session-token")
	if err != nil {
		t.Fatalf("RefreshSession: %v", err)
	}

	want := now.Add(30 * 24 * time.Hour)
	if !info.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at = %s, want %s", info.ExpiresAt, want)
	}
}

func TestLoginRateLimitedAfter21Attempts(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	svc, err := NewService(&stubHumanUsers{}, &stubSessions{}, &stubAPIKeys{}, Options{
		DefaultOrgID: orgID,
		Clock:        oclock.NewFake(time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	for i := 0; i < 20; i++ {
		_, err := svc.Login(ctx, "missing@example.com", "password-123", "203.0.113.44", "ua")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d err = %v, want ErrInvalidCredentials", i+1, err)
		}
	}

	_, err = svc.Login(ctx, "missing@example.com", "password-123", "203.0.113.44", "ua")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("attempt 21 err = %v, want ErrRateLimited", err)
	}
}

type stubHumanUsers struct {
	usersByEmail map[string]repo.HumanUser
	usersByID    map[uuid.UUID]repo.HumanUser
}

func (s *stubHumanUsers) GetByID(ctx context.Context, id uuid.UUID) (repo.HumanUser, error) {
	user, ok := s.usersByID[id]
	if !ok {
		return repo.HumanUser{}, repo.ErrNotFound
	}
	return user, nil
}

func (s *stubHumanUsers) GetByEmail(ctx context.Context, organizationID uuid.UUID, email string) (repo.HumanUser, error) {
	if s.usersByEmail == nil {
		return repo.HumanUser{}, repo.ErrNotFound
	}
	user, ok := s.usersByEmail[email]
	if !ok {
		return repo.HumanUser{}, repo.ErrNotFound
	}
	return user, nil
}

func (s *stubHumanUsers) List(ctx context.Context, organizationID uuid.UUID) ([]repo.HumanUser, error) {
	users := make([]repo.HumanUser, 0, len(s.usersByID))
	for _, user := range s.usersByID {
		users = append(users, user)
	}
	return users, nil
}

func (s *stubHumanUsers) IncrFailedAttempts(ctx context.Context, id uuid.UUID) (repo.HumanUser, error) {
	user, ok := s.usersByID[id]
	if !ok {
		return repo.HumanUser{}, repo.ErrNotFound
	}
	user.FailedLoginAttempts++
	s.usersByID[id] = user
	return user, nil
}

func (s *stubHumanUsers) ResetFailedAttempts(ctx context.Context, id uuid.UUID) (repo.HumanUser, error) {
	user, ok := s.usersByID[id]
	if !ok {
		return repo.HumanUser{}, repo.ErrNotFound
	}
	user.FailedLoginAttempts = 0
	user.LockedUntil = nil
	s.usersByID[id] = user
	return user, nil
}

func (s *stubHumanUsers) SetLockedUntil(ctx context.Context, id uuid.UUID, lockedUntil *time.Time) (repo.HumanUser, error) {
	user, ok := s.usersByID[id]
	if !ok {
		return repo.HumanUser{}, repo.ErrNotFound
	}
	user.LockedUntil = lockedUntil
	s.usersByID[id] = user
	return user, nil
}

func (s *stubHumanUsers) Update(ctx context.Context, user repo.HumanUser) (repo.HumanUser, error) {
	if s.usersByID == nil {
		s.usersByID = map[uuid.UUID]repo.HumanUser{}
	}
	s.usersByID[user.ID] = user
	if s.usersByEmail == nil {
		s.usersByEmail = map[string]repo.HumanUser{}
	}
	s.usersByEmail[user.Email] = user
	return user, nil
}

type stubSessions struct {
	sessionByHash map[string]repo.AuthSession
}

func (s *stubSessions) Create(ctx context.Context, session repo.AuthSession) (repo.AuthSession, error) {
	session.ID = uuid.New()
	session.CreatedAt = time.Now().UTC()
	session.LastUsedAt = session.CreatedAt
	if s.sessionByHash == nil {
		s.sessionByHash = map[string]repo.AuthSession{}
	}
	s.sessionByHash[session.TokenHash] = session
	return session, nil
}

func (s *stubSessions) GetByTokenHash(ctx context.Context, tokenHash string) (repo.AuthSession, error) {
	session, ok := s.sessionByHash[tokenHash]
	if !ok {
		return repo.AuthSession{}, repo.ErrNotFound
	}
	return session, nil
}

func (s *stubSessions) Revoke(ctx context.Context, id uuid.UUID) error {
	for hash, session := range s.sessionByHash {
		if session.ID == id {
			now := time.Now().UTC()
			session.RevokedAt = &now
			s.sessionByHash[hash] = session
			return nil
		}
	}
	return repo.ErrNotFound
}

func (s *stubSessions) RevokeAll(ctx context.Context, userID uuid.UUID) (int64, error) {
	var count int64
	for hash, session := range s.sessionByHash {
		if session.UserID != userID {
			continue
		}
		now := time.Now().UTC()
		session.RevokedAt = &now
		s.sessionByHash[hash] = session
		count++
	}
	return count, nil
}

func (s *stubSessions) TouchLastUsed(ctx context.Context, id uuid.UUID) (repo.AuthSession, error) {
	for hash, session := range s.sessionByHash {
		if session.ID == id {
			session.LastUsedAt = time.Now().UTC()
			s.sessionByHash[hash] = session
			return session, nil
		}
	}
	return repo.AuthSession{}, repo.ErrNotFound
}

func (s *stubSessions) ExtendExpiry(ctx context.Context, id uuid.UUID, expiresAt time.Time) (repo.AuthSession, error) {
	for hash, session := range s.sessionByHash {
		if session.ID == id {
			session.ExpiresAt = expiresAt
			s.sessionByHash[hash] = session
			return session, nil
		}
	}
	return repo.AuthSession{}, repo.ErrNotFound
}

type stubAPIKeys struct{}

func (s *stubAPIKeys) Create(ctx context.Context, apiKey repo.APIKey) (repo.APIKey, error) {
	return apiKey, nil
}

func (s *stubAPIKeys) GetByKeyHash(ctx context.Context, keyHash string) (repo.APIKey, error) {
	return repo.APIKey{}, repo.ErrNotFound
}

func (s *stubAPIKeys) ListByUser(ctx context.Context, userID uuid.UUID) ([]repo.APIKey, error) {
	return nil, nil
}

func (s *stubAPIKeys) Revoke(ctx context.Context, id uuid.UUID) (repo.APIKey, error) {
	return repo.APIKey{}, repo.ErrNotFound
}

func (s *stubAPIKeys) TouchLastUsed(ctx context.Context, id uuid.UUID) (repo.APIKey, error) {
	return repo.APIKey{}, repo.ErrNotFound
}

func stringPtr(v string) *string {
	return &v
}
