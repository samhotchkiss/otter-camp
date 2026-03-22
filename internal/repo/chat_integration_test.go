//go:build integration

package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestChatParticipantRepoRemoveExcludesRemovedEntries(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedChatFixture(t, ctx, pool)

	user := seedTaskRepoUser(t, ctx, pool, fixture.org.ID, "chat-participant")
	participantRepo := NewChatParticipantRepo(pool)

	participant, err := participantRepo.Create(ctx, ChatParticipant{
		SessionID:              fixture.session.ID,
		ParticipantType:        "human_user",
		ParticipantID:          user.ID,
		Role:                   "member",
		NotificationPreference: "all",
	})
	if err != nil {
		t.Fatalf("Create participant: %v", err)
	}

	removed, err := participantRepo.Remove(ctx, participant.ID)
	if err != nil {
		t.Fatalf("Remove participant: %v", err)
	}
	if removed.RemovedAt == nil {
		t.Fatal("removed_at is nil after Remove")
	}

	participants, err := participantRepo.ListBySession(ctx, fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(participants) != 0 {
		t.Fatalf("ListBySession count = %d, want 0", len(participants))
	}

	_, err = participantRepo.GetBySessionAndActor(ctx, fixture.session.ID, "human_user", user.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetBySessionAndActor error = %v, want ErrNotFound", err)
	}
}

func TestChatSummaryRepoRangeCheckConstraint(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedChatFixture(t, ctx, pool)

	summaryRepo := NewChatSummaryRepo(pool)
	_, err := summaryRepo.Create(ctx, ChatSummary{
		SessionID:           fixture.session.ID,
		FromSequence:        10,
		ToSequence:          5,
		SummaryText:         "invalid",
		SummarizedTurnCount: 1,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Create summary range check error = %v, want ErrConflict", err)
	}
}

func TestMemorySourceRepoAllowsNonExistentSessionSoftRef(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedChatFixture(t, ctx, pool)

	memoryRepo := NewMemoryRepo(pool)
	sourceRepo := NewMemorySourceRepo(pool)

	memory, err := memoryRepo.Create(ctx, Memory{
		OrganizationID: fixture.org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "from chat",
		ContentHash:    "chat-source-hash",
		Status:         "active",
	})
	if err != nil {
		t.Fatalf("Create memory: %v", err)
	}

	nonExistentSessionID := uuid.New()
	created, err := sourceRepo.Create(ctx, MemorySource{
		MemoryID:   memory.ID,
		SourceType: "chat_message",
		SourceID:   nil,
		SessionID:  &nonExistentSessionID,
	})
	if err != nil {
		t.Fatalf("Create memory_source: %v", err)
	}
	if created.SessionID == nil || *created.SessionID != nonExistentSessionID {
		t.Fatalf("session_id = %v, want %s", created.SessionID, nonExistentSessionID)
	}
}

func TestChatTurnRepoOrderingAndUniqueConstraint(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedChatFixture(t, ctx, pool)

	turnRepo := NewChatTurnRepo(pool)
	for _, n := range []int{1, 2, 3} {
		if _, err := turnRepo.Create(ctx, ChatTurn{
			SessionID:      fixture.session.ID,
			TurnNumber:     n,
			RespondingType: "agent",
			RespondingID:   fixture.agent.ID,
			Status:         "pending",
		}); err != nil {
			t.Fatalf("Create turn %d: %v", n, err)
		}
	}

	turns, err := turnRepo.ListBySession(ctx, fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(turns) != 3 {
		t.Fatalf("turn count = %d, want 3", len(turns))
	}
	if turns[0].TurnNumber != 1 || turns[1].TurnNumber != 2 || turns[2].TurnNumber != 3 {
		t.Fatalf("turn order = [%d %d %d], want [1 2 3]", turns[0].TurnNumber, turns[1].TurnNumber, turns[2].TurnNumber)
	}

	_, err = turnRepo.Create(ctx, ChatTurn{
		SessionID:      fixture.session.ID,
		TurnNumber:     2,
		RespondingType: "agent",
		RespondingID:   fixture.agent.ID,
		Status:         "pending",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate turn_number create error = %v, want ErrConflict", err)
	}
}

func TestChatTurnRepoCreateForMessageAttemptRepairsCurrentTurnAndReplacesStalePendingEX305(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedChatFixture(t, ctx, pool)

	messageRepo := NewChatMessageRepo(pool)
	sessionRepo := NewChatSessionRepo(pool)
	turnRepo := NewChatTurnRepo(pool)

	firstMessage, err := messageRepo.Create(ctx, ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Content:   "first trigger",
	})
	if err != nil {
		t.Fatalf("create first message: %v", err)
	}
	pendingTurn, err := turnRepo.Create(ctx, ChatTurn{
		SessionID:        fixture.session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     fixture.agent.ID,
		Status:           "pending",
		TriggerMessageID: &firstMessage.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("create pending turn: %v", err)
	}
	staleTurn, err := turnRepo.Create(ctx, ChatTurn{
		SessionID:      fixture.session.ID,
		TurnNumber:     2,
		RespondingType: "agent",
		RespondingID:   fixture.agent.ID,
		Status:         "completed",
	})
	if err != nil {
		t.Fatalf("create stale turn: %v", err)
	}
	if _, err := sessionRepo.UpdateCurrentTurn(ctx, fixture.session.ID, &staleTurn.ID); err != nil {
		t.Fatalf("set stale current turn: %v", err)
	}

	secondMessage, err := messageRepo.Create(ctx, ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Content:   "second trigger",
	})
	if err != nil {
		t.Fatalf("create second message: %v", err)
	}

	turn, created, err := turnRepo.CreateForMessageAttempt(ctx, fixture.session.ID, fixture.agent.ID, secondMessage.ID, 0)
	if err != nil {
		t.Fatalf("CreateForMessageAttempt: %v", err)
	}
	if !created {
		t.Fatal("created = false, want true when stale pending belongs to a different message")
	}
	if turn.ID == pendingTurn.ID {
		t.Fatalf("turn id = %s, want fresh turn for new message", turn.ID)
	}
	if turn.TriggerMessageID == nil || *turn.TriggerMessageID != secondMessage.ID {
		t.Fatalf("trigger_message_id = %v, want %s", turn.TriggerMessageID, secondMessage.ID)
	}

	refreshedSession, err := sessionRepo.GetByID(ctx, fixture.session.ID)
	if err != nil {
		t.Fatalf("GetByID session: %v", err)
	}
	if refreshedSession.CurrentTurnID == nil || *refreshedSession.CurrentTurnID != turn.ID {
		t.Fatalf("current_turn_id = %v, want %s", refreshedSession.CurrentTurnID, turn.ID)
	}

	turns, err := turnRepo.ListBySession(ctx, fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 3 {
		t.Fatalf("turn count = %d, want 3", len(turns))
	}
	pendingCount := 0
	cancelledCount := 0
	for _, candidate := range turns {
		if normalizeChatTurnStatus(candidate.Status) == "pending" {
			pendingCount++
		}
		if candidate.ID == pendingTurn.ID && normalizeChatTurnStatus(candidate.Status) == "cancelled" {
			cancelledCount++
		}
	}
	if pendingCount != 1 {
		t.Fatalf("pending turn count = %d, want 1", pendingCount)
	}
	if cancelledCount != 1 {
		t.Fatalf("cancelled stale pending count = %d, want 1", cancelledCount)
	}
}

func TestChatTurnRepoCreateForMessageAttemptCancelsDuplicateInProgressTurnsEX305(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedChatFixture(t, ctx, pool)

	sessionRepo := NewChatSessionRepo(pool)
	messageRepo := NewChatMessageRepo(pool)
	turnRepo := NewChatTurnRepo(pool)
	cancelledAt := time.Date(2026, time.March, 7, 10, 15, 0, 0, time.UTC)
	turnRepo.now = func() time.Time { return cancelledAt }

	startedOld := cancelledAt.Add(-3 * time.Minute)
	startedNew := cancelledAt.Add(-1 * time.Minute)

	staleTurn, err := turnRepo.Create(ctx, ChatTurn{
		SessionID:      fixture.session.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   fixture.agent.ID,
		Status:         "in_progress",
		StartedAt:      &startedOld,
	})
	if err != nil {
		t.Fatalf("create stale in_progress turn: %v", err)
	}
	liveTurn, err := turnRepo.Create(ctx, ChatTurn{
		SessionID:      fixture.session.ID,
		TurnNumber:     2,
		RespondingType: "agent",
		RespondingID:   fixture.agent.ID,
		Status:         "in_progress",
		StartedAt:      &startedNew,
	})
	if err != nil {
		t.Fatalf("create canonical in_progress turn: %v", err)
	}
	if _, err := sessionRepo.UpdateCurrentTurn(ctx, fixture.session.ID, &staleTurn.ID); err != nil {
		t.Fatalf("set stale current turn: %v", err)
	}

	message, err := messageRepo.Create(ctx, ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Content:   "resume work",
	})
	if err != nil {
		t.Fatalf("create message: %v", err)
	}

	turn, created, err := turnRepo.CreateForMessageAttempt(ctx, fixture.session.ID, fixture.agent.ID, message.ID, 0)
	if err != nil {
		t.Fatalf("CreateForMessageAttempt: %v", err)
	}
	if created {
		t.Fatal("created = true, want false when canonical in_progress turn already exists")
	}
	if turn.ID != liveTurn.ID {
		t.Fatalf("returned turn = %s, want canonical live turn %s", turn.ID, liveTurn.ID)
	}

	refreshedSession, err := sessionRepo.GetByID(ctx, fixture.session.ID)
	if err != nil {
		t.Fatalf("GetByID session: %v", err)
	}
	if refreshedSession.CurrentTurnID == nil || *refreshedSession.CurrentTurnID != liveTurn.ID {
		t.Fatalf("current_turn_id = %v, want %s", refreshedSession.CurrentTurnID, liveTurn.ID)
	}

	staleAfter, err := turnRepo.GetByID(ctx, staleTurn.ID)
	if err != nil {
		t.Fatalf("GetByID stale turn: %v", err)
	}
	if normalizeChatTurnStatus(staleAfter.Status) != "cancelled" {
		t.Fatalf("stale turn status = %q, want cancelled", staleAfter.Status)
	}
	if staleAfter.CancelRequestedAt == nil || !staleAfter.CancelRequestedAt.Equal(cancelledAt) {
		t.Fatalf("stale cancel_requested_at = %v, want %s", staleAfter.CancelRequestedAt, cancelledAt)
	}
	if staleAfter.CompletedAt == nil || !staleAfter.CompletedAt.Equal(cancelledAt) {
		t.Fatalf("stale completed_at = %v, want %s", staleAfter.CompletedAt, cancelledAt)
	}
	if staleAfter.StopReason == nil || *staleAfter.StopReason != "superseded_live_turn" {
		t.Fatalf("stale stop_reason = %v, want superseded_live_turn", staleAfter.StopReason)
	}

	liveAfter, err := turnRepo.GetByID(ctx, liveTurn.ID)
	if err != nil {
		t.Fatalf("GetByID live turn: %v", err)
	}
	if normalizeChatTurnStatus(liveAfter.Status) != "in_progress" {
		t.Fatalf("live turn status = %q, want in_progress", liveAfter.Status)
	}
}

func TestChatTurnRepoCreateForMessageAttemptSupersedesCancellingInProgressTurn(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedChatFixture(t, ctx, pool)

	sessionRepo := NewChatSessionRepo(pool)
	messageRepo := NewChatMessageRepo(pool)
	turnRepo := NewChatTurnRepo(pool)
	cancelledAt := time.Date(2026, time.March, 22, 19, 5, 0, 0, time.UTC)
	turnRepo.now = func() time.Time { return cancelledAt }

	startedAt := cancelledAt.Add(-2 * time.Minute)
	staleMessage, err := messageRepo.Create(ctx, ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Content:   "stale trigger",
	})
	if err != nil {
		t.Fatalf("create stale message: %v", err)
	}
	cancellingTurn, err := turnRepo.Create(ctx, ChatTurn{
		SessionID:         fixture.session.ID,
		TurnNumber:        1,
		RespondingType:    "agent",
		RespondingID:      fixture.agent.ID,
		Status:            "in_progress",
		StartedAt:         &startedAt,
		CancelRequestedAt: &cancelledAt,
		TriggerMessageID:  &staleMessage.ID,
	})
	if err != nil {
		t.Fatalf("create cancelling turn: %v", err)
	}
	if _, err := sessionRepo.UpdateCurrentTurn(ctx, fixture.session.ID, &cancellingTurn.ID); err != nil {
		t.Fatalf("set cancelling current turn: %v", err)
	}

	message, err := messageRepo.Create(ctx, ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Content:   "operator correction",
	})
	if err != nil {
		t.Fatalf("create message: %v", err)
	}

	turn, created, err := turnRepo.CreateForMessageAttempt(ctx, fixture.session.ID, fixture.agent.ID, message.ID, 0)
	if err != nil {
		t.Fatalf("CreateForMessageAttempt: %v", err)
	}
	if !created {
		t.Fatal("created = false, want true when only live turn is already cancelling")
	}
	if turn.ID == cancellingTurn.ID {
		t.Fatalf("turn id = %s, want fresh turn after cancelling live turn", turn.ID)
	}
	if turn.TriggerMessageID == nil || *turn.TriggerMessageID != message.ID {
		t.Fatalf("trigger_message_id = %v, want %s", turn.TriggerMessageID, message.ID)
	}

	refreshedSession, err := sessionRepo.GetByID(ctx, fixture.session.ID)
	if err != nil {
		t.Fatalf("GetByID session: %v", err)
	}
	if refreshedSession.CurrentTurnID == nil || *refreshedSession.CurrentTurnID != turn.ID {
		t.Fatalf("current_turn_id = %v, want %s", refreshedSession.CurrentTurnID, turn.ID)
	}

	staleAfter, err := turnRepo.GetByID(ctx, cancellingTurn.ID)
	if err != nil {
		t.Fatalf("GetByID cancelling turn: %v", err)
	}
	if normalizeChatTurnStatus(staleAfter.Status) != "cancelled" {
		t.Fatalf("cancelling turn status = %q, want cancelled", staleAfter.Status)
	}
	if staleAfter.CompletedAt == nil || !staleAfter.CompletedAt.Equal(cancelledAt) {
		t.Fatalf("cancelling completed_at = %v, want %s", staleAfter.CompletedAt, cancelledAt)
	}
	if staleAfter.StopReason == nil || *staleAfter.StopReason != "superseded_live_turn" {
		t.Fatalf("cancelling stop_reason = %v, want superseded_live_turn", staleAfter.StopReason)
	}
}

func TestChatSessionRepoCloseCancelsNonTerminalTurns(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedChatFixture(t, ctx, pool)

	sessionRepo := NewChatSessionRepo(pool)
	turnRepo := NewChatTurnRepo(pool)

	pendingTurn, err := turnRepo.Create(ctx, ChatTurn{
		SessionID:      fixture.session.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   fixture.agent.ID,
		Status:         "pending",
	})
	if err != nil {
		t.Fatalf("create pending turn: %v", err)
	}
	inProgressTurn, err := turnRepo.Create(ctx, ChatTurn{
		SessionID:      fixture.session.ID,
		TurnNumber:     2,
		RespondingType: "agent",
		RespondingID:   fixture.agent.ID,
		Status:         "in_progress",
	})
	if err != nil {
		t.Fatalf("create in_progress turn: %v", err)
	}
	if _, err := sessionRepo.UpdateCurrentTurn(ctx, fixture.session.ID, &inProgressTurn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}

	closed, err := sessionRepo.Close(ctx, fixture.session.ID)
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if closed.Status != "closed" {
		t.Fatalf("session status = %q, want closed", closed.Status)
	}
	if closed.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", closed.CurrentTurnID)
	}
	if closed.ClosedAt == nil {
		t.Fatal("closed_at is nil")
	}

	assertChatTurnStatus(t, ctx, pool, pendingTurn.ID, "cancelled", "session_closed")
	assertChatTurnStatus(t, ctx, pool, inProgressTurn.ID, "cancelled", "session_closed")
}

