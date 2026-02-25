//go:build integration

package memory

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/memory/compaction"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestCompaction_TaskCompletion_ScopePromotion(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := seedMemoryOrg(t, ctx, pool)
	project := seedMemoryProject(t, ctx, pool, org.ID)
	task := seedMemoryTask(t, ctx, pool, org.ID, project.ID)
	memoryRepo := repo.NewMemoryRepo(pool)

	taskScoped, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: org.ID,
		ProjectTaskID:  &task.ID,
		MemoryType:     "semantic",
		Scope:          "task",
		Content:        "task scoped memory for scope promotion",
		ContentHash:    uuid.NewString(),
		Status:         "active",
		Confidence:     0.8,
		UtilityScore:   0.7,
		TrustTier:      0.8,
	})
	if err != nil {
		t.Fatalf("create task-scoped memory: %v", err)
	}

	consolidator, err := compaction.NewTaskConsolidator(compaction.TaskConsolidatorOptions{
		Pool:         pool,
		SummaryModel: staticTaskSummaryModel{text: "summary"},
	})
	if err != nil {
		t.Fatalf("NewTaskConsolidator: %v", err)
	}
	bus := eventbus.New(pool, nil, eventbus.Config{PollInterval: 10 * time.Millisecond})
	consumer, err := NewEventConsumer(EventConsumerOptions{
		Pool:     pool,
		Events:   bus,
		Enqueuer: &immediateConsolidationEnqueuer{consolidator: consolidator},
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

	promoted, err := memoryRepo.GetByID(ctx, taskScoped.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if promoted.Scope != "project" {
		t.Fatalf("scope = %q, want %q", promoted.Scope, "project")
	}
	if promoted.ProjectID == nil || *promoted.ProjectID != project.ID {
		t.Fatalf("project_id = %v, want %s", promoted.ProjectID, project.ID)
	}
}

func TestCompaction_EpisodicDistillation(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := seedMemoryOrg(t, ctx, pool)
	project := seedMemoryProject(t, ctx, pool, org.ID)
	task := seedMemoryTask(t, ctx, pool, org.ID, project.ID)
	memoryRepo := repo.NewMemoryRepo(pool)

	if _, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: org.ID,
		ProjectID:      &project.ID,
		ProjectTaskID:  &task.ID,
		MemoryType:     "episodic",
		Scope:          "task",
		Content:        "episodic source memory for distillation",
		ContentHash:    uuid.NewString(),
		Status:         "active",
		Confidence:     0.8,
		UtilityScore:   0.6,
		TrustTier:      0.8,
	}); err != nil {
		t.Fatalf("create episodic source memory: %v", err)
	}

	consolidator, err := compaction.NewTaskConsolidator(compaction.TaskConsolidatorOptions{
		Pool:         pool,
		SummaryModel: staticTaskSummaryModel{text: "distilled summary"},
	})
	if err != nil {
		t.Fatalf("NewTaskConsolidator: %v", err)
	}
	if err := consolidator.Consolidate(ctx, org.ID, project.ID, task.ID); err != nil {
		t.Fatalf("Consolidate: %v", err)
	}

	var summaryCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM memory
		WHERE organization_id = $1
		  AND project_task_id = $2
		  AND memory_type IN ('execution_summary', 'episodic_summary')
	`, org.ID, task.ID).Scan(&summaryCount); err != nil {
		t.Fatalf("count distilled summary memories: %v", err)
	}
	if summaryCount == 0 {
		t.Fatal("expected distilled summary memory row to be created")
	}
}

func TestCompaction_DecayHalfLife(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedMemoryOrg(t, ctx, pool)

	old := time.Now().UTC().Add(-31 * 24 * time.Hour)
	var seedID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO memory (
			organization_id,
			memory_type,
			scope,
			content,
			content_hash,
			confidence,
			utility_score,
			status,
			trust_tier,
			created_at,
			updated_at
		)
		VALUES ($1, 'episodic', 'org', $2, $3, 0.6, 0.5, 'active', 0.8, $4, $4)
		RETURNING id
	`, org.ID, "old episodic memory for decay half-life", uuid.NewString(), old).Scan(&seedID); err != nil {
		t.Fatalf("insert aged memory row: %v", err)
	}

	runRepo := repo.NewMemoryCompactionRunRepo(pool)
	run, err := runRepo.Create(ctx, repo.MemoryCompactionRun{
		OrganizationID: org.ID,
		RunType:        "sleep_reflection",
		Status:         "pending",
		ScopeContext:   `{"trigger":"half-life-test"}`,
	})
	if err != nil {
		t.Fatalf("create compaction run: %v", err)
	}

	reflector, err := compaction.NewSleepReflector(compaction.SleepReflectorOptions{
		Pool:         pool,
		Deduplicator: noopCandidateDeduplicator{},
	})
	if err != nil {
		t.Fatalf("NewSleepReflector: %v", err)
	}
	if err := reflector.Run(ctx, org.ID, run.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	updated, err := repo.NewMemoryRepo(pool).GetByID(ctx, seedID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.Confidence >= 0.6 {
		t.Fatalf("confidence = %.3f, want decayed below 0.6", updated.Confidence)
	}
}

type noopCandidateDeduplicator struct{}

func (noopCandidateDeduplicator) ReviewCandidateBatch(context.Context, uuid.UUID, []compaction.CandidateMemory) ([]compaction.CandidateReview, error) {
	return nil, nil
}
