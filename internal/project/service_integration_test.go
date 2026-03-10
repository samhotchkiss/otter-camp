//go:build integration

package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/projectpause"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/workspace"
)

func TestProjectServiceCreateGetBySlugAndUniqueness(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)
	orgRepo := repo.NewOrgRepo(pool)

	orgA, err := orgRepo.Create(ctx, repo.Organization{Slug: "proj-svc-org-a", DisplayName: "Project Service Org A"})
	if err != nil {
		t.Fatalf("create org A: %v", err)
	}
	orgB, err := orgRepo.Create(ctx, repo.Organization{Slug: "proj-svc-org-b", DisplayName: "Project Service Org B"})
	if err != nil {
		t.Fatalf("create org B: %v", err)
	}

	created, err := svc.Create(ctx, CreateProjectRequest{
		OrganizationID: orgA.ID,
		Slug:           "alpha",
		DisplayName:    "Alpha",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	loaded, err := svc.GetBySlug(ctx, orgA.ID, "alpha")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if loaded.ID != created.ID {
		t.Fatalf("GetBySlug id = %s, want %s", loaded.ID, created.ID)
	}

	_, err = svc.Create(ctx, CreateProjectRequest{
		OrganizationID: orgA.ID,
		Slug:           "alpha",
		DisplayName:    "Alpha Duplicate",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
	})
	if !errors.Is(err, ErrSlugTaken) {
		t.Fatalf("duplicate slug in same org err = %v, want ErrSlugTaken", err)
	}

	if _, err := svc.Create(ctx, CreateProjectRequest{
		OrganizationID: orgB.ID,
		Slug:           "alpha",
		DisplayName:    "Alpha Org B",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
	}); err != nil {
		t.Fatalf("same slug in different org should succeed, got: %v", err)
	}
}

func TestProjectServiceCreatePublishesStaffingNeededEvent(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)
	orgRepo := repo.NewOrgRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{Slug: "proj-svc-staffing-" + uuid.NewString()[:8], DisplayName: "Staffing Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	created, err := svc.Create(ctx, CreateProjectRequest{
		OrganizationID: org.ID,
		Slug:           "staffing-required",
		DisplayName:    "Staffing Required",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var staffingCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'project.staffing_needed'
		  AND payload ->> 'project_id' = $2
	`, org.ID, created.ID.String()).Scan(&staffingCount); err != nil {
		t.Fatalf("count project.staffing_needed: %v", err)
	}
	if staffingCount < 1 {
		t.Fatalf("project.staffing_needed count = %d, want >= 1", staffingCount)
	}
}

func TestProjectServiceCreateBindsCanonicalWorkspaceEnvironment(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)
	orgRepo := repo.NewOrgRepo(pool)
	environmentRepo := repo.NewProjectEnvironmentRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{Slug: "proj-svc-repo-bind-" + uuid.NewString()[:8], DisplayName: "Repo Binding Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	created, err := svc.Create(ctx, CreateProjectRequest{
		OrganizationID: org.ID,
		Slug:           "canonical-repo-binding",
		DisplayName:    "Canonical Repo Binding",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	environments, err := environmentRepo.ListByProject(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListByProject environments: %v", err)
	}
	if len(environments) != 1 {
		t.Fatalf("project environment count = %d, want 1", len(environments))
	}

	wantRepoPath, err := workspace.ProjectRoot("", created.Slug)
	if err != nil {
		t.Fatalf("workspace.ProjectRoot: %v", err)
	}
	if got := strings.TrimSpace(pointerValue(environments[0].RepoPath)); got != wantRepoPath {
		t.Fatalf("repo_path = %q, want %q", got, wantRepoPath)
	}
	if environments[0].Name != "workspace" {
		t.Fatalf("environment name = %q, want workspace", environments[0].Name)
	}
	if environments[0].TargetBranch != "main" {
		t.Fatalf("target_branch = %q, want main", environments[0].TargetBranch)
	}
	if !environments[0].IsActive {
		t.Fatal("project environment is_active = false, want true")
	}
}

func TestProjectServiceCreateAutoGeneratesCanonicalBootstrapTaskTree(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)
	orgRepo := repo.NewOrgRepo(pool)
	taskRepo := repo.NewProjectTaskRepo(pool)
	templateRepo := repo.NewFlowTemplateRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "proj-svc-bootstrap-" + uuid.NewString()[:8],
		DisplayName: "Bootstrap Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	loriID := seedStarterAgent(t, ctx, pool, org.ID, "Lori", "pm")
	frankID := seedStarterAgent(t, ctx, pool, org.ID, "Frank", "general")

	created, err := svc.Create(ctx, CreateProjectRequest{
		OrganizationID: org.ID,
		Slug:           "bootstrap-gated-project",
		DisplayName:    "Bootstrap Gated Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	bootstrapTask, err := taskRepo.GetByProjectAndNumber(ctx, created.ID, 1)
	if err != nil {
		t.Fatalf("GetByProjectAndNumber bootstrap task: %v", err)
	}
	if bootstrapTask.BlocksScope != "all" {
		t.Fatalf("bootstrap blocks_scope = %q, want %q", bootstrapTask.BlocksScope, "all")
	}
	if bootstrapTask.FlowTemplateID == nil || *bootstrapTask.FlowTemplateID == uuid.Nil {
		t.Fatal("bootstrap flow_template_id is nil, want non-nil")
	}
	bootstrapMetadata := projectTaskMetadata(t, bootstrapTask.Metadata)
	if root, _ := bootstrapMetadata["bootstrap_tree_root"].(bool); !root {
		t.Fatalf("bootstrap root metadata = %v, want bootstrap_tree_root=true", bootstrapMetadata)
	}

	template, err := templateRepo.GetByID(ctx, *bootstrapTask.FlowTemplateID)
	if err != nil {
		t.Fatalf("GetByID bootstrap flow template: %v", err)
	}
	if template.StartNodeID == nil || *template.StartNodeID == uuid.Nil {
		t.Fatal("bootstrap start_node_id is nil, want non-nil")
	}

	nodes, err := nodeRepo.GetByTemplateOrdered(ctx, template.ID)
	if err != nil {
		t.Fatalf("GetByTemplateOrdered bootstrap nodes: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("bootstrap flow nodes len = %d, want 3", len(nodes))
	}

	setupNode := nodes[0]
	reviewNode := nodes[1]
	mergeNode := nodes[2]
	if setupNode.ID != *template.StartNodeID {
		t.Fatalf("start_node_id = %s, want setup node %s", *template.StartNodeID, setupNode.ID)
	}
	if setupNode.NodeType != "work" {
		t.Fatalf("setup node_type = %q, want work", setupNode.NodeType)
	}
	if reviewNode.NodeType != "review" {
		t.Fatalf("review node_type = %q, want review", reviewNode.NodeType)
	}
	if setupNode.ActorType == nil || *setupNode.ActorType != "agent" {
		t.Fatalf("setup actor_type = %v, want agent", setupNode.ActorType)
	}
	if setupNode.ActorID == nil || *setupNode.ActorID != loriID {
		t.Fatalf("setup actor_id = %v, want Lori %s", setupNode.ActorID, loriID)
	}
	if reviewNode.ActorType == nil || *reviewNode.ActorType != "agent" {
		t.Fatalf("review actor_type = %v, want agent", reviewNode.ActorType)
	}
	if reviewNode.ActorID == nil || *reviewNode.ActorID != frankID {
		t.Fatalf("review actor_id = %v, want Frank %s", reviewNode.ActorID, frankID)
	}
	if setupNode.NextNodeID == nil || *setupNode.NextNodeID != reviewNode.ID {
		t.Fatalf("setup next_node_id = %v, want review node %s", setupNode.NextNodeID, reviewNode.ID)
	}
	if reviewNode.RejectNodeID == nil || *reviewNode.RejectNodeID != setupNode.ID {
		t.Fatalf("review reject_node_id = %v, want setup node %s", reviewNode.RejectNodeID, setupNode.ID)
	}
	if reviewNode.NextNodeID == nil || *reviewNode.NextNodeID != mergeNode.ID {
		t.Fatalf("review next_node_id = %v, want merge node %s", reviewNode.NextNodeID, mergeNode.ID)
	}
	if mergeNode.NodeType != "merge" {
		t.Fatalf("merge node_type = %q, want merge", mergeNode.NodeType)
	}
	if mergeNode.NextNodeID != nil {
		t.Fatalf("merge next_node_id = %v, want nil for terminal completion", mergeNode.NextNodeID)
	}

	allTasks, err := taskRepo.ListByProject(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListByProject bootstrap tasks: %v", err)
	}
	if len(allTasks) != len(bootstrapSetupTaskSpecs)+1 {
		t.Fatalf("project bootstrap task count = %d, want %d", len(allTasks), len(bootstrapSetupTaskSpecs)+1)
	}

	childIDs := taskdecomp.ParseChildTaskIDs(bootstrapTask.Metadata)
	if len(childIDs) != len(bootstrapSetupTaskSpecs) {
		t.Fatalf("bootstrap child_task_ids len = %d, want %d", len(childIDs), len(bootstrapSetupTaskSpecs))
	}

	expectedBySlug := make(map[string]bootstrapSetupTaskSpec, len(bootstrapSetupTaskSpecs))
	for _, spec := range bootstrapSetupTaskSpecs {
		expectedBySlug[spec.Slug] = spec
	}

	seen := make(map[string]repo.ProjectTask, len(bootstrapSetupTaskSpecs))
	for _, taskRecord := range allTasks {
		metadata := projectTaskMetadata(t, taskRecord.Metadata)
		if bootstrapGate, _ := metadata["bootstrap_gate"].(bool); bootstrapGate {
			continue
		}
		if setupTask, _ := metadata["bootstrap_setup_task"].(bool); !setupTask {
			t.Fatalf("bootstrap child metadata missing bootstrap_setup_task: %s", string(taskRecord.Metadata))
		}
		slug := strings.TrimSpace(valueAsString(metadata["bootstrap_step_slug"]))
		spec, ok := expectedBySlug[slug]
		if !ok {
			t.Fatalf("unexpected bootstrap step slug %q", slug)
		}
		if taskRecord.Title != spec.Title {
			t.Fatalf("bootstrap task %q title = %q, want %q", slug, taskRecord.Title, spec.Title)
		}
		if taskRecord.WorkStatus != "draft" {
			t.Fatalf("bootstrap task %q work_status = %q, want draft", slug, taskRecord.WorkStatus)
		}
		if taskRecord.FlowTemplateID == nil || *taskRecord.FlowTemplateID != template.ID {
			t.Fatalf("bootstrap task %q flow_template_id = %v, want %s", slug, taskRecord.FlowTemplateID, template.ID)
		}
		if parentID := taskdecomp.ParseParentTaskID(taskRecord.Metadata); parentID != bootstrapTask.ID {
			t.Fatalf("bootstrap task %q parent_task_id = %s, want %s", slug, parentID, bootstrapTask.ID)
		}
		seen[slug] = taskRecord
	}
	if len(seen) != len(bootstrapSetupTaskSpecs) {
		t.Fatalf("bootstrap child task count = %d, want %d", len(seen), len(bootstrapSetupTaskSpecs))
	}
	if signoff := seen["record-frank-sign-off"]; signoff.AssignedAgentID == nil || *signoff.AssignedAgentID != frankID {
		t.Fatalf("sign-off assigned_agent_id = %v, want Frank %s", signoff.AssignedAgentID, frankID)
	}
	if bind := seen["bind-repo-environment"]; bind.AssignedAgentID == nil || *bind.AssignedAgentID != loriID {
		t.Fatalf("bind-repo assigned_agent_id = %v, want Lori %s", bind.AssignedAgentID, loriID)
	}
}

func TestProjectServiceCreateBootstrapTaskTreeSurvivesProjectSessionRotation(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)
	orgRepo := repo.NewOrgRepo(pool)
	taskRepo := repo.NewProjectTaskRepo(pool)
	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})
	chatSvc, err := chat.NewService(chat.Options{Pool: pool, Events: bus})
	if err != nil {
		t.Fatalf("chat.NewService: %v", err)
	}

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "proj-svc-bootstrap-rotate-" + uuid.NewString()[:8],
		DisplayName: "Bootstrap Rotation Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	created, err := svc.Create(ctx, CreateProjectRequest{
		OrganizationID: org.ID,
		Slug:           "bootstrap-rotation-project",
		DisplayName:    "Bootstrap Rotation Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	beforeTasks, err := taskRepo.ListByProject(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListByProject before rotation: %v", err)
	}

	firstSession, err := chatSvc.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        created.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession first: %v", err)
	}
	if err := chatSvc.CloseSession(ctx, firstSession.ID); err != nil {
		t.Fatalf("CloseSession first: %v", err)
	}
	secondSession, err := chatSvc.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        created.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession second: %v", err)
	}
	if secondSession.ID == firstSession.ID {
		t.Fatalf("rotated session id = %s, want a fresh session id", secondSession.ID)
	}

	afterTasks, err := taskRepo.ListByProject(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListByProject after rotation: %v", err)
	}
	if len(afterTasks) != len(beforeTasks) {
		t.Fatalf("bootstrap task count after rotation = %d, want %d", len(afterTasks), len(beforeTasks))
	}

	beforeBySlug := bootstrapTasksBySlug(t, beforeTasks)
	afterBySlug := bootstrapTasksBySlug(t, afterTasks)
	if len(afterBySlug) != len(beforeBySlug) {
		t.Fatalf("bootstrap task slugs after rotation = %d, want %d", len(afterBySlug), len(beforeBySlug))
	}
	for slug, beforeTask := range beforeBySlug {
		afterTask, ok := afterBySlug[slug]
		if !ok {
			t.Fatalf("missing bootstrap task %q after session rotation", slug)
		}
		if afterTask.ID != beforeTask.ID {
			t.Fatalf("bootstrap task %q id changed from %s to %s across session rotation", slug, beforeTask.ID, afterTask.ID)
		}
		if afterTask.Title != beforeTask.Title {
			t.Fatalf("bootstrap task %q title changed from %q to %q across session rotation", slug, beforeTask.Title, afterTask.Title)
		}
	}
}

func TestProjectServicePauseResumePersistsStateAndPublishesEvents(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)
	orgID, projectID := seedProject(t, ctx, pool)

	pausedByID := uuid.New()
	paused, err := svc.Pause(ctx, orgID, projectID, PauseProjectRequest{
		Reason:       "operator pause",
		Metadata:     json.RawMessage(`{"source":"integration-test"}`),
		PausedByType: "human_user",
		PausedByID:   pausedByID,
	})
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}

	pauseState := projectpause.Parse(paused.Settings)
	if !pauseState.IsPaused {
		t.Fatal("pause state is_paused = false, want true")
	}
	if pauseState.Reason != "operator pause" {
		t.Fatalf("pause reason = %q, want %q", pauseState.Reason, "operator pause")
	}

	resumed, err := svc.Resume(ctx, orgID, projectID, "human_user", pausedByID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if projectpause.Parse(resumed.Settings).IsPaused {
		t.Fatal("pause state after resume = true, want false")
	}
}

func TestProjectServiceDeleteActiveTasksBlocked(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)
	orgID, projectID := seedProject(t, ctx, pool)
	ensureProjectTaskTable(t, ctx, pool)

	if _, err := pool.Exec(ctx, `
		INSERT INTO project_task (
			organization_id,
			project_id,
			task_number,
			title,
			work_status,
			created_by_type,
			metadata
		)
		VALUES ($1, $2, 1, 'active task', 'draft', 'system', '{}'::jsonb)
	`, orgID, projectID); err != nil {
		t.Fatalf("insert active project_task row: %v", err)
	}

	err := svc.Delete(ctx, orgID, projectID)
	if !errors.Is(err, ErrProjectHasActiveTasks) {
		t.Fatalf("Delete err = %v, want ErrProjectHasActiveTasks", err)
	}
}

func TestProjectServiceDeleteCompletedTasksCleansHistoricalReferencesAndPublishesSingleEvent(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)
	projectRepo := repo.NewProjectRepo(pool)
	invocationRepo := repo.NewModelInvocationRepo(pool)

	orgID, projectID := seedProject(t, ctx, pool)
	doneTaskID := seedProjectTask(t, ctx, pool, orgID, projectID, 1, "done")
	_ = seedProjectTask(t, ctx, pool, orgID, projectID, 2, "cancelled")
	invocationID := seedHistoricalModelInvocation(t, ctx, pool, orgID, projectID, doneTaskID)

	if err := svc.Delete(ctx, orgID, projectID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := projectRepo.GetByID(ctx, projectID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("GetByID after delete err = %v, want repo.ErrNotFound", err)
	}

	invocation, err := invocationRepo.GetByID(ctx, invocationID)
	if err != nil {
		t.Fatalf("GetByID invocation: %v", err)
	}
	if invocation.ProjectID != nil {
		t.Fatalf("invocation project_id = %v, want nil", invocation.ProjectID)
	}
	if invocation.ProjectTaskID != nil {
		t.Fatalf("invocation project_task_id = %v, want nil", invocation.ProjectTaskID)
	}

	var deletedCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'project.deleted'
		  AND payload ->> 'project_id' = $2
	`, orgID, projectID.String()).Scan(&deletedCount); err != nil {
		t.Fatalf("count project.deleted: %v", err)
	}
	if deletedCount != 1 {
		t.Fatalf("project.deleted count = %d, want 1", deletedCount)
	}
}

