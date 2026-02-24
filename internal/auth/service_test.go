package auth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"golang.org/x/crypto/bcrypt"
)

func TestGenerateTokenUniqueAcrossIterations(t *testing.T) {
	svc, err := NewService(Options{
		Users:        &stubUserRepo{},
		Sessions:     &stubSessionRepo{},
		APIKeys:      &stubAPIKeyRepo{},
		DefaultOrgID: uuid.New(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	impl := svc.(*service)
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		token, err := impl.generateToken(32)
		if err != nil {
			t.Fatalf("generateToken: %v", err)
		}
		if _, exists := seen[token]; exists {
			t.Fatalf("duplicate token generated at iteration %d", i)
		}
		seen[token] = struct{}{}
	}
}

func TestBcryptCompareHashAndPasswordWiring(t *testing.T) {
	password := "correct-horse-battery-staple"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword: %v", err)
	}

	if err := bcrypt.CompareHashAndPassword(hash, []byte(password)); err != nil {
		t.Fatalf("CompareHashAndPassword correct password failed: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte("wrong-password")); err == nil {
		t.Fatal("CompareHashAndPassword should fail for wrong password")
	}
}

func TestMagicLinkTTLAndSingleUse(t *testing.T) {
	orgID := uuid.New()
	user := repo.HumanUser{
		ID:             uuid.New(),
		OrganizationID: orgID,
		Email:          "sam@example.com",
		DisplayName:    "Sam",
		Role:           "admin",
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	}
	fakeClock := clock.NewFake(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	userRepo := &stubUserRepo{
		usersByEmail: map[string]repo.HumanUser{
			user.Email: user,
		},
		usersByID: map[uuid.UUID]repo.HumanUser{
			user.ID: user,
		},
	}

	svc, err := NewService(Options{
		Users:        userRepo,
		Sessions:     &stubSessionRepo{},
		APIKeys:      &stubAPIKeyRepo{},
		Clock:        fakeClock,
		DefaultOrgID: orgID,
		MagicLinkTTL: 15 * time.Minute,
		BcryptCost:   bcrypt.MinCost,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	result, err := svc.MagicLink(context.Background(), user.Email)
	if err != nil {
		t.Fatalf("MagicLink: %v", err)
	}

	if err := svc.ResetPassword(context.Background(), result.Token, "new-password"); err != nil {
		t.Fatalf("ResetPassword first use: %v", err)
	}
	if err := svc.ResetPassword(context.Background(), result.Token, "new-password-2"); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("ResetPassword second use err = %v, want ErrTokenExpired", err)
	}

	next, err := svc.MagicLink(context.Background(), user.Email)
	if err != nil {
		t.Fatalf("MagicLink second token: %v", err)
	}
	fakeClock.Advance(16 * time.Minute)
	if err := svc.ResetPassword(context.Background(), next.Token, "later-password"); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("ResetPassword after TTL err = %v, want ErrTokenExpired", err)
	}
}

type stubUserRepo struct {
	usersByEmail map[string]repo.HumanUser
	usersByID    map[uuid.UUID]repo.HumanUser
}

func (s *stubUserRepo) GetByID(_ context.Context, id uuid.UUID) (repo.HumanUser, error) {
	user, ok := s.usersByID[id]
	if !ok {
		return repo.HumanUser{}, repo.ErrNotFound
	}
	return user, nil
}

func (s *stubUserRepo) GetByEmail(_ context.Context, _ uuid.UUID, email string) (repo.HumanUser, error) {
	if s.usersByEmail == nil {
		return repo.HumanUser{}, repo.ErrNotFound
	}
	user, ok := s.usersByEmail[email]
	if !ok {
		return repo.HumanUser{}, repo.ErrNotFound
	}
	return user, nil
}

func (s *stubUserRepo) List(_ context.Context, _ uuid.UUID) ([]repo.HumanUser, error) {
	out := make([]repo.HumanUser, 0, len(s.usersByEmail))
	for _, user := range s.usersByEmail {
		out = append(out, user)
	}
	return out, nil
}

func (s *stubUserRepo) Update(_ context.Context, user repo.HumanUser) (repo.HumanUser, error) {
	if s.usersByID == nil {
		s.usersByID = make(map[uuid.UUID]repo.HumanUser)
	}
	if s.usersByEmail == nil {
		s.usersByEmail = make(map[string]repo.HumanUser)
	}
	s.usersByID[user.ID] = user
	s.usersByEmail[user.Email] = user
	return user, nil
}

func (s *stubUserRepo) IncrFailedAttempts(_ context.Context, id uuid.UUID) (repo.HumanUser, error) {
	user, ok := s.usersByID[id]
	if !ok {
		return repo.HumanUser{}, repo.ErrNotFound
	}
	user.FailedLoginAttempts++
	s.usersByID[id] = user
	s.usersByEmail[user.Email] = user
	return user, nil
}

func (s *stubUserRepo) ResetFailedAttempts(_ context.Context, id uuid.UUID) (repo.HumanUser, error) {
	user, ok := s.usersByID[id]
	if !ok {
		return repo.HumanUser{}, repo.ErrNotFound
	}
	user.FailedLoginAttempts = 0
	user.LockedUntil = nil
	s.usersByID[id] = user
	s.usersByEmail[user.Email] = user
	return user, nil
}

func (s *stubUserRepo) SetLockedUntil(_ context.Context, id uuid.UUID, lockedUntil *time.Time) (repo.HumanUser, error) {
	user, ok := s.usersByID[id]
	if !ok {
		return repo.HumanUser{}, repo.ErrNotFound
	}
	user.LockedUntil = lockedUntil
	s.usersByID[id] = user
	s.usersByEmail[user.Email] = user
	return user, nil
}

type stubSessionRepo struct{}

func (s *stubSessionRepo) Create(_ context.Context, session repo.AuthSession) (repo.AuthSession, error) {
	session.ID = uuid.New()
	session.CreatedAt = time.Now()
	session.LastUsedAt = session.CreatedAt
	return session, nil
}

func (s *stubSessionRepo) GetByTokenHash(_ context.Context, _ string) (repo.AuthSession, error) {
	return repo.AuthSession{}, repo.ErrNotFound
}

func (s *stubSessionRepo) Revoke(context.Context, uuid.UUID) error { return nil }

func (s *stubSessionRepo) TouchLastUsed(_ context.Context, id uuid.UUID) (repo.AuthSession, error) {
	return repo.AuthSession{ID: id, LastUsedAt: time.Now()}, nil
}

func (s *stubSessionRepo) ExtendExpiry(_ context.Context, id uuid.UUID, expiresAt time.Time) (repo.AuthSession, error) {
	return repo.AuthSession{ID: id, ExpiresAt: expiresAt}, nil
}

type stubAPIKeyRepo struct{}

func (s *stubAPIKeyRepo) Create(_ context.Context, key repo.APIKey) (repo.APIKey, error) {
	key.ID = uuid.New()
	key.CreatedAt = time.Now()
	return key, nil
}

func (s *stubAPIKeyRepo) GetByID(context.Context, uuid.UUID) (repo.APIKey, error) {
	return repo.APIKey{}, repo.ErrNotFound
}

func (s *stubAPIKeyRepo) GetByKeyHash(context.Context, string) (repo.APIKey, error) {
	return repo.APIKey{}, repo.ErrNotFound
}

func (s *stubAPIKeyRepo) ListByUser(context.Context, uuid.UUID) ([]repo.APIKey, error) {
	return nil, nil
}

func (s *stubAPIKeyRepo) Revoke(context.Context, uuid.UUID) (repo.APIKey, error) {
	return repo.APIKey{}, repo.ErrNotFound
}

func (s *stubAPIKeyRepo) TouchLastUsed(context.Context, uuid.UUID) (repo.APIKey, error) {
	return repo.APIKey{}, repo.ErrNotFound
}
