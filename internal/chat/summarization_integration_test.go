//go:build integration

package chat

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
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestSummarizerIntegrationRunForSessionRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	seedSummarizationProfile(t, ctx, pool, org.ID)

	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)
	agent := seedChatServiceAgent(t, ctx, pool, org.ID)
	seedTurnMessages(t, ctx, pool, session.ID, agent.ID, 10, 4)

	modelStub := &integrationSummarizationModel{invocations: repo.NewModelInvocationRepo(pool)}
	summarizer, err := NewSummarizer(SummarizerOptions{Pool: pool, Model: modelStub})
	if err != nil {
		t.Fatalf("NewSummarizer: %v", err)
	}

	summary, err := summarizer.RunForSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("RunForSession: %v", err)
	}
	if summary.FromSequence != 1 || summary.ToSequence != 12 {
		t.Fatalf("summary range = %d-%d, want 1-12", summary.FromSequence, summary.ToSequence)
	}

	if len(modelStub.calls) != 1 {
		t.Fatalf("model call count = %d, want 1", len(modelStub.calls))
	}
	if modelStub.calls[0].InvocationPurpose != "summarization" {
		t.Fatalf("invocation purpose = %q, want summarization", modelStub.calls[0].InvocationPurpose)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat_summary WHERE session_id = $1`, session.ID).Scan(&count); err != nil {
		t.Fatalf("count chat_summary: %v", err)
	}
	if count != 1 {
		t.Fatalf("chat_summary rows = %d, want 1", count)
	}
}

func TestSummarizerIntegrationRunForSessionIdempotentWhenRangeAlreadyCovered(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	seedSummarizationProfile(t, ctx, pool, org.ID)

	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)
	agent := seedChatServiceAgent(t, ctx, pool, org.ID)
	seedTurnMessages(t, ctx, pool, session.ID, agent.ID, 1, 4)

	modelStub := &integrationSummarizationModel{invocations: repo.NewModelInvocationRepo(pool)}
	summarizer, err := NewSummarizer(SummarizerOptions{Pool: pool, Model: modelStub})
	if err != nil {
		t.Fatalf("NewSummarizer: %v", err)
	}

	if _, err := summarizer.RunForSession(ctx, session.ID); err != nil {
		t.Fatalf("first RunForSession: %v", err)
	}
	if _, err := summarizer.RunForSession(ctx, session.ID); !errors.Is(err, ErrAlreadySummarized) {
		t.Fatalf("second RunForSession error = %v, want ErrAlreadySummarized", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat_summary WHERE session_id = $1`, session.ID).Scan(&count); err != nil {
		t.Fatalf("count chat_summary: %v", err)
	}
	if count != 1 {
		t.Fatalf("chat_summary rows = %d, want 1", count)
	}
}

func TestSummarization_Trigger_ThresholdCheck(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	seedSummarizationProfile(t, ctx, pool, org.ID)

	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)
	agent := seedChatServiceAgent(t, ctx, pool, org.ID)
	seedTurnMessages(t, ctx, pool, session.ID, agent.ID, 6, 2)

	checker, err := NewSummarizationChecker(SummarizationCheckerOptions{Pool: pool})
	if err != nil {
		t.Fatalf("NewSummarizationChecker: %v", err)
	}

	should, err := checker.ShouldSummarize(ctx, session.ID, 80)
	if err != nil {
		t.Fatalf("ShouldSummarize: %v", err)
	}
	if !should {
		t.Fatal("ShouldSummarize = false, want true")
	}

	modelStub := &integrationSummarizationModel{invocations: repo.NewModelInvocationRepo(pool)}
	summarizer, err := NewSummarizer(SummarizerOptions{Pool: pool, Model: modelStub})
	if err != nil {
		t.Fatalf("NewSummarizer: %v", err)
	}
	if _, err := summarizer.RunForSession(ctx, session.ID); err != nil {
		t.Fatalf("RunForSession: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat_summary WHERE session_id = $1`, session.ID).Scan(&count); err != nil {
		t.Fatalf("count chat_summary: %v", err)
	}
	if count != 1 {
		t.Fatalf("chat_summary rows = %d, want 1", count)
	}
}

