//go:build integration

package repo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestAgentProjectAssignmentRepoReturnsPMConflictOnSecondActivePM(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, project, agentA, agentB := seedAssignmentProjectAgents(t, ctx, pool)
	_ = org
	repo := NewAgentProjectAssignmentRepo(pool)

	if _, err := repo.Assign(ctx, AgentProjectAssignment{
		AgentID:        agentA.ID,
		ProjectID:      project.ID,
		Role:           "project_manager",
		AssignedByType: "system",
	}); err != nil {
		t.Fatalf("Assign agentA as PM: %v", err)
	}

	_, err := repo.Assign(ctx, AgentProjectAssignment{
		AgentID:        agentB.ID,
		ProjectID:      project.ID,
		Role:           "project_manager",
		AssignedByType: "system",
	})
	if !errors.Is(err, ErrPMConflict) {
		t.Fatalf("Assign second active PM error = %v, want ErrPMConflict", err)
	}
}

func TestAgentProjectAssignmentPartialUniqueIndexRejectsDuplicatePMRows(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	_, project, agentA, agentB := seedAssignmentProjectAgents(t, ctx, pool)

	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_project_assignment (
			agent_id,
			project_id,
			role,
			assigned_by_type,
			assigned_by_id,
			is_active
		)
		VALUES ($1, $2, 'project_manager', 'system', NULL, true)
	`, agentA.ID, project.ID); err != nil {
		t.Fatalf("insert first PM row: %v", err)
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO agent_project_assignment (
			agent_id,
			project_id,
			role,
			assigned_by_type,
			assigned_by_id,
			is_active
		)
		VALUES ($1, $2, 'project_manager', 'system', NULL, true)
	`, agentB.ID, project.ID)
	if err == nil {
		t.Fatal("expected second active PM insert to fail")
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected pg error, got %T (%v)", err, err)
	}
	if pgErr.Code != "23505" {
		t.Fatalf("duplicate PM insert code = %s, want 23505", pgErr.Code)
	}
}

func TestAgentProjectAssignmentMigrationConvertsLegacyPMRowsAndRepairsTempAgents(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, project, _, _ := seedAssignmentProjectAgents(t, ctx, pool)
	agentRepo := NewAgentRepo(pool)
	tempPM, err := agentRepo.Create(ctx, Agent{
		OrganizationID:       org.ID,
		DisplayName:          "Legacy Temp PM",
		AgentClass:           "temp",
		LifecycleStatus:      "active",
		SystemPrompt:         "prompt",
		OperatorInstructions: "",
		AgentType:            "pm",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "agent"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		TempProjectID:        &project.ID,
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create temp PM agent: %v", err)
	}

	if _, err := pool.Exec(ctx, `DROP TRIGGER IF EXISTS agent_project_assignment_project_manager_staff_chk ON agent_project_assignment`); err != nil {
		t.Fatalf("drop project_manager staff trigger: %v", err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE agent_project_assignment DROP CONSTRAINT IF EXISTS agent_project_assignment_role_check`); err != nil {
		t.Fatalf("drop role check constraint: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		ALTER TABLE agent_project_assignment
		ADD CONSTRAINT agent_project_assignment_role_check
		CHECK (role IN ('pm', 'project_manager', 'worker', 'reviewer', 'observer'))
	`); err != nil {
		t.Fatalf("add permissive role check constraint: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_project_assignment (
			agent_id,
			project_id,
			role,
			assigned_by_type,
			assigned_by_id,
			is_active
		)
		VALUES ($1, $2, 'pm', 'system', NULL, true)
	`, tempPM.ID, project.ID); err != nil {
		t.Fatalf("insert legacy pm row: %v", err)
	}

	var legacyCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM agent_project_assignment
		WHERE role = 'pm'
	`).Scan(&legacyCount); err != nil {
		t.Fatalf("count legacy pm rows: %v", err)
	}
	if legacyCount != 1 {
		t.Fatalf("legacy pm rows = %d, want 1", legacyCount)
	}

	migrationPath := filepath.Join("..", "..", "migrations", "0108_project_manager_role_normalization.sql")
	sqlBytes, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read migration file: %v", err)
	}
	if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil {
		t.Fatalf("execute migration SQL: %v", err)
	}

	var canonicalCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM agent_project_assignment
		WHERE role = 'project_manager'
		  AND project_id = $1
	`, project.ID).Scan(&canonicalCount); err != nil {
		t.Fatalf("count canonical project_manager rows: %v", err)
	}
	if canonicalCount != 1 {
		t.Fatalf("project_manager rows = %d, want 1", canonicalCount)
	}

	if err := pool.QueryRow(ctx, `SELECT agent_class FROM agent WHERE id = $1`, tempPM.ID).Scan(&tempPM.AgentClass); err != nil {
		t.Fatalf("query temp PM class: %v", err)
	}
	if tempPM.AgentClass != "staff" {
		t.Fatalf("temp PM class after migration = %q, want staff", tempPM.AgentClass)
	}
}

func TestAgentSkillAttachmentReattachKeepsSingleRow(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, _, agentA, _ := seedAssignmentProjectAgents(t, ctx, pool)
	skillA := seedAssignmentSkill(t, ctx, pool, org.ID, "skill-a")
	repo := NewAgentSkillAttachmentRepo(pool)

	if _, err := repo.Attach(ctx, AgentSkillAttachment{
		AgentID:        agentA.ID,
		SkillID:        skillA.ID,
		Priority:       100,
		AttachedByType: "system",
	}); err != nil {
		t.Fatalf("Attach first: %v", err)
	}

	if _, err := repo.Detach(ctx, agentA.ID, skillA.ID); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	if _, err := repo.Attach(ctx, AgentSkillAttachment{
		AgentID:        agentA.ID,
		SkillID:        skillA.ID,
		Priority:       10,
		AttachedByType: "system",
	}); err != nil {
		t.Fatalf("Attach second: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM agent_skill_attachment
		WHERE agent_id = $1
		  AND skill_id = $2
	`, agentA.ID, skillA.ID).Scan(&count); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if count != 1 {
		t.Fatalf("attachment row count = %d, want 1", count)
	}

	row, err := repo.GetByAgentAndSkill(ctx, agentA.ID, skillA.ID)
	if err != nil {
		t.Fatalf("GetByAgentAndSkill: %v", err)
	}
	if !row.IsActive {
		t.Fatalf("reattached row is_active = false, want true")
	}
	if row.Priority != 10 {
		t.Fatalf("reattached row priority = %d, want 10", row.Priority)
	}
}

