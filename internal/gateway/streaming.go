package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	rollupUpdateJobType    = "rollup_update"
	defaultRollupPriority  = 50
	defaultRollupQueueDate = "2006-01-02"
)

var ErrAwaitResponseUnavailable = errors.New("await response source is not configured")

type ResponseWriter interface {
	WriteChunk(chunk []byte) error
}

type modelInvocationLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.ModelInvocation, error)
	UpdateCompletion(ctx context.Context, id uuid.UUID, inputTokens, outputTokens, cacheTokens, latencyMS, totalDurationMS int, promptKey, responseKey *string) error
}

type responseAwaiter interface {
	Await(ctx context.Context, invocationID uuid.UUID) ([]byte, error)
}

type rollupJobEnqueuer interface {
	Enqueue(ctx context.Context, tx pgx.Tx, jobType string, priority int, payload any, runAfter *time.Time) (uuid.UUID, error)
}

type RollupUpdatePayload struct {
	OrgID      uuid.UUID `json:"org_id"`
	RollupDate string    `json:"rollup_date"`
}

type StreamProcessorOptions struct {
	Invocations modelInvocationLookup
	Providers   providerByIDLookup
	Awaiter     responseAwaiter
	Capture     *CaptureService
	JobEnqueuer rollupJobEnqueuer
	TokenCounter
	Logger     *slog.Logger
	Now        func() time.Time
	Sleep      func(context.Context, time.Duration) error
	FixtureDir string
}

type StreamProcessor struct {
	invocations  modelInvocationLookup
	providers    providerByIDLookup
	awaiter      responseAwaiter
	capture      *CaptureService
	enqueuer     rollupJobEnqueuer
	tokenCounter TokenCounter
	logger       *slog.Logger
	now          func() time.Time
	sleep        func(context.Context, time.Duration) error
	fixtureDir   string
}

func NewStreamProcessor(opts StreamProcessorOptions) (*StreamProcessor, error) {
	if opts.Invocations == nil {
		return nil, fmt.Errorf("model invocation repository is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Sleep == nil {
		opts.Sleep = defaultSleep
	}
	if opts.TokenCounter == nil {
		opts.TokenCounter = NewDefaultTokenCounter()
	}

	return &StreamProcessor{
		invocations:  opts.Invocations,
		providers:    opts.Providers,
		awaiter:      opts.Awaiter,
		capture:      opts.Capture,
		enqueuer:     opts.JobEnqueuer,
		tokenCounter: opts.TokenCounter,
		logger:       opts.Logger,
		now:          opts.Now,
		sleep:        opts.Sleep,
		fixtureDir:   opts.FixtureDir,
	}, nil
}

func (p *StreamProcessor) StreamResponse(ctx context.Context, invocationID uuid.UUID, chunkChan <-chan []byte, writer ResponseWriter) error {
	invocation, err := p.invocations.GetByID(ctx, invocationID)
	if err != nil {
		return err
	}

	startedAt := p.now().UTC()
	var usageOverride *TokenUsage
	if chunkChan == nil {
		if deterministicModeEnabled() {
			fixture, fixtureErr := loadDeterministicFixture(ctx, p.providers, p.fixtureDir, invocation)
			if fixtureErr != nil {
				return fixtureErr
			}
			usage := TokenUsage{
				InputTokens:     fixture.Metadata.InputTokens,
				OutputTokens:    fixture.Metadata.OutputTokens,
				CacheReadTokens: fixture.Metadata.CacheReadTokens,
			}
			usageOverride = &usage
			chunkChan = streamDeterministicFixture(ctx, p.sleep, fixture.Response)
		} else if p.awaiter != nil {
			response, awaitErr := p.awaiter.Await(ctx, invocationID)
			if awaitErr != nil {
				return awaitErr
			}
			chunkChan = singleChunk(response)
		} else {
			return ErrAwaitResponseUnavailable
		}
	}

	var (
		firstChunkAt time.Time
		combined     bytes.Buffer
	)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk, ok := <-chunkChan:
			if !ok {
				return p.completeInvocation(ctx, invocation, combined.Bytes(), startedAt, firstChunkAt, usageOverride)
			}
			if firstChunkAt.IsZero() {
				firstChunkAt = p.now().UTC()
			}
			if writer != nil {
				if err := writer.WriteChunk(chunk); err != nil {
					return err
				}
			}
			_, _ = combined.Write(chunk)
		}
	}
}