func TestSummarization_ImmutableRow(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	seedSummarizationProfile(t, ctx, pool, org.ID)

	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)
	agent := seedChatServiceAgent(t, ctx, pool, org.ID)
	seedTurnMessages(t, ctx, pool, session.ID, agent.ID, 1, 2)

	modelStub := &integrationSummarizationModel{invocations: repo.NewModelInvocationRepo(pool)}
	summarizer, err := NewSummarizer(SummarizerOptions{Pool: pool, Model: modelStub})
	if err != nil {
		t.Fatalf("NewSummarizer: %v", err)
	}

	first, err := summarizer.RunForSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("RunForSession first: %v", err)
	}
	if _, err := summarizer.RunForSession(ctx, session.ID); !errors.Is(err, ErrAlreadySummarized) {
		t.Fatalf("RunForSession second error = %v, want ErrAlreadySummarized", err)
	}

	stored, err := repo.NewChatSummaryRepo(pool).GetLatestForSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetLatestForSession: %v", err)
	}
	if stored.FromSequence != first.FromSequence || stored.ToSequence != first.ToSequence {
		t.Fatalf("summary range changed to %d-%d, want %d-%d", stored.FromSequence, stored.ToSequence, first.FromSequence, first.ToSequence)
	}
}

func TestSummarization_PreservesArtifactRefs(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	seedSummarizationProfile(t, ctx, pool, org.ID)

	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)
	agent := seedChatServiceAgent(t, ctx, pool, org.ID)

	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:      session.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   agent.ID,
		Status:         "completed",
	})
	if err != nil {
		t.Fatalf("create turn: %v", err)
	}
	refOne := "/workspace/src/main.go"
	refTwo := "s3://build-artifacts/run-42/output.log"
	if _, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		TurnID:    &turn.ID,
		Role:      "assistant",
		Status:    "final",
		Content:   "Preserve refs: " + refOne + " and " + refTwo,
	}); err != nil {
		t.Fatalf("create message with refs: %v", err)
	}

	summarizer, err := NewSummarizer(SummarizerOptions{
		Pool:  pool,
		Model: echoSummarizationModel{},
	})
	if err != nil {
		t.Fatalf("NewSummarizer: %v", err)
	}
	summary, err := summarizer.RunForSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("RunForSession: %v", err)
	}
	if !strings.Contains(summary.SummaryText, refOne) {
		t.Fatalf("summary missing ref %q in %q", refOne, summary.SummaryText)
	}
	if !strings.Contains(summary.SummaryText, refTwo) {
		t.Fatalf("summary missing ref %q in %q", refTwo, summary.SummaryText)
	}
}

func TestSummarizerIntegrationShrinksCandidateToProfileBudget(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	seedSmallSummarizationProfile(t, ctx, pool, org.ID)

	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)
	agent := seedChatServiceAgent(t, ctx, pool, org.ID)

	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:      session.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   agent.ID,
		Status:         "completed",
	})
	if err != nil {
		t.Fatalf("create turn: %v", err)
	}
	messageRepo := repo.NewChatMessageRepo(pool)
	for i := 0; i < 3; i++ {
		if _, err := messageRepo.Create(ctx, repo.ChatMessage{
			SessionID: session.ID,
			TurnID:    &turn.ID,
			Role:      "user",
			Status:    "final",
			Content:   strings.Repeat(string(rune('a'+i)), 1800),
		}); err != nil {
			t.Fatalf("create message %d: %v", i, err)
		}
	}

	modelStub := &integrationSummarizationModel{invocations: repo.NewModelInvocationRepo(pool)}
	summarizer, err := NewSummarizer(SummarizerOptions{Pool: pool, Model: modelStub})
	if err != nil {
		t.Fatalf("NewSummarizer: %v", err)
	}

	summary, err := summarizer.RunForSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("RunForSession: %v", err)
	}
	if summary.FromSequence != 1 || summary.ToSequence != 1 {
		t.Fatalf("summary range = %d-%d, want 1-1", summary.FromSequence, summary.ToSequence)
	}
	if len(modelStub.calls) != 1 {
		t.Fatalf("model call count = %d, want 1", len(modelStub.calls))
	}
	if got := len(modelStub.calls[0].Messages); got != 1 {
		t.Fatalf("model message count = %d, want 1", got)
	}
}

