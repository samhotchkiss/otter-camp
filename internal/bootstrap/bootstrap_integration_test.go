//go:build integration

package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestBootstrapFreshAndIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := Config{
		OrgSlug:       "bootstrap-test-org",
		OrgName:       "Bootstrap Test Org",
		AdminEmail:    "admin@example.com",
		AdminPassword: "admin-password",
		SkillsDir:     t.TempDir(),
	}
	bootstrapper := New(Options{
		Pool:    pool,
		Logger:  logger,
		Version: "test-version",
		Config:  cfg,
	})

	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}

	orgID := mustOrgIDBySlug(t, pool, cfg.OrgSlug)

	assertCount(t, pool, `SELECT COUNT(*) FROM organization WHERE slug = $1`, 1, cfg.OrgSlug)
	assertCount(t, pool, `SELECT COUNT(*) FROM human_user WHERE organization_id = $1 AND role = 'admin'`, 1, orgID)
	assertCount(t, pool, `SELECT COUNT(*) FROM skill WHERE organization_id = $1 AND created_by_type = 'system'`, 3, orgID)
	assertCount(t, pool, `SELECT COUNT(*) FROM model_provider WHERE slug IN ('anthropic','openai')`, 2)
	assertCount(t, pool, `SELECT COUNT(*) FROM model_profile WHERE organization_id = $1 AND is_current = true`, 3, orgID)
	assertCount(t, pool, `SELECT COUNT(*) FROM model_profile_assignment WHERE organization_id = $1 AND scope_type = 'organization' AND scope_id = $1`, 6, orgID)
	assertCount(t, pool, `SELECT COUNT(*) FROM audit_event WHERE organization_id = $1 AND event_type = 'system.bootstrap'`, 1, orgID)

	for _, filename := range []string{"summarize.md", "code-review.md", "plan-task.md"} {
		path := filepath.Join(cfg.SkillsDir, filename)
		if _, err := os.ReadFile(path); err != nil {
			t.Fatalf("read seeded skill %s: %v", filename, err)
		}
	}

	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}

	assertCount(t, pool, `SELECT COUNT(*) FROM organization WHERE slug = $1`, 1, cfg.OrgSlug)
	assertCount(t, pool, `SELECT COUNT(*) FROM human_user WHERE organization_id = $1 AND role = 'admin'`, 1, orgID)
	assertCount(t, pool, `SELECT COUNT(*) FROM skill WHERE organization_id = $1`, 3, orgID)
	assertCount(t, pool, `SELECT COUNT(*) FROM model_profile WHERE organization_id = $1 AND is_current = true`, 3, orgID)
	assertCount(t, pool, `SELECT COUNT(*) FROM model_profile_assignment WHERE organization_id = $1 AND scope_type = 'organization' AND scope_id = $1`, 6, orgID)
	assertCount(t, pool, `SELECT COUNT(*) FROM audit_event WHERE organization_id = $1 AND event_type = 'system.bootstrap'`, 1, orgID)
}

func TestBootstrapSkipsAdminCreationWhenCredentialsMissing(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := Config{
		OrgSlug:   "bootstrap-no-admin",
		OrgName:   "Bootstrap No Admin",
		SkillsDir: t.TempDir(),
	}
	bootstrapper := New(Options{
		Pool:    pool,
		Logger:  logger,
		Version: "test-version",
		Config:  cfg,
	})

	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	orgID := mustOrgIDBySlug(t, pool, cfg.OrgSlug)
	assertCount(t, pool, `SELECT COUNT(*) FROM human_user WHERE organization_id = $1`, 0, orgID)
}

func mustOrgIDBySlug(t *testing.T, pool *pgxpool.Pool, slug string) uuid.UUID {
	t.Helper()
	var orgID uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT id FROM organization WHERE slug = $1`, slug).Scan(&orgID); err != nil {
		t.Fatalf("lookup org id for slug %s: %v", slug, err)
	}
	return orgID
}

func assertCount(t *testing.T, pool *pgxpool.Pool, query string, want int, args ...any) {
	t.Helper()
	var got int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&got); err != nil {
		t.Fatalf("count query failed: %v\nquery=%s", err, query)
	}
	if got != want {
		t.Fatalf("count mismatch: got=%d want=%d query=%s", got, want, query)
	}
}
