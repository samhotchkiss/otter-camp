package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/turn"
)

const (
	defaultLiveGatewayTimeout       = 2 * time.Minute
	defaultAnthropicVersion         = "2023-06-01"
	defaultAnthropicMaxOutputTokens = 1024
)

type SecretResolver interface {
	ResolveRef(ctx context.Context, orgID uuid.UUID, ref string) (string, error)
}

type liveModelInvocationRepo interface {
	modelInvocationLookup
	Create(ctx context.Context, invocation repo.ModelInvocation) (repo.ModelInvocation, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, errorCode, errorMessage *string) (repo.ModelInvocation, error)
}

type LiveModelGatewayOptions struct {
	Router      *Router
	Profiles    modelProfileLookup
	Connections providerConnectionLookup
	Providers   providerByIDLookup
	Secrets     SecretResolver
	Invocations liveModelInvocationRepo
	Enqueuer    rollupJobEnqueuer
	Health      *HealthChecker
	Concurrency *ConcurrencyManager
	HTTPClient  *http.Client
	Logger      *slog.Logger
	Now         func() time.Time
}

type LiveModelGateway struct {
	router      *Router
	providers   providerByIDLookup
	secrets     SecretResolver
	invocations liveModelInvocationRepo
	enqueuer    rollupJobEnqueuer
	health      *HealthChecker
	concurrency *ConcurrencyManager
	httpClient  *http.Client
	logger      *slog.Logger
	now         func() time.Time
}

func NewLiveModelGateway(opts LiveModelGatewayOptions) (*LiveModelGateway, error) {
	if opts.Health == nil {
		opts.Health = NewHealthChecker()
	}
	if opts.Router == nil {
		if opts.Profiles == nil || opts.Connections == nil {
			return nil, fmt.Errorf("gateway router is required")
		}
		opts.Router = NewRouter(opts.Profiles, opts.Connections, opts.Health)
	}
	if opts.Providers == nil {
		return nil, fmt.Errorf("provider repository is required")
	}
	if opts.Secrets == nil {
		return nil, fmt.Errorf("secret resolver is required")
	}
	if opts.Invocations == nil {
		return nil, fmt.Errorf("model invocation repository is required")
	}
	if opts.Concurrency == nil {
		opts.Concurrency = NewConcurrencyManager(defaultGlobalSlots, nil)
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: defaultLiveGatewayTimeout}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	return &LiveModelGateway{
		router:      opts.Router,
		providers:   opts.Providers,
		secrets:     opts.Secrets,
		invocations: opts.Invocations,
		enqueuer:    opts.Enqueuer,
		health:      opts.Health,
		concurrency: opts.Concurrency,
		httpClient:  opts.HTTPClient,
		logger:      opts.Logger,
		now:         opts.Now,
	}, nil
}

func (g *LiveModelGateway) StreamComplete(ctx context.Context, req turn.ModelRequest, onChunk func(string) error) (turn.ModelResponse, error) {
	if onChunk == nil {
		onChunk = func(string) error { return nil }
	}
	return g.complete(ctx, req, true, onChunk)
}

func (g *LiveModelGateway) Complete(ctx context.Context, req turn.ModelRequest) (turn.ModelResponse, error) {
	return g.complete(ctx, req, false, nil)
}

type providerCallResult struct {
	Content      string
	ToolCalls    []turn.ModelToolCall
	Usage        *turn.ModelUsage
	FirstChunkAt time.Time
}