func TestSummarization_SummarizesOldestTurns(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	seedSummarizationProfile(t, ctx, pool, org.ID)

	svc := newIntegrationService(t, pool, nil)
	session := mustCreateSession(t, ctx, svc, org.ID)
	agent := seedChatServiceAgent(t, ctx, pool, org.ID)
	seedTurnMessages(t, ctx, pool, session.ID, agent.ID, 10, 1)

	modelStub := &integrationSummarizationModel{invocations: repo.NewModelInvocationRepo(pool)}
	summarizer, err := NewSummarizer(SummarizerOptions{Pool: pool, Model: modelStub})
	if err != nil {
		t.Fatalf("NewSummarizer: %v", err)
	}

	summary, err := summarizer.RunForSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("RunForSession: %v", err)
	}

	var maxSequence int64
	if err := pool.QueryRow(ctx, `SELECT MAX(sequence_number) FROM chat_message WHERE session_id = $1`, session.ID).Scan(&maxSequence); err != nil {
		t.Fatalf("query max sequence: %v", err)
	}
	if summary.FromSequence != 1 {
		t.Fatalf("summary.FromSequence = %d, want 1", summary.FromSequence)
	}
	if summary.ToSequence >= maxSequence {
		t.Fatalf("summary.ToSequence = %d, want < max sequence %d", summary.ToSequence, maxSequence)
	}

	var recentUnsummarizedCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM chat_message
		WHERE session_id = $1
		  AND sequence_number > $2
	`, session.ID, summary.ToSequence).Scan(&recentUnsummarizedCount); err != nil {
		t.Fatalf("count recent unsummarized messages: %v", err)
	}
	if recentUnsummarizedCount == 0 {
		t.Fatal("expected most recent messages to remain unsummarized, found none")
	}
}

func TestRetention_SessionCleanup(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	bus := newIntegrationEventBus(pool)
	svc := newIntegrationService(t, pool, bus)
	session := mustCreateSession(t, ctx, svc, org.ID)

	events := make(chan eventbus.DomainEvent, 1)
	sub := bus.Subscribe("chat-session-cleanup-"+uuid.NewString(), &org.ID, func(_ context.Context, event eventbus.DomainEvent) error {
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
	case <-events:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for chat.session.closed event")
	}

	rows, err := pool.Query(ctx, `
		SELECT job_type, payload, run_after
		FROM job_queue
		WHERE payload->>'session_id' = $1
	`, session.ID.String())
	if err != nil {
		t.Fatalf("query session cleanup jobs: %v", err)
	}
	defer rows.Close()

	seenSummarize := false
	cleanupTypes := map[string]bool{}
	for rows.Next() {
		var jobType string
		var payload json.RawMessage
		var runAfter *time.Time
		if err := rows.Scan(&jobType, &payload, &runAfter); err != nil {
			t.Fatalf("scan job row: %v", err)
		}

		switch jobType {
		case ChatSummarizeJobType:
			seenSummarize = true
		case ChatSessionCleanupJobType:
			var decoded map[string]any
			if err := json.Unmarshal(payload, &decoded); err != nil {
				t.Fatalf("unmarshal cleanup payload: %v", err)
			}
			cleanupType, _ := decoded["cleanup_type"].(string)
			cleanupTypes[cleanupType] = true
			if runAfter == nil {
				t.Fatalf("cleanup job %q has nil run_after", cleanupType)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate jobs: %v", err)
	}

	if !seenSummarize {
		t.Fatal("missing chat_summarize job")
	}
	for _, cleanupType := range []string{
		CleanupTypeEphemeralPurge,
		CleanupTypeToolCompaction,
		CleanupTypeSummaryConsolidate,
	} {
		if !cleanupTypes[cleanupType] {
			t.Fatalf("missing cleanup job type %q", cleanupType)
		}
	}
}

func TestSessionCleanerIntegrationSummaryConsolidation(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)
	seedSummarizationProfile(t, ctx, pool, org.ID)

	session := seedClosedSession(t, ctx, pool, org.ID, "sync", json.RawMessage(`{}`))
	summaryRepo := repo.NewChatSummaryRepo(pool)
	for idx := 0; idx < 7; idx++ {
		from := int64(idx*10 + 1)
		to := int64((idx + 1) * 10)
		if _, err := summaryRepo.Create(ctx, repo.ChatSummary{
			SessionID:           session.ID,
			FromSequence:        from,
			ToSequence:          to,
			SummaryText:         fmt.Sprintf("segment-%d", idx+1),
			SummarizedTurnCount: 1,
		}); err != nil {
			t.Fatalf("create summary %d: %v", idx, err)
		}
	}

	modelStub := &integrationSummarizationModel{invocations: repo.NewModelInvocationRepo(pool)}
	cleaner, err := NewSessionCleaner(SessionCleanerOptions{Pool: pool, Model: modelStub})
	if err != nil {
		t.Fatalf("NewSessionCleaner: %v", err)
	}

	if err := cleaner.RunCleanup(ctx, session.ID, CleanupTypeSummaryConsolidate); err != nil {
		t.Fatalf("RunCleanup summary_consolidation: %v", err)
	}

	summaries, err := summaryRepo.ListBySession(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("summary count = %d, want 1", len(summaries))
	}
	if summaries[0].FromSequence != 1 || summaries[0].ToSequence != 70 {
		t.Fatalf("consolidated range = %d-%d, want 1-70", summaries[0].FromSequence, summaries[0].ToSequence)
	}
}

func TestSessionCleanerIntegrationToolResultCompaction(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)

	session := seedClosedSession(t, ctx, pool, org.ID, "sync", json.RawMessage(`{}`))
	messageRepo := repo.NewChatMessageRepo(pool)
	msg, err := messageRepo.Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "tool_result",
		Content:   strings.Repeat("x", 5000),
		Status:    "final",
		Metadata:  json.RawMessage(`{"source":"tool"}`),
	})
	if err != nil {
		t.Fatalf("create tool_result message: %v", err)
	}

	cleaner, err := NewSessionCleaner(SessionCleanerOptions{Pool: pool})
	if err != nil {
		t.Fatalf("NewSessionCleaner: %v", err)
	}
	if err := cleaner.RunCleanup(ctx, session.ID, CleanupTypeToolCompaction); err != nil {
		t.Fatalf("RunCleanup tool_result_compaction: %v", err)
	}

	var content, status string
	var metadata json.RawMessage
	if err := pool.QueryRow(ctx, `SELECT content, status, metadata FROM chat_message WHERE id = $1`, msg.ID).Scan(&content, &status, &metadata); err != nil {
		t.Fatalf("select compacted message: %v", err)
	}
	if content != "[tool_result compacted — 5000 bytes]" {
		t.Fatalf("content = %q, want %q", content, "[tool_result compacted — 5000 bytes]")
	}
	if status != "final" {
		t.Fatalf("status = %s, want final", status)
	}

	var parsed map[string]any
	if err := json.Unmarshal(metadata, &parsed); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if compacted, ok := parsed["compacted"].(bool); !ok || !compacted {
		t.Fatalf("metadata.compacted = %v, want true", parsed["compacted"])
	}
}

func TestSessionCleanerIntegrationEphemeralPurgePreservesAssistantAndUser(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)

	session := seedClosedSession(t, ctx, pool, org.ID, "async", json.RawMessage(`{"flow_node_execution_id":"`+uuid.NewString()+`"}`))
	messageRepo := repo.NewChatMessageRepo(pool)
	toolCall, err := messageRepo.Create(ctx, repo.ChatMessage{SessionID: session.ID, Role: "tool_call", Content: "call", Status: "final"})
	if err != nil {
		t.Fatalf("create tool_call: %v", err)
	}
	toolResult, err := messageRepo.Create(ctx, repo.ChatMessage{SessionID: session.ID, Role: "tool_result", Content: "result", Status: "final"})
	if err != nil {
		t.Fatalf("create tool_result: %v", err)
	}
	assistant, err := messageRepo.Create(ctx, repo.ChatMessage{SessionID: session.ID, Role: "assistant", Content: "assistant", Status: "final"})
	if err != nil {
		t.Fatalf("create assistant: %v", err)
	}
	user, err := messageRepo.Create(ctx, repo.ChatMessage{SessionID: session.ID, Role: "user", Content: "user", Status: "final"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	old := time.Now().UTC().Add(-91 * 24 * time.Hour)
	if _, err := pool.Exec(ctx, `UPDATE chat_message SET created_at = $2 WHERE id = ANY($1::uuid[])`, []uuid.UUID{toolCall.ID, toolResult.ID, assistant.ID, user.ID}, old); err != nil {
		t.Fatalf("age chat_message rows: %v", err)
	}

	cleaner, err := NewSessionCleaner(SessionCleanerOptions{Pool: pool})
	if err != nil {
		t.Fatalf("NewSessionCleaner: %v", err)
	}
	if err := cleaner.RunCleanup(ctx, session.ID, CleanupTypeEphemeralPurge); err != nil {
		t.Fatalf("RunCleanup ephemeral_purge: %v", err)
	}

	var roles []string
	rows, err := pool.Query(ctx, `SELECT role FROM chat_message WHERE session_id = $1 ORDER BY sequence_number`, session.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			t.Fatalf("scan role: %v", err)
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate roles: %v", err)
	}

	joined := strings.Join(roles, ",")
	if joined != "assistant,user" {
		t.Fatalf("remaining roles = %q, want assistant,user", joined)
	}
}

func TestRetentionSweeperIntegrationArchivesClosedSessions(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := seedChatServiceOrg(t, ctx, pool)

	session := seedClosedSession(t, ctx, pool, org.ID, "sync", json.RawMessage(`{}`))
	messageRepo := repo.NewChatMessageRepo(pool)
	artifactRepo := repo.NewChatArtifactRepo(pool)
	summaryRepo := repo.NewChatSummaryRepo(pool)

	message, err := messageRepo.Create(ctx, repo.ChatMessage{SessionID: session.ID, Role: "assistant", Content: "history", Status: "final"})
	if err != nil {
		t.Fatalf("create message: %v", err)
	}
	if _, err := artifactRepo.Create(ctx, repo.ChatArtifact{SessionID: session.ID, MessageID: message.ID, ArtifactType: "file", Filename: strPtr("report.txt")}); err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	if _, err := summaryRepo.Create(ctx, repo.ChatSummary{SessionID: session.ID, FromSequence: 1, ToSequence: 1, SummaryText: "summary", SummarizedTurnCount: 1}); err != nil {
		t.Fatalf("create summary: %v", err)
	}

	oldClosedAt := time.Now().UTC().Add(-100 * 24 * time.Hour)
	if _, err := pool.Exec(ctx, `UPDATE chat_session SET closed_at = $2 WHERE id = $1`, session.ID, oldClosedAt); err != nil {
		t.Fatalf("age chat_session.closed_at: %v", err)
	}

	sweeper, err := NewRetentionSweeper(RetentionSweeperOptions{Pool: pool})
	if err != nil {
		t.Fatalf("NewRetentionSweeper: %v", err)
	}
	if err := sweeper.RunSweep(ctx, time.Now().UTC().Add(-90*24*time.Hour)); err != nil {
		t.Fatalf("RunSweep: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM chat_session WHERE id = $1`, session.ID).Scan(&status); err != nil {
		t.Fatalf("select chat_session status: %v", err)
	}
	if status != "archived" {
		t.Fatalf("session status = %s, want archived", status)
	}

	assertArchivedMetadataKey(t, ctx, pool, "chat_message", "session_id", session.ID)
	assertArchivedMetadataKey(t, ctx, pool, "chat_artifact", "session_id", session.ID)
	assertArchivedMetadataKey(t, ctx, pool, "chat_summary", "session_id", session.ID)
}