func (p *StreamProcessor) AwaitResponse(ctx context.Context, invocationID uuid.UUID) ([]byte, error) {
	invocation, err := p.invocations.GetByID(ctx, invocationID)
	if err != nil {
		return nil, err
	}

	startedAt := p.now().UTC()
	var (
		response      []byte
		usageOverride *TokenUsage
	)
	if deterministicModeEnabled() {
		fixture, fixtureErr := loadDeterministicFixture(ctx, p.providers, p.fixtureDir, invocation)
		if fixtureErr != nil {
			return nil, fixtureErr
		}
		response = fixture.Response
		usage := TokenUsage{
			InputTokens:     fixture.Metadata.InputTokens,
			OutputTokens:    fixture.Metadata.OutputTokens,
			CacheReadTokens: fixture.Metadata.CacheReadTokens,
		}
		usageOverride = &usage
	} else {
		if p.awaiter == nil {
			return nil, ErrAwaitResponseUnavailable
		}
		response, err = p.awaiter.Await(ctx, invocationID)
		if err != nil {
			return nil, err
		}
	}

	completedAt := p.now().UTC()
	if err := p.completeInvocation(ctx, invocation, response, startedAt, completedAt, usageOverride); err != nil {
		return nil, err
	}
	return append([]byte(nil), response...), nil
}

func (p *StreamProcessor) completeInvocation(
	ctx context.Context,
	invocation repo.ModelInvocation,
	response []byte,
	startedAt time.Time,
	firstChunkAt time.Time,
	usageOverride *TokenUsage,
) error {
	usage := TokenUsage{}
	switch {
	case usageOverride != nil:
		usage = *usageOverride
	default:
		if parsedUsage, ok := usageFromResponsePayload(response); ok {
			usage = parsedUsage
		} else {
			usage = p.tokenCounter.Count(invocation, response)
		}
	}

	promptKey, responseKey := (*string)(nil), (*string)(nil)
	if p.capture != nil {
		promptKey, responseKey = p.capture.Capture(ctx, invocation, response)
	}

	latencyMS := 0
	if !firstChunkAt.IsZero() {
		latencyMS = durationMS(firstChunkAt.Sub(startedAt))
	}
	totalDurationMS := durationMS(p.now().UTC().Sub(startedAt))
	if err := p.invocations.UpdateCompletion(
		ctx,
		invocation.ID,
		usage.InputTokens,
		usage.OutputTokens,
		usage.CacheReadTokens,
		latencyMS,
		totalDurationMS,
		promptKey,
		responseKey,
	); err != nil {
		return err
	}

	p.enqueueRollupJob(ctx, invocation)
	return nil
}

func (p *StreamProcessor) enqueueRollupJob(ctx context.Context, invocation repo.ModelInvocation) {
	if p.enqueuer == nil {
		return
	}

	rollupDate := invocation.CreatedAt.UTC()
	if rollupDate.IsZero() {
		rollupDate = p.now().UTC()
	}

	payload := RollupUpdatePayload{
		OrgID:      invocation.OrganizationID,
		RollupDate: rollupDate.Format(defaultRollupQueueDate),
	}
	if _, err := p.enqueuer.Enqueue(ctx, nil, rollupUpdateJobType, defaultRollupPriority, payload, nil); err != nil {
		p.logger.Warn("failed to enqueue rollup update job", "invocation_id", invocation.ID, "organization_id", invocation.OrganizationID, "error", err)
	}
}

func usageFromResponsePayload(raw []byte) (TokenUsage, bool) {
	if len(bytes.TrimSpace(raw)) == 0 || !json.Valid(raw) {
		return TokenUsage{}, false
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return TokenUsage{}, false
	}

	metadata, _ := payload["metadata"].(map[string]any)
	input, inOK := readIntField(payload, metadata, "input_tokens")
	output, outOK := readIntField(payload, metadata, "output_tokens")
	cache, cacheOK := readIntField(payload, metadata, "cache_read_tokens")
	if !inOK && !outOK && !cacheOK {
		return TokenUsage{}, false
	}

	return TokenUsage{
		InputTokens:     input,
		OutputTokens:    output,
		CacheReadTokens: cache,
	}, true
}

func readIntField(root map[string]any, metadata map[string]any, key string) (int, bool) {
	if value, ok := parseInt(root[key]); ok {
		return value, true
	}
	if value, ok := parseInt(metadata[key]); ok {
		return value, true
	}
	return 0, false
}

func parseInt(raw any) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, true
	case int32:
		return int(value), true
	case int64:
		return int(value), true
	case float64:
		return int(value), true
	default:
		return 0, false
	}
}

func durationMS(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(d / time.Millisecond)
}

func singleChunk(payload []byte) <-chan []byte {
	ch := make(chan []byte, 1)
	ch <- append([]byte(nil), payload...)
	close(ch)
	return ch
}

func StreamingEnabled(priority PriorityTier) bool {
	return priority == PrioritySyncInteractive || priority == PrioritySyncSystem
}