func (g *LiveModelGateway) complete(ctx context.Context, req turn.ModelRequest, stream bool, onChunk func(string) error) (turn.ModelResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if g == nil || g.router == nil {
		return turn.ModelResponse{}, fmt.Errorf("live model gateway is not configured")
	}

	orgID := req.OrganizationID
	if orgID == uuid.Nil && req.Profile.OrganizationID != nil {
		orgID = *req.Profile.OrganizationID
	}
	if orgID == uuid.Nil {
		return turn.ModelResponse{}, fmt.Errorf("organization id is required")
	}
	if strings.TrimSpace(req.Profile.LogicalProfileID) == "" {
		return turn.ModelResponse{}, fmt.Errorf("model profile id is required")
	}
	if strings.TrimSpace(req.Profile.ModelName) == "" {
		return turn.ModelResponse{}, fmt.Errorf("model name is required")
	}

	priority := priorityForPurpose(req.Purpose)
	attemptedConnections := make(map[uuid.UUID]struct{}, maxFallbackHops)
	var lastErr error

	for hop := 0; hop < maxFallbackHops; hop++ {
		connection, provider, err := g.selectConnection(ctx, orgID, req, priority)
		if err != nil {
			if lastErr != nil {
				return turn.ModelResponse{}, lastErr
			}
			return turn.ModelResponse{}, err
		}
		if _, seen := attemptedConnections[connection.ID]; seen {
			if lastErr != nil {
				return turn.ModelResponse{}, lastErr
			}
			return turn.ModelResponse{}, ErrNoHealthyConnection
		}
		attemptedConnections[connection.ID] = struct{}{}

		invocation, err := g.createInvocation(ctx, orgID, req, provider, connection, stream)
		if err != nil {
			return turn.ModelResponse{}, err
		}

		release, reserveErr := g.reserveConnection(ctx, connection)
		if reserveErr != nil {
			g.markInvocationFailed(ctx, invocation.ID, reserveErr)
			return turn.ModelResponse{}, reserveErr
		}

		startedAt := g.now().UTC()
		result, callErr := g.callProvider(ctx, orgID, req, provider, connection, stream, onChunk)
		release()

		if callErr != nil {
			g.markInvocationFailed(ctx, invocation.ID, callErr)
			mapped, retryable := g.mapProviderError(connection.ID, callErr)
			lastErr = mapped
			if !retryable {
				return turn.ModelResponse{}, mapped
			}
			continue
		}

		g.health.RecordSuccess(connection.ID)
		usageOverride := tokenUsageFromModelUsage(result.Usage)
		if err := g.completeInvocation(ctx, invocation, result.Content, startedAt, result.FirstChunkAt, usageOverride); err != nil {
			return turn.ModelResponse{}, err
		}
		return turn.ModelResponse{
			Content:   result.Content,
			ToolCalls: result.ToolCalls,
			Usage:     result.Usage,
		}, nil
	}

	if lastErr != nil {
		return turn.ModelResponse{}, lastErr
	}
	return turn.ModelResponse{}, ErrNoHealthyConnection
}

func (g *LiveModelGateway) selectConnection(ctx context.Context, orgID uuid.UUID, req turn.ModelRequest, priority PriorityTier) (repo.ProviderConnection, repo.ModelProvider, error) {
	connection, err := g.router.SelectConnection(ctx, orgID, req.Profile.LogicalProfileID, req.Purpose, priority)
	if err != nil {
		return repo.ProviderConnection{}, repo.ModelProvider{}, err
	}

	provider, err := g.providers.GetByID(ctx, connection.ProviderID)
	if err != nil {
		return repo.ProviderConnection{}, repo.ModelProvider{}, err
	}
	return *connection, provider, nil
}

func (g *LiveModelGateway) reserveConnection(ctx context.Context, connection repo.ProviderConnection) (func(), error) {
	if g.concurrency == nil {
		return func() {}, nil
	}
	g.concurrency.SetConnectionLimit(connection.ID, max(1, connection.MaxConcurrent))
	return g.concurrency.Reserve(ctx, connection.ID)
}

func (g *LiveModelGateway) createInvocation(
	ctx context.Context,
	orgID uuid.UUID,
	req turn.ModelRequest,
	provider repo.ModelProvider,
	connection repo.ProviderConnection,
	stream bool,
) (repo.ModelInvocation, error) {
	purpose := strings.TrimSpace(req.Purpose)
	if purpose == "" {
		purpose = "agent_turn"
	}

	metadata := buildLiveInvocationMetadata(req)
	invocation := repo.ModelInvocation{
		OrganizationID:       orgID,
		ModelProviderID:      provider.ID,
		ProviderConnectionID: &connection.ID,
		ModelProfileID:       stringPtr(strings.TrimSpace(req.Profile.LogicalProfileID)),
		InvocationPurpose:    purpose,
		Status:               "in_flight",
		ModelName:            strings.TrimSpace(req.Profile.ModelName),
		IsStreaming:          stream,
		Metadata:             metadata,
	}
	if req.AgentID != uuid.Nil {
		invocation.AgentID = uuidPtr(req.AgentID)
	}
	if req.SessionID != uuid.Nil {
		invocation.SessionID = uuidPtr(req.SessionID)
	}
	if req.TurnID != uuid.Nil {
		invocation.TurnID = uuidPtr(req.TurnID)
	}

	return g.invocations.Create(ctx, invocation)
}

