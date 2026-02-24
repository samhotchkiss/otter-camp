package gateway

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type stubInvocationRepo struct {
	mu         sync.Mutex
	invocation repo.ModelInvocation
	completion struct {
		inputTokens     int
		outputTokens    int
		cacheTokens     int
		latencyMS       int
		totalDurationMS int
		promptKey       *string
		responseKey     *string
	}
}

func (s *stubInvocationRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ModelInvocation, error) {
	if s.invocation.ID != id {
		return repo.ModelInvocation{}, repo.ErrNotFound
	}
	return s.invocation, nil
}

func (s *stubInvocationRepo) UpdateCompletion(_ context.Context, _ uuid.UUID, inputTokens, outputTokens, cacheTokens, latencyMS, totalDurationMS int, promptKey, responseKey *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completion.inputTokens = inputTokens
	s.completion.outputTokens = outputTokens
	s.completion.cacheTokens = cacheTokens
	s.completion.latencyMS = latencyMS
	s.completion.totalDurationMS = totalDurationMS
	s.completion.promptKey = promptKey
	s.completion.responseKey = responseKey
	return nil
}

type stubWriter struct {
	mu     sync.Mutex
	chunks [][]byte
}

func (w *stubWriter) WriteChunk(chunk []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.chunks = append(w.chunks, append([]byte(nil), chunk...))
	return nil
}

func (w *stubWriter) Combined() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	var combined bytes.Buffer
	for _, chunk := range w.chunks {
		_, _ = combined.Write(chunk)
	}
	return combined.Bytes()
}

type stubTokenCounter struct {
	usage TokenUsage
}

func (s stubTokenCounter) Count(_ repo.ModelInvocation, _ []byte) TokenUsage {
	return s.usage
}

type stubAwaiter struct {
	response []byte
	delay    time.Duration
}

func (s stubAwaiter) Await(ctx context.Context, _ uuid.UUID) ([]byte, error) {
	timer := time.NewTimer(s.delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return append([]byte(nil), s.response...), nil
	}
}

type stubJobEnqueuer struct {
	mu      sync.Mutex
	payload any
}

func (s *stubJobEnqueuer) Enqueue(_ context.Context, _ pgx.Tx, _ string, _ int, payload any, _ *time.Time) (uuid.UUID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.payload = payload
	return uuid.New(), nil
}

func TestStreamResponseAccumulatesChunksAndRecordsCompletion(t *testing.T) {
	invocationID := uuid.New()
	orgID := uuid.New()

	repoStub := &stubInvocationRepo{
		invocation: repo.ModelInvocation{
			ID:             invocationID,
			OrganizationID: orgID,
			CreatedAt:      time.Now().UTC(),
			ModelName:      "gpt-4o-mini",
		},
	}
	writer := &stubWriter{}
	enqueuer := &stubJobEnqueuer{}
	processor, err := NewStreamProcessor(StreamProcessorOptions{
		Invocations: repoStub,
		TokenCounter: stubTokenCounter{usage: TokenUsage{
			InputTokens:     12,
			OutputTokens:    8,
			CacheReadTokens: 1,
		}},
		JobEnqueuer: enqueuer,
	})
	if err != nil {
		t.Fatalf("NewStreamProcessor: %v", err)
	}

	chunks := make(chan []byte, 5)
	chunks <- []byte("one")
	chunks <- []byte("-")
	chunks <- []byte("two")
	chunks <- []byte("-")
	chunks <- []byte("three")
	close(chunks)

	if err := processor.StreamResponse(context.Background(), invocationID, chunks, writer); err != nil {
		t.Fatalf("StreamResponse: %v", err)
	}

	if got := string(writer.Combined()); got != "one-two-three" {
		t.Fatalf("combined stream = %q, want %q", got, "one-two-three")
	}
	if repoStub.completion.inputTokens != 12 {
		t.Fatalf("input_tokens = %d, want 12", repoStub.completion.inputTokens)
	}
	payload, ok := enqueuer.payload.(RollupUpdatePayload)
	if !ok {
		t.Fatalf("job payload type = %T, want RollupUpdatePayload", enqueuer.payload)
	}
	if payload.OrgID != orgID {
		t.Fatalf("payload org_id = %s, want %s", payload.OrgID, orgID)
	}
}

func TestAwaitResponseBlocksUntilFullResponse(t *testing.T) {
	invocationID := uuid.New()
	started := time.Now()

	repoStub := &stubInvocationRepo{
		invocation: repo.ModelInvocation{
			ID:             invocationID,
			OrganizationID: uuid.New(),
			CreatedAt:      started.UTC(),
			ModelName:      "gpt-4o-mini",
		},
	}
	processor, err := NewStreamProcessor(StreamProcessorOptions{
		Invocations: repoStub,
		Awaiter: stubAwaiter{
			response: []byte(`{"metadata":{"input_tokens":4,"output_tokens":5}}`),
			delay:    20 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("NewStreamProcessor: %v", err)
	}

	response, err := processor.AwaitResponse(context.Background(), invocationID)
	if err != nil {
		t.Fatalf("AwaitResponse: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond {
		t.Fatalf("AwaitResponse returned too early: %s", elapsed)
	}
	if string(response) == "" {
		t.Fatal("AwaitResponse returned empty response")
	}
	if repoStub.completion.inputTokens != 4 || repoStub.completion.outputTokens != 5 {
		t.Fatalf("completion usage = (%d,%d), want (4,5)", repoStub.completion.inputTokens, repoStub.completion.outputTokens)
	}
}

func TestAwaitResponseWithoutSourceReturnsError(t *testing.T) {
	processor, err := NewStreamProcessor(StreamProcessorOptions{
		Invocations: &stubInvocationRepo{
			invocation: repo.ModelInvocation{
				ID:             uuid.New(),
				OrganizationID: uuid.New(),
				CreatedAt:      time.Now().UTC(),
			},
		},
	})
	if err != nil {
		t.Fatalf("NewStreamProcessor: %v", err)
	}

	_, err = processor.AwaitResponse(context.Background(), uuid.New())
	if !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("AwaitResponse error = %v, want repo.ErrNotFound", err)
	}
}