func TestProjectServiceDeleteRemovesProjectAndTaskScopedChatSessions(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)
	sessionRepo := repo.NewChatSessionRepo(pool)

	orgID, projectID := seedProject(t, ctx, pool)
	taskID := seedProjectTask(t, ctx, pool, orgID, projectID, 1, "done")

	projectSessionID := seedScopedChatSession(t, ctx, pool, orgID, "project", projectID)
	taskSessionID := seedScopedChatSession(t, ctx, pool, orgID, "project_task", taskID)
	orgSessionID := seedScopedChatSession(t, ctx, pool, orgID, "organization", orgID)

	if err := svc.Delete(ctx, orgID, projectID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := sessionRepo.GetByID(ctx, projectSessionID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("project-scoped session err = %v, want repo.ErrNotFound", err)
	}
	if _, err := sessionRepo.GetByID(ctx, taskSessionID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("task-scoped session err = %v, want repo.ErrNotFound", err)
	}
	if _, err := sessionRepo.GetByID(ctx, orgSessionID); err != nil {
		t.Fatalf("organization-scoped session should remain, got err: %v", err)
	}
}

func TestProjectServiceArchiveClosesProjectAndTaskScopedChatSessions(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)
	projectRepo := repo.NewProjectRepo(pool)
	sessionRepo := repo.NewChatSessionRepo(pool)
	messageRepo := repo.NewChatMessageRepo(pool)

	orgID, projectID := seedProject(t, ctx, pool)
	taskID := seedProjectTask(t, ctx, pool, orgID, projectID, 1, "done")

	projectSessionID := seedScopedChatSession(t, ctx, pool, orgID, "project", projectID)
	taskSessionID := seedScopedChatSession(t, ctx, pool, orgID, "project_task", taskID)
	orgSessionID := seedScopedChatSession(t, ctx, pool, orgID, "organization", orgID)

	seedScopedChatMessage(t, ctx, pool, projectSessionID, "project transcript")
	seedScopedChatMessage(t, ctx, pool, taskSessionID, "task transcript")

	archived, err := svc.Archive(ctx, orgID, projectID)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if archived.Status != "archived" {
		t.Fatalf("archive status = %q, want archived", archived.Status)
	}

	storedProject, err := projectRepo.GetByID(ctx, projectID)
	if err != nil {
		t.Fatalf("GetByID project: %v", err)
	}
	if storedProject.Status != "archived" {
		t.Fatalf("stored project status = %q, want archived", storedProject.Status)
	}

	projectSession, err := sessionRepo.GetByID(ctx, projectSessionID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	if projectSession.Status != "closed" || projectSession.ClosedAt == nil {
		t.Fatalf("project session = %+v, want status=closed with closed_at", projectSession)
	}

	taskSession, err := sessionRepo.GetByID(ctx, taskSessionID)
	if err != nil {
		t.Fatalf("GetByID task session: %v", err)
	}
	if taskSession.Status != "closed" || taskSession.ClosedAt == nil {
		t.Fatalf("task session = %+v, want status=closed with closed_at", taskSession)
	}

	orgSession, err := sessionRepo.GetByID(ctx, orgSessionID)
	if err != nil {
		t.Fatalf("GetByID organization session: %v", err)
	}
	if orgSession.Status != "active" {
		t.Fatalf("organization session status = %q, want active", orgSession.Status)
	}

	projectMessages, err := messageRepo.ListBySession(ctx, projectSessionID)
	if err != nil {
		t.Fatalf("ListBySession project messages: %v", err)
	}
	if len(projectMessages) != 1 || projectMessages[0].Content != "project transcript" {
		t.Fatalf("project messages = %+v, want preserved transcript", projectMessages)
	}

	taskMessages, err := messageRepo.ListBySession(ctx, taskSessionID)
	if err != nil {
		t.Fatalf("ListBySession task messages: %v", err)
	}
	if len(taskMessages) != 1 || taskMessages[0].Content != "task transcript" {
		t.Fatalf("task messages = %+v, want preserved transcript", taskMessages)
	}

	var activeScopedCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM chat_session
		WHERE status = 'active'
		  AND (
			(scope_type = 'project' AND scope_id = $1)
			OR (scope_type = 'project_task' AND scope_id IN (
				SELECT id
				FROM project_task
				WHERE project_id = $1
			))
		  )
	`, projectID).Scan(&activeScopedCount); err != nil {
		t.Fatalf("count active scoped sessions: %v", err)
	}
	if activeScopedCount != 0 {
		t.Fatalf("active scoped sessions = %d, want 0", activeScopedCount)
	}
}

func seedStarterAgent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, displayName, agentType string) uuid.UUID {
	t.Helper()
	created, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          displayName,
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "Bootstrap starter",
		OperatorInstructions: "",
		AgentType:            agentType,
		IsStarterTrio:        true,
		PrivateMemory:        false,
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create starter agent %s: %v", displayName, err)
	}
	return created.ID
}

func TestProjectServiceUpdateFlowTemplateInUseCreatesNewVersion(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)
	orgID, projectID := seedProject(t, ctx, pool)
	templateRepo := repo.NewFlowTemplateRepo(pool)
	ensureProjectTaskTable(t, ctx, pool)

	template, err := svc.CreateFlowTemplate(ctx, CreateFlowTemplateRequest{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "deploy-flow",
		DisplayName:    "Deploy Flow",
		Description:    "v1",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateFlowTemplate: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO project_task (
			organization_id,
			project_id,
			task_number,
			title,
			flow_template_id,
			work_status,
			created_by_type,
			metadata
		)
		VALUES ($1, $2, 1, 'active task', $3, 'in_progress', 'system', '{}'::jsonb)
	`, orgID, projectID, template.ID); err != nil {
		t.Fatalf("insert in-use project_task row: %v", err)
	}

	newName := "Deploy Flow v2"
	newSlug := "deploy-flow-v2"
	updated, err := svc.UpdateFlowTemplate(ctx, orgID, template.ID, UpdateFlowTemplateRequest{
		DisplayName:   &newName,
		Slug:          &newSlug,
		UpdatedByType: "system",
	})
	if err != nil {
		t.Fatalf("UpdateFlowTemplate: %v", err)
	}
	if updated.ID == template.ID {
		t.Fatalf("updated template id should change when in use")
	}
	if updated.Version != template.Version+1 {
		t.Fatalf("updated version = %d, want %d", updated.Version, template.Version+1)
	}
	if updated.Slug != newSlug {
		t.Fatalf("updated slug = %q, want %q", updated.Slug, newSlug)
	}

	oldRow, err := templateRepo.GetByID(ctx, template.ID)
	if err != nil {
		t.Fatalf("GetByID old template: %v", err)
	}
	if oldRow.IsCurrent {
		t.Fatalf("old template is_current = true, want false")
	}
}

