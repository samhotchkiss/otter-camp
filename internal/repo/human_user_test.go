package repo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestHumanUserRepoGetByEmailReturnsErrNotFound(t *testing.T) {
	repo := newHumanUserRepoWithQuerier(stubQuerier{
		queryRow: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return errRow(pgx.ErrNoRows)
		},
	})

	_, err := repo.GetByEmail(context.Background(), uuid.New(), "missing@example.com")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByEmail error = %v, want ErrNotFound", err)
	}
}

func TestHumanUserRepoUpdatePasswordHashBehavior(t *testing.T) {
	const (
		oldHash = "$2a$12$oldoldoldoldoldoldoldoldoldoldoldoldoldoldoldoldoldold"
		newHash = "$2a$12$newnewnewnewnewnewnewnewnewnewnewnewnewnewnewnewnewnew"
	)

	baseNow := time.Now().UTC()
	userID := uuid.New()
	orgID := uuid.New()
	settings := []byte(`{"theme":"dark"}`)

	t.Run("preserves existing password hash when omitted", func(t *testing.T) {
		repo := newHumanUserRepoWithQuerier(stubQuerier{
			queryRow: func(ctx context.Context, sql string, args ...any) pgx.Row {
				if !strings.Contains(sql, "password_hash = COALESCE($4, password_hash)") {
					t.Fatalf("query missing COALESCE password preservation clause: %s", sql)
				}
				if got, ok := args[3].(*string); ok {
					if got != nil {
						t.Fatalf("password arg = %v, want nil pointer to preserve existing hash", got)
					}
				} else if args[3] != nil {
					t.Fatalf("password arg = %v, want nil to preserve existing hash", args[3])
				}

				old := oldHash
				return rowFromValues(
					userID, orgID, "user@example.com", "User", &old, "admin", true, 0,
					(*time.Time)(nil), (*time.Time)(nil), baseNow, baseNow, settings,
				)
			},
		})

		updated, err := repo.Update(context.Background(), HumanUserUpdate{
			ID:          userID,
			Email:       "user@example.com",
			DisplayName: "User",
			Role:        "admin",
			Settings:    settings,
		})
		if err != nil {
			t.Fatalf("Update failed: %v", err)
		}
		if updated.PasswordHash == nil || *updated.PasswordHash != oldHash {
			t.Fatalf("PasswordHash = %v, want existing hash", updated.PasswordHash)
		}
	})

	t.Run("updates password hash when explicitly provided", func(t *testing.T) {
		repo := newHumanUserRepoWithQuerier(stubQuerier{
			queryRow: func(ctx context.Context, sql string, args ...any) pgx.Row {
				got, ok := args[3].(*string)
				if !ok || got == nil || *got != newHash {
					t.Fatalf("password arg = %#v, want *string(%q)", args[3], newHash)
				}

				updated := newHash
				return rowFromValues(
					userID, orgID, "user@example.com", "User", &updated, "admin", true, 0,
					(*time.Time)(nil), (*time.Time)(nil), baseNow, baseNow, settings,
				)
			},
		})

		nextHash := newHash
		updated, err := repo.Update(context.Background(), HumanUserUpdate{
			ID:           userID,
			Email:        "user@example.com",
			DisplayName:  "User",
			PasswordHash: &nextHash,
			Role:         "admin",
			Settings:     settings,
		})
		if err != nil {
			t.Fatalf("Update failed: %v", err)
		}
		if updated.PasswordHash == nil || *updated.PasswordHash != newHash {
			t.Fatalf("PasswordHash = %v, want %q", updated.PasswordHash, newHash)
		}
	})
}

func TestHumanUserRepoIncrFailedAttemptsUsesAtomicIncrement(t *testing.T) {
	now := time.Now().UTC()
	repo := newHumanUserRepoWithQuerier(stubQuerier{
		queryRow: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if !strings.Contains(sql, "failed_login_attempts = failed_login_attempts + 1") {
				t.Fatalf("query missing atomic increment expression: %s", sql)
			}

			userID := uuid.New()
			orgID := uuid.New()
			return rowFromValues(
				userID, orgID, "user@example.com", "User", (*string)(nil), "member", true, 3,
				(*time.Time)(nil), (*time.Time)(nil), now, now, []byte(`{}`),
			)
		},
	})

	got, err := repo.IncrFailedAttempts(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("IncrFailedAttempts failed: %v", err)
	}
	if got.FailedLoginAttempts != 3 {
		t.Fatalf("FailedLoginAttempts = %d, want 3", got.FailedLoginAttempts)
	}
}
