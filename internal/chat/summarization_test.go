package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/model"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestSummarizationCheckerShouldSummarizeThreshold(t *testing.T) {
	t.Run("estimated at roughly 52 percent content crosses threshold", func(t *testing.T) {
		sessionID := uuid.New()
		checker, err := NewSummarizationChecker(SummarizationCheckerOptions{
			Messages: &fakeMessageRepo{
				listBySessionFn: func(_ context.Context, id uuid.UUID) ([]repo.ChatMessage, error) {
					if id != sessionID {
						t.Fatalf("unexpected session id %s", id)
					}
					return []repo.ChatMessage{{SessionID: sessionID, SequenceNumber: 1, Role: "assistant", Content: strings.Repeat("a", 208)}}, nil
				},
			},
			Summaries: &fakeSummaryRepo{},
		})
		if err != nil {
			t.Fatalf("NewSummarizationChecker: %v", err)
		}

		should, err := checker.ShouldSummarize(context.Background(), sessionID, 100)
		if err != nil {
			t.Fatalf("ShouldSummarize: %v", err)
		}
		if !should {
			t.Fatal("ShouldSummarize = false, want true")
		}
	})

	t.Run("estimated at roughly 48 percent content stays below threshold", func(t *testing.T) {
		sessionID := uuid.New()
		checker, err := NewSummarizationChecker(SummarizationCheckerOptions{
			Messages: &fakeMessageRepo{
				listBySessionFn: func(_ context.Context, id uuid.UUID) ([]repo.ChatMessage, error) {
					if id != sessionID {
						t.Fatalf("unexpected session id %s", id)
					}
					return []repo.ChatMessage{{SessionID: sessionID, SequenceNumber: 1, Role: "assistant", Content: strings.Repeat("a", 192)}}, nil
				},
			},
			Summaries: &fakeSummaryRepo{},
		})
		if err != nil {
			t.Fatalf("NewSummarizationChecker: %v", err)
		}

		should, err := checker.ShouldSummarize(context.Background(), sessionID, 100)
		if err != nil {
			t.Fatalf("ShouldSummarize: %v", err)
		}
		if should {
			t.Fatal("ShouldSummarize = true, want false")
		}
	})
}

