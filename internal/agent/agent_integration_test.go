//go:build integration

package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestAgent_CRUD(t *testing.T) {
	ctx := context.Background()
	fixture := newAgentFixture(t, ctx)

	created, err := fixture.service.Create(ctx, CreateAgentRequest{
		OrganizationID: fixture.org.ID,
		DisplayName:    "CRUD Agent",
		SystemPrompt:   "Do work",
		AgentType:      "worker",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	fetched, err := fixture.service.Get(ctx, fixture.org.ID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if fetched.DisplayName != "CRUD Agent" {
		t.Fatalf("display_name = %q, want %q", fetched.DisplayName, "CRUD Agent")
	}

	renamed, err := fixture.service.Update(ctx, fixture.org.ID, created.ID, UpdateAgentRequest{
		DisplayName: ptrString72("CRUD Agent Updated"),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if renamed.DisplayName != "CRUD Agent Updated" {
		t.Fatalf("updated display_name = %q, want %q", renamed.DisplayName, "CRUD Agent Updated")
	}

	items, err := fixture.service.List(ctx, fixture.org.ID, AgentFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !containsAgent(items, created.ID) {
		t.Fatalf("List missing created agent %s", created.ID)
	}

	// No hard-delete API or service exists; lifecycle retirement is the supported path.
	if err := fixture.service.Unpause(ctx, fixture.org.ID, created.ID); err != nil {
		t.Fatalf("Unpause before retire: %v", err)
	}
	if err := fixture.service.Retire(ctx, fixture.org.ID, created.ID); err != nil {
		t.Fatalf("Retire: %v", err)
	}
}

func TestAgent_StaffLifecycle_DraftToActive(t *testing.T) {
	ctx := context.Background()
	fixture := newAgentFixture(t, ctx)
	item := seedDraftStaffAgent72(t, ctx, fixture.pool, fixture.org.ID)

	if err := fixture.service.Unpause(ctx, fixture.org.ID, item.ID); err != nil {
		t.Fatalf("Unpause draft->active: %v", err)
	}

	updated, err := fixture.agentRepo.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.LifecycleStatus != statusActive {
		t.Fatalf("lifecycle_status = %q, want %q", updated.LifecycleStatus, statusActive)
	}
	assertDomainEventCount(t, ctx, fixture.pool, fixture.org.ID, "agent.activated", 1)
}

func TestAgent_StaffLifecycle_ActiveToPaused(t *testing.T) {
	ctx := context.Background()
	fixture := newAgentFixture(t, ctx)
	item := seedDraftStaffAgent72(t, ctx, fixture.pool, fixture.org.ID)

	if err := fixture.service.Unpause(ctx, fixture.org.ID, item.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if err := fixture.service.Pause(ctx, fixture.org.ID, item.ID); err != nil {
		t.Fatalf("Pause active->paused: %v", err)
	}

	updated, err := fixture.agentRepo.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.LifecycleStatus != statusPaused {
		t.Fatalf("lifecycle_status = %q, want %q", updated.LifecycleStatus, statusPaused)
	}
	assertDomainEventCount(t, ctx, fixture.pool, fixture.org.ID, "agent.paused", 1)
}

func TestAgent_StaffLifecycle_Retire(t *testing.T) {
	ctx := context.Background()
	fixture := newAgentFixture(t, ctx)
	item := seedDraftStaffAgent72(t, ctx, fixture.pool, fixture.org.ID)

	if err := fixture.service.Unpause(ctx, fixture.org.ID, item.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if err := fixture.service.Retire(ctx, fixture.org.ID, item.ID); err != nil {
		t.Fatalf("Retire active->retired: %v", err)
	}

	updated, err := fixture.agentRepo.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.LifecycleStatus != statusRetired {
		t.Fatalf("lifecycle_status = %q, want %q", updated.LifecycleStatus, statusRetired)
	}

	activeOnly, err := fixture.service.List(ctx, fixture.org.ID, AgentFilter{LifecycleStatus: statusActive})
	if err != nil {
		t.Fatalf("List active: %v", err)
	}
	if containsAgent(activeOnly, item.ID) {
		t.Fatalf("retired agent %s unexpectedly returned in active filter", item.ID)
	}
	retiredOnly, err := fixture.service.List(ctx, fixture.org.ID, AgentFilter{LifecycleStatus: statusRetired})
	if err != nil {
		t.Fatalf("List retired: %v", err)
	}
	if !containsAgent(retiredOnly, item.ID) {
		t.Fatalf("retired agent %s missing from retired filter", item.ID)
	}

	assertDomainEventCount(t, ctx, fixture.pool, fixture.org.ID, "agent.retired", 1)
}

func TestAgent_StaffLifecycle_IllegalTransition(t *testing.T) {
	ctx := context.Background()
	fixture := newAgentFixture(t, ctx)
	item := seedDraftStaffAgent72(t, ctx, fixture.pool, fixture.org.ID)

	svcImpl := fixture.service.(*service)
	_, err := svcImpl.transitionStaffLifecycle(ctx, fixture.org.ID, item.ID, statusExpired)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("transition to expired error = %v, want ErrInvalidTransition", err)
	}

	updated, err := fixture.agentRepo.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.LifecycleStatus != statusDraft {
		t.Fatalf("lifecycle_status = %q, want unchanged %q", updated.LifecycleStatus, statusDraft)
	}
}

func TestAgent_TempAgent_Scoping(t *testing.T) {
	ctx := context.Background()
	fixture := newAgentFixture(t, ctx)

	temp, err := fixture.service.CreateTemp(ctx, fixture.org.ID, CreateTempAgentRequest{
		DisplayName:   "Scoped Temp",
		AgentType:     "worker",
		TempProjectID: fixture.project.ID,
		CreatedByType: "system",
	})
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if temp.TempProjectID == nil || *temp.TempProjectID != fixture.project.ID {
		t.Fatalf("temp_project_id = %v, want %s", temp.TempProjectID, fixture.project.ID)
	}

	listed, err := fixture.service.List(ctx, fixture.org.ID, AgentFilter{AgentClass: agentClassTemp})
	if err != nil {
		t.Fatalf("List temp agents: %v", err)
	}
	if !containsAgent(listed, temp.ID) {
		t.Fatalf("temp agent %s missing in temp-filtered list", temp.ID)
	}
}

func TestAgent_TempAgent_TTLExpiry(t *testing.T) {
	ctx := context.Background()
	fixture := newAgentFixture(t, ctx)

	expiresAt := time.Now().UTC().Add(-5 * time.Minute)
	temp, err := fixture.agentRepo.Create(ctx, repo.Agent{
		OrganizationID:       fixture.org.ID,
		DisplayName:          "TTL Temp",
		AgentClass:           agentClassTemp,
		LifecycleStatus:      statusActive,
		SystemPrompt:         "temp",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "agent"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		TempProjectID:        ptrUUID72(fixture.project.ID),
		TempExpiresAt:        &expiresAt,
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("seed expired temp: %v", err)
	}

	if err := fixture.service.RetireExpiredTemps(ctx); err != nil {
		t.Fatalf("RetireExpiredTemps: %v", err)
	}

	updated, err := fixture.agentRepo.GetByID(ctx, temp.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.LifecycleStatus != statusExpired {
		t.Fatalf("lifecycle_status = %q, want %q", updated.LifecycleStatus, statusExpired)
	}

	var summary string
	if err := fixture.pool.QueryRow(ctx, `
		SELECT payload->>'archival_summary'
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'agent.expired'
		  AND payload->>'agent_id' = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, fixture.org.ID, temp.ID.String()).Scan(&summary); err != nil {
		t.Fatalf("query agent.expired domain_event: %v", err)
	}
	if summary == "" {
		t.Fatal("expected non-empty archival_summary in agent.expired payload")
	}
}

func TestAgent_TempAgent_TaskCompletion_AutoRetire(t *testing.T) {
	ctx := context.Background()
	fixture := newAgentFixture(t, ctx)

	temp, err := fixture.service.CreateTemp(ctx, fixture.org.ID, CreateTempAgentRequest{
		DisplayName:   "Task Temp",
		AgentType:     "worker",
		TempProjectID: fixture.project.ID,
		CreatedByType: "system",
	})
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}

	if _, err := fixture.pool.Exec(ctx, `
		INSERT INTO domain_event (organization_id, event_type, actor_type, payload)
		VALUES ($1, 'task.completed', 'system', jsonb_build_object('agent_id', $2::text))
	`, fixture.org.ID, temp.ID.String()); err != nil {
		t.Fatalf("insert task.completed event: %v", err)
	}

	updated, err := fixture.agentRepo.GetByID(ctx, temp.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.LifecycleStatus != statusActive {
		t.Fatalf("lifecycle_status = %q, want %q (temp should not auto-retire on task completion)", updated.LifecycleStatus, statusActive)
	}
}

func TestAgent_TempAgent_ExplicitRetire(t *testing.T) {
	ctx := context.Background()
	fixture := newAgentFixture(t, ctx)

	temp, err := fixture.service.CreateTemp(ctx, fixture.org.ID, CreateTempAgentRequest{
		DisplayName:   "Retire Temp",
		AgentType:     "worker",
		TempProjectID: fixture.project.ID,
		CreatedByType: "system",
	})
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}

	if err := fixture.service.Retire(ctx, fixture.org.ID, temp.ID); err != nil {
		t.Fatalf("Retire temp: %v", err)
	}

	updated, err := fixture.agentRepo.GetByID(ctx, temp.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.LifecycleStatus != statusRetired {
		t.Fatalf("lifecycle_status = %q, want %q", updated.LifecycleStatus, statusRetired)
	}

	assertDomainEventCount(t, ctx, fixture.pool, fixture.org.ID, "agent.retired", 1)
}

func TestAgent_TempAgent_ConcurrentLimit(t *testing.T) {
	ctx := context.Background()
	fixture := newAgentFixture(t, ctx)

	if _, err := fixture.pool.Exec(ctx, `
		UPDATE organization
		SET settings = jsonb_set(COALESCE(settings, '{}'::jsonb), '{agents,max_concurrent_temps}', '2'::jsonb, true)
		WHERE id = $1
	`, fixture.org.ID); err != nil {
		t.Fatalf("update max_concurrent_temps setting: %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, err := fixture.service.CreateTemp(ctx, fixture.org.ID, CreateTempAgentRequest{
			DisplayName:   "Concurrent Temp " + uuid.NewString()[:8],
			AgentType:     "worker",
			TempProjectID: fixture.project.ID,
			CreatedByType: "system",
		}); err != nil {
			t.Fatalf("CreateTemp #%d: %v", i, err)
		}
	}

	_, err := fixture.service.CreateTemp(ctx, fixture.org.ID, CreateTempAgentRequest{
		DisplayName:   "Concurrent Temp 3",
		AgentType:     "worker",
		TempProjectID: fixture.project.ID,
		CreatedByType: "system",
	})
	if !errors.Is(err, ErrConcurrentTempLimitReached) {
		t.Fatalf("third CreateTemp error = %v, want ErrConcurrentTempLimitReached", err)
	}

	count, err := fixture.agentRepo.CountActiveTemps(ctx, fixture.org.ID)
	if err != nil {
		t.Fatalf("CountActiveTemps: %v", err)
	}
	if count != 2 {
		t.Fatalf("active temp count = %d, want 2", count)
	}
}

func TestAgent_ProjectAssignment(t *testing.T) {
	ctx := context.Background()
	fixture := newAgentFixture(t, ctx)
	agentA := seedActiveStaffAgent72(t, ctx, fixture.pool, fixture.org.ID, "assign-a")
	agentB := seedActiveStaffAgent72(t, ctx, fixture.pool, fixture.org.ID, "assign-b")

	assigned, err := fixture.assignmentService.AssignToProject(ctx, agentA.ID, fixture.project.ID, "worker", AssignmentActor{Type: "system"})
	if err != nil {
		t.Fatalf("AssignToProject worker: %v", err)
	}
	if !assigned.IsActive {
		t.Fatal("assignment is_active = false, want true")
	}

	list, err := fixture.projectAssignmentRepo.ListByAgent(ctx, agentA.ID)
	if err != nil {
		t.Fatalf("ListByAgent: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListByAgent count = %d, want 1", len(list))
	}

	removed, err := fixture.assignmentService.RemoveFromProject(ctx, agentA.ID, fixture.project.ID)
	if err != nil {
		t.Fatalf("RemoveFromProject: %v", err)
	}
	if removed.DeactivatedAt == nil {
		t.Fatal("deactivated_at is nil after RemoveFromProject")
	}

	if _, err := fixture.projectAssignmentRepo.Assign(ctx, repo.AgentProjectAssignment{
		AgentID:        agentA.ID,
		ProjectID:      fixture.project.ID,
		Role:           "pm",
		AssignedByType: "system",
	}); err != nil {
		t.Fatalf("seed PM assignment A: %v", err)
	}
	_, err = fixture.projectAssignmentRepo.Assign(ctx, repo.AgentProjectAssignment{
		AgentID:        agentB.ID,
		ProjectID:      fixture.project.ID,
		Role:           "pm",
		AssignedByType: "system",
	})
	if !errors.Is(err, ErrPMConflict) {
		t.Fatalf("second PM assignment conflict error = %v, want ErrPMConflict", err)
	}
}

func TestAgent_SkillAttachment(t *testing.T) {
	ctx := context.Background()
	fixture := newAgentFixture(t, ctx)
	agentRecord := seedActiveStaffAgent72(t, ctx, fixture.pool, fixture.org.ID, "skills-agent")
	skillRepo := repo.NewSkillRepo(fixture.pool)

	skillA, err := skillRepo.Create(ctx, repo.Skill{
		OrganizationID: fixture.org.ID,
		Slug:           "skill-a-" + uuid.NewString()[:8],
		DisplayName:    "Skill A",
		Description:    "Skill A",
		FilePath:       "skills/a.md",
		Version:        1,
		IsActive:       true,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create skill A: %v", err)
	}
	skillB, err := skillRepo.Create(ctx, repo.Skill{
		OrganizationID: fixture.org.ID,
		Slug:           "skill-b-" + uuid.NewString()[:8],
		DisplayName:    "Skill B",
		Description:    "Skill B",
		FilePath:       "skills/b.md",
		Version:        1,
		IsActive:       true,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create skill B: %v", err)
	}

	if _, err := fixture.assignmentService.AttachSkill(ctx, agentRecord.ID, skillA.ID, 1, AssignmentActor{Type: "system"}); err != nil {
		t.Fatalf("AttachSkill A: %v", err)
	}
	if _, err := fixture.assignmentService.AttachSkill(ctx, agentRecord.ID, skillB.ID, 5, AssignmentActor{Type: "system"}); err != nil {
		t.Fatalf("AttachSkill B: %v", err)
	}

	// Duplicate attach updates the same row in current implementation.
	dupe, err := fixture.assignmentService.AttachSkill(ctx, agentRecord.ID, skillA.ID, 3, AssignmentActor{Type: "system"})
	if err != nil {
		t.Fatalf("AttachSkill duplicate: %v", err)
	}
	if dupe.Priority != 3 {
		t.Fatalf("duplicate attach priority = %d, want 3", dupe.Priority)
	}

	attached, err := fixture.skillAttachmentRepo.ListByAgent(ctx, agentRecord.ID)
	if err != nil {
		t.Fatalf("ListByAgent skills: %v", err)
	}
	if len(attached) != 2 {
		t.Fatalf("attached skill count = %d, want 2", len(attached))
	}
	if attached[0].Priority > attached[1].Priority {
		t.Fatalf("skill priorities not sorted: [%d, %d]", attached[0].Priority, attached[1].Priority)
	}

	if _, err := fixture.assignmentService.DetachSkill(ctx, agentRecord.ID, skillA.ID); err != nil {
		t.Fatalf("DetachSkill A: %v", err)
	}
	afterDetach, err := fixture.skillAttachmentRepo.ListByAgent(ctx, agentRecord.ID)
	if err != nil {
		t.Fatalf("ListByAgent after detach: %v", err)
	}
	if len(afterDetach) != 1 {
		t.Fatalf("attached skill count after detach = %d, want 1", len(afterDetach))
	}
}

func TestAgent_PromotionWorkflow(t *testing.T) {
	ctx := context.Background()
	fixture := newAgentFixture(t, ctx)

	temp, err := fixture.agentRepo.Create(ctx, repo.Agent{
		OrganizationID:        fixture.org.ID,
		DisplayName:           "Promotable Temp",
		AgentClass:            agentClassTemp,
		LifecycleStatus:       statusActive,
		SystemPrompt:          "prompt",
		OperatorInstructions:  "instructions",
		AgentType:             "worker",
		PrivateMemory:         false,
		MemoryReadScopes:      []string{"org", "project", "agent"},
		ToolAllowList:         []string{"file.*"},
		ToolDenyList:          []string{"secret.*"},
		DefaultModelProfileID: ptrString72("balanced"),
		TempProjectID:         ptrUUID72(fixture.project.ID),
		CreatedByType:         "system",
		CreatedByID:           uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}

	staff, err := fixture.service.PromoteTemp(ctx, fixture.org.ID, temp.ID, PromoteTempRequest{CreatedByType: "system"})
	if err != nil {
		t.Fatalf("PromoteTemp: %v", err)
	}
	if staff.AgentClass != agentClassStaff {
		t.Fatalf("staff class = %q, want %q", staff.AgentClass, agentClassStaff)
	}
	if staff.LifecycleStatus != statusDraft {
		t.Fatalf("staff lifecycle_status = %q, want %q", staff.LifecycleStatus, statusDraft)
	}

	updatedTemp, err := fixture.agentRepo.GetByID(ctx, temp.ID)
	if err != nil {
		t.Fatalf("GetByID temp: %v", err)
	}
	if updatedTemp.LifecycleStatus != statusPromoted {
		t.Fatalf("temp lifecycle_status = %q, want %q", updatedTemp.LifecycleStatus, statusPromoted)
	}
	if updatedTemp.PromotedToAgentID == nil || *updatedTemp.PromotedToAgentID != staff.ID {
		t.Fatalf("promoted_to_agent_id = %v, want %s", updatedTemp.PromotedToAgentID, staff.ID)
	}
}

func TestAgent_BudgetCap_Stub(t *testing.T) {
	ctx := context.Background()
	fixture := newAgentFixture(t, ctx)

	capTokens := int64(5000)
	period := "daily"

	// ISSUES #1 and #23 are resolved: this test verifies persistence only.
	// Enforcement behavior is intentionally covered in policy/control-plane tests.
	created, err := fixture.service.Create(ctx, CreateAgentRequest{
		OrganizationID:  fixture.org.ID,
		DisplayName:     "Budget Stub Agent",
		SystemPrompt:    "prompt",
		AgentType:       "worker",
		BudgetCapTokens: &capTokens,
		BudgetPeriod:    &period,
		CreatedByType:   "system",
	})
	if err != nil {
		t.Fatalf("Create budget agent: %v", err)
	}

	stored, err := fixture.agentRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.BudgetCapTokens == nil || *stored.BudgetCapTokens != capTokens {
		t.Fatalf("budget_cap_tokens = %v, want %d", stored.BudgetCapTokens, capTokens)
	}
	if stored.BudgetPeriod == nil || *stored.BudgetPeriod != period {
		t.Fatalf("budget_period = %v, want %q", stored.BudgetPeriod, period)
	}
}

type agentFixture struct {
	pool                  *pgxpool.Pool
	org                   repo.Organization
	project               repo.Project
	service               AgentService
	agentRepo             *repo.AgentRepo
	assignmentService     AssignmentService
	projectAssignmentRepo *repo.AgentProjectAssignmentRepo
	skillAttachmentRepo   *repo.AgentSkillAttachmentRepo
}

func newAgentFixture(t *testing.T, ctx context.Context) agentFixture {
	t.Helper()

	pool := testdb.New(t)
	orgRepo := repo.NewOrgRepo(pool)
	projectRepo := repo.NewProjectRepo(pool)
	agentRepo := repo.NewAgentRepo(pool)
	projectAssignmentRepo := repo.NewAgentProjectAssignmentRepo(pool)
	skillAttachmentRepo := repo.NewAgentSkillAttachmentRepo(pool)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := eventbus.New(pool, logger, eventbus.Config{})

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "agent-it-org-" + uuid.NewString()[:8],
		DisplayName: "Agent Integration Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	project, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "agent-it-project-" + uuid.NewString()[:8],
		DisplayName:    "Agent Integration Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	svc, err := NewService(Options{Pool: pool, Agents: agentRepo, Events: bus, Logger: logger})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	assignmentSvc, err := NewAssignmentService(AssignmentServiceOptions{Pool: pool, Events: bus})
	if err != nil {
		t.Fatalf("NewAssignmentService: %v", err)
	}

	return agentFixture{
		pool:                  pool,
		org:                   org,
		project:               project,
		service:               svc,
		agentRepo:             agentRepo,
		assignmentService:     assignmentSvc,
		projectAssignmentRepo: projectAssignmentRepo,
		skillAttachmentRepo:   skillAttachmentRepo,
	}
}

func seedDraftStaffAgent72(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) repo.Agent {
	t.Helper()
	created, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          "Draft Staff " + uuid.NewString()[:8],
		AgentClass:           agentClassStaff,
		LifecycleStatus:      statusDraft,
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
		t.Fatalf("create draft staff agent: %v", err)
	}
	return created
}

func seedActiveStaffAgent72(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, prefix string) repo.Agent {
	t.Helper()
	created, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          prefix + "-" + uuid.NewString()[:8],
		AgentClass:           agentClassStaff,
		LifecycleStatus:      statusActive,
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
		t.Fatalf("create active staff agent: %v", err)
	}
	return created
}

func assertDomainEventCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, eventType string, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = $2
	`, orgID, eventType).Scan(&count); err != nil {
		t.Fatalf("query domain_event %q: %v", eventType, err)
	}
	if count != want {
		t.Fatalf("domain_event %q count = %d, want %d", eventType, count, want)
	}
}

func containsAgent(items []*Agent, id uuid.UUID) bool {
	for _, item := range items {
		if item != nil && item.ID == id {
			return true
		}
	}
	return false
}

func ptrUUID72(v uuid.UUID) *uuid.UUID {
	return &v
}

func ptrString72(v string) *string {
	return &v
}
