//go:build integration

package chat

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestChatServiceIntegrationCloseSessionPublishesSessionClosedEvent(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)

	bus := newIntegrationEventBus(pool)
	svc := newIntegrationService(t, pool, bus)

	session, err := svc.CreateSession(ctx, CreateSessionInput{
		OrganizationID: org.ID,
		ScopeType:      "organization",
		ScopeID:        org.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	events := make(chan eventbus.DomainEvent, 1)
	sub := bus.Subscribe("chat-close-"+uuid.NewString(), &org.ID, func(_ context.Context, event eventbus.DomainEvent) error {
		if event.EventType == "chat.session.closed" {
			events <- event
		}
		return nil
	})
	defer bus.Unsubscribe(sub)

	if err := svc.CloseSession(ctx, session.ID); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}

	select {
	case event := <-events:
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("unmarshal event payload: %v", err)
		}
		if payload["session_id"] != session.ID.String() {
			t.Fatalf("payload session_id = %v, want %s", payload["session_id"], session.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for chat.session.closed event")
	}
}

func TestChatServiceIntegrationAppendMessageConcurrentSequenceContiguous(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	user := seedChatServiceUser(t, ctx, pool, org.ID, "chat-concurrency", "member")

	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)

	authorType := "human_user"
	var wg sync.WaitGroup
	errCh := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.AppendMessage(ctx, AppendMessageInput{
				SessionID:  session.ID,
				AuthorType: &authorType,
				AuthorID:   &user.ID,
				Role:       "user",
				Content:    "message-" + string(rune('a'+i)),
			})
			errCh <- err
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("AppendMessage concurrent error: %v", err)
		}
	}

	messages, err := svc.ListMessages(ctx, session.ID, MessageFilter{Status: "pending", Limit: 100})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 10 {
		t.Fatalf("message count = %d, want 10", len(messages))
	}

	seen := make(map[int64]struct{}, len(messages))
	for _, message := range messages {
		if _, ok := seen[message.SequenceNumber]; ok {
			t.Fatalf("duplicate sequence_number %d", message.SequenceNumber)
		}
		seen[message.SequenceNumber] = struct{}{}
	}
	for i := int64(1); i <= 10; i++ {
		if _, ok := seen[i]; !ok {
			t.Fatalf("missing sequence_number %d", i)
		}
	}
}