func TestSummarizerRunForSessionSelectsOldestRange(t *testing.T) {
	t.Run("100 unsummarized turns chooses 1-28", func(t *testing.T) {
		sessionID := uuid.New()
		orgID := uuid.New()

		messages := make([]repo.ChatMessage, 0, 100)
		for turn := 1; turn <= 100; turn++ {
			turnID := uuid.New()
			messages = append(messages, repo.ChatMessage{
				SessionID:      sessionID,
				TurnID:         &turnID,
				SequenceNumber: int64(turn),
				Role:           "assistant",
				Content:        fmt.Sprintf("turn-%d", turn),
			})
		}

		summaryRepo := &fakeSummaryRepo{}
		summarizer, err := NewSummarizer(SummarizerOptions{
			Sessions: &fakeSessionRepo{
				getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
					return repo.ChatSession{ID: id, OrganizationID: orgID, ScopeType: "organization", ScopeID: orgID, Status: "active"}, nil
				},
			},
			Messages:  &fakeMessageRepo{listBySessionFn: func(context.Context, uuid.UUID) ([]repo.ChatMessage, error) { return messages, nil }},
			Summaries: summaryRepo,
			Tasks:     &fakeTaskRepo{},
			Resolver:  fixedResolver{profile: repo.ModelProfile{LogicalProfileID: "summary", InvocationPurpose: "summarization"}},
			Model: &fakeSummarizationModel{summarizeFn: func(_ context.Context, req SummarizationRequest) (SummarizationResponse, error) {
				if req.InvocationPurpose != "summarization" {
					t.Fatalf("invocation purpose = %q, want summarization", req.InvocationPurpose)
				}
				return SummarizationResponse{SummaryText: "ok"}, nil
			}},
		})
		if err != nil {
			t.Fatalf("NewSummarizer: %v", err)
		}

		summary, err := summarizer.RunForSession(context.Background(), sessionID)
		if err != nil {
			t.Fatalf("RunForSession: %v", err)
		}
		if summary.FromSequence != 1 || summary.ToSequence != 28 {
			t.Fatalf("range = %d-%d, want 1-28", summary.FromSequence, summary.ToSequence)
		}
	})

	t.Run("turns 1-50 summarized and 51-150 unsummarized chooses 51-87", func(t *testing.T) {
		sessionID := uuid.New()
		orgID := uuid.New()

		messages := make([]repo.ChatMessage, 0, 200)
		sequence := int64(1)
		for turn := 1; turn <= 150; turn++ {
			turnID := uuid.New()
			messageCount := 1
			if turn >= 88 && turn <= 119 {
				messageCount = 2
			}
			for idx := 0; idx < messageCount; idx++ {
				messages = append(messages, repo.ChatMessage{
					SessionID:      sessionID,
					TurnID:         &turnID,
					SequenceNumber: sequence,
					Role:           "assistant",
					Content:        fmt.Sprintf("turn-%d-msg-%d", turn, idx+1),
				})
				sequence++
			}
		}

		summaryRepo := &fakeSummaryRepo{
			listBySessionFn: func(_ context.Context, id uuid.UUID) ([]repo.ChatSummary, error) {
				if id != sessionID {
					t.Fatalf("unexpected session id %s", id)
				}
				return []repo.ChatSummary{{SessionID: sessionID, FromSequence: 1, ToSequence: 50, SummaryText: "already summarized", SummarizedTurnCount: 50}}, nil
			},
		}

		summarizer, err := NewSummarizer(SummarizerOptions{
			Sessions: &fakeSessionRepo{
				getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
					return repo.ChatSession{ID: id, OrganizationID: orgID, ScopeType: "organization", ScopeID: orgID, Status: "active"}, nil
				},
			},
			Messages:  &fakeMessageRepo{listBySessionFn: func(context.Context, uuid.UUID) ([]repo.ChatMessage, error) { return messages, nil }},
			Summaries: summaryRepo,
			Tasks:     &fakeTaskRepo{},
			Resolver:  fixedResolver{profile: repo.ModelProfile{LogicalProfileID: "summary", InvocationPurpose: "summarization"}},
			Model: &fakeSummarizationModel{summarizeFn: func(_ context.Context, _ SummarizationRequest) (SummarizationResponse, error) {
				return SummarizationResponse{SummaryText: "ok"}, nil
			}},
		})
		if err != nil {
			t.Fatalf("NewSummarizer: %v", err)
		}

		summary, err := summarizer.RunForSession(context.Background(), sessionID)
		if err != nil {
			t.Fatalf("RunForSession: %v", err)
		}
		if summary.FromSequence != 51 || summary.ToSequence != 87 {
			t.Fatalf("range = %d-%d, want 51-87", summary.FromSequence, summary.ToSequence)
		}
	})
}

func TestSummarizerRunForSessionAlreadySummarizedReturnsErrAlreadySummarized(t *testing.T) {
	sessionID := uuid.New()
	orgID := uuid.New()

	messages := make([]repo.ChatMessage, 0, 40)
	for seq := int64(1); seq <= 40; seq++ {
		messages = append(messages, repo.ChatMessage{SessionID: sessionID, SequenceNumber: seq, Role: "assistant", Content: "msg"})
	}

	summaryRepo := &fakeSummaryRepo{
		listBySessionFn: func(_ context.Context, _ uuid.UUID) ([]repo.ChatSummary, error) {
			return []repo.ChatSummary{{SessionID: sessionID, FromSequence: 1, ToSequence: 40, SummaryText: "done", SummarizedTurnCount: 40}}, nil
		},
	}

	summarizer, err := NewSummarizer(SummarizerOptions{
		Sessions: &fakeSessionRepo{getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
			return repo.ChatSession{ID: id, OrganizationID: orgID, ScopeType: "organization", ScopeID: orgID, Status: "active"}, nil
		}},
		Messages:  &fakeMessageRepo{listBySessionFn: func(context.Context, uuid.UUID) ([]repo.ChatMessage, error) { return messages, nil }},
		Summaries: summaryRepo,
		Tasks:     &fakeTaskRepo{},
		Resolver:  fixedResolver{profile: repo.ModelProfile{LogicalProfileID: "summary", InvocationPurpose: "summarization"}},
		Model: &fakeSummarizationModel{summarizeFn: func(_ context.Context, _ SummarizationRequest) (SummarizationResponse, error) {
			return SummarizationResponse{SummaryText: "should not be used"}, nil
		}},
	})
	if err != nil {
		t.Fatalf("NewSummarizer: %v", err)
	}

	_, err = summarizer.RunForSession(context.Background(), sessionID)
	if !errors.Is(err, ErrAlreadySummarized) {
		t.Fatalf("RunForSession error = %v, want ErrAlreadySummarized", err)
	}
	if summaryRepo.createCalls != 0 {
		t.Fatalf("summary create calls = %d, want 0", summaryRepo.createCalls)
	}
}

