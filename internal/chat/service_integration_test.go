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