func TestChatSessionRepoCloseProjectScopedCancelsNonTerminalTurns(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedChatFixture(t, ctx, pool)

	sessionRepo := NewChatSessionRepo(pool)
	turnRepo := NewChatTurnRepo(pool)
	taskRepo := NewProjectTaskRepo(pool)

	projectSession, err := sessionRepo.Create(ctx, ChatSession{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project",
		ScopeID:        fixture.project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       jsonRaw(`{"source":"close-project-scoped"}`),
	})
	if err != nil {
		t.Fatalf("create project session: %v", err)
	}
	projectTurn, err := turnRepo.Create(ctx, ChatTurn{
		SessionID:      projectSession.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   fixture.agent.ID,
		Status:         "pending",
	})
	if err != nil {
		t.Fatalf("create project turn: %v", err)
	}
	if _, err := sessionRepo.UpdateCurrentTurn(ctx, projectSession.ID, &projectTurn.ID); err != nil {
		t.Fatalf("set project current turn: %v", err)
	}

	task, err := taskRepo.Create(ctx, ProjectTask{
		OrganizationID: fixture.org.ID,
		ProjectID:      fixture.project.ID,
		Title:          "Task scoped chat",
		WorkStatus:     "draft",
		CreatedByType:  "system",
		Metadata:       jsonRaw(`{"source":"close-project-scoped"}`),
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}
	taskSession, err := sessionRepo.Create(ctx, ChatSession{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       jsonRaw(`{"source":"close-project-scoped-task"}`),
	})
	if err != nil {
		t.Fatalf("create task session: %v", err)
	}
	taskTurn, err := turnRepo.Create(ctx, ChatTurn{
		SessionID:      taskSession.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   fixture.agent.ID,
		Status:         "in_progress",
	})
	if err != nil {
		t.Fatalf("create task turn: %v", err)
	}
	if _, err := sessionRepo.UpdateCurrentTurn(ctx, taskSession.ID, &taskTurn.ID); err != nil {
		t.Fatalf("set task current turn: %v", err)
	}

	otherOrg, otherProject := seedTaskRepoOrgProject(t, ctx, pool)
	otherAgent := seedChatAgent(t, ctx, pool, otherOrg.ID)
	otherSession, err := sessionRepo.Create(ctx, ChatSession{
		OrganizationID: otherOrg.ID,
		ScopeType:      "project",
		ScopeID:        otherProject.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       jsonRaw(`{"source":"close-project-scoped-other"}`),
	})
	if err != nil {
		t.Fatalf("create other session: %v", err)
	}
	otherTurn, err := turnRepo.Create(ctx, ChatTurn{
		SessionID:      otherSession.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   otherAgent.ID,
		Status:         "pending",
	})
	if err != nil {
		t.Fatalf("create other turn: %v", err)
	}
	if _, err := sessionRepo.UpdateCurrentTurn(ctx, otherSession.ID, &otherTurn.ID); err != nil {
		t.Fatalf("set other current turn: %v", err)
	}

	if err := sessionRepo.CloseProjectScoped(ctx, fixture.project.ID); err != nil {
		t.Fatalf("CloseProjectScoped: %v", err)
	}

	assertChatSessionState(t, ctx, pool, projectSession.ID, "closed", false)
	assertChatSessionState(t, ctx, pool, taskSession.ID, "closed", false)
	assertChatSessionState(t, ctx, pool, otherSession.ID, "active", true)

	assertChatTurnStatus(t, ctx, pool, projectTurn.ID, "cancelled", "session_closed")
	assertChatTurnStatus(t, ctx, pool, taskTurn.ID, "cancelled", "session_closed")
	assertChatTurnStatus(t, ctx, pool, otherTurn.ID, "pending", "")
}

func TestChatMessageReactionUniqueConstraint(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedChatFixture(t, ctx, pool)

	messageRepo := NewChatMessageRepo(pool)
	reactionRepo := NewChatMessageReactionRepo(pool)

	message, err := messageRepo.Create(ctx, ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "system",
		Content:   "system message",
	})
	if err != nil {
		t.Fatalf("Create message: %v", err)
	}

	reaction := ChatMessageReaction{
		MessageID:   message.ID,
		SessionID:   fixture.session.ID,
		ReactorType: "agent",
		ReactorID:   fixture.agent.ID,
		Emoji:       "👍",
	}
	if _, err := reactionRepo.Create(ctx, reaction); err != nil {
		t.Fatalf("Create first reaction: %v", err)
	}
	if _, err := reactionRepo.Create(ctx, reaction); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate reaction error = %v, want ErrConflict", err)
	}
}

