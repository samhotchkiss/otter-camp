//go:build integration

package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/audit"
	"github.com/samhotchkiss/otter-camp/internal/config"
	"github.com/samhotchkiss/otter-camp/internal/migrate"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/server"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestBootstrapRunFreshAndIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	t.Setenv("OTTERCAMP_ORG_SLUG", "bootstrap-org")
	t.Setenv("OTTERCAMP_ORG_NAME", "Bootstrap Org")
	t.Setenv("OTTERCAMP_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("OTTERCAMP_ADMIN_PASSWORD", "admin-password")

	bootstrapper := newIntegrationBootstrapper(pool, filepath.Join(t.TempDir(), "skills"), "test-version")
	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("first bootstrap run failed: %v", err)
	}

	orgID := mustOrganizationIDBySlug(t, pool, "bootstrap-org")
	assertCount(t, pool, `SELECT COUNT(*) FROM organization WHERE slug = 'bootstrap-org'`, 1)
	assertCount(t, pool, `SELECT COUNT(*) FROM human_user WHERE organization_id = $1 AND role = 'admin'`, 1, orgID)
	assertCount(t, pool, `SELECT COUNT(*) FROM skill WHERE organization_id = $1`, 3, orgID)
	assertCount(t, pool, `SELECT COUNT(*) FROM model_provider WHERE slug IN ('anthropic', 'openai')`, 2)
	assertCount(t, pool, `SELECT COUNT(*) FROM model_profile WHERE organization_id = $1 AND is_current = true`, 3, orgID)
	assertCount(t, pool, `SELECT COUNT(*) FROM model_profile_assignment WHERE organization_id = $1`, 8, orgID)
	assertCount(t, pool, `SELECT COUNT(*) FROM audit_event WHERE organization_id = $1 AND event_type = 'system.bootstrap'`, 1, orgID)

	assertAssignment(t, pool, orgID, "agent_turn", "high-capability")
	assertAssignment(t, pool, orgID, "listening_eval", "haiku")
	assertAssignment(t, pool, orgID, "summarization", "standard")
	assertAssignment(t, pool, orgID, "memory_extraction", "haiku")

	assertFileExists(t, filepath.Join(bootstrapper.skillsDir, "summarize.md"))
	assertFileExists(t, filepath.Join(bootstrapper.skillsDir, "code-review.md"))
	assertFileExists(t, filepath.Join(bootstrapper.skillsDir, "plan-task.md"))

	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("second bootstrap run failed: %v", err)
	}

	assertCount(t, pool, `SELECT COUNT(*) FROM organization WHERE slug = 'bootstrap-org'`, 1)
	assertCount(t, pool, `SELECT COUNT(*) FROM human_user WHERE organization_id = $1 AND role = 'admin'`, 1, orgID)
	assertCount(t, pool, `SELECT COUNT(*) FROM skill WHERE organization_id = $1`, 3, orgID)
	assertCount(t, pool, `SELECT COUNT(*) FROM model_profile WHERE organization_id = $1 AND is_current = true`, 3, orgID)
	assertCount(t, pool, `SELECT COUNT(*) FROM model_profile_assignment WHERE organization_id = $1`, 8, orgID)
	assertCount(t, pool, `SELECT COUNT(*) FROM audit_event WHERE organization_id = $1 AND event_type = 'system.bootstrap'`, 1, orgID)
}

func TestBootstrapRunWithoutAdminCredentialsSkipsUserCreation(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	t.Setenv("OTTERCAMP_ORG_SLUG", "bootstrap-no-admin")
	t.Setenv("OTTERCAMP_ORG_NAME", "Bootstrap No Admin")
	t.Setenv("OTTERCAMP_ADMIN_EMAIL", "")
	t.Setenv("OTTERCAMP_ADMIN_PASSWORD", "")

	bootstrapper := newIntegrationBootstrapper(pool, filepath.Join(t.TempDir(), "skills"), "test-version")
	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("bootstrap run failed: %v", err)
	}

	orgID := mustOrganizationIDBySlug(t, pool, "bootstrap-no-admin")
	assertCount(t, pool, `SELECT COUNT(*) FROM human_user WHERE organization_id = $1`, 0, orgID)
	assertCount(t, pool, `SELECT COUNT(*) FROM audit_event WHERE organization_id = $1 AND event_type = 'system.bootstrap'`, 1, orgID)
}

