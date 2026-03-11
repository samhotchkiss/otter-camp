//go:build integration

package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestDeliveryServiceTriggerContinuousDeliveryLockContention(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	_, project := seedDeliveryOrgProject(t, ctx, pool)
	environment := seedDeliveryEnvironment(t, ctx, pool, project.ID, "production", "continuous")

	service, err := NewService(Options{Pool: pool})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	lockAcquired := make(chan struct{})
	releaseLock := make(chan struct{})
	service.lockAcquiredHook = func() {
		close(lockAcquired)
		<-releaseLock
	}

	var firstErr error
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		_, firstErr = service.TriggerContinuousDelivery(ctx, environment.ID, "abc123", 0)
	}()

	select {
	case <-lockAcquired:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first call to acquire lock")
	}

	var secondErr error
	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		_, secondErr = service.TriggerContinuousDelivery(ctx, environment.ID, "abc123", 0)
	}()

	select {
	case <-secondDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second call under lock contention")
	}

	close(releaseLock)
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first call completion")
	}

	if firstErr != nil {
		t.Fatalf("first TriggerContinuousDelivery: %v", firstErr)
	}
	if secondErr != nil {
		t.Fatalf("second TriggerContinuousDelivery: %v", secondErr)
	}

	var (
		totalJobs       int
		immediateJobs   int
		delayedRetryJob int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE run_after <= created_at + interval '5 seconds'),
			COUNT(*) FILTER (WHERE run_after >= created_at + interval '25 seconds')
		FROM job_queue
		WHERE job_type = 'push_execute'
	`).Scan(&totalJobs, &immediateJobs, &delayedRetryJob); err != nil {
		t.Fatalf("query push_execute jobs: %v", err)
	}

	if totalJobs != 2 {
		t.Fatalf("push_execute job count = %d, want 2", totalJobs)
	}
	if immediateJobs != 1 {
		t.Fatalf("immediate push_execute jobs = %d, want 1", immediateJobs)
	}
	if delayedRetryJob != 1 {
		t.Fatalf("delayed push_execute jobs = %d, want 1", delayedRetryJob)
	}
}

func TestDeliveryServiceCreateDeployTaskSetsEnvironmentDeployTaskID(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedDeliveryOrgProject(t, ctx, pool)
	pmUser := seedDeliveryUser(t, ctx, pool, org.ID, "pm-user", "admin")
	pmAgent := seedDeliveryAgent(t, ctx, pool, org.ID, "PM Agent", pmUser.ID)
	assignDeliveryPM(t, ctx, pool, pmAgent.ID, project.ID)

	environment := seedDeliveryEnvironment(t, ctx, pool, project.ID, "production", "gated")
	bus := eventbus.New(pool, nil, eventbus.Config{})
	service, err := NewService(Options{Pool: pool, Events: bus})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	deployTask, err := service.CreateDeployTask(ctx, environment.ID, "deadbeef")
	if err != nil {
		t.Fatalf("CreateDeployTask: %v", err)
	}

	if deployTask.WorkStatus != "queued" {
		t.Fatalf("deploy task work_status = %q, want queued", deployTask.WorkStatus)
	}
	if deployTask.AssignedAgentID == nil || *deployTask.AssignedAgentID != pmAgent.ID {
		t.Fatalf("deploy task assigned_agent_id = %v, want %s", deployTask.AssignedAgentID, pmAgent.ID)
	}
	if !strings.HasPrefix(deployTask.Title, "Deploy production-") || !strings.HasSuffix(deployTask.Title, "to https://git.example.com/org/repo.git") {
		t.Fatalf("deploy task title = %q, want prefix/suffix format", deployTask.Title)
	}

	taskRecord, err := repo.NewProjectTaskRepo(pool).GetByID(ctx, deployTask.ID)
	if err != nil {
		t.Fatalf("GetByID deploy task: %v", err)
	}
	if taskRecord.WorkStatus != "queued" {
		t.Fatalf("stored deploy task work_status = %q, want queued", taskRecord.WorkStatus)
	}

	environmentRecord, err := repo.NewProjectEnvironmentRepo(pool).GetByID(ctx, environment.ID)
	if err != nil {
		t.Fatalf("GetByID environment: %v", err)
	}
	if environmentRecord.DeployTaskID == nil || *environmentRecord.DeployTaskID != deployTask.ID {
		t.Fatalf("environment deploy_task_id = %v, want %s", environmentRecord.DeployTaskID, deployTask.ID)
	}
	var statusEventCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM project_task_event
		WHERE task_id = $1
		  AND event_type = 'status.changed'
		  AND payload->>'to_status' = 'queued'
	`, deployTask.ID).Scan(&statusEventCount); err != nil {
		t.Fatalf("count queued status events: %v", err)
	}
	if statusEventCount < 1 {
		t.Fatalf("queued status events = %d, want >= 1", statusEventCount)
	}
}

