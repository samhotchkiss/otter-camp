//go:build integration

package chat

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestMessage_StateMachine_PendingToFinal(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	user := seedChatServiceUser(t, ctx, pool, org.ID, "pending-final", "member")
	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)

	authorType := "human_user"
	message, err := svc.AppendMessage(ctx, AppendMessageInput{
		SessionID:  session.ID,
		AuthorType: &authorType,
		AuthorID:   &user.ID,
		Role:       "user",
		Content:    "pending",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	if err := svc.UpdateMessageStatus(ctx, message.ID, "streaming", ""); err != nil {
		t.Fatalf("UpdateMessageStatus streaming: %v", err)
	}
	if err := svc.UpdateMessageStatus(ctx, message.ID, "final", ""); err != nil {
		t.Fatalf("UpdateMessageStatus final: %v", err)
	}

	got, err := svc.GetMessage(ctx, message.ID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.Status != "final" {
		t.Fatalf("status = %q, want final", got.Status)
	}

	if err := svc.UpdateMessageStatus(ctx, message.ID, "failed", "late failure"); !errors.Is(err, ErrInvalidStatusTransition) {
		t.Fatalf("UpdateMessageStatus after final error = %v, want ErrInvalidStatusTransition", err)
	}
}

func TestMessage_StateMachine_Failed(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	user := seedChatServiceUser(t, ctx, pool, org.ID, "stream-failed", "member")
	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)

	authorType := "human_user"
	content := "keep this content"
	message, err := svc.AppendMessage(ctx, AppendMessageInput{
		SessionID:  session.ID,
		AuthorType: &authorType,
		AuthorID:   &user.ID,
		Role:       "user",
		Content:    content,
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := svc.UpdateMessageStatus(ctx, message.ID, "streaming", ""); err != nil {
		t.Fatalf("UpdateMessageStatus streaming: %v", err)
	}
	if err := svc.UpdateMessageStatus(ctx, message.ID, "failed", "provider timeout"); err != nil {
		t.Fatalf("UpdateMessageStatus failed: %v", err)
	}

	got, err := svc.GetMessage(ctx, message.ID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.Status != "failed" {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if got.Content != content {
		t.Fatalf("content = %q, want %q", got.Content, content)
	}
}

func TestMessage_Redaction(t *testing.T) {
	baseCtx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, baseCtx, pool)
	user := seedChatServiceUser(t, baseCtx, pool, org.ID, "redaction-author", "member")
	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, baseCtx, svc, org.ID)
	ctx := principalContext(baseCtx, org.ID, user.ID, "member")

	authorType := "human_user"
	message, err := svc.AppendMessage(ctx, AppendMessageInput{
		SessionID:  session.ID,
		AuthorType: &authorType,
		AuthorID:   &user.ID,
		Role:       "user",
		Content:    "sensitive text",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := svc.UpdateMessageStatus(ctx, message.ID, "streaming", ""); err != nil {
		t.Fatalf("UpdateMessageStatus streaming: %v", err)
	}
	if err := svc.UpdateMessageStatus(ctx, message.ID, "final", ""); err != nil {
		t.Fatalf("UpdateMessageStatus final: %v", err)
	}
	if err := svc.RedactMessage(ctx, message.ID); err != nil {
		t.Fatalf("RedactMessage: %v", err)
	}

	got, err := svc.GetMessage(baseCtx, message.ID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.Content != "" {
		t.Fatalf("content = %q, want empty", got.Content)
	}
	if got.Status != "redacted" {
		t.Fatalf("status = %q, want redacted", got.Status)
	}
	if got.RedactedAt == nil {
		t.Fatal("redacted_at is nil, want non-nil")
	}

	var rowCount int
	if err := pool.QueryRow(baseCtx, `SELECT COUNT(*) FROM chat_message WHERE id = $1`, message.ID).Scan(&rowCount); err != nil {
		t.Fatalf("count chat_message rows: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("chat_message row count = %d, want 1", rowCount)
	}
}

func TestMessage_AppendOnlyAfterFinal(t *testing.T) {
	baseCtx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, baseCtx, pool)
	user := seedChatServiceUser(t, baseCtx, pool, org.ID, "append-only", "member")
	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, baseCtx, svc, org.ID)
	ctx := principalContext(baseCtx, org.ID, user.ID, "member")

	authorType := "human_user"
	message, err := svc.AppendMessage(ctx, AppendMessageInput{
		SessionID:  session.ID,
		AuthorType: &authorType,
		AuthorID:   &user.ID,
		Role:       "user",
		Content:    "immutable content",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := svc.UpdateMessageStatus(ctx, message.ID, "streaming", ""); err != nil {
		t.Fatalf("UpdateMessageStatus streaming: %v", err)
	}
	if err := svc.UpdateMessageStatus(ctx, message.ID, "final", ""); err != nil {
		t.Fatalf("UpdateMessageStatus final: %v", err)
	}

	before, err := svc.GetMessage(baseCtx, message.ID)
	if err != nil {
		t.Fatalf("GetMessage before edit: %v", err)
	}
	beforeUpdatedAt := before.UpdatedAt

	err = svc.EditQueuedMessage(ctx, message.ID, "edited text")
	if !errors.Is(err, ErrMessageNotEditable) {
		t.Fatalf("EditQueuedMessage error = %v, want ErrMessageNotEditable", err)
	}

	after, err := svc.GetMessage(baseCtx, message.ID)
	if err != nil {
		t.Fatalf("GetMessage after edit: %v", err)
	}
	if after.Content != "immutable content" {
		t.Fatalf("content = %q, want immutable content", after.Content)
	}
	if !after.UpdatedAt.Equal(beforeUpdatedAt) {
		t.Fatalf("updated_at changed from %s to %s; expected no write", beforeUpdatedAt, after.UpdatedAt)
	}
}

func TestMessage_ToolCallAndResult(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)

	toolCall, err := svc.AppendMessage(ctx, AppendMessageInput{
		SessionID: session.ID,
		Role:      "tool_call",
		Content:   `{"tool":"browser.navigate"}`,
	})
	if err != nil {
		t.Fatalf("AppendMessage tool_call: %v", err)
	}
	if err := svc.UpdateMessageStatus(ctx, toolCall.ID, "streaming", ""); err != nil {
		t.Fatalf("UpdateMessageStatus tool_call streaming: %v", err)
	}
	if err := svc.UpdateMessageStatus(ctx, toolCall.ID, "final", ""); err != nil {
		t.Fatalf("UpdateMessageStatus tool_call final: %v", err)
	}

	toolResult, err := svc.AppendMessage(ctx, AppendMessageInput{
		SessionID: session.ID,
		Role:      "tool_result",
		Content:   `{"ok":true}`,
	})
	if err != nil {
		t.Fatalf("AppendMessage tool_result: %v", err)
	}
	if err := svc.UpdateMessageStatus(ctx, toolResult.ID, "streaming", ""); err != nil {
		t.Fatalf("UpdateMessageStatus tool_result streaming: %v", err)
	}
	if err := svc.UpdateMessageStatus(ctx, toolResult.ID, "final", ""); err != nil {
		t.Fatalf("UpdateMessageStatus tool_result final: %v", err)
	}

	messages, err := svc.ListMessages(ctx, session.ID, MessageFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(messages))
	}
	if messages[0].Role != "tool_call" || messages[1].Role != "tool_result" {
		t.Fatalf("roles = %q, %q, want tool_call, tool_result", messages[0].Role, messages[1].Role)
	}
	if messages[0].AuthorType != nil || messages[1].AuthorType != nil {
		t.Fatal("expected nil author_type for tool_call/tool_result messages")
	}
}

func TestMessage_QueuedEdit(t *testing.T) {
	baseCtx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, baseCtx, pool)
	user := seedChatServiceUser(t, baseCtx, pool, org.ID, "queued-edit", "member")
	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, baseCtx, svc, org.ID)
	ctx := principalContext(baseCtx, org.ID, user.ID, "member")

	authorType := "human_user"
	message, err := svc.AppendMessage(ctx, AppendMessageInput{
		SessionID:  session.ID,
		AuthorType: &authorType,
		AuthorID:   &user.ID,
		Role:       "user",
		Content:    "before edit",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := svc.EditQueuedMessage(ctx, message.ID, "after edit"); err != nil {
		t.Fatalf("EditQueuedMessage: %v", err)
	}

	got, err := svc.GetMessage(baseCtx, message.ID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.Content != "after edit" {
		t.Fatalf("content = %q, want after edit", got.Content)
	}
}

func TestReaction_Recording(t *testing.T) {
	baseCtx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, baseCtx, pool)
	user := seedChatServiceUser(t, baseCtx, pool, org.ID, "reaction", "member")
	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, baseCtx, svc, org.ID)
	ctx := principalContext(baseCtx, org.ID, user.ID, "member")

	authorType := "human_user"
	message, err := svc.AppendMessage(ctx, AppendMessageInput{
		SessionID:  session.ID,
		AuthorType: &authorType,
		AuthorID:   &user.ID,
		Role:       "user",
		Content:    "react here",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	reaction, err := svc.AddReaction(ctx, message.ID, "human_user", user.ID, "👍")
	if err != nil {
		t.Fatalf("AddReaction: %v", err)
	}
	if _, err := svc.AddReaction(ctx, message.ID, "human_user", user.ID, "👍"); !errors.Is(err, ErrDuplicateReaction) {
		t.Fatalf("duplicate AddReaction error = %v, want ErrDuplicateReaction", err)
	}

	reactions, err := svc.ListReactions(baseCtx, message.ID)
	if err != nil {
		t.Fatalf("ListReactions: %v", err)
	}
	if len(reactions) != 1 {
		t.Fatalf("reaction count = %d, want 1", len(reactions))
	}

	if err := svc.RemoveReaction(ctx, reaction.ID); err != nil {
		t.Fatalf("RemoveReaction: %v", err)
	}
	reactions, err = svc.ListReactions(baseCtx, message.ID)
	if err != nil {
		t.Fatalf("ListReactions after delete: %v", err)
	}
	if len(reactions) != 0 {
		t.Fatalf("reaction count after delete = %d, want 0", len(reactions))
	}
}

func TestReaction_MemoryFeedback(t *testing.T) {
	baseCtx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, baseCtx, pool)
	user := seedChatServiceUser(t, baseCtx, pool, org.ID, "reaction-feedback", "member")
	bus := newIntegrationEventBus(pool)
	svc := newIntegrationService(t, pool, bus)
	session := mustCreateSession(t, baseCtx, svc, org.ID)
	ctx := principalContext(baseCtx, org.ID, user.ID, "member")

	authorType := "human_user"
	message, err := svc.AppendMessage(ctx, AppendMessageInput{
		SessionID:  session.ID,
		AuthorType: &authorType,
		AuthorID:   &user.ID,
		Role:       "user",
		Content:    "react here",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	events := make(chan eventbus.DomainEvent, 1)
	sub := bus.Subscribe("chat-reaction-feedback-"+uuid.NewString(), &org.ID, func(_ context.Context, event eventbus.DomainEvent) error {
		if event.EventType == "chat.reaction.added" {
			events <- event
		}
		return nil
	})
	defer bus.Unsubscribe(sub)

	if _, err := svc.AddReaction(ctx, message.ID, "human_user", user.ID, "👍"); err != nil {
		t.Fatalf("AddReaction: %v", err)
	}

	select {
	case event := <-events:
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("unmarshal event payload: %v", err)
		}
		if payload["message_id"] != message.ID.String() {
			t.Fatalf("payload message_id = %v, want %s", payload["message_id"], message.ID)
		}
		if payload["session_id"] != session.ID.String() {
			t.Fatalf("payload session_id = %v, want %s", payload["session_id"], session.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for chat.reaction.added event")
	}
}

func TestTurn_StateMachine(t *testing.T) {
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
	if turn.Status != "pending" {
		t.Fatalf("status = %q, want pending", turn.Status)
	}
	if turn.RespondingType != "agent" {
		t.Fatalf("responding_type = %q, want agent", turn.RespondingType)
	}

	if err := svc.StartTurn(ctx, turn.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	if err := svc.CompleteTurn(ctx, turn.ID); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	got, err := svc.GetTurn(ctx, turn.ID)
	if err != nil {
		t.Fatalf("GetTurn: %v", err)
	}
	if got.Status != "completed" {
		t.Fatalf("status = %q, want completed", got.Status)
	}
	if got.CompletedAt == nil {
		t.Fatal("completed_at is nil, want non-nil")
	}
}

func TestTurn_Cancellation(t *testing.T) {
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
	if err := svc.CancelCurrentTurn(ctx, session.ID); err != nil {
		t.Fatalf("CancelCurrentTurn: %v", err)
	}

	got, err := svc.GetTurn(ctx, turn.ID)
	if err != nil {
		t.Fatalf("GetTurn: %v", err)
	}
	if got.Status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", got.Status)
	}
	if got.CancelRequestedAt == nil {
		t.Fatal("cancel_requested_at is nil, want non-nil")
	}
}

func TestTurn_Steer(t *testing.T) {
	baseCtx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, baseCtx, pool)
	user := seedChatServiceUser(t, baseCtx, pool, org.ID, "steer-author", "member")
	agent := seedChatServiceAgent(t, baseCtx, pool, org.ID)
	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, baseCtx, svc, org.ID)
	ctx := principalContext(baseCtx, org.ID, user.ID, "member")

	authorType := "human_user"
	original, err := svc.AppendMessage(ctx, AppendMessageInput{
		SessionID:  session.ID,
		AuthorType: &authorType,
		AuthorID:   &user.ID,
		Role:       "user",
		Content:    "initial direction",
	})
	if err != nil {
		t.Fatalf("AppendMessage original: %v", err)
	}
	turn, err := svc.CreateTurn(baseCtx, session.ID, agent.ID)
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if err := svc.StartTurn(baseCtx, turn.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	if err := svc.SteerTurn(ctx, session.ID, original.ID, "new direction"); err != nil {
		t.Fatalf("SteerTurn: %v", err)
	}

	cancelledTurn, err := svc.GetTurn(baseCtx, turn.ID)
	if err != nil {
		t.Fatalf("GetTurn cancelled: %v", err)
	}
	if cancelledTurn.Status != "cancelled" {
		t.Fatalf("original turn status = %q, want cancelled", cancelledTurn.Status)
	}

	turns, err := svc.ListTurns(baseCtx, session.ID)
	if err != nil {
		t.Fatalf("ListTurns: %v", err)
	}
	var pendingTurn *ChatTurn
	for _, item := range turns {
		if item.ID == turn.ID {
			continue
		}
		if item.Status == "pending" {
			pendingTurn = item
			break
		}
	}
	if pendingTurn == nil {
		t.Fatal("expected a new pending turn after steer")
	}

	redactedOriginal, err := svc.GetMessage(baseCtx, original.ID)
	if err != nil {
		t.Fatalf("GetMessage original: %v", err)
	}
	if redactedOriginal.Status != "redacted" {
		t.Fatalf("original message status = %q, want redacted", redactedOriginal.Status)
	}

	messages, err := svc.ListMessages(baseCtx, session.ID, MessageFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) < 2 {
		t.Fatalf("message count = %d, want at least 2", len(messages))
	}
	steer := messages[len(messages)-1]
	if steer.Content != "new direction" {
		t.Fatalf("steer content = %q, want new direction", steer.Content)
	}

	var metadata map[string]any
	if err := json.Unmarshal(steer.Metadata, &metadata); err != nil {
		t.Fatalf("unmarshal steer metadata: %v", err)
	}
	if metadata["steer_message_id"] != original.ID.String() {
		t.Fatalf("metadata.steer_message_id = %v, want %s", metadata["steer_message_id"], original.ID)
	}
	if metadata["steer_turn_id"] != turn.ID.String() {
		t.Fatalf("metadata.steer_turn_id = %v, want %s", metadata["steer_turn_id"], turn.ID)
	}
	if steer.TurnID == nil || *steer.TurnID != pendingTurn.ID {
		t.Fatalf("steer turn_id = %v, want %s", steer.TurnID, pendingTurn.ID)
	}
}

func TestMultiHuman_MessageQueue(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	u1 := seedChatServiceUser(t, ctx, pool, org.ID, "queue-1", "member")
	u2 := seedChatServiceUser(t, ctx, pool, org.ID, "queue-2", "member")
	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)

	if _, err := svc.AddParticipant(ctx, session.ID, "human_user", u1.ID, "member"); err != nil {
		t.Fatalf("AddParticipant u1: %v", err)
	}
	if _, err := svc.AddParticipant(ctx, session.ID, "human_user", u2.ID, "member"); err != nil {
		t.Fatalf("AddParticipant u2: %v", err)
	}

	authorType := "human_user"
	if _, err := svc.AppendMessage(ctx, AppendMessageInput{
		SessionID:  session.ID,
		AuthorType: &authorType,
		AuthorID:   &u1.ID,
		Role:       "user",
		Content:    "first",
	}); err != nil {
		t.Fatalf("AppendMessage first: %v", err)
	}
	if _, err := svc.AppendMessage(ctx, AppendMessageInput{
		SessionID:  session.ID,
		AuthorType: &authorType,
		AuthorID:   &u2.ID,
		Role:       "user",
		Content:    "second",
	}); err != nil {
		t.Fatalf("AppendMessage second: %v", err)
	}

	messages, err := svc.ListMessages(ctx, session.ID, MessageFilter{Status: "pending", Limit: 10})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("pending message count = %d, want 2", len(messages))
	}
	if messages[0].Content != "first" || messages[1].Content != "second" {
		t.Fatalf("message order = %q, %q, want first, second", messages[0].Content, messages[1].Content)
	}
	if messages[0].SequenceNumber >= messages[1].SequenceNumber {
		t.Fatalf("sequence numbers not FIFO: %d then %d", messages[0].SequenceNumber, messages[1].SequenceNumber)
	}
}

func principalContext(ctx context.Context, orgID, userID uuid.UUID, role string) context.Context {
	return middleware.WithPrincipal(ctx, middleware.Principal{
		UserID:         userID,
		OrganizationID: orgID,
		Role:           role,
	})
}
