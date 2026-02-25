package testutil

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
)

func MakeSecret(t testing.TB, db *pgxpool.Pool, orgID uuid.UUID, slug, value string) string {
	t.Helper()

	key, err := occrypto.NewMasterKey(1, fixedTestKeyMaterial())
	if err != nil {
		t.Fatalf("build test master key: %v", err)
	}
	service := secretsvc.NewServiceWithKeyLoader(repo.NewSecretRepo(db), func(version int) (occrypto.MasterKey, error) {
		return key, nil
	})

	normalizedSlug := strings.TrimSpace(slug)
	if normalizedSlug == "" {
		normalizedSlug = "secret-" + uuid.NewString()[:8]
	}
	if err := service.Set(context.Background(), orgID, normalizedSlug, normalizedSlug, "", value, secretsvc.Principal{Type: "system", ID: uuid.Nil}); err != nil {
		t.Fatalf("set secret %q: %v", normalizedSlug, err)
	}
	return normalizedSlug
}

func MakeAuditEvent(t testing.TB, db *pgxpool.Pool, orgID uuid.UUID, action, principalType string, principalID uuid.UUID) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	err := db.QueryRow(context.Background(), `
		INSERT INTO audit_event (
			organization_id,
			event_type,
			principal_type,
			principal_id,
			metadata
		)
		VALUES ($1, $2, $3, $4, '{}'::jsonb)
		RETURNING id
	`, orgID, strings.TrimSpace(action), strings.TrimSpace(principalType), principalID).Scan(&id)
	if err != nil {
		t.Fatalf("insert audit_event %q: %v", action, err)
	}
	return id
}

func HashContent(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func fixedTestKeyMaterial() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}
