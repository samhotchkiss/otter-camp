//go:build integration

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestDBMigrateDryRunAndStatus(t *testing.T) {
	pool := testdb.New(t)
	t.Setenv("OTTERCAMP_DATABASE_URL", pool.Config().ConnString())

	migrationsDir := t.TempDir()
	migrationPath := filepath.Join(migrationsDir, "9999_cli_dry_run_probe.sql")
	if err := os.WriteFile(migrationPath, []byte("SELECT 1;"), 0o600); err != nil {
		t.Fatalf("WriteFile migration: %v", err)
	}
	t.Setenv("OTTERCAMP_MIGRATIONS_PATH", migrationsDir)

	code, stdout, stderr := captureCommandOutput(t, func() int {
		return runDBMigrate([]string{"--dry-run"})
	})
	if code != 0 {
		t.Fatalf("db migrate --dry-run exit=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "pending 9999_cli_dry_run_probe") {
		t.Fatalf("db migrate --dry-run output = %q", stdout)
	}

	var count int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM schema_migrations WHERE version = 9999`).Scan(&count); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected dry-run not to apply migration, count=%d", count)
	}

	applyCode, applyOut, applyErr := captureCommandOutput(t, func() int {
		return runDBMigrate([]string{})
	})
	if applyCode != 0 {
		t.Fatalf("db migrate apply exit=%d stderr=%q", applyCode, applyErr)
	}
	if !strings.Contains(applyOut, "Applying 9999_cli_dry_run_probe... done (") {
		t.Fatalf("db migrate apply output = %q", applyOut)
	}

	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM schema_migrations WHERE version = 9999`).Scan(&count); err != nil {
		t.Fatalf("query schema_migrations after apply: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected apply to record migration, count=%d", count)
	}

	statusCode, statusOut, statusErr := captureCommandOutput(t, func() int {
		return runDBStatus([]string{"--output", "json"})
	})
	if statusCode != 0 {
		t.Fatalf("db status exit=%d stderr=%q", statusCode, statusErr)
	}
	if !strings.Contains(statusOut, `"version": 9999`) {
		t.Fatalf("db status output missing target migration: %q", statusOut)
	}
	if !strings.Contains(statusOut, `"applied": true`) {
		t.Fatalf("db status output missing applied flag: %q", statusOut)
	}
}

func TestDBTokenUsageJSONIncludesCacheReadsAndAttribution(t *testing.T) {
	pool := testdb.New(t)
	t.Setenv("OTTERCAMP_DATABASE_URL", pool.Config().ConnString())

	ctx := context.Background()
	orgRepo := repo.NewOrgRepo(pool)
	providerRepo := repo.NewModelProviderRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "cli-token-usage-org",
		DisplayName: "CLI Token Usage Org",
	})
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}
	provider, err := providerRepo.Create(ctx, repo.ModelProvider{
		Slug:        "cli-token-usage-provider",
		DisplayName: "Anthropic CLI Test",
		APIBaseURL:  "https://provider.example",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	sessionID := uuid.New()
	turnID := uuid.New()
	createdAt := time.Now().UTC()

	if _, err := pool.Exec(ctx, `
		INSERT INTO model_invocation (
			organization_id, model_provider_id, invocation_purpose, status, model_name,
			input_tokens, output_tokens, cache_read_tokens, session_id, turn_id, created_at
		) VALUES ($1, $2, 'agent_turn', 'completed', 'claude-opus-4-6', $3, $4, $5, $6, $7, $8)
	`, org.ID, provider.ID, 100, 25, 50, sessionID, turnID, createdAt); err != nil {
		t.Fatalf("insert completed invocation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO model_invocation (
			organization_id, model_provider_id, invocation_purpose, status, model_name,
			input_tokens, output_tokens, cache_read_tokens, session_id, turn_id, created_at, error_code, error_message
		) VALUES ($1, $2, 'summarization', 'failed', 'claude-haiku-4-5-20251001', $3, $4, $5, $6, $7, $8, 'rate_limit', '429 rate limit')
	`, org.ID, provider.ID, 10, 5, 15, sessionID, turnID, createdAt.Add(time.Minute)); err != nil {
		t.Fatalf("insert failed invocation: %v", err)
	}

	code, stdout, stderr := captureCommandOutput(t, func() int {
		return runDBTokenUsage([]string{"--output", "json", "--hours", "24", "--limit", "5", "--org", org.ID.String()})
	})
	if code != 0 {
		t.Fatalf("db token-usage exit=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, `"total_tokens": 205`) {
		t.Fatalf("db token-usage output missing total tokens: %q", stdout)
	}
	if !strings.Contains(stdout, `"cache_read_tokens": 65`) {
		t.Fatalf("db token-usage output missing cache read tokens: %q", stdout)
	}
	if !strings.Contains(stdout, `"rate_limited_failures": 1`) {
		t.Fatalf("db token-usage output missing rate-limited failures: %q", stdout)
	}
	if !strings.Contains(stdout, sessionID.String()) {
		t.Fatalf("db token-usage output missing session id: %q", stdout)
	}
	if !strings.Contains(stdout, turnID.String()) {
		t.Fatalf("db token-usage output missing turn id: %q", stdout)
	}
}