type integrationSummarizationModel struct {
	invocations *repo.ModelInvocationRepo
	calls       []SummarizationRequest
}

type echoSummarizationModel struct{}

func (echoSummarizationModel) Summarize(_ context.Context, req SummarizationRequest) (SummarizationResponse, error) {
	parts := make([]string, 0, len(req.Messages)+len(req.Summaries))
	for _, item := range req.Messages {
		if strings.TrimSpace(item.Content) == "" {
			continue
		}
		parts = append(parts, item.Content)
	}
	for _, item := range req.Summaries {
		if strings.TrimSpace(item.SummaryText) == "" {
			continue
		}
		parts = append(parts, item.SummaryText)
	}
	if len(parts) == 0 {
		parts = append(parts, "summary")
	}
	return SummarizationResponse{SummaryText: strings.Join(parts, "\n")}, nil
}

func (m *integrationSummarizationModel) Summarize(ctx context.Context, req SummarizationRequest) (SummarizationResponse, error) {
	m.calls = append(m.calls, req)

	profileID := req.Profile.LogicalProfileID
	sessionID := req.SessionID
	now := time.Now().UTC()
	invocation, err := m.invocations.Create(ctx, repo.ModelInvocation{
		OrganizationID:    req.OrganizationID,
		ModelProviderID:   req.Profile.ProviderID,
		ModelProfileID:    &profileID,
		InvocationPurpose: req.InvocationPurpose,
		Status:            "completed",
		ModelName:         req.Profile.ModelName,
		SessionID:         &sessionID,
		CompletedAt:       &now,
	})
	if err != nil {
		return SummarizationResponse{}, err
	}

	summaryText := "summary"
	if len(req.Summaries) > 0 {
		summaryText = "consolidated summary"
	}
	return SummarizationResponse{SummaryText: summaryText, ModelInvocationID: &invocation.ID}, nil
}

