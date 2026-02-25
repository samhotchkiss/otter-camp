//go:build integration

package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	occrypto "github.com/samhotchkiss/otter-camp/internal/crypto"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	secretsvc "github.com/samhotchkiss/otter-camp/internal/secret"
	"golang.org/x/crypto/bcrypt"
)

func makeSecurityOrgID(t testing.TB, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	org, err := repo.NewOrgRepo(pool).Create(context.Background(), repo.Organization{
		Slug:        "security-org-" + uuid.NewString()[:8],
		DisplayName: "Security Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return org.ID
}

func makeSecurityUser(t testing.TB, pool *pgxpool.Pool, orgID uuid.UUID, role string) repo.HumanUser {
	t.Helper()
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("password-123"), 12)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user, err := repo.NewHumanUserRepo(pool).Create(context.Background(), repo.HumanUser{
		OrganizationID: orgID,
		Email:          strings.ToLower(uuid.NewString()) + "@example.com",
		DisplayName:    "Security User",
		PasswordHash:   strPtr(string(passwordHash)),
		Role:           strings.TrimSpace(role),
		IsActive:       true,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

func makeSecuritySecret(t testing.TB, pool *pgxpool.Pool, orgID uuid.UUID, slug, value string) string {
	t.Helper()
	key, err := occrypto.NewMasterKey(1, fixedSecurityKey())
	if err != nil {
		t.Fatalf("new master key: %v", err)
	}
	service := secretsvc.NewServiceWithKeyLoader(repo.NewSecretRepo(pool), func(version int) (occrypto.MasterKey, error) {
		return key, nil
	})

	if strings.TrimSpace(slug) == "" {
		slug = "secret-" + uuid.NewString()[:8]
	}
	if err := service.Set(context.Background(), orgID, slug, slug, "", value, secretsvc.Principal{Type: "system", ID: uuid.Nil}); err != nil {
		t.Fatalf("set secret: %v", err)
	}
	return slug
}

func hashSecurityContent(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func fixedSecurityKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}

func strPtr(v string) *string {
	return &v
}
