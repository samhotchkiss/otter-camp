//go:build integration

package repo

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestHumanUserRepoCRUDAndConstraints(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgRepo := NewOrgRepo(pool)
	userRepo := NewHumanUserRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "auth-org", DisplayName: "Auth Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	passwordHash := "$2a$12$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	created, err := userRepo.Create(ctx, HumanUser{
		OrganizationID: org.ID,
		Email:          "sam@example.com",
		DisplayName:    "Sam",
		PasswordHash:   &passwordHash,
		Role:           "admin",
		IsActive:       true,
		Settings:       json.RawMessage(`{"push_preferences":{"email":true}}`),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	byID, err := userRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if byID.Email != "sam@example.com" {
		t.Fatalf("email = %q, want %q", byID.Email, "sam@example.com")
	}

	byEmail, err := userRepo.GetByEmail(ctx, org.ID, "sam@example.com")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if byEmail.ID != created.ID {
		t.Fatalf("GetByEmail id = %s, want %s", byEmail.ID, created.ID)
	}

	if _, err := userRepo.GetByEmail(ctx, org.ID, "missing@example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByEmail missing err = %v, want ErrNotFound", err)
	}

	updatedNoPassword, err := userRepo.Update(ctx, HumanUser{
		ID:          created.ID,
		Email:       "sam+updated@example.com",
		DisplayName: "Sam Updated",
		Role:        "admin",
		Settings:    json.RawMessage(`{"push_preferences":{"email":false}}`),
	})
	if err != nil {
		t.Fatalf("Update without password hash: %v", err)
	}
	if updatedNoPassword.PasswordHash == nil || *updatedNoPassword.PasswordHash != passwordHash {
		t.Fatalf("password hash changed unexpectedly: got %v", updatedNoPassword.PasswordHash)
	}

	newPasswordHash := "$2a$12$bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	updatedWithPassword, err := userRepo.Update(ctx, HumanUser{
		ID:           created.ID,
		Email:        "sam+updated@example.com",
		DisplayName:  "Sam Updated Again",
		Role:         "admin",
		PasswordHash: &newPasswordHash,
		Settings:     json.RawMessage(`{"ui":{"theme":"light"}}`),
	})
	if err != nil {
		t.Fatalf("Update with password hash: %v", err)
	}
	if updatedWithPassword.PasswordHash == nil || *updatedWithPassword.PasswordHash != newPasswordHash {
		t.Fatalf("password hash not updated: got %v, want %q", updatedWithPassword.PasswordHash, newPasswordHash)
	}

	incrOne, err := userRepo.IncrFailedAttempts(ctx, created.ID)
	if err != nil {
		t.Fatalf("IncrFailedAttempts first: %v", err)
	}
	if incrOne.FailedLoginAttempts != 1 {
		t.Fatalf("failed_login_attempts = %d, want 1", incrOne.FailedLoginAttempts)
	}
	incrTwo, err := userRepo.IncrFailedAttempts(ctx, created.ID)
	if err != nil {
		t.Fatalf("IncrFailedAttempts second: %v", err)
	}
	if incrTwo.FailedLoginAttempts != 2 {
		t.Fatalf("failed_login_attempts = %d, want 2", incrTwo.FailedLoginAttempts)
	}

	lockedUntil := time.Now().Add(30 * time.Minute).UTC()
	locked, err := userRepo.SetLockedUntil(ctx, created.ID, &lockedUntil)
	if err != nil {
		t.Fatalf("SetLockedUntil: %v", err)
	}
	if locked.LockedUntil == nil {
		t.Fatal("expected locked_until to be set")
	}

	reset, err := userRepo.ResetFailedAttempts(ctx, created.ID)
	if err != nil {
		t.Fatalf("ResetFailedAttempts: %v", err)
	}
	if reset.FailedLoginAttempts != 0 {
		t.Fatalf("failed_login_attempts = %d, want 0", reset.FailedLoginAttempts)
	}
	if reset.LockedUntil != nil {
		t.Fatalf("locked_until = %v, want nil", reset.LockedUntil)
	}

	inactive, err := userRepo.SetActive(ctx, created.ID, false)
	if err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if inactive.IsActive {
		t.Fatal("expected user to be inactive")
	}

	listed, err := userRepo.List(ctx, org.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List count = %d, want 1", len(listed))
	}

	_, err = userRepo.Create(ctx, HumanUser{
		OrganizationID: org.ID,
		Email:          "sam+updated@example.com",
		DisplayName:    "Sam Duplicate",
		Role:           "admin",
		IsActive:       true,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate org/email error = %v, want ErrConflict", err)
	}

	_, err = userRepo.Create(ctx, HumanUser{
		OrganizationID: org.ID,
		Email:          "invalid-role@example.com",
		DisplayName:    "Role Fail",
		Role:           "owner",
		IsActive:       true,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid role error = %v, want ErrConflict", err)
	}
}

func TestAuthSessionRepoLifecycleAndDeleteExpired(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	_, user := createTestOrgAndUser(t, pool, "session-org", "session@example.com")
	sessionRepo := NewAuthSessionRepo(pool)

	expires := time.Now().Add(2 * time.Hour).UTC()
	session, err := sessionRepo.Create(ctx, AuthSession{
		UserID:    user.ID,
		TokenHash: "token-hash-1",
		ExpiresAt: expires,
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	_, err = sessionRepo.Create(ctx, AuthSession{
		UserID:    user.ID,
		TokenHash: "token-hash-1",
		ExpiresAt: time.Now().Add(1 * time.Hour).UTC(),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate token_hash error = %v, want ErrConflict", err)
	}

	fetched, err := sessionRepo.GetByTokenHash(ctx, "token-hash-1")
	if err != nil {
		t.Fatalf("GetByTokenHash: %v", err)
	}
	if fetched.User == nil || fetched.User.ID != user.ID {
		t.Fatalf("expected eager-loaded user id %s, got %+v", user.ID, fetched.User)
	}

	touched, err := sessionRepo.TouchLastUsed(ctx, session.ID)
	if err != nil {
		t.Fatalf("TouchLastUsed: %v", err)
	}
	if touched.LastUsedAt.Before(session.LastUsedAt) {
		t.Fatal("expected last_used_at to advance")
	}

	extendedExpiry := time.Now().Add(72 * time.Hour).UTC()
	extended, err := sessionRepo.ExtendExpiry(ctx, session.ID, extendedExpiry)
	if err != nil {
		t.Fatalf("ExtendExpiry: %v", err)
	}
	if !extended.ExpiresAt.Equal(extendedExpiry) {
		t.Fatalf("expires_at = %s, want %s", extended.ExpiresAt, extendedExpiry)
	}

	if err := sessionRepo.Revoke(ctx, session.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	revokedFetched, err := sessionRepo.GetByTokenHash(ctx, "token-hash-1")
	if err != nil {
		t.Fatalf("GetByTokenHash revoked session: %v", err)
	}
	if revokedFetched.RevokedAt == nil {
		t.Fatal("expected revoked_at to be set")
	}

	expiredSession, err := sessionRepo.Create(ctx, AuthSession{
		UserID:    user.ID,
		TokenHash: "token-hash-expired",
		ExpiresAt: time.Now().Add(-1 * time.Hour).UTC(),
	})
	if err != nil {
		t.Fatalf("create expired session: %v", err)
	}

	activeSession, err := sessionRepo.Create(ctx, AuthSession{
		UserID:    user.ID,
		TokenHash: "token-hash-active",
		ExpiresAt: time.Now().Add(4 * time.Hour).UTC(),
	})
	if err != nil {
		t.Fatalf("create active session: %v", err)
	}

	revokedNotExpired, err := sessionRepo.Create(ctx, AuthSession{
		UserID:    user.ID,
		TokenHash: "token-hash-revoked",
		ExpiresAt: time.Now().Add(4 * time.Hour).UTC(),
	})
	if err != nil {
		t.Fatalf("create revoked-not-expired session: %v", err)
	}
	if err := sessionRepo.Revoke(ctx, revokedNotExpired.ID); err != nil {
		t.Fatalf("revoke revoked-not-expired session: %v", err)
	}

	deleted, err := sessionRepo.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeleteExpired removed = %d, want 1", deleted)
	}

	assertSessionExists(t, pool, expiredSession.ID, false)
	assertSessionExists(t, pool, activeSession.ID, true)
	assertSessionExists(t, pool, revokedNotExpired.ID, true)

	activeList, err := sessionRepo.ListActive(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(activeList) != 1 || activeList[0].ID != activeSession.ID {
		t.Fatalf("ListActive = %+v, want only %s", activeList, activeSession.ID)
	}
}

func TestAPIKeyRepoLifecycleAndCascadeDelete(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, user := createTestOrgAndUser(t, pool, "api-key-org", "apikey@example.com")
	keyRepo := NewAPIKeyRepo(pool)
	sessionRepo := NewAuthSessionRepo(pool)

	created, err := keyRepo.Create(ctx, APIKey{
		UserID:      user.ID,
		KeyHash:     "key-hash-1",
		KeyPrefix:   "otk_AbCd",
		DisplayName: "Primary",
		Scopes:      []string{"read", "write"},
	})
	if err != nil {
		t.Fatalf("Create api key: %v", err)
	}
	if len(created.KeyPrefix) > 8 {
		t.Fatalf("key_prefix length = %d, want <= 8", len(created.KeyPrefix))
	}

	fetched, err := keyRepo.GetByKeyHash(ctx, "key-hash-1")
	if err != nil {
		t.Fatalf("GetByKeyHash: %v", err)
	}
	if fetched.ID != created.ID {
		t.Fatalf("GetByKeyHash id = %s, want %s", fetched.ID, created.ID)
	}

	listed, err := keyRepo.ListByUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListByUser count = %d, want 1", len(listed))
	}

	revoked, err := keyRepo.Revoke(ctx, created.ID)
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if revoked.RevokedAt == nil {
		t.Fatal("expected revoked_at to be set")
	}

	touched, err := keyRepo.TouchLastUsed(ctx, created.ID)
	if err != nil {
		t.Fatalf("TouchLastUsed: %v", err)
	}
	if touched.LastUsedAt == nil {
		t.Fatal("expected last_used_at to be set")
	}

	_, err = keyRepo.Create(ctx, APIKey{
		UserID:      user.ID,
		KeyHash:     "key-hash-1",
		KeyPrefix:   "otk_XyZ1",
		DisplayName: "Duplicate Hash",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate key_hash error = %v, want ErrConflict", err)
	}

	_, err = keyRepo.Create(ctx, APIKey{
		UserID:      user.ID,
		KeyHash:     "key-hash-too-long-prefix",
		KeyPrefix:   "otk_too_long",
		DisplayName: "Too Long Prefix",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("long key_prefix error = %v, want ErrConflict", err)
	}

	_, err = sessionRepo.Create(ctx, AuthSession{
		UserID:    user.ID,
		TokenHash: "cascade-token",
		ExpiresAt: time.Now().Add(1 * time.Hour).UTC(),
	})
	if err != nil {
		t.Fatalf("create session for cascade: %v", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM organization WHERE id = $1`, org.ID); err != nil {
		t.Fatalf("delete organization: %v", err)
	}

	assertCount(t, pool, `SELECT COUNT(*) FROM human_user WHERE organization_id = $1`, org.ID, 0)
	assertCount(t, pool, `SELECT COUNT(*) FROM auth_session WHERE user_id = $1`, user.ID, 0)
	assertCount(t, pool, `SELECT COUNT(*) FROM api_key WHERE user_id = $1`, user.ID, 0)
}

func createTestOrgAndUser(t *testing.T, pool *pgxpool.Pool, orgSlug, email string) (Organization, HumanUser) {
	t.Helper()
	ctx := context.Background()

	orgRepo := NewOrgRepo(pool)
	userRepo := NewHumanUserRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: orgSlug, DisplayName: orgSlug})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	user, err := userRepo.Create(ctx, HumanUser{
		OrganizationID: org.ID,
		Email:          email,
		DisplayName:    "Test User",
		Role:           "member",
		IsActive:       true,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	return org, user
}

func assertSessionExists(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, want bool) {
	t.Helper()

	var exists bool
	if err := pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM auth_session WHERE id = $1)`, id).Scan(&exists); err != nil {
		t.Fatalf("session existence query: %v", err)
	}
	if exists != want {
		t.Fatalf("session %s exists=%v, want %v", id, exists, want)
	}
}

func assertCount(t *testing.T, pool *pgxpool.Pool, query string, arg uuid.UUID, want int) {
	t.Helper()

	var count int
	if err := pool.QueryRow(context.Background(), query, arg).Scan(&count); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != want {
		t.Fatalf("count = %d, want %d", count, want)
	}
}