func TestChatSummaryUniqueFromSequenceConstraint(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedChatFixture(t, ctx, pool)

	summaryRepo := NewChatSummaryRepo(pool)
	if _, err := summaryRepo.Create(ctx, ChatSummary{
		SessionID:           fixture.session.ID,
		FromSequence:        1,
		ToSequence:          10,
		SummaryText:         "summary a",
		SummarizedTurnCount: 2,
	}); err != nil {
		t.Fatalf("Create first summary: %v", err)
	}

	_, err := summaryRepo.Create(ctx, ChatSummary{
		SessionID:           fixture.session.ID,
		FromSequence:        1,
		ToSequence:          20,
		SummaryText:         "summary b",
		SummarizedTurnCount: 3,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate from_sequence error = %v, want ErrConflict", err)
	}
}

func TestChatSessionScopeCanStoreMultipleRowsWithoutDBUniqueConstraint(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedChatFixture(t, ctx, pool)

	sessionRepo := NewChatSessionRepo(pool)

	_, err := sessionRepo.Create(ctx, ChatSession{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project",
		ScopeID:        fixture.project.ID,
		Mode:           "sync",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       jsonRaw(`{"index":1}`),
	})
	if err != nil {
		t.Fatalf("Create first duplicate-scope session: %v", err)
	}

	_, err = sessionRepo.Create(ctx, ChatSession{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project",
		ScopeID:        fixture.project.ID,
		Mode:           "sync",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       jsonRaw(`{"index":2}`),
	})
	if err != nil {
		t.Fatalf("Create second duplicate-scope session: %v", err)
	}
}

func TestChatReadCursorRepoUpsertKeepsSingleRowAndLatestSequence(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedChatFixture(t, ctx, pool)
	user := seedTaskRepoUser(t, ctx, pool, fixture.org.ID, "chat-read-cursor")

	cursorRepo := NewChatReadCursorRepo(pool)

	first, err := cursorRepo.Upsert(ctx, ChatReadCursor{
		SessionID:        fixture.session.ID,
		UserID:           user.ID,
		LastReadSequence: 3,
	})
	if err != nil {
		t.Fatalf("first Upsert: %v", err)
	}

	second, err := cursorRepo.Upsert(ctx, ChatReadCursor{
		SessionID:        fixture.session.ID,
		UserID:           user.ID,
		LastReadSequence: 8,
	})
	if err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("cursor id changed from %s to %s", first.ID, second.ID)
	}
	if second.LastReadSequence != 8 {
		t.Fatalf("last_read_sequence = %d, want 8", second.LastReadSequence)
	}

	var count int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM chat_read_cursor
		WHERE session_id = $1
		  AND user_id = $2
	`, fixture.session.ID, user.ID).Scan(&count); err != nil {
		t.Fatalf("count chat_read_cursor rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("chat_read_cursor row count = %d, want 1", count)
	}
}

func TestChatMigrationsRecordedInSchemaMigrations(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	var count int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM schema_migrations
		WHERE version BETWEEN 57 AND 65
	`).Scan(&count); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if count != 9 {
		t.Fatalf("chat migration count = %d, want 9", count)
	}
}