func TestProjectServiceUpdateFlowTemplateNotInUseUpdatesInPlace(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)
	orgID, projectID := seedProject(t, ctx, pool)
	templateRepo := repo.NewFlowTemplateRepo(pool)

	template, err := svc.CreateFlowTemplate(ctx, CreateFlowTemplateRequest{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "review-flow",
		DisplayName:    "Review Flow",
		Description:    "v1",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateFlowTemplate: %v", err)
	}

	newName := "Review Flow Updated"
	updated, err := svc.UpdateFlowTemplate(ctx, orgID, template.ID, UpdateFlowTemplateRequest{
		DisplayName:   &newName,
		UpdatedByType: "system",
	})
	if err != nil {
		t.Fatalf("UpdateFlowTemplate: %v", err)
	}
	if updated.ID != template.ID {
		t.Fatalf("updated template id = %s, want %s", updated.ID, template.ID)
	}

	all, err := templateRepo.ListCurrent(ctx, &orgID, &projectID)
	if err != nil {
		t.Fatalf("ListCurrent: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("current template count = %d, want 1", len(all))
	}
	if all[0].DisplayName != newName {
		t.Fatalf("updated display_name = %q, want %q", all[0].DisplayName, newName)
	}
}

func TestProjectServiceCreateListAndDeleteSchedule(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)
	orgID, projectA := seedProject(t, ctx, pool)
	_, projectB := seedProjectWithSlug(t, ctx, pool, orgID, "sched-project-b")

	templateA, err := svc.CreateFlowTemplate(ctx, CreateFlowTemplateRequest{
		OrganizationID: &orgID,
		ProjectID:      &projectA,
		Slug:           "schedule-flow-a",
		DisplayName:    "Schedule Flow A",
		Description:    "A",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateFlowTemplate A: %v", err)
	}

	schedule, err := svc.CreateSchedule(ctx, CreateScheduleRequest{
		OrganizationID: orgID,
		ProjectID:      projectA,
		FlowTemplateID: templateA.ID,
		DisplayName:    "Daily Trigger",
		CronExpression: "0 9 * * 1-5",
		OverlapPolicy:  "skip",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	if schedule.NextFireAt == nil {
		t.Fatal("next_fire_at is nil, want computed value")
	}

	schedulesA, err := svc.ListSchedules(ctx, projectA)
	if err != nil {
		t.Fatalf("ListSchedules project A: %v", err)
	}
	if len(schedulesA) != 1 {
		t.Fatalf("project A schedule count = %d, want 1", len(schedulesA))
	}

	schedulesB, err := svc.ListSchedules(ctx, projectB)
	if err != nil {
		t.Fatalf("ListSchedules project B: %v", err)
	}
	if len(schedulesB) != 0 {
		t.Fatalf("project B schedule count = %d, want 0", len(schedulesB))
	}

	if err := svc.DeleteSchedule(ctx, schedule.ID); err != nil {
		t.Fatalf("DeleteSchedule: %v", err)
	}
	if _, err := svc.GetSchedule(ctx, schedule.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("GetSchedule after delete err = %v, want repo.ErrNotFound", err)
	}
}

func TestProjectServiceEnableDisableSchedule(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	svc := newIntegrationService(t, pool)
	orgID, projectID := seedProject(t, ctx, pool)

	template, err := svc.CreateFlowTemplate(ctx, CreateFlowTemplateRequest{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "schedule-toggle-flow",
		DisplayName:    "Schedule Toggle Flow",
		Description:    "toggle",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateFlowTemplate: %v", err)
	}

	schedule, err := svc.CreateSchedule(ctx, CreateScheduleRequest{
		OrganizationID: orgID,
		ProjectID:      projectID,
		FlowTemplateID: template.ID,
		DisplayName:    "Toggle Trigger",
		CronExpression: "*/5 * * * *",
		OverlapPolicy:  "skip",
		IsEnabled:      false,
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}

	enabled, err := svc.EnableSchedule(ctx, schedule.ID)
	if err != nil {
		t.Fatalf("EnableSchedule: %v", err)
	}
	if !enabled.IsEnabled {
		t.Fatalf("enabled flag = false, want true")
	}
	if enabled.NextFireAt == nil {
		t.Fatal("next_fire_at = nil, want value")
	}

	disabled, err := svc.DisableSchedule(ctx, schedule.ID)
	if err != nil {
		t.Fatalf("DisableSchedule: %v", err)
	}
	if disabled.IsEnabled {
		t.Fatalf("enabled flag = true, want false")
	}
	if disabled.NextFireAt != nil {
		t.Fatalf("next_fire_at = %v, want nil", *disabled.NextFireAt)
	}
}

func newIntegrationService(t *testing.T, pool *pgxpool.Pool) ProjectService {
	t.Helper()
	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})
	svc, err := NewService(Options{Pool: pool, Events: bus})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func seedProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	orgRepo := repo.NewOrgRepo(pool)
	projectRepo := repo.NewProjectRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{Slug: "proj-svc-org-" + uuid.NewString()[:8], DisplayName: "Project Service Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "proj-svc-" + uuid.NewString()[:8],
		DisplayName:    "Project Service Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return org.ID, project.ID
}

func bootstrapTasksBySlug(t *testing.T, tasks []repo.ProjectTask) map[string]repo.ProjectTask {
	t.Helper()

	bySlug := make(map[string]repo.ProjectTask)
	for _, taskRecord := range tasks {
		metadata := projectTaskMetadata(t, taskRecord.Metadata)
		slug := strings.TrimSpace(valueAsString(metadata["bootstrap_step_slug"]))
		if slug == "" {
			if bootstrapGate, _ := metadata["bootstrap_gate"].(bool); bootstrapGate {
				slug = "bootstrap-governance-gate"
			} else {
				continue
			}
		}
		bySlug[slug] = taskRecord
	}
	return bySlug
}

func projectTaskMetadata(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()

	if len(raw) == 0 {
		return map[string]any{}
	}
	var metadata map[string]any
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatalf("unmarshal project task metadata: %v", err)
	}
	return metadata
}

func valueAsString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func seedProjectWithSlug(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, slug string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	projectRepo := repo.NewProjectRepo(pool)

	project, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: orgID,
		Slug:           slug,
		DisplayName:    "Project " + slug,
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project (%s): %v", slug, err)
	}
	return orgID, project.ID
}

func seedProjectTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, projectID uuid.UUID, taskNumber int, workStatus string) uuid.UUID {
	t.Helper()

	var flowTemplateID *uuid.UUID
	switch workStatus {
	case "queued", "in_progress", "review", "done":
		id := seedProjectFlowTemplate(t, ctx, pool, orgID, projectID)
		flowTemplateID = &id
	}

	var taskID uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO project_task (
			organization_id,
			project_id,
			task_number,
			title,
			flow_template_id,
			work_status,
			created_by_type,
			metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'system', '{}'::jsonb)
		RETURNING id
	`, orgID, projectID, taskNumber, workStatus+" task", flowTemplateID, workStatus).Scan(&taskID)
	if err != nil {
		t.Fatalf("insert project_task row: %v", err)
	}
	return taskID
}

func seedProjectFlowTemplate(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, projectID uuid.UUID) uuid.UUID {
	t.Helper()

	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "project-delete-flow-" + uuid.NewString()[:8],
		DisplayName:    "Project Delete Flow",
		Description:    "Delete-path test flow template",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	return template.ID
}

func seedHistoricalModelInvocation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, projectID, taskID uuid.UUID) uuid.UUID {
	t.Helper()

	providerRepo := repo.NewModelProviderRepo(pool)
	connectionRepo := repo.NewProviderConnectionRepo(pool)
	invocationRepo := repo.NewModelInvocationRepo(pool)

	provider, err := providerRepo.Create(ctx, repo.ModelProvider{
		Slug:        "project-delete-provider-" + uuid.NewString()[:8],
		DisplayName: "Project Delete Provider",
		APIBaseURL:  "https://provider.example/v1",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	connection, err := connectionRepo.Create(ctx, repo.ProviderConnection{
		OrganizationID: orgID,
		ProviderID:     provider.ID,
		DisplayName:    "Project Delete Connection",
		APIKeyRef:      "ref:project-delete-" + uuid.NewString()[:8],
		IsEnabled:      true,
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}

	profileID := "standard"
	invocation, err := invocationRepo.Create(ctx, repo.ModelInvocation{
		OrganizationID:       orgID,
		ModelProviderID:      provider.ID,
		ProviderConnectionID: &connection.ID,
		ModelProfileID:       &profileID,
		InvocationPurpose:    "agent_turn",
		Status:               "completed",
		ModelName:            "gpt-4o-mini",
		ProjectID:            &projectID,
		ProjectTaskID:        &taskID,
	})
	if err != nil {
		t.Fatalf("create model invocation: %v", err)
	}
	return invocation.ID
}

func seedScopedChatSession(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, scopeType string, scopeID uuid.UUID) uuid.UUID {
	t.Helper()

	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: orgID,
		ScopeType:      scopeType,
		ScopeID:        scopeID,
		Mode:           "sync",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create %s chat session: %v", scopeType, err)
	}
	return session.ID
}

func seedScopedChatMessage(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID uuid.UUID, content string) uuid.UUID {
	t.Helper()

	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: sessionID,
		Role:      "user",
		Content:   content,
		Status:    "final",
		Metadata:  json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create scoped chat message: %v", err)
	}
	return message.ID
}

func ensureProjectTaskTable(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS project_task (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			project_id uuid NOT NULL,
			flow_template_id uuid,
			work_status text NOT NULL
		)
	`); err != nil {
		t.Fatalf("create test project_task table: %v", err)
	}
}
