//go:build integration

package bootstrap

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samhotchkiss/otter-camp/internal/audit"
	"github.com/samhotchkiss/otter-camp/internal/flowpolicy"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/server"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestBootstrapRunSeedsAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	bootstrapper := NewBootstrapper(Options{
		Pool:          pool,
		Logger:        logger,
		SkillsDir:     filepath.Join(t.TempDir(), "skills"),
		OrgSlug:       "default",
		OrgName:       "OtterCamp",
		AdminEmail:    "admin@example.com",
		AdminPassword: "admin-password",
		Version:       "test-version",
	})

	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("first bootstrap run: %v", err)
	}
	before := countSnapshot(t, ctx, pool)

	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("second bootstrap run: %v", err)
	}
	after := countSnapshot(t, ctx, pool)

	if before != after {
		t.Fatalf("bootstrap counts changed after rerun: before=%+v after=%+v", before, after)
	}

	var (
		principalType string
		principalID   uuid.UUID
		metadataJSON  []byte
	)
	if err := pool.QueryRow(ctx, `
		SELECT principal_type, principal_id, metadata
		FROM audit_event
		WHERE event_type = 'system.bootstrap'
		  AND organization_id = $1
	`, before.organizationID).Scan(&principalType, &principalID, &metadataJSON); err != nil {
		t.Fatalf("load bootstrap audit event: %v", err)
	}
	if principalType != "system" {
		t.Fatalf("bootstrap audit principal_type = %q, want %q", principalType, "system")
	}
	if principalID != audit.SystemPrincipalID {
		t.Fatalf("bootstrap audit principal_id = %s, want %s", principalID, audit.SystemPrincipalID)
	}

	var metadata map[string]any
	if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
		t.Fatalf("decode bootstrap metadata: %v", err)
	}
	if gotVersion, ok := metadata["version"].(string); !ok || gotVersion != "test-version" {
		t.Fatalf("bootstrap metadata version = %#v, want %q", metadata["version"], "test-version")
	}

	orgRepo := repo.NewOrgRepo(pool)
	org, err := orgRepo.GetBySlug(ctx, "default")
	if err != nil {
		t.Fatalf("GetBySlug default: %v", err)
	}
	if org.DisplayName != "OtterCamp" {
		t.Fatalf("org display_name = %q, want %q", org.DisplayName, "OtterCamp")
	}

	assignmentRepo := repo.NewModelProfileAssignmentRepo(pool)
	for purpose, logicalID := range map[string]string{
		"agent_turn":              "high-capability",
		"listening_eval":          "haiku",
		"summarization":           "standard",
		"memory_extraction":       "haiku",
		"memory_distillation":     "haiku",
		"memory_entity_synthesis": "haiku",
	} {
		got, getErr := assignmentRepo.GetByScope(ctx, org.ID, "organization", org.ID, purpose)
		if getErr != nil {
			t.Fatalf("assignment %s: %v", purpose, getErr)
		}
		if got.LogicalProfileID != logicalID {
			t.Fatalf("assignment %s logical_profile_id = %q, want %q", purpose, got.LogicalProfileID, logicalID)
		}
	}

	profileRepo := repo.NewModelProfileRepo(pool)
	for logicalID, expectedModel := range map[string]string{
		"high-capability": "claude-opus-4-6",
		"standard":        "claude-sonnet-4-20250514",
		"haiku":           "claude-haiku-4-5-20251001",
	} {
		profile, getErr := profileRepo.GetCurrentByLogicalID(ctx, org.ID, logicalID)
		if getErr != nil {
			t.Fatalf("profile %s: %v", logicalID, getErr)
		}
		if profile.ModelName != expectedModel {
			t.Fatalf("profile %s model_name = %q, want %q", logicalID, profile.ModelName, expectedModel)
		}
	}
}