func seedSummarizationProfile(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) {
	t.Helper()
	providerRepo := repo.NewModelProviderRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)
	assignmentRepo := repo.NewModelProfileAssignmentRepo(pool)

	provider, err := providerRepo.Create(ctx, repo.ModelProvider{
		Slug:        "chat-summary-provider-" + uuid.NewString()[:8],
		DisplayName: "Chat Summary Provider",
		APIBaseURL:  "https://example.invalid/v1",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}

	logicalProfileID := "chat-summary-" + uuid.NewString()[:8]
	created, err := profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    logicalProfileID,
		OrganizationID:      &orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          provider.ID,
		ModelName:           "gpt-4o-mini",
		DisplayName:         "Chat Summary",
		ContextWindowTokens: 64000,
		MaxOutputTokens:     2000,
		SupportsStreaming:   false,
		SupportsVision:      false,
		InvocationPurpose:   "summarization",
	})
	if err != nil {
		t.Fatalf("create model profile: %v", err)
	}

	if _, err := assignmentRepo.Upsert(ctx, repo.ModelProfileAssignment{
		OrganizationID:    orgID,
		ScopeType:         "organization",
		ScopeID:           orgID,
		LogicalProfileID:  created.LogicalProfileID,
		InvocationPurpose: "summarization",
	}); err != nil {
		t.Fatalf("upsert model profile assignment: %v", err)
	}
}

