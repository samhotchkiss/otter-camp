//go:build integration

package push

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

func TestPreferenceRepositoryRoundTrip(t *testing.T) {
	pool := testdb.New(t)
	user := seedPushUser(t, pool)
	repository := NewPreferenceRepository(pool)
	ctx := context.Background()

	start := "22:00"
	end := "06:00"
	tz := "UTC"
	first := PushPreferences{
		TierEnabled:        map[string]bool{TierUrgent: true, TierHigh: false, TierNormal: true, TierLow: false},
		ProjectOverrides:   []ProjectPushOverride{{ProjectID: uuid.New(), Enabled: true, Tiers: map[string]bool{TierHigh: true}}},
		QuietHoursEnabled:  true,
		QuietHoursStart:    &start,
		QuietHoursEnd:      &end,
		QuietHoursTimezone: &tz,
		EventTypeOverrides: map[string]bool{"task.completed": false},
	}
	if err := repository.SavePreferences(ctx, user.ID, first); err != nil {
		t.Fatalf("SavePreferences first: %v", err)
	}

	loaded, err := repository.GetPreferences(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetPreferences after first save: %v", err)
	}
	if loaded.TierEnabled[TierHigh] != false {
		t.Fatalf("tier high = %v, want false", loaded.TierEnabled[TierHigh])
	}

	second := PushPreferences{
		TierEnabled:       map[string]bool{TierUrgent: true, TierHigh: true, TierNormal: false, TierLow: false},
		ProjectOverrides:  []ProjectPushOverride{},
		QuietHoursEnabled: false,
		EventTypeOverrides: map[string]bool{
			"task.completed": true,
		},
	}
	if err := repository.SavePreferences(ctx, user.ID, second); err != nil {
		t.Fatalf("SavePreferences second: %v", err)
	}

	loaded, err = repository.GetPreferences(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetPreferences after second save: %v", err)
	}
	if loaded.TierEnabled[TierNormal] != false {
		t.Fatalf("tier normal = %v, want false", loaded.TierEnabled[TierNormal])
	}
	if loaded.QuietHoursEnabled {
		t.Fatal("expected quiet hours disabled after overwrite")
	}

	var count int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM human_user
		WHERE id = $1
		  AND settings ? 'push_preferences'
	`, user.ID).Scan(&count); err != nil {
		t.Fatalf("count push_preferences key: %v", err)
	}
	if count != 1 {
		t.Fatalf("push_preferences key rows = %d, want 1", count)
	}
}

func TestRegisterTokenUpsertsByDeviceID(t *testing.T) {
	pool := testdb.New(t)
	user := seedPushUser(t, pool)
	repository := NewPreferenceRepository(pool)
	ctx := context.Background()

	if err := repository.RegisterToken(ctx, user.ID, PushToken{Token: "token-a", Platform: "apns", DeviceID: "device-a"}); err != nil {
		t.Fatalf("register token-a: %v", err)
	}
	if err := repository.RegisterToken(ctx, user.ID, PushToken{Token: "token-a2", Platform: "apns", DeviceID: "device-a"}); err != nil {
		t.Fatalf("register token-a update: %v", err)
	}
	if err := repository.RegisterToken(ctx, user.ID, PushToken{Token: "token-b", Platform: "fcm", DeviceID: "device-b"}); err != nil {
		t.Fatalf("register token-b: %v", err)
	}

	tokens, err := repository.GetTokens(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetTokens: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("tokens len = %d, want 2", len(tokens))
	}

	seen := map[string]string{}
	for _, token := range tokens {
		seen[token.DeviceID] = token.Token
	}
	if got := seen["device-a"]; got != "token-a2" {
		t.Fatalf("device-a token = %q, want %q", got, "token-a2")
	}
	if got := seen["device-b"]; got != "token-b" {
		t.Fatalf("device-b token = %q, want %q", got, "token-b")
	}
}

func seedPushUser(t *testing.T, pool *pgxpool.Pool) repo.HumanUser {
	t.Helper()
	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "push-org-" + uuid.NewString()[:8],
		DisplayName: "Push Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte("password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	password := string(hashed)

	user, err := repo.NewHumanUserRepo(pool).Create(ctx, repo.HumanUser{
		OrganizationID: org.ID,
		Email:          "push-" + uuid.NewString()[:8] + "@example.com",
		DisplayName:    "Push User",
		PasswordHash:   &password,
		Role:           "member",
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}