func TestChatServiceIntegrationAppendMessagePublishesUserSentForUserMessage(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	user := seedChatServiceUser(t, ctx, pool, org.ID, "append-message-user-sent", "member")

	bus := newIntegrationEventBus(pool)
	svc := newIntegrationService(t, pool, bus)
	session := mustCreateSession(t, ctx, svc, org.ID)

	events := make(chan eventbus.DomainEvent, 8)
	sub := bus.Subscribe("chat-append-message-user-sent-"+uuid.NewString(), &org.ID, func(_ context.Context, event eventbus.DomainEvent) error {
		if event.EventType == "chat.message.created" || event.EventType == "chat.message.user_sent" {
			events <- event
		}
		return nil
	})
	defer bus.Unsubscribe(sub)

	authorType := "human_user"
	message, err := svc.AppendMessage(ctx, AppendMessageInput{
		SessionID:  session.ID,
		AuthorType: &authorType,
		AuthorID:   &user.ID,
		Role:       "user",
		Content:    "hello from integration test",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	received := map[string]eventbus.DomainEvent{}
	deadline := time.After(5 * time.Second)
	for len(received) < 2 {
		select {
		case event := <-events:
			received[event.EventType] = event
		case <-deadline:
			t.Fatalf("timed out waiting for message events; got %d", len(received))
		}
	}

	created := received["chat.message.created"]
	if created.EventType == "" {
		t.Fatal("missing chat.message.created event")
	}
	userSent := received["chat.message.user_sent"]
	if userSent.EventType == "" {
		t.Fatal("missing chat.message.user_sent event")
	}

	for _, event := range []eventbus.DomainEvent{created, userSent} {
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("unmarshal %s payload: %v", event.EventType, err)
		}
		if payload["session_id"] != session.ID.String() {
			t.Fatalf("%s payload session_id = %v, want %s", event.EventType, payload["session_id"], session.ID)
		}
		if payload["message_id"] != message.ID.String() {
			t.Fatalf("%s payload message_id = %v, want %s", event.EventType, payload["message_id"], message.ID)
		}
	}
}

func TestChatServiceIntegrationAppendMessageSteerMetadataSkipsUserSentEvent(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	user := seedChatServiceUser(t, ctx, pool, org.ID, "append-message-steer", "member")

	bus := newIntegrationEventBus(pool)
	svc := newIntegrationService(t, pool, bus)
	session := mustCreateSession(t, ctx, svc, org.ID)

	events := make(chan eventbus.DomainEvent, 8)
	sub := bus.Subscribe("chat-append-message-steer-"+uuid.NewString(), &org.ID, func(_ context.Context, event eventbus.DomainEvent) error {
		if event.EventType == "chat.message.created" || event.EventType == "chat.message.user_sent" {
			events <- event
		}
		return nil
	})
	defer bus.Unsubscribe(sub)

	metadata := json.RawMessage(`{"steer_turn_id":"` + uuid.NewString() + `"}`)
	authorType := "human_user"
	message, err := svc.AppendMessage(ctx, AppendMessageInput{
		SessionID:  session.ID,
		AuthorType: &authorType,
		AuthorID:   &user.ID,
		Role:       "user",
		Content:    "steer payload",
		Metadata:   metadata,
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	var created eventbus.DomainEvent
	select {
	case created = <-events:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for chat.message.created event")
	}
	if created.EventType != "chat.message.created" {
		t.Fatalf("first event = %s, want chat.message.created", created.EventType)
	}

	var payload map[string]any
	if err := json.Unmarshal(created.Payload, &payload); err != nil {
		t.Fatalf("unmarshal created payload: %v", err)
	}
	if payload["message_id"] != message.ID.String() {
		t.Fatalf("created payload message_id = %v, want %s", payload["message_id"], message.ID)
	}

	select {
	case event := <-events:
		if event.EventType == "chat.message.user_sent" {
			t.Fatal("unexpected chat.message.user_sent event for steer metadata")
		}
	case <-time.After(600 * time.Millisecond):
		// expected: no additional user_sent event
	}
}

func TestChatServiceIntegrationMultiHumanQueueFIFO(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)

	u1 := seedChatServiceUser(t, ctx, pool, org.ID, "fifo-1", "member")
	u2 := seedChatServiceUser(t, ctx, pool, org.ID, "fifo-2", "member")
	u3 := seedChatServiceUser(t, ctx, pool, org.ID, "fifo-3", "member")
	authorType := "human_user"

	if _, err := svc.AppendMessage(ctx, AppendMessageInput{SessionID: session.ID, AuthorType: &authorType, AuthorID: &u1.ID, Role: "user", Content: "first"}); err != nil {
		t.Fatalf("AppendMessage first: %v", err)
	}
	if _, err := svc.AppendMessage(ctx, AppendMessageInput{SessionID: session.ID, AuthorType: &authorType, AuthorID: &u2.ID, Role: "user", Content: "second"}); err != nil {
		t.Fatalf("AppendMessage second: %v", err)
	}
	if _, err := svc.AppendMessage(ctx, AppendMessageInput{SessionID: session.ID, AuthorType: &authorType, AuthorID: &u3.ID, Role: "user", Content: "third"}); err != nil {
		t.Fatalf("AppendMessage third: %v", err)
	}

	messages, err := svc.ListMessages(ctx, session.ID, MessageFilter{Status: "pending", Limit: 100})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("pending message count = %d, want 3", len(messages))
	}

	for i := 1; i < len(messages); i++ {
		if messages[i-1].SequenceNumber >= messages[i].SequenceNumber {
			t.Fatalf("messages not sorted by sequence_number: %d then %d", messages[i-1].SequenceNumber, messages[i].SequenceNumber)
		}
	}
}

func TestChatServiceIntegrationParticipantRemoveAndReadd(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	user := seedChatServiceUser(t, ctx, pool, org.ID, "participant-readd", "member")

	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)

	first, err := svc.AddParticipant(ctx, session.ID, "human_user", user.ID, "member")
	if err != nil {
		t.Fatalf("AddParticipant first: %v", err)
	}
	if err := svc.RemoveParticipant(ctx, session.ID, user.ID); err != nil {
		t.Fatalf("RemoveParticipant: %v", err)
	}

	participants, err := svc.ListParticipants(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListParticipants after remove: %v", err)
	}
	if len(participants) != 0 {
		t.Fatalf("participant count after remove = %d, want 0", len(participants))
	}

	second, err := svc.AddParticipant(ctx, session.ID, "human_user", user.ID, "member")
	if err != nil {
		t.Fatalf("AddParticipant second: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("re-added participant reused row id %s", first.ID)
	}
}

func TestChatServiceIntegrationCancelTurnSetsCompletedAt(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	agent := seedChatServiceAgent(t, ctx, pool, org.ID)

	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)
	turn, err := svc.CreateTurn(ctx, session.ID, agent.ID)
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if err := svc.StartTurn(ctx, turn.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	if err := svc.CancelTurn(ctx, turn.ID, "integration-test"); err != nil {
		t.Fatalf("CancelTurn: %v", err)
	}

	var status string
	var cancelRequestedAt *time.Time
	var completedAt *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT status, cancel_requested_at, completed_at
		FROM chat_turn
		WHERE id = $1
	`, turn.ID).Scan(&status, &cancelRequestedAt, &completedAt); err != nil {
		t.Fatalf("query chat_turn: %v", err)
	}

	if status != "cancelled" {
		t.Fatalf("turn status = %s, want cancelled", status)
	}
	if cancelRequestedAt == nil {
		t.Fatal("cancel_requested_at is NULL, want non-NULL")
	}
	if completedAt == nil {
		t.Fatal("completed_at is NULL, want non-NULL")
	}
}

func TestChatServiceIntegrationCancelTurnDrainsLogicalMessageDispatches(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	agent := seedChatServiceAgent(t, ctx, pool, org.ID)
	user := seedChatServiceUser(t, ctx, pool, org.ID, "cancel-logical", "member")

	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)
	authorType := "human_user"
	message, err := svc.AppendMessage(ctx, AppendMessageInput{
		SessionID:  session.ID,
		AuthorType: &authorType,
		AuthorID:   &user.ID,
		Role:       "user",
		Content:    "cancel this work",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	turnRepo := repo.NewChatTurnRepo(pool)
	sessionRepo := repo.NewChatSessionRepo(pool)
	startedAt := time.Now().UTC()
	activeCycleID := uuid.New()
	retryCycleID := uuid.New()
	active, err := turnRepo.Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		CycleID:          &activeCycleID,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		StartedAt:        &startedAt,
		TriggerMessageID: &message.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("create active turn: %v", err)
	}
	if _, err := turnRepo.Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       2,
		CycleID:          &retryCycleID,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &message.ID,
		RetryCount:       1,
	}); err != nil {
		t.Fatalf("create retry turn: %v", err)
	}
	if _, err := sessionRepo.UpdateCurrentTurn(ctx, session.ID, &active.ID); err != nil {
		t.Fatalf("update current turn: %v", err)
	}

	worker := jobqueue.New(pool, nil, jobqueue.Config{})
	if _, err := worker.Enqueue(ctx, nil, "agent_turn", 70, map[string]any{
		"session_id":  session.ID,
		"message_id":  message.ID,
		"retry_count": 1,
	}, nil); err != nil {
		t.Fatalf("enqueue retry job: %v", err)
	}

	if err := svc.CancelTurn(ctx, active.ID, "integration-test"); err != nil {
		t.Fatalf("CancelTurn: %v", err)
	}

	turns, err := turnRepo.ListBySession(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("turn count = %d, want 2", len(turns))
	}
	for _, turn := range turns {
		if turn.Status != "cancelled" {
			t.Fatalf("turn %s status = %s, want cancelled", turn.ID, turn.Status)
		}
	}

	updatedMessage, err := repo.NewChatMessageRepo(pool).GetByID(ctx, message.ID)
	if err != nil {
		t.Fatalf("GetByID message: %v", err)
	}
	if !AgentTurnDispatchCancelled(updatedMessage.Metadata) {
		t.Fatalf("expected cancelled dispatch metadata, got %s", string(updatedMessage.Metadata))
	}

	var activeJobs int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE group_key = $1
		  AND status IN ('pending', 'claimed')
	`, jobqueue.AgentTurnGroupKey(session.ID, message.ID)).Scan(&activeJobs); err != nil {
		t.Fatalf("count active queued jobs: %v", err)
	}
	if activeJobs != 0 {
		t.Fatalf("active queued jobs = %d, want 0", activeJobs)
	}
}