func seedSmallSummarizationProfile(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) {
	t.Helper()
	providerRepo := repo.NewModelProviderRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)
	assignmentRepo := repo.NewModelProfileAssignmentRepo(pool)

	provider, err := providerRepo.Create(ctx, repo.ModelProvider{
		Slug:        "chat-summary-small-provider-" + uuid.NewString()[:8],
		DisplayName: "Chat Summary Small Provider",
		APIBaseURL:  "https://example.invalid/v1",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}

	logicalProfileID := "chat-summary-small-" + uuid.NewString()[:8]
	created, err := profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    logicalProfileID,
		OrganizationID:      &orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          provider.ID,
		ModelName:           "gpt-4o-mini",
		DisplayName:         "Chat Summary Small",
		ContextWindowTokens: 1400,
		MaxOutputTokens:     200,
		SupportsStreaming:   false,
		SupportsVision:      false,
		InvocationPurpose:   "summarization",
	})
	if err != nil {
		t.Fatalf("create model profile: %v", err)
	}

	if _, err := assignmentRepo.Upsert(ctx, repo.ModelProfileAssignment{
		OrganizationID:    orgID,
		ScopeType:         "organization",
		ScopeID:           orgID,
		LogicalProfileID:  created.LogicalProfileID,
		InvocationPurpose: "summarization",
	}); err != nil {
		t.Fatalf("upsert model profile assignment: %v", err)
	}
}

