//go:build integration

package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
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

	var memoriesExamined, memoriesUpdated, memoriesArchived int
	if err := pool.QueryRow(ctx, `
		SELECT memories_examined, memories_updated, memories_archived
		FROM memory_compaction_run
		WHERE organization_id = $1
		  AND run_type = 'task_consolidation'
		  AND status = 'completed'
		  AND scope_context->>'task_id' = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, org.ID, task.ID.String()).Scan(&memoriesExamined, &memoriesUpdated, &memoriesArchived); err != nil {
		t.Fatalf("fetch completed task consolidation run counters: %v", err)
	}
	if memoriesExamined != 5 {
		t.Fatalf("memories_examined = %d, want 5", memoriesExamined)
	}
	if memoriesArchived < 2 {
		t.Fatalf("memories_archived = %d, want >= 2", memoriesArchived)
	}
	if memoriesUpdated < 3 {
		t.Fatalf("memories_updated = %d, want >= 3", memoriesUpdated)
	}
}

func TestTurnExtractionTriggeredByTurnCompletedEvent(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := seedMemoryOrg(t, ctx, pool)
	sessionID, turnID := seedAgentTurnForMemoryExtraction(t, ctx, pool, org.ID, "Frank noted that db-prod-01 handles production traffic and requires strict backup windows every Monday morning.")

	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{PollInterval: 10 * time.Millisecond})
	worker := jobqueue.New(pool, nil, jobqueue.Config{})
	extractor := &recordingTurnExtractor{pool: pool}
	consumer, err := NewEventConsumer(EventConsumerOptions{
		Pool:      pool,
		Events:    bus,
		Enqueuer:  worker,
		Extractor: extractor,
	})
	if err != nil {
		t.Fatalf("NewEventConsumer: %v", err)
	}
	consumer.RegisterJobs(worker)

	sub := consumer.SubscribeTurnCompleted(&org.ID)
	defer bus.Unsubscribe(sub)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	workerErr := make(chan error, 1)
	go func() {
		workerErr <- worker.Start(runCtx)
	}()
	defer func() {
		_ = worker.Stop()
		if err := <-workerErr; err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("job worker returned error: %v", err)
		}
	}()

	payload, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"turn_id":    turnID,
	})
	if err := bus.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: org.ID,
		EventType:      "chat.turn.completed",
		ActorType:      "system",
		Payload:        payload,
	}); err != nil {
		t.Fatalf("publish chat.turn.completed: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if extractor.CallCount() >= 1 {
			var memoryCount int
			if err := pool.QueryRow(ctx, `
				SELECT COUNT(*)
				FROM memory
				WHERE organization_id = $1
			`, org.ID).Scan(&memoryCount); err == nil && memoryCount > 0 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if extractor.CallCount() != 1 {
		t.Fatalf("extractor call count = %d, want 1", extractor.CallCount())
	}

	var memoryCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM memory
		WHERE organization_id = $1
	`, org.ID).Scan(&memoryCount); err != nil {
		t.Fatalf("count extracted memories: %v", err)
	}
	if memoryCount == 0 {
		t.Fatal("expected extracted memory row")
	}

	// Re-publishing the same event should be idempotent and not enqueue a second extraction job.
	if err := bus.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: org.ID,
		EventType:      "chat.turn.completed",
		ActorType:      "system",
		Payload:        payload,
	}); err != nil {
		t.Fatalf("publish duplicate chat.turn.completed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if extractor.CallCount() != 1 {
		t.Fatalf("extractor call count after duplicate event = %d, want 1", extractor.CallCount())
	}
}

func TestTurnExtractionHandlesEmptyTurnGracefully(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := seedMemoryOrg(t, ctx, pool)
	sessionID, turnID := seedAgentTurnForMemoryExtraction(t, ctx, pool, org.ID, "   ")

	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{PollInterval: 10 * time.Millisecond})
	worker := jobqueue.New(pool, nil, jobqueue.Config{})
	extractor := &recordingTurnExtractor{pool: pool}
	consumer, err := NewEventConsumer(EventConsumerOptions{
		Pool:      pool,
		Events:    bus,
		Enqueuer:  worker,
		Extractor: extractor,
	})
	if err != nil {
		t.Fatalf("NewEventConsumer: %v", err)
	}
	consumer.RegisterJobs(worker)

	sub := consumer.SubscribeTurnCompleted(&org.ID)
	defer bus.Unsubscribe(sub)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	workerErr := make(chan error, 1)
	go func() {
		workerErr <- worker.Start(runCtx)
	}()
	defer func() {
		_ = worker.Stop()
		if err := <-workerErr; err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("job worker returned error: %v", err)
		}
	}()

	payload, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"turn_id":    turnID,
	})
	if err := bus.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: org.ID,
		EventType:      "chat.turn.completed",
		ActorType:      "system",
		Payload:        payload,
	}); err != nil {
		t.Fatalf("publish chat.turn.completed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	if extractor.CallCount() != 0 {
		t.Fatalf("extractor call count = %d, want 0 for empty turn", extractor.CallCount())
	}

	var memoryCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM memory
		WHERE organization_id = $1
	`, org.ID).Scan(&memoryCount); err != nil {
		t.Fatalf("count extracted memories: %v", err)
	}
	if memoryCount != 0 {
		t.Fatalf("memory count = %d, want 0 for empty turn", memoryCount)
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
		WorkStatus:     "draft",
		CreatedByType:  "system",
		CreatedByID:    nil,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task
}

func seedAgentTurnForMemoryExtraction(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, content string) (uuid.UUID, uuid.UUID) {
	t.Helper()

	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: orgID,
		ScopeType:      "organization",
		ScopeID:        orgID,
		Mode:           "sync",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create chat session: %v", err)
	}

	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          "Turn Extraction Agent",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		AgentType:            "worker",
		MemoryReadScopes:     []string{"org"},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
		PrivateMemory:        false,
		OperatorInstructions: "",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:      session.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   agent.ID,
		Status:         "completed",
	})
	if err != nil {
		t.Fatalf("create chat turn: %v", err)
	}

	authorType := "agent"
	if _, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID:      session.ID,
		TurnID:         &turn.ID,
		SequenceNumber: 1,
		AuthorType:     &authorType,
		AuthorID:       &agent.ID,
		Role:           "assistant",
		Content:        content,
		Status:         "final",
		Metadata:       json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("create chat message: %v", err)
	}

	return session.ID, turn.ID
}

type recordingTurnExtractor struct {
	pool *pgxpool.Pool

	mu    sync.Mutex
	calls int
}

func (e *recordingTurnExtractor) ExtractFromMessages(ctx context.Context, orgID uuid.UUID, msgs []ChatMessage, _ ExtractionSourceContext) error {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()

	content := strings.TrimSpace(msgs[0].Content)
	if content == "" {
		return nil
	}
	_, err := repo.NewMemoryRepo(e.pool).Create(ctx, repo.Memory{
		OrganizationID: orgID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        content,
		ContentHash:    uuid.NewString(),
		Confidence:     0.8,
		UtilityScore:   0.8,
		Status:         "active",
		TrustTier:      0.8,
	})
	return err
}

func (e *recordingTurnExtractor) CallCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}