func TestBootstrapRunPreservesExistingCurrentModelProfileVersion(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	bootstrapper := NewBootstrapper(Options{
		Pool:          pool,
		Logger:        logger,
		SkillsDir:     filepath.Join(t.TempDir(), "skills"),
		OrgSlug:       "default",
		OrgName:       "OtterCamp",
		AdminEmail:    "admin@example.com",
		AdminPassword: "admin-password",
		Version:       "test-version",
	})

	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("bootstrap run 1: %v", err)
	}

	orgRepo := repo.NewOrgRepo(pool)
	org, err := orgRepo.GetBySlug(ctx, "default")
	if err != nil {
		t.Fatalf("GetBySlug default: %v", err)
	}

	providerRepo := repo.NewModelProviderRepo(pool)
	anthropic, err := providerRepo.GetBySlug(ctx, "anthropic")
	if err != nil {
		t.Fatalf("GetBySlug anthropic: %v", err)
	}

	localProvider, err := providerRepo.Create(ctx, repo.ModelProvider{
		Slug:        "test-local-provider",
		DisplayName: "Test Local Provider",
		APIBaseURL:  "http://localhost:11434/v1",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create local provider: %v", err)
	}

	profileRepo := repo.NewModelProfileRepo(pool)
	current, err := profileRepo.GetCurrentByLogicalID(ctx, org.ID, "high-capability")
	if err != nil {
		t.Fatalf("GetCurrentByLogicalID high-capability: %v", err)
	}

	localCurrent, err := profileRepo.Deprecate(ctx, current.ID, repo.ModelProfile{
		ProviderID:          localProvider.ID,
		ModelName:           "qwen2.5:72b",
		ContextWindowTokens: current.ContextWindowTokens,
		MaxOutputTokens:     current.MaxOutputTokens,
		SupportsStreaming:   current.SupportsStreaming,
		SupportsVision:      current.SupportsVision,
		InvocationPurpose:   current.InvocationPurpose,
	})
	if err != nil {
		t.Fatalf("Deprecate to local current: %v", err)
	}

	seededHistorical, err := profileRepo.Deprecate(ctx, localCurrent.ID, repo.ModelProfile{
		ProviderID:          anthropic.ID,
		ModelName:           current.ModelName,
		ContextWindowTokens: current.ContextWindowTokens,
		MaxOutputTokens:     current.MaxOutputTokens,
		SupportsStreaming:   current.SupportsStreaming,
		SupportsVision:      current.SupportsVision,
		InvocationPurpose:   current.InvocationPurpose,
	})
	if err != nil {
		t.Fatalf("Deprecate back to seeded profile: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE model_profile
		SET is_current = false
		WHERE id = $1
	`, seededHistorical.ID); err != nil {
		t.Fatalf("clear seeded historical current flag: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE model_profile
		SET is_current = true
		WHERE id = $1
	`, localCurrent.ID); err != nil {
		t.Fatalf("rewind current profile pointer: %v", err)
	}

	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("bootstrap rerun after manual current rewind: %v", err)
	}

	restored, err := profileRepo.GetCurrentByLogicalID(ctx, org.ID, "high-capability")
	if err != nil {
		t.Fatalf("GetCurrentByLogicalID restored high-capability: %v", err)
	}
	if restored.ID != localCurrent.ID {
		t.Fatalf("current profile id = %s, want preserved operator-selected current %s", restored.ID, localCurrent.ID)
	}
	if !restored.IsCurrent {
		t.Fatal("restored profile IsCurrent = false, want true")
	}

	var currentCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM model_profile
		WHERE organization_id = $1
		  AND logical_profile_id = 'high-capability'
		  AND is_current = true
	`, org.ID).Scan(&currentCount); err != nil {
		t.Fatalf("count current profiles: %v", err)
	}
	if currentCount != 1 {
		t.Fatalf("current profile count = %d, want 1", currentCount)
	}

	var maxVersion int
	if err := pool.QueryRow(ctx, `
		SELECT MAX(version)
		FROM model_profile
		WHERE organization_id = $1
		  AND logical_profile_id = 'high-capability'
	`, org.ID).Scan(&maxVersion); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if maxVersion != seededHistorical.Version {
		t.Fatalf("max version = %d, want no new version beyond existing history %d", maxVersion, seededHistorical.Version)
	}
}

func TestBootstrapRunSkipsAdminUserWhenCredentialsMissing(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	bootstrapper := NewBootstrapper(Options{
		Pool:      pool,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		SkillsDir: filepath.Join(t.TempDir(), "skills"),
		OrgSlug:   "default",
		OrgName:   "OtterCamp",
		Version:   "test-version",
	})

	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("bootstrap run: %v", err)
	}

	orgRepo := repo.NewOrgRepo(pool)
	org, err := orgRepo.GetBySlug(ctx, "default")
	if err != nil {
		t.Fatalf("GetBySlug default: %v", err)
	}

	var adminCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM human_user
		WHERE organization_id = $1
		  AND role = 'admin'
	`, org.ID).Scan(&adminCount); err != nil {
		t.Fatalf("count admin users: %v", err)
	}
	if adminCount != 0 {
		t.Fatalf("admin user count = %d, want 0", adminCount)
	}
}