func seedTurnMessages(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID, agentID uuid.UUID, turns int, messagesPerTurn int) {
	t.Helper()
	turnRepo := repo.NewChatTurnRepo(pool)
	messageRepo := repo.NewChatMessageRepo(pool)

	for turnNumber := 1; turnNumber <= turns; turnNumber++ {
		turn, err := turnRepo.Create(ctx, repo.ChatTurn{
			SessionID:      sessionID,
			TurnNumber:     turnNumber,
			RespondingType: "agent",
			RespondingID:   agentID,
			Status:         "completed",
		})
		if err != nil {
			t.Fatalf("create chat_turn %d: %v", turnNumber, err)
		}
		for idx := 0; idx < messagesPerTurn; idx++ {
			if _, err := messageRepo.Create(ctx, repo.ChatMessage{
				SessionID: sessionID,
				TurnID:    &turn.ID,
				Role:      "assistant",
				Status:    "final",
				Content:   fmt.Sprintf("turn %d message %d", turnNumber, idx+1),
			}); err != nil {
				t.Fatalf("create chat_message turn %d msg %d: %v", turnNumber, idx+1, err)
			}
		}
	}
}

func seedClosedSession(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, mode string, metadata json.RawMessage) repo.ChatSession {
	t.Helper()
	sessionRepo := repo.NewChatSessionRepo(pool)
	session, err := sessionRepo.Create(ctx, repo.ChatSession{
		OrganizationID: orgID,
		ScopeType:      "organization",
		ScopeID:        orgID,
		Mode:           mode,
		Status:         "closed",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       metadata,
	})
	if err != nil {
		t.Fatalf("create closed session: %v", err)
	}
	return session
}

func assertArchivedMetadataKey(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, key string, value uuid.UUID) {
	t.Helper()
	query := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s = $1 AND metadata ? 'archived_at'`, table, key)
	var count int
	if err := pool.QueryRow(ctx, query, value).Scan(&count); err != nil {
		t.Fatalf("assert archived_at metadata on %s: %v", table, err)
	}
	if count == 0 {
		t.Fatalf("%s rows missing metadata.archived_at marker", table)
	}
}

func strPtr(value string) *string {
	copy := value
	return &copy
}