func (g *LiveModelGateway) markInvocationFailed(ctx context.Context, invocationID uuid.UUID, cause error) {
	if invocationID == uuid.Nil || g == nil || g.invocations == nil {
		return
	}
	errCode := "model_error"
	errMsg := strings.TrimSpace(cause.Error())
	if errMsg == "" {
		errMsg = "model provider call failed"
	}
	if _, err := g.invocations.UpdateStatus(ctx, invocationID, "failed", &errCode, &errMsg); err != nil {
		g.logger.Warn("failed to mark model invocation as failed", "invocation_id", invocationID, "error", err)
	}
}

func (g *LiveModelGateway) completeInvocation(
	ctx context.Context,
	invocation repo.ModelInvocation,
	content string,
	startedAt time.Time,
	firstChunkAt time.Time,
	usageOverride *TokenUsage,
) error {
	processor, err := NewStreamProcessor(StreamProcessorOptions{
		Invocations: g.invocations,
		JobEnqueuer: g.enqueuer,
		Logger:      g.logger,
		Now:         g.now,
	})
	if err != nil {
		return err
	}

	payload := map[string]any{
		"content": strings.TrimSpace(content),
	}
	if usageOverride != nil {
		payload["metadata"] = map[string]any{
			"input_tokens":      usageOverride.InputTokens,
			"output_tokens":     usageOverride.OutputTokens,
			"cache_read_tokens": usageOverride.CacheReadTokens,
		}
	}
	responseJSON, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return processor.completeInvocation(ctx, invocation, responseJSON, startedAt, firstChunkAt, usageOverride)
}

func (g *LiveModelGateway) mapProviderError(connectionID uuid.UUID, err error) (error, bool) {
	if err == nil {
		return nil, false
	}

	var providerErr ProviderHTTPError
	if errors.As(err, &providerErr) {
		switch providerErr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			g.health.MarkUnavailable(connectionID)
			return fmt.Errorf("%w", turn.ErrAuthFailed), false
		case http.StatusTooManyRequests:
			g.health.MarkRateLimited(connectionID)
			return fmt.Errorf("%w", turn.ErrRateLimited), true
		default:
			if providerErr.StatusCode >= http.StatusInternalServerError {
				g.health.MarkDegraded(connectionID)
				return fmt.Errorf("%w", turn.ErrModelTransient), true
			}
		}
		g.health.RecordFailure(connectionID, err)
		return err, false
	}

	if errors.Is(err, context.Canceled) {
		return err, false
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		g.health.MarkDegraded(connectionID)
		return fmt.Errorf("%w", turn.ErrModelTransient), true
	}

	if errors.Is(err, context.DeadlineExceeded) {
		g.health.MarkDegraded(connectionID)
		return fmt.Errorf("%w", turn.ErrModelTransient), true
	}

	return err, false
}