func TestAgentSkillAttachmentListByAgentOrdersByPriorityAndExcludesInactive(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org, _, agentA, _ := seedAssignmentProjectAgents(t, ctx, pool)
	skillA := seedAssignmentSkill(t, ctx, pool, org.ID, "skill-a")
	skillB := seedAssignmentSkill(t, ctx, pool, org.ID, "skill-b")
	skillC := seedAssignmentSkill(t, ctx, pool, org.ID, "skill-c")
	repo := NewAgentSkillAttachmentRepo(pool)

	if _, err := repo.Attach(ctx, AgentSkillAttachment{
		AgentID:        agentA.ID,
		SkillID:        skillA.ID,
		Priority:       200,
		AttachedByType: "system",
	}); err != nil {
		t.Fatalf("attach skillA: %v", err)
	}
	if _, err := repo.Attach(ctx, AgentSkillAttachment{
		AgentID:        agentA.ID,
		SkillID:        skillB.ID,
		Priority:       50,
		AttachedByType: "system",
	}); err != nil {
		t.Fatalf("attach skillB: %v", err)
	}
	if _, err := repo.Attach(ctx, AgentSkillAttachment{
		AgentID:        agentA.ID,
		SkillID:        skillC.ID,
		Priority:       100,
		AttachedByType: "system",
	}); err != nil {
		t.Fatalf("attach skillC: %v", err)
	}

	ordered, err := repo.ListByAgent(ctx, agentA.ID)
	if err != nil {
		t.Fatalf("ListByAgent initial: %v", err)
	}
	if len(ordered) != 3 {
		t.Fatalf("ListByAgent initial len = %d, want 3", len(ordered))
	}
	if ordered[0].Priority != 50 || ordered[1].Priority != 100 || ordered[2].Priority != 200 {
		t.Fatalf("ListByAgent initial priorities = [%d %d %d], want [50 100 200]", ordered[0].Priority, ordered[1].Priority, ordered[2].Priority)
	}

	if _, err := repo.Detach(ctx, agentA.ID, skillC.ID); err != nil {
		t.Fatalf("Detach skillC: %v", err)
	}

	activeOnly, err := repo.ListByAgent(ctx, agentA.ID)
	if err != nil {
		t.Fatalf("ListByAgent after detach: %v", err)
	}
	if len(activeOnly) != 2 {
		t.Fatalf("ListByAgent after detach len = %d, want 2", len(activeOnly))
	}
	for _, item := range activeOnly {
		if !item.IsActive {
			t.Fatalf("ListByAgent returned inactive row: %+v", item)
		}
	}
}

func seedAssignmentProjectAgents(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (Organization, Project, Agent, Agent) {
	t.Helper()

	orgRepo := NewOrgRepo(pool)
	projectRepo := NewProjectRepo(pool)
	agentRepo := NewAgentRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{
		Slug:        "assign-org-" + uuid.NewString()[:8],
		DisplayName: "Assignment Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	project, err := projectRepo.Create(ctx, Project{
		OrganizationID: org.ID,
		Slug:           "assign-project-" + uuid.NewString()[:8],
		DisplayName:    "Assignment Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	agentA, err := agentRepo.Create(ctx, Agent{
		OrganizationID:       org.ID,
		DisplayName:          "Agent A",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "prompt",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "agent"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent A: %v", err)
	}

	agentB, err := agentRepo.Create(ctx, Agent{
		OrganizationID:       org.ID,
		DisplayName:          "Agent B",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "prompt",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "agent"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent B: %v", err)
	}

	return org, project, agentA, agentB
}

func seedAssignmentSkill(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, slug string) Skill {
	t.Helper()

	skillRepo := NewSkillRepo(pool)
	created, err := skillRepo.Create(ctx, Skill{
		OrganizationID: orgID,
		Slug:           slug + "-" + uuid.NewString()[:8],
		DisplayName:    slug,
		Description:    slug + " desc",
		FilePath:       "skills/" + slug + ".md",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create skill %s: %v", slug, err)
	}
	return created
}