func TestBootstrapSeedsSystemFlowTemplatesIdempotently(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	bootstrapper := NewBootstrapper(Options{
		Pool:      pool,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		SkillsDir: filepath.Join(t.TempDir(), "skills"),
		OrgSlug:   "default",
		OrgName:   "OtterCamp",
		Version:   "test-version",
	})

	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("bootstrap run 1: %v", err)
	}
	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("bootstrap run 2: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM flow_template
		WHERE organization_id IS NULL
		  AND project_id IS NULL
		  AND is_system = true
		  AND slug IN ('default-single-agent', 'default-review', 'default-review-refinement')
	`).Scan(&count); err != nil {
		t.Fatalf("count flow templates: %v", err)
	}
	if count != 3 {
		t.Fatalf("system flow template count = %d, want 3", count)
	}

	rows, err := pool.Query(ctx, `
		SELECT slug, start_node_id
		FROM flow_template
		WHERE organization_id IS NULL
		  AND project_id IS NULL
		  AND is_system = true
		  AND slug IN ('default-single-agent', 'default-review', 'default-review-refinement')
		ORDER BY slug ASC
	`)
	if err != nil {
		t.Fatalf("list system templates: %v", err)
	}
	defer rows.Close()

	startNodeBySlug := map[string]uuid.UUID{}
	for rows.Next() {
		var (
			slug        string
			startNodeID *uuid.UUID
		)
		if err := rows.Scan(&slug, &startNodeID); err != nil {
			t.Fatalf("scan system template row: %v", err)
		}
		if startNodeID == nil || *startNodeID == uuid.Nil {
			t.Fatalf("template %q start_node_id = %v, want non-nil", slug, startNodeID)
		}
		startNodeBySlug[slug] = *startNodeID
	}
	if rows.Err() != nil {
		t.Fatalf("iterate system templates: %v", rows.Err())
	}

	for _, slug := range []string{"default-review", "default-review-refinement", "default-single-agent"} {
		if _, ok := startNodeBySlug[slug]; !ok {
			t.Fatalf("missing seeded system template %q", slug)
		}
	}

	assertExecutableSystemTemplates(t, ctx, pool)
}

func TestBootstrapRunReconcilesMissingSystemFlowTemplatesOnExistingInstall(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	bootstrapper := NewBootstrapper(Options{
		Pool:      pool,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		SkillsDir: filepath.Join(t.TempDir(), "skills"),
		OrgSlug:   "default",
		OrgName:   "OtterCamp",
		Version:   "test-version",
	})

	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("bootstrap run 1: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		DELETE FROM flow_node
		WHERE flow_template_id IN (
			SELECT id
			FROM flow_template
			WHERE slug = 'default-review-refinement'
			  AND organization_id IS NULL
			  AND project_id IS NULL
		)
	`); err != nil {
		t.Fatalf("delete review refinement nodes: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		DELETE FROM flow_template
		WHERE slug = 'default-review-refinement'
		  AND organization_id IS NULL
		  AND project_id IS NULL
	`); err != nil {
		t.Fatalf("delete review refinement template: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		DELETE FROM flow_node
		WHERE id IN (
			SELECT n.id
			FROM flow_node n
			JOIN flow_template t ON t.id = n.flow_template_id
			WHERE t.slug = 'default-single-agent'
			  AND t.organization_id IS NULL
			  AND t.project_id IS NULL
			  AND n.node_type = 'merge'
		)
	`); err != nil {
		t.Fatalf("delete single-agent merge node: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE flow_node
		SET next_node_id = NULL
		WHERE id IN (
			SELECT n.id
			FROM flow_node n
			JOIN flow_template t ON t.id = n.flow_template_id
			WHERE t.slug = 'default-review'
			  AND t.organization_id IS NULL
			  AND t.project_id IS NULL
			  AND n.display_name = 'Internal Review'
		)
	`); err != nil {
		t.Fatalf("break default-review path: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		DELETE FROM flow_node
		WHERE id IN (
			SELECT n.id
			FROM flow_node n
			JOIN flow_template t ON t.id = n.flow_template_id
			WHERE t.slug = 'default-review-refinement'
			  AND t.organization_id IS NULL
			  AND t.project_id IS NULL
			  AND n.node_type = 'merge'
		)
	`); err != nil {
		t.Fatalf("delete review-refinement merge node: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE flow_node
		SET next_node_id = NULL
		WHERE id IN (
			SELECT n.id
			FROM flow_node n
			JOIN flow_template t ON t.id = n.flow_template_id
			WHERE t.slug = 'default-review-refinement'
			  AND t.organization_id IS NULL
			  AND t.project_id IS NULL
			  AND n.display_name = 'Human Review'
		)
	`); err != nil {
		t.Fatalf("break review-refinement path: %v", err)
	}

	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("bootstrap run 2: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT slug, is_current, start_node_id
		FROM flow_template
		WHERE organization_id IS NULL
		  AND project_id IS NULL
		  AND is_system = true
		  AND slug IN ('default-single-agent', 'default-review', 'default-review-refinement')
		ORDER BY slug ASC
	`)
	if err != nil {
		t.Fatalf("list reconciled system templates: %v", err)
	}
	defer rows.Close()

	seen := map[string]bool{}
	for rows.Next() {
		var (
			slug        string
			isCurrent   bool
			startNodeID *uuid.UUID
		)
		if err := rows.Scan(&slug, &isCurrent, &startNodeID); err != nil {
			t.Fatalf("scan reconciled system template row: %v", err)
		}
		if !isCurrent {
			t.Fatalf("template %q is_current = false, want true", slug)
		}
		if startNodeID == nil || *startNodeID == uuid.Nil {
			t.Fatalf("template %q start_node_id = %v, want non-nil", slug, startNodeID)
		}
		seen[slug] = true
	}
	if rows.Err() != nil {
		t.Fatalf("iterate reconciled system templates: %v", rows.Err())
	}

	for _, slug := range []string{"default-single-agent", "default-review", "default-review-refinement"} {
		if !seen[slug] {
			t.Fatalf("missing reconciled template %q", slug)
		}
	}
	assertExecutableSystemTemplates(t, ctx, pool)
}

func assertExecutableSystemTemplates(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	rows, err := pool.Query(ctx, `
		SELECT id, slug, start_node_id
		FROM flow_template
		WHERE organization_id IS NULL
		  AND project_id IS NULL
		  AND is_system = true
		  AND slug IN ('default-single-agent', 'default-review', 'default-review-refinement')
		ORDER BY slug ASC
	`)
	if err != nil {
		t.Fatalf("list executable system templates: %v", err)
	}
	defer rows.Close()

	nodeRepo := repo.NewFlowNodeRepo(pool)
	totalNodes := 0
	for rows.Next() {
		var (
			templateID   uuid.UUID
			slug         string
			startNodeID  *uuid.UUID
		)
		if err := rows.Scan(&templateID, &slug, &startNodeID); err != nil {
			t.Fatalf("scan executable system template row: %v", err)
		}
		if startNodeID == nil || *startNodeID == uuid.Nil {
			t.Fatalf("template %q start_node_id = %v, want non-nil", slug, startNodeID)
		}
		nodes, err := nodeRepo.GetByTemplateOrdered(ctx, templateID)
		if err != nil {
			t.Fatalf("load nodes for %q: %v", slug, err)
		}
		totalNodes += len(nodes)
		if err := flowpolicy.ValidateExecutableFlowTemplate(startNodeID, nodes); err != nil {
			t.Fatalf("template %q executable validation err = %v, want nil", slug, err)
		}
	}
	if rows.Err() != nil {
		t.Fatalf("iterate executable system templates: %v", rows.Err())
	}
	if totalNodes != 10 {
		t.Fatalf("system flow node count = %d, want 10", totalNodes)
	}
}

func TestTestResetRouteResetsAndRebootstrapsInTestMode(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	bootstrapper := NewBootstrapper(Options{
		Pool:          pool,
		Logger:        logger,
		SkillsDir:     filepath.Join(t.TempDir(), "skills"),
		OrgSlug:       "default",
		OrgName:       "OtterCamp",
		AdminEmail:    "admin@example.com",
		AdminPassword: "admin-password",
		Version:       "test-version",
	})
	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("bootstrap run: %v", err)
	}

	orgRepo := repo.NewOrgRepo(pool)
	extraOrg, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "extra-org",
		DisplayName: "Extra Org",
	})
	if err != nil {
		t.Fatalf("create extra org: %v", err)
	}
	if extraOrg.ID == uuid.Nil {
		t.Fatal("expected extra org id")
	}

	handler := server.NewHandlerWithOptions(server.HandlerOptions{
		Logger:       logger,
		Pool:         pool,
		TestMode:     true,
		TestResetter: NewResetter(pool, bootstrapper),
	})

	req := httptest.NewRequest(http.MethodPost, "/test/reset", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("/test/reset status = %d, want %d body=%s", recorder.Code, http.StatusNoContent, recorder.Body.String())
	}

	var orgCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM organization`).Scan(&orgCount); err != nil {
		t.Fatalf("count organizations: %v", err)
	}
	if orgCount != 1 {
		t.Fatalf("organization count after reset = %d, want 1", orgCount)
	}

	assertIntCount(t, ctx, pool, `SELECT COUNT(*) FROM human_user WHERE role = 'admin'`, 1)
	assertIntCount(t, ctx, pool, `SELECT COUNT(*) FROM skill WHERE created_by_type = 'system'`, 3)
	assertIntCount(t, ctx, pool, `SELECT COUNT(*) FROM model_profile WHERE is_current = true`, 3)
	assertIntCount(t, ctx, pool, `SELECT COUNT(*) FROM model_profile_assignment WHERE scope_type = 'organization'`, 8)
}

func TestTestResetRouteNotRegisteredOutsideTestMode(t *testing.T) {
	handler := server.NewHandlerWithOptions(server.HandlerOptions{
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		TestMode: false,
	})

	req := httptest.NewRequest(http.MethodPost, "/test/reset", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("/test/reset status in production = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

type snapshot struct {
	organizationID uuid.UUID
	organizations  int
	adminUsers     int
	skills         int
	modelProfiles  int
	assignments    int
	bootstrapLogs  int
}

func countSnapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool) snapshot {
	t.Helper()

	var s snapshot
	mustCount := func(query string, args ...any) int {
		t.Helper()
		var value int
		row := pool.QueryRow(ctx, query, args...)
		if err := row.Scan(&value); err != nil {
			t.Fatalf("count query failed: %v", err)
		}
		return value
	}

	s.organizations = mustCount(`SELECT COUNT(*) FROM organization`)
	if err := pool.QueryRow(ctx, `SELECT id FROM organization WHERE slug = 'default'`).Scan(&s.organizationID); err != nil {
		t.Fatalf("load default organization id: %v", err)
	}
	s.adminUsers = mustCount(`SELECT COUNT(*) FROM human_user WHERE role = 'admin'`)
	s.skills = mustCount(`SELECT COUNT(*) FROM skill`)
	s.modelProfiles = mustCount(`SELECT COUNT(*) FROM model_profile WHERE is_current = true`)
	s.assignments = mustCount(`SELECT COUNT(*) FROM model_profile_assignment`)
	s.bootstrapLogs = mustCount(`SELECT COUNT(*) FROM audit_event WHERE event_type = 'system.bootstrap'`)
	return s
}

func assertIntCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, want int, args ...any) {
	t.Helper()

	var got int
	if err := pool.QueryRow(ctx, query, args...).Scan(&got); err != nil {
		t.Fatalf("count query failed: %v (query=%q)", err, query)
	}
	if got != want {
		t.Fatalf("count query returned %d, want %d (query=%q)", got, want, query)
	}
}