func TestChatServiceIntegrationUpdateMessageStatusPersistsErrorMessage(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	user := seedChatServiceUser(t, ctx, pool, org.ID, "status-error", "member")

	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)
	authorType := "human_user"
	message, err := svc.AppendMessage(ctx, AppendMessageInput{
		SessionID:  session.ID,
		AuthorType: &authorType,
		AuthorID:   &user.ID,
		Role:       "user",
		Content:    "fails later",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	const failure = "provider timeout"
	if err := svc.UpdateMessageStatus(ctx, message.ID, "failed", failure); err != nil {
		t.Fatalf("UpdateMessageStatus: %v", err)
	}

	var status string
	var errorMessage *string
	if err := pool.QueryRow(ctx, `
		SELECT status, error_message
		FROM chat_message
		WHERE id = $1
	`, message.ID).Scan(&status, &errorMessage); err != nil {
		t.Fatalf("query chat_message: %v", err)
	}
	if status != "failed" {
		t.Fatalf("status = %s, want failed", status)
	}
	if errorMessage == nil || *errorMessage != failure {
		t.Fatalf("error_message = %v, want %q", errorMessage, failure)
	}
}

func TestGetOrCreateNodeSessionPublishesCreatedEvent(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	agent := seedChatServiceAgent(t, ctx, pool, org.ID)
	execution := seedChatServiceFlowNodeExecution(t, ctx, pool, org.ID)

	bus := newIntegrationEventBus(pool)
	svc := newIntegrationService(t, pool, bus)

	events := make(chan eventbus.DomainEvent, 4)
	sub := bus.Subscribe("chat-node-session-created-"+uuid.NewString(), &org.ID, func(_ context.Context, event eventbus.DomainEvent) error {
		if event.EventType == "chat.session.created" {
			events <- event
		}
		return nil
	})
	defer bus.Unsubscribe(sub)

	first, err := svc.GetOrCreateNodeSession(ctx, execution.ID, agent.ID)
	if err != nil {
		t.Fatalf("GetOrCreateNodeSession first: %v", err)
	}

	var firstEvent eventbus.DomainEvent
	select {
	case firstEvent = <-events:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first chat.session.created event")
	}

	var payload map[string]any
	if err := json.Unmarshal(firstEvent.Payload, &payload); err != nil {
		t.Fatalf("unmarshal created payload: %v", err)
	}
	if payload["session_id"] != first.ID.String() {
		t.Fatalf("payload session_id = %v, want %s", payload["session_id"], first.ID)
	}

	second, err := svc.GetOrCreateNodeSession(ctx, execution.ID, agent.ID)
	if err != nil {
		t.Fatalf("GetOrCreateNodeSession second: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("idempotent session id = %s, want %s", second.ID, first.ID)
	}

	select {
	case event := <-events:
		t.Fatalf("unexpected duplicate created event: %s", event.ID)
	case <-time.After(500 * time.Millisecond):
		// no duplicate event expected
	}
}

func newIntegrationService(t *testing.T, pool *pgxpool.Pool, bus eventPublisher) ChatService {
	t.Helper()
	if bus == nil {
		bus = newIntegrationEventBus(pool)
	}
	svc, err := NewService(Options{Pool: pool, Events: bus})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func newIntegrationEventBus(pool *pgxpool.Pool) *eventbus.Bus {
	return eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{PollInterval: 10 * time.Millisecond, BatchSize: 100})
}

func seedChatServiceOrg(t *testing.T, ctx context.Context, pool *pgxpool.Pool) repo.Organization {
	t.Helper()
	orgRepo := repo.NewOrgRepo(pool)
	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "chat-org-" + uuid.NewString()[:8],
		DisplayName: "Chat Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return org
}

func seedChatServiceUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, slug string, role string) repo.HumanUser {
	t.Helper()
	userRepo := repo.NewHumanUserRepo(pool)
	user, err := userRepo.Create(ctx, repo.HumanUser{
		OrganizationID: orgID,
		Email:          slug + "@example.com",
		DisplayName:    "User " + slug,
		Role:           role,
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

func seedChatServiceAgent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) repo.Agent {
	t.Helper()
	agentRepo := repo.NewAgentRepo(pool)
	agent, err := agentRepo.Create(ctx, repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          "Chat Worker " + uuid.NewString()[:8],
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "You are helpful.",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "task", "agent_private"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return agent
}

func seedChatServiceFlowNodeExecution(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) repo.FlowNodeExecution {
	t.Helper()

	projectRepo := repo.NewProjectRepo(pool)
	templateRepo := repo.NewFlowTemplateRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)
	taskRepo := repo.NewProjectTaskRepo(pool)
	executionRepo := repo.NewFlowNodeExecutionRepo(pool)

	project, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: orgID,
		Slug:           "chat-proj-" + uuid.NewString()[:8],
		DisplayName:    "Chat Project",
		Description:    "",
		DeliveryMode:   "gated",
		Settings:       json.RawMessage(`{}`),
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	template, err := templateRepo.Create(ctx, repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &project.ID,
		Slug:           "chat-flow-" + uuid.NewString()[:8],
		DisplayName:    "Chat Flow",
		Description:    "",
		IsCurrent:      true,
		Version:        1,
		IsSystem:       false,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	node, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work Node",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}

	task, err := taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID:    orgID,
		ProjectID:         project.ID,
		Title:             "Chat task",
		WorkStatus:        "in_progress",
		CurrentFlowNodeID: &node.ID,
		FlowTemplateID:    &template.ID,
		CreatedByType:     "system",
		CreatedByID:       nil,
		Metadata:          json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	execution, err := executionRepo.Create(ctx, repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  node.ID,
		VisitNumber: 1,
		Status:      "active",
		Metadata:    json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node execution: %v", err)
	}
	return execution
}

func mustCreateSession(t *testing.T, ctx context.Context, svc ChatService, orgID uuid.UUID) *ChatSession {
	t.Helper()
	session, err := svc.CreateSession(ctx, CreateSessionInput{
		OrganizationID: orgID,
		ScopeType:      "organization",
		ScopeID:        orgID,
		Mode:           "sync",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return session
}
