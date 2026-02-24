//go:build integration

package secret

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestServiceSetGetListAndDelete(t *testing.T) {
	t.Setenv("OTTERCAMP_MASTER_KEY", base64.StdEncoding.EncodeToString(makeTestKey()))
	t.Setenv("OTTERCAMP_MASTER_KEY_FILE", "")
	t.Setenv("OTTERCAMP_KMS_KEY_ID", "")

	pool := testdb.New(t)
	orgRepo := repo.NewOrgRepo(pool)
	secretRepo := repo.NewSecretRepo(pool)
	service := NewService(secretRepo)

	org, err := orgRepo.Create(context.Background(), repo.Organization{
		Slug:        "secret-service-org",
		DisplayName: "Secret Service Org",
	})
	if err != nil {
		t.Fatalf("create org failed: %v", err)
	}

	err = service.Set(context.Background(), org.ID, "build-token", "Build Token", "for CI", "super-secret-token", Principal{Type: "human", ID: uuid.Nil})
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	var (
		ciphertext []byte
		nonce      []byte
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT ciphertext, nonce
		FROM secret
		WHERE organization_id = $1
		  AND slug = $2
	`, org.ID, "build-token").Scan(&ciphertext, &nonce); err != nil {
		t.Fatalf("query stored secret row failed: %v", err)
	}
	if len(ciphertext) == 0 {
		t.Fatal("stored ciphertext must not be empty")
	}
	if string(ciphertext) == "super-secret-token" {
		t.Fatal("ciphertext must not equal plaintext")
	}
	if len(nonce) != 12 {
		t.Fatalf("nonce length = %d, want 12", len(nonce))
	}

	value, err := service.Get(context.Background(), org.ID, "build-token")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if value != "super-secret-token" {
		t.Fatalf("Get value = %q, want %q", value, "super-secret-token")
	}

	listed, err := service.List(context.Background(), org.ID)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List len = %d, want 1", len(listed))
	}
	if listed[0].KeyVersion != 1 {
		t.Fatalf("KeyVersion = %d, want 1", listed[0].KeyVersion)
	}

	resolved, err := service.ResolveRef(context.Background(), org.ID, "ref:build-token")
	if err != nil {
		t.Fatalf("ResolveRef ref failed: %v", err)
	}
	if resolved != "super-secret-token" {
		t.Fatalf("ResolveRef value = %q, want %q", resolved, "super-secret-token")
	}

	passthrough, err := service.ResolveRef(context.Background(), org.ID, "literal-value")
	if err != nil {
		t.Fatalf("ResolveRef passthrough failed: %v", err)
	}
	if passthrough != "literal-value" {
		t.Fatalf("ResolveRef passthrough = %q, want %q", passthrough, "literal-value")
	}

	if err := service.Delete(context.Background(), org.ID, "build-token", Principal{Type: "human", ID: uuid.Nil}); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if _, err := service.Get(context.Background(), org.ID, "build-token"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("Get after delete error = %v, want ErrNotFound", err)
	}
}

func TestServiceDeleteBlockedByMCPTransportReference(t *testing.T) {
	t.Setenv("OTTERCAMP_MASTER_KEY", base64.StdEncoding.EncodeToString(makeTestKey()))
	t.Setenv("OTTERCAMP_MASTER_KEY_FILE", "")
	t.Setenv("OTTERCAMP_KMS_KEY_ID", "")

	pool := testdb.New(t)
	orgRepo := repo.NewOrgRepo(pool)
	secretRepo := repo.NewSecretRepo(pool)
	service := NewService(secretRepo)

	org, err := orgRepo.Create(context.Background(), repo.Organization{
		Slug:        "delete-safety-org",
		DisplayName: "Delete Safety Org",
	})
	if err != nil {
		t.Fatalf("create org failed: %v", err)
	}

	err = service.Set(context.Background(), org.ID, "mcp-api-key", "MCP API Key", "", "mcp-secret", Principal{Type: "human", ID: uuid.Nil})
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	if _, err := pool.Exec(context.Background(), `
		CREATE TABLE mcp_connection (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			organization_id uuid NOT NULL,
			display_name text NOT NULL,
			slug text NOT NULL,
			transport_config jsonb NOT NULL DEFAULT '{}'
		)
	`); err != nil {
		t.Fatalf("create mcp_connection table failed: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO mcp_connection (organization_id, display_name, slug, transport_config)
		VALUES ($1, 'my-server', 'my-server', '{"api_key":"ref:mcp-api-key"}'::jsonb)
	`, org.ID); err != nil {
		t.Fatalf("insert mcp_connection row failed: %v", err)
	}

	blockers, err := service.CheckDeleteSafety(context.Background(), org.ID, "mcp-api-key")
	if err != nil {
		t.Fatalf("CheckDeleteSafety failed: %v", err)
	}
	if len(blockers) != 1 {
		t.Fatalf("len(blockers) = %d, want 1", len(blockers))
	}
	if !strings.Contains(blockers[0], "mcp_connection") {
		t.Fatalf("blocker = %q, expected mcp_connection description", blockers[0])
	}

	err = service.Delete(context.Background(), org.ID, "mcp-api-key", Principal{Type: "human", ID: uuid.Nil})
	if !errors.Is(err, ErrSecretInUse) {
		t.Fatalf("Delete error = %v, want ErrSecretInUse", err)
	}
}

func makeTestKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}
