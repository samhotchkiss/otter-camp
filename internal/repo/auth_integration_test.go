//go:build integration

package repo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestHumanUserRepoCRUDAndConflicts(t *testing.T) {
	pool := testdb.New(t)
	orgRepo := NewOrgRepo(pool)
	userRepo := NewHumanUserRepo(pool)

	org, err := orgRepo.Create(context.Background(), Organization{
		Slug:        "auth-human-user-org",
		DisplayName: "Auth Human User Org",
	})
	if err != nil {
		t.Fatalf("create org failed: %v", err)
	}

	passwordHash := "$2a$12$abcdefghijklmnopqrstuvabcdefghijklmnopqrstuvabcdefghij"
	created, err := userRepo.Create(context.Background(), HumanUser{
		OrganizationID: org.ID,
		Email:          "alice@example.com",
		DisplayName:    "Alice",
		PasswordHash:   &passwordHash,
		Role:           "admin",
		IsActive:       true,
		Settings:       []byte(`{"push_preferences":{"enabled":true}}`),
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	byID, err := userRepo.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if byID.Email != "alice@example.com" {
		t.Fatalf("GetByID email = %q, want alice@example.com", byID.Email)
	}

	byEmail, err := userRepo.GetByEmail(context.Background(), org.ID, "alice@example.com")
	if err != nil {
		t.Fatalf("GetByEmail failed: %v", err)
	}
	if byEmail.ID != created.ID {
		t.Fatalf("GetByEmail id = %s, want %s", byEmail.ID, created.ID)
	}

	list, err := userRepo.List(context.Background(), org.ID)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List len = %d, want 1", len(list))
	}

	updatedWithoutPassword, err := userRepo.Update(context.Background(), HumanUserUpdate{
		ID:          created.ID,
		Email:       "alice@example.com",
		DisplayName: "Alice Updated",
		Role:        "member",
		Settings:    []byte(`{"theme":"dark"}`),
	})
	if err != nil {
		t.Fatalf("Update without password failed: %v", err)
	}
	if updatedWithoutPassword.PasswordHash == nil || *updatedWithoutPassword.PasswordHash != passwordHash {
		t.Fatalf("password hash unexpectedly changed on update without password")
	}

	nextHash := "$2a$12$mnopqrstuvwxyzabcdefghijklmnopqrstuvabcdefghijklmnopqr"
	updatedWithPassword, err := userRepo.Update(context.Background(), HumanUserUpdate{
		ID:           created.ID,
		Email:        "alice@example.com",
		DisplayName:  "Alice Updated 2",
		PasswordHash: &nextHash,
		Role:         "member",
		Settings:     []byte(`{"theme":"light"}`),
	})
	if err != nil {
		t.Fatalf("Update with password failed: %v", err)
	}
	if updatedWithPassword.PasswordHash == nil || *updatedWithPassword.PasswordHash != nextHash {
		t.Fatalf("password hash = %v, want updated hash", updatedWithPassword.PasswordHash)
	}

	inc1, err := userRepo.IncrFailedAttempts(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("IncrFailedAttempts #1 failed: %v", err)
	}
	inc2, err := userRepo.IncrFailedAttempts(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("IncrFailedAttempts #2 failed: %v", err)
	}
	if inc1.FailedLoginAttempts != 1 || inc2.FailedLoginAttempts != 2 {
		t.Fatalf("failed login attempts progression = (%d, %d), want (1, 2)", inc1.FailedLoginAttempts, inc2.FailedLoginAttempts)
	}

	reset, err := userRepo.ResetFailedAttempts(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("ResetFailedAttempts failed: %v", err)
	}
	if reset.FailedLoginAttempts != 0 {
		t.Fatalf("failed_login_attempts after reset = %d, want 0", reset.FailedLoginAttempts)
	}

	lockUntil := time.Now().UTC().Add(30 * time.Minute)
	locked, err := userRepo.SetLockedUntil(context.Background(), created.ID, &lockUntil)
	if err != nil {
		t.Fatalf("SetLockedUntil failed: %v", err)
	}
	if locked.LockedUntil == nil {
		t.Fatal("LockedUntil = nil, want non-nil")
	}

	deactivated, err := userRepo.SetActive(context.Background(), created.ID, false)
	if err != nil {
		t.Fatalf("SetActive failed: %v", err)
	}
	if deactivated.IsActive {
		t.Fatal("IsActive = true, want false")
	}

	if _, err := userRepo.GetByEmail(context.Background(), org.ID, "missing@example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByEmail missing error = %v, want ErrNotFound", err)
	}

	_, err = userRepo.Create(context.Background(), HumanUser{
		OrganizationID: org.ID,
		Email:          "alice@example.com",
		DisplayName:    "Alice Clone",
		Role:           "member",
		IsActive:       true,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate (organization_id,email) error = %v, want ErrConflict", err)
	}
}

func TestAuthSessionRepoLifecycleAndDuplicateTokenHash(t *testing.T) {
	pool := testdb.New(t)
	user := mustCreateAuthUser(t, pool, "auth-session-org", "session-user@example.com")
	sessionRepo := NewAuthSessionRepo(pool)

	tokenHash := strings.Repeat("a", 64)
	created, err := sessionRepo.Create(context.Background(), AuthSession{
		UserID:    user.ID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
		UserAgent: strPtr("test-agent"),
		IPAddress: strPtr("127.0.0.1"),
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	touchedAt := time.Now().UTC().Add(15 * time.Minute)
	touched, err := sessionRepo.TouchLastUsed(context.Background(), created.ID, touchedAt)
	if err != nil {
		t.Fatalf("TouchLastUsed failed: %v", err)
	}
	if !touched.LastUsedAt.Equal(touchedAt) {
		t.Fatalf("last_used_at = %v, want %v", touched.LastUsedAt, touchedAt)
	}

	extendedAt := time.Now().UTC().Add(48 * time.Hour)
	extended, err := sessionRepo.ExtendExpiry(context.Background(), created.ID, extendedAt)
	if err != nil {
		t.Fatalf("ExtendExpiry failed: %v", err)
	}
	if !extended.ExpiresAt.Equal(extendedAt) {
		t.Fatalf("expires_at = %v, want %v", extended.ExpiresAt, extendedAt)
	}

	revoked, err := sessionRepo.Revoke(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}
	if revoked.RevokedAt == nil {
		t.Fatal("revoked_at = nil, want non-nil")
	}

	loaded, err := sessionRepo.GetByTokenHash(context.Background(), tokenHash)
	if err != nil {
		t.Fatalf("GetByTokenHash failed: %v", err)
	}
	if loaded.RevokedAt == nil {
		t.Fatal("GetByTokenHash revoked_at = nil, want non-nil")
	}
	if loaded.User == nil || loaded.User.ID != user.ID {
		t.Fatalf("GetByTokenHash user preload failed: %+v", loaded.User)
	}

	active, err := sessionRepo.ListActive(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("ListActive failed: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("ListActive len = %d, want 0 for revoked-only session set", len(active))
	}

	_, err = sessionRepo.Create(context.Background(), AuthSession{
		UserID:    user.ID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate token_hash error = %v, want ErrConflict", err)
	}
}

func TestAPIKeyRepoLifecycleAndDuplicateKeyHash(t *testing.T) {
	pool := testdb.New(t)
	user := mustCreateAuthUser(t, pool, "api-key-org", "api-user@example.com")
	keyRepo := NewAPIKeyRepo(pool)

	raw := "otk_AbCdEfGhIjKlMnOpQrStUvWxYz0123456789"
	prefix := raw[:8]
	keyHash := strings.Repeat("b", 64)

	created, err := keyRepo.Create(context.Background(), APIKey{
		UserID:      user.ID,
		KeyHash:     keyHash,
		KeyPrefix:   prefix,
		DisplayName: "Local Dev Key",
		Scopes:      []string{"read", "write"},
		ExpiresAt:   nil,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if len(created.KeyPrefix) > 8 {
		t.Fatalf("key_prefix length = %d, want <= 8", len(created.KeyPrefix))
	}
	if created.KeyPrefix == raw {
		t.Fatal("key_prefix should not equal full raw key")
	}

	loaded, err := keyRepo.GetByKeyHash(context.Background(), keyHash)
	if err != nil {
		t.Fatalf("GetByKeyHash failed: %v", err)
	}
	if loaded.ID != created.ID {
		t.Fatalf("GetByKeyHash id = %s, want %s", loaded.ID, created.ID)
	}

	listed, err := keyRepo.ListByUser(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("ListByUser failed: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListByUser len = %d, want 1", len(listed))
	}

	revoked, err := keyRepo.Revoke(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}
	if revoked.RevokedAt == nil {
		t.Fatal("revoked_at = nil, want non-nil")
	}

	_, err = keyRepo.Create(context.Background(), APIKey{
		UserID:      user.ID,
		KeyHash:     keyHash,
		KeyPrefix:   "otk_other",
		DisplayName: "Duplicate",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate key_hash error = %v, want ErrConflict", err)
	}
}

func TestAuthSessionRepoDeleteExpired(t *testing.T) {
	pool := testdb.New(t)
	user := mustCreateAuthUser(t, pool, "session-delete-expired-org", "delete-expired@example.com")
	sessionRepo := NewAuthSessionRepo(pool)

	expired, err := sessionRepo.Create(context.Background(), AuthSession{
		UserID:    user.ID,
		TokenHash: strings.Repeat("1", 64),
		ExpiresAt: time.Now().UTC().Add(-2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create expired session failed: %v", err)
	}

	active, err := sessionRepo.Create(context.Background(), AuthSession{
		UserID:    user.ID,
		TokenHash: strings.Repeat("2", 64),
		ExpiresAt: time.Now().UTC().Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create active session failed: %v", err)
	}

	revoked, err := sessionRepo.Create(context.Background(), AuthSession{
		UserID:    user.ID,
		TokenHash: strings.Repeat("3", 64),
		ExpiresAt: time.Now().UTC().Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create revoked session failed: %v", err)
	}
	if _, err := sessionRepo.Revoke(context.Background(), revoked.ID); err != nil {
		t.Fatalf("revoke session failed: %v", err)
	}

	removed, err := sessionRepo.DeleteExpired(context.Background())
	if err != nil {
		t.Fatalf("DeleteExpired failed: %v", err)
	}
	if removed != 1 {
		t.Fatalf("DeleteExpired removed = %d, want 1", removed)
	}

	assertRowCountByID(t, pool, "auth_session", expired.ID, 0)
	assertRowCountByID(t, pool, "auth_session", active.ID, 1)
	assertRowCountByID(t, pool, "auth_session", revoked.ID, 1)
}

func TestAuthSchemaCascadeFromOrganizationDelete(t *testing.T) {
	pool := testdb.New(t)
	orgRepo := NewOrgRepo(pool)
	userRepo := NewHumanUserRepo(pool)
	sessionRepo := NewAuthSessionRepo(pool)
	keyRepo := NewAPIKeyRepo(pool)

	org, err := orgRepo.Create(context.Background(), Organization{
		Slug:        "auth-cascade-org",
		DisplayName: "Auth Cascade Org",
	})
	if err != nil {
		t.Fatalf("create org failed: %v", err)
	}

	user, err := userRepo.Create(context.Background(), HumanUser{
		OrganizationID: org.ID,
		Email:          "cascade@example.com",
		DisplayName:    "Cascade User",
		Role:           "admin",
		IsActive:       true,
	})
	if err != nil {
		t.Fatalf("create user failed: %v", err)
	}

	session, err := sessionRepo.Create(context.Background(), AuthSession{
		UserID:    user.ID,
		TokenHash: strings.Repeat("4", 64),
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create session failed: %v", err)
	}

	key, err := keyRepo.Create(context.Background(), APIKey{
		UserID:      user.ID,
		KeyHash:     strings.Repeat("5", 64),
		KeyPrefix:   "otk_casc",
		DisplayName: "Cascade Key",
	})
	if err != nil {
		t.Fatalf("create api key failed: %v", err)
	}

	if _, err := pool.Exec(context.Background(), `DELETE FROM organization WHERE id = $1`, org.ID); err != nil {
		t.Fatalf("delete org failed: %v", err)
	}

	assertRowCountByID(t, pool, "human_user", user.ID, 0)
	assertRowCountByID(t, pool, "auth_session", session.ID, 0)
	assertRowCountByID(t, pool, "api_key", key.ID, 0)
}

func mustCreateAuthUser(t *testing.T, pool *pgxpool.Pool, orgSlug, email string) HumanUser {
	t.Helper()

	orgRepo := NewOrgRepo(pool)
	userRepo := NewHumanUserRepo(pool)

	org, err := orgRepo.Create(context.Background(), Organization{
		Slug:        orgSlug,
		DisplayName: orgSlug,
	})
	if err != nil {
		t.Fatalf("create org failed: %v", err)
	}

	user, err := userRepo.Create(context.Background(), HumanUser{
		OrganizationID: org.ID,
		Email:          email,
		DisplayName:    "Auth User",
		Role:           "admin",
		IsActive:       true,
	})
	if err != nil {
		t.Fatalf("create user failed: %v", err)
	}

	return user
}

func assertRowCountByID(t *testing.T, pool *pgxpool.Pool, table string, id uuid.UUID, want int) {
	t.Helper()

	var count int
	query := "SELECT COUNT(*) FROM " + table + " WHERE id = $1"
	if err := pool.QueryRow(context.Background(), query, id).Scan(&count); err != nil {
		t.Fatalf("count query failed for %s: %v", table, err)
	}
	if count != want {
		t.Fatalf("%s row count for %s = %d, want %d", table, id, count, want)
	}
}