func (g *LiveModelGateway) callProvider(
	ctx context.Context,
	orgID uuid.UUID,
	req turn.ModelRequest,
	provider repo.ModelProvider,
	connection repo.ProviderConnection,
	stream bool,
	onChunk func(string) error,
) (providerCallResult, error) {
	providerType := detectProviderType(provider, req.Profile.ModelName)
	baseURL := resolveProviderBaseURL(provider, connection)
	if strings.TrimSpace(baseURL) == "" {
		return providerCallResult{}, ProviderHTTPError{
			StatusCode: http.StatusServiceUnavailable,
			Err:        errors.New("provider base url is not configured"),
		}
	}
	endpointURL, err := providerEndpoint(baseURL, providerType)
	if err != nil {
		return providerCallResult{}, err
	}

	apiKey, err := g.secrets.ResolveRef(ctx, orgID, connection.APIKeyRef)
	if err != nil {
		return providerCallResult{}, err
	}

	body, err := buildProviderBody(providerType, req, stream)
	if err != nil {
		return providerCallResult{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(body))
	if err != nil {
		return providerCallResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	applyAuthHeaders(httpReq, providerType, apiKey)

	resp, err := g.httpClient.Do(httpReq)
	if err != nil {
		return providerCallResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return providerCallResult{}, providerHTTPErrorFromResponse(resp)
	}

	if stream {
		return parseProviderStream(resp.Body, providerType, onChunk)
	}
	return parseProviderCompletion(resp.Body, providerType)
}

func parseProviderCompletion(body io.Reader, providerType string) (providerCallResult, error) {
	raw, err := io.ReadAll(body)
	if err != nil {
		return providerCallResult{}, err
	}

	switch providerType {
	case "anthropic":
		return parseAnthropicCompletion(raw)
	default:
		return parseOpenAICompletion(raw)
	}
}

func parseProviderStream(body io.Reader, providerType string, onChunk func(string) error) (providerCallResult, error) {
	if onChunk == nil {
		onChunk = func(string) error { return nil }
	}

	var (
		builder      strings.Builder
		usage        turn.ModelUsage
		usageSeen    bool
		firstChunkAt time.Time
	)

	err := parseSSE(body, func(eventType, data string) error {
		if strings.TrimSpace(data) == "" {
			return nil
		}

		switch providerType {
		case "anthropic":
			chunk, eventUsage, done, err := parseAnthropicStreamEvent(eventType, data)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			if eventUsage != nil {
				usageSeen = true
				mergeUsage(&usage, eventUsage)
			}
			if chunk != "" {
				if firstChunkAt.IsZero() {
					firstChunkAt = time.Now().UTC()
				}
				builder.WriteString(chunk)
				if err := onChunk(chunk); err != nil {
					return err
				}
			}
			return nil
		default:
			chunk, eventUsage, done, err := parseOpenAIStreamEvent(data)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			if eventUsage != nil {
				usageSeen = true
				mergeUsage(&usage, eventUsage)
			}
			if chunk != "" {
				if firstChunkAt.IsZero() {
					firstChunkAt = time.Now().UTC()
				}
				builder.WriteString(chunk)
				if err := onChunk(chunk); err != nil {
					return err
				}
			}
			return nil
		}
	})
	if err != nil {
		return providerCallResult{}, err
	}

	result := providerCallResult{
		Content:      builder.String(),
		FirstChunkAt: firstChunkAt,
	}
	if usageSeen {
		result.Usage = &usage
	}
	return result, nil
}

func parseSSE(body io.Reader, onEvent func(eventType, data string) error) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var (
		eventType string
		dataLines []string
	)

	flush := func() error {
		if len(dataLines) == 0 {
			eventType = ""
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		defer func() { eventType = "" }()
		return onEvent(eventType, data)
	}

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}

type openAIStreamPayload struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

func parseOpenAIStreamEvent(data string) (chunk string, usage *turn.ModelUsage, done bool, err error) {
	if strings.EqualFold(strings.TrimSpace(data), "[DONE]") {
		return "", nil, true, nil
	}

	var payload openAIStreamPayload
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return "", nil, false, err
	}

	if payload.Usage != nil {
		eventUsage := &turn.ModelUsage{
			InputTokens:  payload.Usage.PromptTokens,
			OutputTokens: payload.Usage.CompletionTokens,
		}
		if payload.Usage.PromptTokensDetails != nil {
			eventUsage.CacheReadTokens = payload.Usage.PromptTokensDetails.CachedTokens
		}
		usage = eventUsage
	}

	for _, choice := range payload.Choices {
		if strings.TrimSpace(choice.Delta.Content) != "" {
			chunk += choice.Delta.Content
		}
	}
	return chunk, usage, false, nil
}

type anthropicStreamPayload struct {
	Type  string `json:"type"`
	Delta *struct {
		Text string `json:"text"`
	} `json:"delta"`
	ContentBlock *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content_block"`
	Usage *struct {
		InputTokens          int `json:"input_tokens"`
		OutputTokens         int `json:"output_tokens"`
		CacheReadInputTokens int `json:"cache_read_input_tokens"`
	} `json:"usage"`
	Message *struct {
		Usage *struct {
			InputTokens          int `json:"input_tokens"`
			OutputTokens         int `json:"output_tokens"`
			CacheReadInputTokens int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

func parseAnthropicStreamEvent(eventType, data string) (chunk string, usage *turn.ModelUsage, done bool, err error) {
	var payload anthropicStreamPayload
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return "", nil, false, err
	}

	if payload.Usage != nil {
		usage = &turn.ModelUsage{
			InputTokens:     payload.Usage.InputTokens,
			OutputTokens:    payload.Usage.OutputTokens,
			CacheReadTokens: payload.Usage.CacheReadInputTokens,
		}
	}
	if payload.Message != nil && payload.Message.Usage != nil {
		usage = &turn.ModelUsage{
			InputTokens:     payload.Message.Usage.InputTokens,
			OutputTokens:    payload.Message.Usage.OutputTokens,
			CacheReadTokens: payload.Message.Usage.CacheReadInputTokens,
		}
	}

	switch strings.TrimSpace(eventType) {
	case "message_stop":
		return "", usage, true, nil
	case "content_block_delta":
		if payload.Delta != nil {
			chunk = payload.Delta.Text
		}
	case "content_block_start":
		if payload.ContentBlock != nil {
			chunk = payload.ContentBlock.Text
		}
	default:
		if payload.Delta != nil {
			chunk = payload.Delta.Text
		}
	}

	return chunk, usage, false, nil
}

type openAICompletionPayload struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

func parseOpenAICompletion(raw []byte) (providerCallResult, error) {
	var payload openAICompletionPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return providerCallResult{}, err
	}

	contentBuilder := strings.Builder{}
	for _, choice := range payload.Choices {
		if strings.TrimSpace(choice.Message.Content) == "" {
			continue
		}
		contentBuilder.WriteString(choice.Message.Content)
	}

	result := providerCallResult{
		Content: contentBuilder.String(),
	}
	if payload.Usage != nil {
		result.Usage = &turn.ModelUsage{
			InputTokens:  payload.Usage.PromptTokens,
			OutputTokens: payload.Usage.CompletionTokens,
		}
		if payload.Usage.PromptTokensDetails != nil {
			result.Usage.CacheReadTokens = payload.Usage.PromptTokensDetails.CachedTokens
		}
	}
	return result, nil
}

type anthropicCompletionPayload struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage *struct {
		InputTokens          int `json:"input_tokens"`
		OutputTokens         int `json:"output_tokens"`
		CacheReadInputTokens int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

func parseAnthropicCompletion(raw []byte) (providerCallResult, error) {
	var payload anthropicCompletionPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return providerCallResult{}, err
	}

	contentBuilder := strings.Builder{}
	for _, block := range payload.Content {
		if strings.TrimSpace(block.Text) == "" {
			continue
		}
		contentBuilder.WriteString(block.Text)
	}

	result := providerCallResult{
		Content: contentBuilder.String(),
	}
	if payload.Usage != nil {
		result.Usage = &turn.ModelUsage{
			InputTokens:     payload.Usage.InputTokens,
			OutputTokens:    payload.Usage.OutputTokens,
			CacheReadTokens: payload.Usage.CacheReadInputTokens,
		}
	}
	return result, nil
}

func mergeUsage(target *turn.ModelUsage, source *turn.ModelUsage) {
	if target == nil || source == nil {
		return
	}
	if source.InputTokens > 0 {
		target.InputTokens = source.InputTokens
	}
	if source.OutputTokens > 0 {
		target.OutputTokens = source.OutputTokens
	}
	if source.CacheReadTokens > 0 {
		target.CacheReadTokens = source.CacheReadTokens
	}
}

func providerHTTPErrorFromResponse(resp *http.Response) error {
	const maxResponseBytes = 8192

	var responseError string
	if resp != nil && resp.Body != nil {
		limited := io.LimitReader(resp.Body, maxResponseBytes)
		if body, err := io.ReadAll(limited); err == nil {
			responseError = strings.TrimSpace(string(body))
		}
	}
	if responseError == "" {
		responseError = http.StatusText(resp.StatusCode)
	}

	return ProviderHTTPError{
		StatusCode: resp.StatusCode,
		RetryAfter: parseRetryAfter(resp),
		Err:        errors.New(responseError),
	}
}

func parseRetryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	raw := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if ts, err := http.ParseTime(raw); err == nil {
		delta := time.Until(ts)
		if delta > 0 {
			return delta
		}
	}
	return 0
}

func applyAuthHeaders(req *http.Request, providerType, apiKey string) {
	switch providerType {
	case "anthropic":
		req.Header.Set("x-api-key", strings.TrimSpace(apiKey))
		req.Header.Set("anthropic-version", defaultAnthropicVersion)
	default:
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	}
}

func resolveProviderBaseURL(provider repo.ModelProvider, connection repo.ProviderConnection) string {
	if connection.APIBaseURLOverride != nil && strings.TrimSpace(*connection.APIBaseURLOverride) != "" {
		return strings.TrimSpace(*connection.APIBaseURLOverride)
	}
	return strings.TrimSpace(provider.APIBaseURL)
}

func providerEndpoint(baseURL string, providerType string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", err
	}
	path := strings.TrimRight(parsed.Path, "/")

	switch providerType {
	case "anthropic":
		switch {
		case strings.HasSuffix(path, "/v1/messages"):
		case strings.HasSuffix(path, "/v1"):
			path += "/messages"
		default:
			path += "/v1/messages"
		}
	default:
		if !strings.HasSuffix(path, "/chat/completions") {
			path += "/chat/completions"
		}
	}

	parsed.Path = path
	return parsed.String(), nil
}

