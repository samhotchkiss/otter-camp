//go:build integration

package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/memory/compaction"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestTaskConsolidationTriggeredByTaskCompletedEvent(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := seedMemoryOrg(t, ctx, pool)
	project := seedMemoryProject(t, ctx, pool, org.ID)
	task := seedMemoryTask(t, ctx, pool, org.ID, project.ID)

	memoryRepo := repo.NewMemoryRepo(pool)
	_, _ = memoryRepo.Create(ctx, repo.Memory{OrganizationID: org.ID, ProjectID: &project.ID, ProjectTaskID: &task.ID, MemoryType: "episodic", Scope: "task", Content: "episode", ContentHash: uuid.NewString(), Confidence: 0.8, UtilityScore: 0.5, Status: "active", TrustTier: 0.8})
	semantic, _ := memoryRepo.Create(ctx, repo.Memory{OrganizationID: org.ID, ProjectID: &project.ID, ProjectTaskID: &task.ID, MemoryType: "semantic", Scope: "task", Content: "semantic", ContentHash: uuid.NewString(), Confidence: 0.8, UtilityScore: 0.5, Status: "active", TrustTier: 0.8})
	procedural, _ := memoryRepo.Create(ctx, repo.Memory{OrganizationID: org.ID, ProjectID: &project.ID, ProjectTaskID: &task.ID, MemoryType: "procedural", Scope: "task", Content: "procedural", ContentHash: uuid.NewString(), Confidence: 0.8, UtilityScore: 0.5, Status: "active", TrustTier: 0.8})
	preference, _ := memoryRepo.Create(ctx, repo.Memory{OrganizationID: org.ID, ProjectID: &project.ID, ProjectTaskID: &task.ID, MemoryType: "preference", Scope: "task", Content: "preference", ContentHash: uuid.NewString(), Confidence: 0.8, UtilityScore: 0.5, Status: "active", TrustTier: 0.8})
	_, _ = memoryRepo.Create(ctx, repo.Memory{OrganizationID: org.ID, ProjectID: &project.ID, ProjectTaskID: &task.ID, MemoryType: "entity_profile", Scope: "task", Content: "entity profile", ContentHash: uuid.NewString(), Confidence: 0.8, UtilityScore: 0.5, Status: "active", TrustTier: 0.8})

	consolidator, err := compaction.NewTaskConsolidator(compaction.TaskConsolidatorOptions{
		Pool:         pool,
		SummaryModel: staticTaskSummaryModel{text: "Execution summary"},
	})
	if err != nil {
		t.Fatalf("NewTaskConsolidator: %v", err)
	}

	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{PollInterval: 10 * time.Millisecond})
	enqueuer := &immediateConsolidationEnqueuer{consolidator: consolidator}
	consumer, err := NewEventConsumer(EventConsumerOptions{
		Pool:     pool,
		Events:   bus,
		Enqueuer: enqueuer,
	})
	if err != nil {
		t.Fatalf("NewEventConsumer: %v", err)
	}
	sub := consumer.SubscribeTaskCompleted(&org.ID)
	defer bus.Unsubscribe(sub)

	payload, _ := json.Marshal(map[string]any{
		"project_id": project.ID,
		"task_id":    task.ID,
	})
	if err := bus.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: org.ID,
		EventType:      "task.completed",
		ActorType:      "system",
		Payload:        payload,
	}); err != nil {
		t.Fatalf("publish task.completed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	semanticUpdated, err := memoryRepo.GetByID(ctx, semantic.ID)
	if err != nil {
		t.Fatalf("get semantic memory: %v", err)
	}
	if semanticUpdated.Scope != "project" || semanticUpdated.ProjectTaskID != nil {
		t.Fatalf("semantic memory promotion failed: scope=%s project_task_id=%v", semanticUpdated.Scope, semanticUpdated.ProjectTaskID)
	}

	for _, id := range []uuid.UUID{procedural.ID, preference.ID} {
		item, getErr := memoryRepo.GetByID(ctx, id)
		if getErr != nil {
			t.Fatalf("get promoted memory %s: %v", id, getErr)
		}
		if item.Scope != "project" || item.ProjectTaskID != nil {
			t.Fatalf("memory %s not promoted: scope=%s project_task_id=%v", id, item.Scope, item.ProjectTaskID)
		}
	}

	var executionSummaryCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM memory
		WHERE organization_id = $1
		  AND project_task_id = $2
		  AND memory_type = 'execution_summary'
	`, org.ID, task.ID).Scan(&executionSummaryCount); err != nil {
		t.Fatalf("count execution summary memories: %v", err)
	}
	if executionSummaryCount == 0 {
		t.Fatal("execution summary memory was not created")
	}

	var runCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM memory_compaction_run
		WHERE organization_id = $1
		  AND run_type = 'task_consolidation'
		  AND status = 'completed'
		  AND scope_context->>'task_id' = $2
	`, org.ID, task.ID.String()).Scan(&runCount); err != nil {
		t.Fatalf("count completed task consolidation runs: %v", err)
	}
	if runCount == 0 {
		t.Fatal("no completed task consolidation run found")
	}
}

type staticTaskSummaryModel struct {
	text string
}

func (m staticTaskSummaryModel) SynthesizeTaskSummary(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, []repo.Memory) (string, error) {
	return m.text, nil
}

type immediateConsolidationEnqueuer struct {
	consolidator *compaction.TaskConsolidator
}

func (e *immediateConsolidationEnqueuer) Enqueue(ctx context.Context, _ pgx.Tx, jobType string, _ int, payload any, _ *time.Time) (uuid.UUID, error) {
	if jobType != compaction.MemoryTaskConsolidationJobType {
		return uuid.New(), nil
	}
	typed, ok := payload.(compaction.TaskConsolidationPayload)
	if !ok {
		return uuid.Nil, fmt.Errorf("unexpected payload type %T", payload)
	}
	if err := e.consolidator.Consolidate(ctx, typed.OrganizationID, typed.ProjectID, typed.TaskID); err != nil {
		return uuid.Nil, err
	}
	return uuid.New(), nil
}

func seedMemoryOrg(t *testing.T, ctx context.Context, pool *pgxpool.Pool) repo.Organization {
	t.Helper()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{Slug: "memory-org-" + uuid.NewString()[:8], DisplayName: "Memory Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return org
}

func seedMemoryProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) repo.Project {
	t.Helper()
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: orgID,
		Slug:           "memory-project-" + uuid.NewString()[:8],
		DisplayName:    "Memory Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return project
}

func seedMemoryTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, projectID uuid.UUID) repo.ProjectTask {
	t.Helper()
	task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      projectID,
		Title:          "Memory task",
		WorkStatus:     "done",
		CreatedByType:  "system",
		CreatedByID:    nil,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task
}
