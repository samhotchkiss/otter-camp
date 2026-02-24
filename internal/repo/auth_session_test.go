package repo

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestAuthSessionRepoGetByTokenHashReturnsRevokedSessionWithUserLoaded(t *testing.T) {
	now := time.Now().UTC()
	revokedAt := now.Add(-5 * time.Minute)
	userPassword := "$2a$12$abcdefghijklmnopqrstuvabcdefghijklmnopqrstuvabcdefghij"

	sessionID := uuid.New()
	userID := uuid.New()
	orgID := uuid.New()
	tokenHash := strings.Repeat("a", 64)

	repo := newAuthSessionRepoWithQuerier(stubQuerier{
		queryRow: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if !strings.Contains(sql, "JOIN human_user u ON u.id = s.user_id") {
				t.Fatalf("query must eager-load user via join, got: %s", sql)
			}
			return rowFromValues(
				sessionID, userID, tokenHash, now.Add(24*time.Hour), now, now, &revokedAt, strPtr("ua"), strPtr("127.0.0.1"),
				userID, orgID, "person@example.com", "Person", &userPassword, "admin", true, 0, (*time.Time)(nil), (*time.Time)(nil), now, now, []byte(`{"x":1}`),
			)
		},
	})

	// Repo returns revoked sessions; caller must check RevokedAt before authenticating.
	session, err := repo.GetByTokenHash(context.Background(), tokenHash)
	if err != nil {
		t.Fatalf("GetByTokenHash failed: %v", err)
	}
	if session.RevokedAt == nil {
		t.Fatal("RevokedAt = nil, want non-nil revoked marker")
	}
	if session.User == nil {
		t.Fatal("User = nil, want eagerly loaded user")
	}
	if session.User.ID != userID {
		t.Fatalf("User.ID = %s, want %s", session.User.ID, userID)
	}
	if session.User.Email != "person@example.com" {
		t.Fatalf("User.Email = %q, want person@example.com", session.User.Email)
	}
}

func strPtr(s string) *string {
	return &s
}