func buildProviderBody(providerType string, req turn.ModelRequest, stream bool) ([]byte, error) {
	switch providerType {
	case "anthropic":
		systemPrompt, messages := anthropicMessages(req)
		maxTokens := req.Profile.MaxOutputTokens
		if maxTokens <= 0 {
			maxTokens = defaultAnthropicMaxOutputTokens
		}
		payload := map[string]any{
			"model":      strings.TrimSpace(req.Profile.ModelName),
			"messages":   messages,
			"max_tokens": maxTokens,
			"stream":     stream,
		}
		if systemPrompt != "" {
			payload["system"] = systemPrompt
		}
		if toolDefs := anthropicTools(req); len(toolDefs) > 0 {
			payload["tools"] = toolDefs
		}
		return json.Marshal(payload)
	default:
		payload := map[string]any{
			"model":    strings.TrimSpace(req.Profile.ModelName),
			"messages": openAIMessages(req),
			"stream":   stream,
		}
		if req.Profile.MaxOutputTokens > 0 {
			payload["max_tokens"] = req.Profile.MaxOutputTokens
		}
		if stream {
			payload["stream_options"] = map[string]any{"include_usage": true}
		}
		if toolDefs := openAITools(req); len(toolDefs) > 0 {
			payload["tools"] = toolDefs
			payload["tool_choice"] = "auto"
		}
		return json.Marshal(payload)
	}
}

