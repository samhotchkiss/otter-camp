//go:build integration

package bootstrap

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestBootstrapRunSeedsAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	skillsDir := filepath.Join(t.TempDir(), "skills")

	bootstrapper := NewBootstrapper(Options{
		Pool:          pool,
		OrgSlug:       "bootstrap-seed",
		OrgName:       "Bootstrap Seed Org",
		AdminEmail:    "admin@example.com",
		AdminPassword: "bootstrap-password",
		SkillsDir:     skillsDir,
		AppVersion:    "test-version",
	})

	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("first bootstrap run: %v", err)
	}
	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("second bootstrap run: %v", err)
	}

	var orgID uuid.UUID
	var displayName string
	if err := pool.QueryRow(ctx, `
		SELECT id, display_name
		FROM organization
		WHERE slug = $1
	`, "bootstrap-seed").Scan(&orgID, &displayName); err != nil {
		t.Fatalf("query organization: %v", err)
	}
	if displayName != "Bootstrap Seed Org" {
		t.Fatalf("organization display_name = %q, want %q", displayName, "Bootstrap Seed Org")
	}

	assertCount(t, pool, `SELECT COUNT(*) FROM organization WHERE slug = 'bootstrap-seed'`, 1)
	assertCount(t, pool, `SELECT COUNT(*) FROM human_user WHERE organization_id = $1 AND role = 'admin'`, 1, orgID)

	assertCount(t, pool, `
		SELECT COUNT(*)
		FROM skill
		WHERE organization_id = $1
		  AND created_by_type = 'system'
		  AND slug IN ('summarize', 'code-review', 'plan-task')
	`, 3, orgID)

	for _, fileName := range []string{"summarize.md", "code-review.md", "plan-task.md"} {
		if _, err := os.Stat(filepath.Join(skillsDir, fileName)); err != nil {
			t.Fatalf("expected skill file %q: %v", fileName, err)
		}
	}

	assertCount(t, pool, `
		SELECT COUNT(*)
		FROM model_profile
		WHERE organization_id = $1
		  AND is_current = true
		  AND logical_profile_id IN ('high-capability', 'standard', 'haiku')
	`, 3, orgID)

	assertCount(t, pool, `
		SELECT COUNT(*)
		FROM model_profile_assignment
		WHERE organization_id = $1
		  AND scope_type = 'organization'
		  AND scope_id = $1
		  AND invocation_purpose IN (
			'agent_turn',
			'listening_eval',
			'summarization',
			'skill_summarization',
			'memory_extraction',
			'memory_distillation',
			'memory_entity_synthesis',
			'replay'
		  )
	`, 8, orgID)

	assertCount(t, pool, `
		SELECT COUNT(*)
		FROM audit_event
		WHERE organization_id = $1
		  AND event_type = 'system.bootstrap'
	`, 1, orgID)

	var metadataRaw []byte
	if err := pool.QueryRow(ctx, `
		SELECT metadata
		FROM audit_event
		WHERE organization_id = $1
		  AND event_type = 'system.bootstrap'
		LIMIT 1
	`, orgID).Scan(&metadataRaw); err != nil {
		t.Fatalf("query bootstrap metadata: %v", err)
	}

	var metadata map[string]any
	if err := json.Unmarshal(metadataRaw, &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if got := metadata["version"]; got != "test-version" {
		t.Fatalf("metadata.version = %v, want %q", got, "test-version")
	}
}

func TestBootstrapRunSkipsAdminWhenCredentialsMissing(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	bootstrapper := NewBootstrapper(Options{
		Pool:       pool,
		OrgSlug:    "bootstrap-no-admin",
		OrgName:    "Bootstrap No Admin",
		SkillsDir:  filepath.Join(t.TempDir(), "skills"),
		AppVersion: "test-version",
	})

	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("bootstrap run: %v", err)
	}

	var orgID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM organization WHERE slug = 'bootstrap-no-admin'`).Scan(&orgID); err != nil {
		t.Fatalf("query org id: %v", err)
	}

	assertCount(t, pool, `SELECT COUNT(*) FROM human_user WHERE organization_id = $1`, 0, orgID)
}

func assertCount(t *testing.T, pool *pgxpool.Pool, sql string, want int64, args ...any) {
	t.Helper()
	var got int64
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&got); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if got != want {
		t.Fatalf("count = %d, want %d (sql=%q)", got, want, sql)
	}
}
