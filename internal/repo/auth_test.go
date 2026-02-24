package repo

import (
	"strings"
	"testing"
)

func TestPasswordHashUpdateArg(t *testing.T) {
	set, value := passwordHashUpdateArg(nil)
	if set {
		t.Fatal("expected set=false when password hash is nil")
	}
	if value != nil {
		t.Fatal("expected nil password hash value when set=false")
	}

	hash := "$2a$12$abc123"
	set, value = passwordHashUpdateArg(&hash)
	if !set {
		t.Fatal("expected set=true when password hash is provided")
	}
	if value == nil || *value != hash {
		t.Fatalf("password hash value = %v, want %q", value, hash)
	}

	query := strings.ToUpper(humanUserUpdateQuery)
	if !strings.Contains(query, "PASSWORD_HASH = CASE WHEN") {
		t.Fatal("expected update query to keep existing password_hash unless explicitly set")
	}
}

func TestGetByTokenHashQueryJoinsUserAndIncludesRevokedRows(t *testing.T) {
	query := strings.ToUpper(authSessionGetByTokenHashQuery)
	if !strings.Contains(query, "JOIN HUMAN_USER") {
		t.Fatal("expected GetByTokenHash to eagerly load user via JOIN")
	}
	if strings.Contains(query, "REVOKED_AT IS NULL") {
		t.Fatal("expected GetByTokenHash to return revoked sessions for caller-side checks")
	}
}