func TestResetEndpointTruncatesAndRebootstraps(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	t.Setenv("OTTERCAMP_ORG_SLUG", "bootstrap-reset")
	t.Setenv("OTTERCAMP_ORG_NAME", "Bootstrap Reset")
	t.Setenv("OTTERCAMP_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("OTTERCAMP_ADMIN_PASSWORD", "admin-password")

	bootstrapper := newIntegrationBootstrapper(pool, filepath.Join(t.TempDir(), "skills"), "test-version")
	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("bootstrap run failed: %v", err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO organization (slug, display_name, settings) VALUES ('temporary-org', 'Temporary Org', '{}'::jsonb)`); err != nil {
		t.Fatalf("insert temporary org: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	resetter := NewResetter(pool, bootstrapper, logger)
	handler := server.NewHandlerWithOptions(server.HandlerOptions{
		Mode:         config.ModeTest,
		Logger:       logger,
		Pool:         pool,
		TestResetter: resetter,
	})

	ts := httptest.NewServer(handler)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/test/reset", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /test/reset failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	assertCount(t, pool, `SELECT COUNT(*) FROM organization WHERE slug = 'temporary-org'`, 0)
	orgID := mustOrganizationIDBySlug(t, pool, "bootstrap-reset")
	assertCount(t, pool, `SELECT COUNT(*) FROM organization`, 1)
	assertCount(t, pool, `SELECT COUNT(*) FROM human_user WHERE organization_id = $1`, 1, orgID)
	assertCount(t, pool, `SELECT COUNT(*) FROM skill WHERE organization_id = $1`, 3, orgID)
	assertCount(t, pool, `SELECT COUNT(*) FROM audit_event WHERE organization_id = $1 AND event_type = 'system.bootstrap'`, 1, orgID)
}

func newIntegrationBootstrapper(pool *pgxpool.Pool, skillsDir string, version string) *Bootstrapper {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditRepo := repo.NewAuditEventRepo(pool)
	auditService := audit.NewService(auditRepo, logger)

	return New(Options{
		Logger:                     logger,
		Pool:                       pool,
		Migrator:                   migrate.NewRunner(pool, logger),
		OrgRepo:                    repo.NewOrgRepo(pool),
		UserRepo:                   repo.NewHumanUserRepo(pool),
		SkillRepo:                  repo.NewSkillRepo(pool),
		ModelProviderRepo:          repo.NewModelProviderRepo(pool),
		ModelProfileRepo:           repo.NewModelProfileRepo(pool),
		ModelProfileAssignmentRepo: repo.NewModelProfileAssignmentRepo(pool),
		AuditRecorder:              auditService,
		SkillsDir:                  skillsDir,
		AppVersion:                 version,
	})
}

func mustOrganizationIDBySlug(t *testing.T, pool *pgxpool.Pool, slug string) uuid.UUID {
	t.Helper()
	var orgID uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT id FROM organization WHERE slug = $1`, slug).Scan(&orgID); err != nil {
		t.Fatalf("lookup org id by slug %q: %v", slug, err)
	}
	return orgID
}

func assertCount(t *testing.T, pool *pgxpool.Pool, query string, want int, args ...any) {
	t.Helper()
	var got int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&got); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if got != want {
		t.Fatalf("count = %d, want %d query=%s", got, want, query)
	}
}

func assertAssignment(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, purpose, profile string) {
	t.Helper()
	var got string
	if err := pool.QueryRow(context.Background(), `
		SELECT logical_profile_id
		FROM model_profile_assignment
		WHERE organization_id = $1
		  AND scope_type = 'organization'
		  AND scope_id = $1
		  AND invocation_purpose = $2
	`, orgID, purpose).Scan(&got); err != nil {
		t.Fatalf("assignment lookup failed for %q: %v", purpose, err)
	}
	if got != profile {
		t.Fatalf("assignment for %q = %q, want %q", purpose, got, profile)
	}
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file %q to exist: %v", path, err)
	}
}