func TestDeliveryServiceCreateDeployTaskRequiresCanonicalTaskService(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedDeliveryOrgProject(t, ctx, pool)
	pmUser := seedDeliveryUser(t, ctx, pool, org.ID, "pm-user", "admin")
	pmAgent := seedDeliveryAgent(t, ctx, pool, org.ID, "PM Agent", pmUser.ID)
	assignDeliveryPM(t, ctx, pool, pmAgent.ID, project.ID)
	environment := seedDeliveryEnvironment(t, ctx, pool, project.ID, "production", "gated")

	service, err := NewService(Options{Pool: pool})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	if _, err := service.CreateDeployTask(ctx, environment.ID, "deadbeef"); !errors.Is(err, ErrCanonicalTaskServiceRequired) {
		t.Fatalf("CreateDeployTask err = %v, want ErrCanonicalTaskServiceRequired", err)
	}
}

func TestDeliveryServiceRecordDeploymentUpdatesCommitAndTimestamp(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	_, project := seedDeliveryOrgProject(t, ctx, pool)
	environment := seedDeliveryEnvironment(t, ctx, pool, project.ID, "production", "continuous")

	fakeClock := clock.NewFake(time.Date(2026, 2, 24, 14, 0, 0, 0, time.UTC))
	service, err := NewService(Options{
		Pool:  pool,
		Clock: fakeClock,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	if _, err := service.RecordDeployment(ctx, environment.ID, "sha-1", nil); err != nil {
		t.Fatalf("RecordDeployment first: %v", err)
	}
	fakeClock.Advance(15 * time.Minute)
	recorded, err := service.RecordDeployment(ctx, environment.ID, "sha-2", nil)
	if err != nil {
		t.Fatalf("RecordDeployment second: %v", err)
	}

	if recorded.LastDeployedCommit == nil || *recorded.LastDeployedCommit != "sha-2" {
		t.Fatalf("last_deployed_commit = %v, want sha-2", recorded.LastDeployedCommit)
	}
	if recorded.LastDeployedAt == nil || !recorded.LastDeployedAt.Equal(fakeClock.Now().UTC()) {
		t.Fatalf("last_deployed_at = %v, want %v", recorded.LastDeployedAt, fakeClock.Now().UTC())
	}
}

func TestDeliveryServiceHandlePushFailureEscalatesAfterThreeAttempts(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedDeliveryOrgProject(t, ctx, pool)
	admin := seedDeliveryUser(t, ctx, pool, org.ID, "admin-user", "admin")
	environment := seedDeliveryEnvironment(t, ctx, pool, project.ID, "production", "continuous")

	service, err := NewService(Options{Pool: pool})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	if err := service.HandlePushFailure(ctx, environment.ID, "sha", 0, errors.New("network")); err != nil {
		t.Fatalf("HandlePushFailure attempt 0: %v", err)
	}
	if err := service.HandlePushFailure(ctx, environment.ID, "sha", 1, errors.New("network")); err != nil {
		t.Fatalf("HandlePushFailure attempt 1: %v", err)
	}
	if err := service.HandlePushFailure(ctx, environment.ID, "sha", 2, errors.New("network")); err != nil {
		t.Fatalf("HandlePushFailure attempt 2: %v", err)
	}

	var pushJobs int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'push_execute'
	`).Scan(&pushJobs); err != nil {
		t.Fatalf("count push_execute jobs: %v", err)
	}
	if pushJobs != 2 {
		t.Fatalf("push_execute jobs = %d, want 2", pushJobs)
	}

	var (
		alertCount int
	)
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM inbox_item
		WHERE source_project_id = $1
	`, project.ID).Scan(&alertCount); err != nil {
		t.Fatalf("query inbox alerts: %v", err)
	}
	if alertCount != 1 {
		t.Fatalf("alert inbox count = %d, want 1", alertCount)
	}

	var (
		alertItemType string
		targetUserID  *uuid.UUID
	)
	if err := pool.QueryRow(ctx, `
		SELECT item_type, target_user_id
		FROM inbox_item
		WHERE source_project_id = $1
		LIMIT 1
	`, project.ID).Scan(&alertItemType, &targetUserID); err != nil {
		t.Fatalf("fetch alert item: %v", err)
	}
	if alertItemType != "system_alert" {
		t.Fatalf("alert item_type = %q, want system_alert", alertItemType)
	}
	if targetUserID == nil || *targetUserID != admin.ID {
		t.Fatalf("alert target_user_id = %v, want %s", targetUserID, admin.ID)
	}
}

func TestRollbackServiceInitiateRollbackQueuesCanonicalTask(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedDeliveryOrgProject(t, ctx, pool)
	pmUser := seedDeliveryUser(t, ctx, pool, org.ID, "rollback-pm", "admin")
	pmAgent := seedDeliveryAgent(t, ctx, pool, org.ID, "Rollback PM", pmUser.ID)
	assignDeliveryPM(t, ctx, pool, pmAgent.ID, project.ID)

	bus := eventbus.New(pool, nil, eventbus.Config{})
	service, err := NewRollbackService(RollbackServiceOptions{
		Pool:   pool,
		Git:    AllowAllCommitGitService{},
		Events: bus,
	})
	if err != nil {
		t.Fatalf("NewRollbackService: %v", err)
	}

	taskID, err := service.InitiateRollback(ctx, project.ID, "deadbeefcafebabe")
	if err != nil {
		t.Fatalf("InitiateRollback: %v", err)
	}

	taskRecord, err := repo.NewProjectTaskRepo(pool).GetByID(ctx, taskID)
	if err != nil {
		t.Fatalf("GetByID rollback task: %v", err)
	}
	if taskRecord.WorkStatus != "queued" {
		t.Fatalf("rollback task work_status = %q, want queued", taskRecord.WorkStatus)
	}
	if taskRecord.AssignedAgentID == nil || *taskRecord.AssignedAgentID != pmAgent.ID {
		t.Fatalf("rollback task assigned_agent_id = %v, want %s", taskRecord.AssignedAgentID, pmAgent.ID)
	}
	if taskRecord.BranchName == nil || !strings.HasPrefix(*taskRecord.BranchName, "rollback-") {
		t.Fatalf("rollback branch_name = %v, want rollback-*", taskRecord.BranchName)
	}

	var metadata map[string]any
	if err := json.Unmarshal(taskRecord.Metadata, &metadata); err != nil {
		t.Fatalf("unmarshal rollback task metadata: %v", err)
	}
	if metadata["task_type"] != deliveryTaskTypeDeploy {
		t.Fatalf("rollback task_type = %v, want %q", metadata["task_type"], deliveryTaskTypeDeploy)
	}
	if metadata["rollback"] != true {
		t.Fatalf("rollback metadata flag = %v, want true", metadata["rollback"])
	}
	if metadata["target_commit_sha"] != "deadbeefcafebabe" {
		t.Fatalf("target_commit_sha = %v, want deadbeefcafebabe", metadata["target_commit_sha"])
	}

	var statusEventCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM project_task_event
		WHERE task_id = $1
		  AND event_type = 'status.changed'
		  AND payload->>'to_status' = 'queued'
	`, taskID).Scan(&statusEventCount); err != nil {
		t.Fatalf("count rollback queued status events: %v", err)
	}
	if statusEventCount < 1 {
		t.Fatalf("rollback queued status events = %d, want >= 1", statusEventCount)
	}
}

func TestRollbackServiceInitiateRollbackRequiresCanonicalTaskService(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	_, project := seedDeliveryOrgProject(t, ctx, pool)
	service, err := NewRollbackService(RollbackServiceOptions{
		Pool: pool,
		Git:  AllowAllCommitGitService{},
	})
	if err != nil {
		t.Fatalf("NewRollbackService: %v", err)
	}

	if _, err := service.InitiateRollback(ctx, project.ID, "deadbeefcafebabe"); !errors.Is(err, ErrCanonicalTaskServiceRequired) {
		t.Fatalf("InitiateRollback err = %v, want ErrCanonicalTaskServiceRequired", err)
	}
}

func seedDeliveryOrgProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (repo.Organization, repo.Project) {
	t.Helper()
	orgRepo := repo.NewOrgRepo(pool)
	projectRepo := repo.NewProjectRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "delivery-org-" + uuid.NewString()[:8],
		DisplayName: "Delivery Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	project, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "delivery-project-" + uuid.NewString()[:8],
		DisplayName:    "Delivery Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "delivery-flow-" + uuid.NewString()[:8],
		DisplayName:    "Delivery Flow",
		Description:    "Flow template used for delivery lifecycle tasks",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project flow template: %v", err)
	}

	nodeRepo := repo.NewFlowNodeRepo(pool)
	workNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Delivery Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      5,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create delivery work node: %v", err)
	}
	reviewNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Delivery Review",
		NodeType:       "review",
		Position:       2,
		MaxVisits:      5,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create delivery review node: %v", err)
	}
	mergeNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Delivery Merge",
		NodeType:       "merge",
		Position:       3,
		MaxVisits:      5,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create delivery merge node: %v", err)
	}

	workNode.NextNodeID = &reviewNode.ID
	if _, err := nodeRepo.Update(ctx, workNode); err != nil {
		t.Fatalf("update delivery work next node: %v", err)
	}
	reviewNode.NextNodeID = &mergeNode.ID
	if _, err := nodeRepo.Update(ctx, reviewNode); err != nil {
		t.Fatalf("update delivery review next node: %v", err)
	}
	template.StartNodeID = &workNode.ID
	if _, err := repo.NewFlowTemplateRepo(pool).Update(ctx, template); err != nil {
		t.Fatalf("set delivery flow start node: %v", err)
	}

	project.DeployFlowTemplateID = &template.ID
	project, err = projectRepo.Update(ctx, project)
	if err != nil {
		t.Fatalf("set deploy flow template: %v", err)
	}
	return org, project
}

func seedDeliveryEnvironment(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID, name string, mode string) repo.ProjectEnvironment {
	t.Helper()
	remoteRepo := repo.NewProjectRemoteRepo(pool)
	environmentRepo := repo.NewProjectEnvironmentRepo(pool)

	remoteRecord, err := remoteRepo.Create(ctx, repo.ProjectRemote{
		ProjectID: projectID,
		Name:      "origin-" + uuid.NewString()[:8],
		URL:       "https://git.example.com/org/repo.git",
		IsDefault: true,
		Transport: "https",
	})
	if err != nil && !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("create remote: %v", err)
	}
	if errors.Is(err, repo.ErrConflict) {
		remoteRecord, err = remoteRepo.GetDefault(ctx, projectID)
		if err != nil {
			t.Fatalf("GetDefault remote after conflict: %v", err)
		}
	}

	environment, err := environmentRepo.Create(ctx, repo.ProjectEnvironment{
		ProjectID:    projectID,
		Name:         name + "-" + uuid.NewString()[:6],
		DeliveryMode: mode,
		RemoteID:     &remoteRecord.ID,
		TargetBranch: "main",
		IsActive:     true,
	})
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}

	return environment
}

func seedDeliveryUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, prefix, role string) repo.HumanUser {
	t.Helper()
	userRepo := repo.NewHumanUserRepo(pool)
	user, err := userRepo.Create(ctx, repo.HumanUser{
		OrganizationID: orgID,
		Email:          fmt.Sprintf("%s+%s@example.com", prefix, uuid.NewString()[:8]),
		DisplayName:    prefix,
		Role:           role,
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

func seedDeliveryAgent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, displayName string, createdByID uuid.UUID) repo.Agent {
	t.Helper()
	agentRepo := repo.NewAgentRepo(pool)
	agentRecord, err := agentRepo.Create(ctx, repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          displayName,
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		AgentType:            "pm",
		CreatedByType:        "human_user",
		CreatedByID:          createdByID,
		MemoryReadScopes:     []string{},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		OperatorInstructions: "",
		SystemPrompt:         "",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return agentRecord
}

func assignDeliveryPM(t *testing.T, ctx context.Context, pool *pgxpool.Pool, agentID, projectID uuid.UUID) {
	t.Helper()
	assignmentRepo := repo.NewAgentProjectAssignmentRepo(pool)
	if _, err := assignmentRepo.Assign(ctx, repo.AgentProjectAssignment{
		AgentID:        agentID,
		ProjectID:      projectID,
		Role:           "pm",
		AssignedByType: "system",
	}); err != nil {
		t.Fatalf("assign PM: %v", err)
	}
}