type chatFixture struct {
	org     Organization
	project Project
	agent   Agent
	session ChatSession
}

func seedChatFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) chatFixture {
	t.Helper()

	org, project := seedTaskRepoOrgProject(t, ctx, pool)
	agent := seedChatAgent(t, ctx, pool, org.ID)
	sessionRepo := NewChatSessionRepo(pool)

	session, err := sessionRepo.Create(ctx, ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "sync",
		Status:         "active",
		Title:          trimTestString("Chat Session"),
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       jsonRaw(`{"source":"integration"}`),
	})
	if err != nil {
		t.Fatalf("create chat session: %v", err)
	}

	return chatFixture{org: org, project: project, agent: agent, session: session}
}

func seedChatAgent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) Agent {
	t.Helper()

	agentRepo := NewAgentRepo(pool)
	agent, err := agentRepo.Create(ctx, Agent{
		OrganizationID:       orgID,
		DisplayName:          "Chat Agent",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "You are a chat agent",
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
		t.Fatalf("create chat agent: %v", err)
	}
	return agent
}

func trimTestString(value string) *string {
	trimmed := value
	return &trimmed
}

func assertChatTurnStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, turnID uuid.UUID, wantStatus string, wantStopReason string) {
	t.Helper()

	var status string
	var stopReason *string
	if err := pool.QueryRow(ctx, `
		SELECT status, stop_reason
		FROM chat_turn
		WHERE id = $1
	`, turnID).Scan(&status, &stopReason); err != nil {
		t.Fatalf("query turn %s: %v", turnID, err)
	}
	if normalizeChatTurnStatus(status) != wantStatus {
		t.Fatalf("turn %s status = %q, want %q", turnID, status, wantStatus)
	}
	if wantStopReason == "" {
		if stopReason != nil {
			t.Fatalf("turn %s stop_reason = %v, want nil", turnID, stopReason)
		}
		return
	}
	if stopReason == nil || *stopReason != wantStopReason {
		t.Fatalf("turn %s stop_reason = %v, want %q", turnID, stopReason, wantStopReason)
	}
}

func assertChatSessionState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID uuid.UUID, wantStatus string, wantCurrentTurn bool) {
	t.Helper()

	var status string
	var currentTurnID *uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT status, current_turn_id
		FROM chat_session
		WHERE id = $1
	`, sessionID).Scan(&status, &currentTurnID); err != nil {
		t.Fatalf("query session %s: %v", sessionID, err)
	}
	if status != wantStatus {
		t.Fatalf("session %s status = %q, want %q", sessionID, status, wantStatus)
	}
	if wantCurrentTurn && currentTurnID == nil {
		t.Fatalf("session %s current_turn_id = nil, want present", sessionID)
	}
	if !wantCurrentTurn && currentTurnID != nil {
		t.Fatalf("session %s current_turn_id = %v, want nil", sessionID, currentTurnID)
	}
}