func TestSummarizerRunForSessionPreservesVerbatimPathInSummary(t *testing.T) {
	sessionID := uuid.New()
	orgID := uuid.New()
	path := "/workspace/src/main.go"
	turnID := uuid.New()

	messages := []repo.ChatMessage{
		{SessionID: sessionID, TurnID: &turnID, SequenceNumber: 1, Role: "user", Content: "Please inspect " + path},
		{SessionID: sessionID, TurnID: &turnID, SequenceNumber: 2, Role: "assistant", Content: "I inspected " + path},
		{SessionID: sessionID, TurnID: &turnID, SequenceNumber: 3, Role: "tool_result", Content: "read file " + path},
		{SessionID: sessionID, SequenceNumber: 4, Role: "assistant", Content: "done"},
	}

	var sawPath bool
	summaryRepo := &fakeSummaryRepo{}
	summarizer, err := NewSummarizer(SummarizerOptions{
		Sessions: &fakeSessionRepo{getByIDFn: func(_ context.Context, id uuid.UUID) (repo.ChatSession, error) {
			return repo.ChatSession{ID: id, OrganizationID: orgID, ScopeType: "organization", ScopeID: orgID, Status: "active"}, nil
		}},
		Messages:  &fakeMessageRepo{listBySessionFn: func(context.Context, uuid.UUID) ([]repo.ChatMessage, error) { return messages, nil }},
		Summaries: summaryRepo,
		Tasks:     &fakeTaskRepo{},
		Resolver:  fixedResolver{profile: repo.ModelProfile{LogicalProfileID: "summary", InvocationPurpose: "summarization"}},
		Model: &fakeSummarizationModel{summarizeFn: func(_ context.Context, req SummarizationRequest) (SummarizationResponse, error) {
			for _, message := range req.Messages {
				if strings.Contains(message.Content, path) {
					sawPath = true
					break
				}
			}
			return SummarizationResponse{SummaryText: "Preserved path: " + path}, nil
		}},
	})
	if err != nil {
		t.Fatalf("NewSummarizer: %v", err)
	}

	summary, err := summarizer.RunForSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("RunForSession: %v", err)
	}
	if !sawPath {
		t.Fatal("model request did not include source path")
	}
	if !strings.Contains(summary.SummaryText, path) {
		t.Fatalf("summary text %q missing verbatim path %q", summary.SummaryText, path)
	}
}

func TestCompactToolResultPayload(t *testing.T) {
	content := strings.Repeat("x", 5000)
	metadata := json.RawMessage(`{"source":"tool"}`)

	updatedContent, updatedMetadata := compactToolResultPayload(content, metadata)
	if updatedContent != "[tool_result compacted — 5000 bytes]" {
		t.Fatalf("content = %q, want %q", updatedContent, "[tool_result compacted — 5000 bytes]")
	}
	var parsed map[string]any
	if err := json.Unmarshal(updatedMetadata, &parsed); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if compacted, ok := parsed["compacted"].(bool); !ok || !compacted {
		t.Fatalf("metadata.compacted = %v, want true", parsed["compacted"])
	}
}

type fixedResolver struct {
	profile repo.ModelProfile
	err     error
}

func (r fixedResolver) Resolve(context.Context, uuid.UUID, string, ...model.Scope) (*repo.ModelProfile, error) {
	if r.err != nil {
		return nil, r.err
	}
	profile := r.profile
	return &profile, nil
}

type fakeSummarizationModel struct {
	summarizeFn func(ctx context.Context, req SummarizationRequest) (SummarizationResponse, error)
}

func (f *fakeSummarizationModel) Summarize(ctx context.Context, req SummarizationRequest) (SummarizationResponse, error) {
	if f.summarizeFn != nil {
		return f.summarizeFn(ctx, req)
	}
	return SummarizationResponse{SummaryText: "summary"}, nil
}

type fakeSummaryRepo struct {
	createFn        func(ctx context.Context, summary repo.ChatSummary) (repo.ChatSummary, error)
	listBySessionFn func(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatSummary, error)
	createCalls     int
}

func (f *fakeSummaryRepo) Create(ctx context.Context, summary repo.ChatSummary) (repo.ChatSummary, error) {
	f.createCalls++
	if f.createFn != nil {
		return f.createFn(ctx, summary)
	}
	summary.ID = uuid.New()
	return summary, nil
}

func (f *fakeSummaryRepo) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatSummary, error) {
	if f.listBySessionFn != nil {
		return f.listBySessionFn(ctx, sessionID)
	}
	return []repo.ChatSummary{}, nil
}