func openAITools(req turn.ModelRequest) []map[string]any {
	if req.Prompt == nil || len(req.Prompt.ToolDescriptors) == 0 {
		return nil
	}

	result := make([]map[string]any, 0, len(req.Prompt.ToolDescriptors))
	for _, descriptor := range req.Prompt.ToolDescriptors {
		result = append(result, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        strings.TrimSpace(descriptor.Name),
				"description": strings.TrimSpace(descriptor.Description),
				"parameters":  normalizeToolSchema(descriptor.InputSchema),
			},
		})
	}
	return result
}

func anthropicTools(req turn.ModelRequest) []map[string]any {
	if req.Prompt == nil || len(req.Prompt.ToolDescriptors) == 0 {
		return nil
	}

	result := make([]map[string]any, 0, len(req.Prompt.ToolDescriptors))
	for _, descriptor := range req.Prompt.ToolDescriptors {
		result = append(result, map[string]any{
			"name":         strings.TrimSpace(descriptor.Name),
			"description":  strings.TrimSpace(descriptor.Description),
			"input_schema": normalizeToolSchema(descriptor.InputSchema),
		})
	}
	return result
}

func normalizeToolSchema(schema json.RawMessage) any {
	trimmed := bytes.TrimSpace(schema)
	if len(trimmed) == 0 {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	var parsed any
	if err := json.Unmarshal(trimmed, &parsed); err != nil || parsed == nil {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}
	return parsed
}

type providerMessage struct {
	Role       string  `json:"role"`
	Content    string  `json:"content"`
	ToolCallID *string `json:"tool_call_id,omitempty"`
}

func openAIMessages(req turn.ModelRequest) []providerMessage {
	base := requestMessages(req)
	messages := make([]providerMessage, 0, len(base))
	for _, item := range base {
		role := strings.ToLower(strings.TrimSpace(item.Role))
		switch role {
		case "system", "user", "assistant", "tool":
		case "tool_result":
			role = "tool"
		default:
			role = "user"
		}
		message := providerMessage{
			Role:    role,
			Content: item.Content,
		}
		if role == "tool" && item.ToolCallID != nil {
			message.ToolCallID = item.ToolCallID
		}
		messages = append(messages, message)
	}
	if len(messages) == 0 {
		messages = append(messages, providerMessage{Role: "user", Content: "Respond with a concise answer."})
	}
	return messages
}

func anthropicMessages(req turn.ModelRequest) (string, []providerMessage) {
	base := requestMessages(req)
	systemParts := make([]string, 0, 2)
	out := make([]providerMessage, 0, len(base))
	for _, item := range base {
		role := strings.ToLower(strings.TrimSpace(item.Role))
		switch role {
		case "system":
			if strings.TrimSpace(item.Content) != "" {
				systemParts = append(systemParts, item.Content)
			}
		case "assistant":
			out = append(out, providerMessage{Role: "assistant", Content: item.Content})
		default:
			out = append(out, providerMessage{Role: "user", Content: item.Content})
		}
	}
	if len(out) == 0 {
		out = append(out, providerMessage{Role: "user", Content: "Respond with a concise answer."})
	}
	return strings.Join(systemParts, "\n\n"), out
}

func requestMessages(req turn.ModelRequest) []providerMessage {
	messages := make([]providerMessage, 0)
	if req.Prompt != nil && len(req.Prompt.Messages) > 0 {
		for _, item := range req.Prompt.Messages {
			if strings.TrimSpace(item.Content) == "" {
				continue
			}
			messages = append(messages, providerMessage{
				Role:       strings.TrimSpace(item.Role),
				Content:    item.Content,
				ToolCallID: item.ToolCallID,
			})
		}
	}
	if len(messages) > 0 {
		return messages
	}

	if strings.TrimSpace(req.InstructionHint) != "" {
		messages = append(messages, providerMessage{Role: "system", Content: strings.TrimSpace(req.InstructionHint)})
	}
	for _, item := range req.HumanMessages {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		messages = append(messages, providerMessage{Role: "user", Content: trimmed})
	}
	return messages
}

func detectProviderType(provider repo.ModelProvider, modelName string) string {
	slug := strings.ToLower(strings.TrimSpace(provider.Slug))
	model := strings.ToLower(strings.TrimSpace(modelName))
	switch {
	case strings.Contains(slug, "anthropic"):
		return "anthropic"
	case strings.Contains(slug, "openai"):
		return "openai"
	case strings.Contains(model, "claude"):
		return "anthropic"
	default:
		return "openai"
	}
}

func priorityForPurpose(purpose string) PriorityTier {
	switch strings.TrimSpace(strings.ToLower(purpose)) {
	case "listening_eval", "continuation_summary", "memory_extraction", "memory_retrieval", "summarization":
		return PrioritySyncSystem
	default:
		return PrioritySyncInteractive
	}
}

func buildLiveInvocationMetadata(req turn.ModelRequest) json.RawMessage {
	payload := map[string]any{
		"profile_id":       strings.TrimSpace(req.Profile.LogicalProfileID),
		"model_name":       strings.TrimSpace(req.Profile.ModelName),
		"instruction_hint": strings.TrimSpace(req.InstructionHint),
		"human_messages":   append([]string(nil), req.HumanMessages...),
	}
	if req.Prompt != nil {
		payload["total_prompt_tokens"] = req.Prompt.TotalTokens
		payload["messages"] = req.Prompt.Messages
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func tokenUsageFromModelUsage(usage *turn.ModelUsage) *TokenUsage {
	if usage == nil {
		return nil
	}
	return &TokenUsage{
		InputTokens:     usage.InputTokens,
		OutputTokens:    usage.OutputTokens,
		CacheReadTokens: usage.CacheReadTokens,
	}
}

func stringPtr(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func uuidPtr(value uuid.UUID) *uuid.UUID {
	if value == uuid.Nil {
		return nil
	}
	copied := value
	return &copied
}
