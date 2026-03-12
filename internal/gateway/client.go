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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/metrics"
	"github.com/samhotchkiss/otter-camp/internal/prompt"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/tools"
	"github.com/samhotchkiss/otter-camp/internal/turn"
)

const (
	defaultLiveGatewayTimeout       = 5 * time.Minute
	defaultCleanupTimeout           = 5 * time.Second
	defaultAnthropicVersion         = "2023-06-01"
	defaultAnthropicMaxOutputTokens = 16384
	anthropicAuthModeAPIKey         = "api_key"
	anthropicAuthModeSubscription   = "subscription"
	anthropicSubscriptionTokenURL   = "https://console.anthropic.com/v1/oauth/token"
	anthropicSubscriptionClientID   = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	anthropicClaudeCodeUserAgent    = "claude-cli/2.1.2 (external, cli)"
	anthropicSubscriptionBeta       = "claude-code-20250219,oauth-2025-04-20,fine-grained-tool-streaming-2025-05-14"
	anthropicTokenRefreshSkew       = 5 * time.Minute
)

const (
	providerConnectionMetadataAuthMode                          = "auth_mode"
	providerConnectionMetadataSubscriptionAccessTokenSecretRef  = "subscription_access_token_secret_ref"
	providerConnectionMetadataSubscriptionRefreshTokenSecretRef = "subscription_refresh_token_secret_ref"
	providerConnectionMetadataSubscriptionTokenURL              = "subscription_token_url"
	providerConnectionMetadataSubscriptionClientID              = "subscription_client_id"
	providerConnectionMetadataSubscriptionExpiresAt             = "subscription_expires_at"
)

type SecretResolver interface {
	ResolveRef(ctx context.Context, orgID uuid.UUID, ref string) (string, error)
}

type liveModelInvocationRepo interface {
	modelInvocationLookup
	Create(ctx context.Context, invocation repo.ModelInvocation) (repo.ModelInvocation, error)
	UpdateRouting(ctx context.Context, id, providerID, connectionID uuid.UUID) (repo.ModelInvocation, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, errorCode, errorMessage *string) (repo.ModelInvocation, error)
	UpdateFailure(ctx context.Context, id uuid.UUID, status string, failureClass, errorCode, errorMessage *string) (repo.ModelInvocation, error)
}

type providerConnectionHealthWriter interface {
	SetHealthStatus(ctx context.Context, id uuid.UUID, healthStatus string) (repo.ProviderConnection, error)
}

type traceSpanCreator interface {
	Create(ctx context.Context, span repo.TraceSpan) (repo.TraceSpan, error)
}

type LiveModelGatewayOptions struct {
	Router      *Router
	Profiles    modelProfileLookup
	Connections providerConnectionLookup
	Providers   providerByIDLookup
	Secrets     SecretResolver
	Invocations liveModelInvocationRepo
	HealthStore providerConnectionHealthWriter
	Enqueuer    rollupJobEnqueuer
	Health      *HealthChecker
	Concurrency *ConcurrencyManager
	Spans       traceSpanCreator
	HTTPClient  *http.Client
	Logger      *slog.Logger
	Now         func() time.Time
}

type LiveModelGateway struct {
	router       *Router
	providers    providerByIDLookup
	secrets      SecretResolver
	invocations  liveModelInvocationRepo
	healthStore  providerConnectionHealthWriter
	enqueuer     rollupJobEnqueuer
	health       *HealthChecker
	concurrency  *ConcurrencyManager
	spans        traceSpanCreator
	httpClient   *http.Client
	logger       *slog.Logger
	now          func() time.Time
	subscription struct {
		mu     sync.Mutex
		tokens map[uuid.UUID]anthropicSubscriptionTokenState
	}
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
		healthStore: opts.HealthStore,
		enqueuer:    opts.Enqueuer,
		health:      opts.Health,
		concurrency: opts.Concurrency,
		spans:       opts.Spans,
		httpClient:  opts.HTTPClient,
		logger:      opts.Logger,
		now:         opts.Now,
		subscription: struct {
			mu     sync.Mutex
			tokens map[uuid.UUID]anthropicSubscriptionTokenState
		}{
			tokens: make(map[uuid.UUID]anthropicSubscriptionTokenState),
		},
	}, nil
}

type anthropicAuthConfig struct {
	Mode            string
	APIKeySecretRef string
	AccessTokenRef  string
	RefreshTokenRef string
	TokenURL        string
	ClientID        string
	ExpiresAt       time.Time
}

type anthropicSubscriptionTokenState struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

type anthropicTokenRefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
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
	var precreatedInvocation *repo.ModelInvocation
	if req.InvocationID != nil && *req.InvocationID != uuid.Nil {
		existing, err := g.invocations.GetByID(ctx, *req.InvocationID)
		if err != nil {
			return turn.ModelResponse{}, fmt.Errorf("lookup precreated invocation %s: %w", req.InvocationID.String(), err)
		}
		precreatedInvocation = &existing
	}

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

		var invocation repo.ModelInvocation
		if precreatedInvocation != nil {
			invocation = *precreatedInvocation
			if invocation.ProviderConnectionID == nil || *invocation.ProviderConnectionID != connection.ID || invocation.ModelProviderID != provider.ID {
				updated, updateErr := g.invocations.UpdateRouting(ctx, invocation.ID, provider.ID, connection.ID)
				if updateErr != nil {
					return turn.ModelResponse{}, updateErr
				}
				invocation = updated
				precreatedInvocation = &updated
			}
		} else {
			invocation, err = g.createInvocation(ctx, orgID, req, provider, connection, stream)
			if err != nil {
				return turn.ModelResponse{}, err
			}
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
			g.recordSpan(orgID, req, provider, connection, invocation.ID, startedAt, stream, callErr)
			mapped, retryable := g.mapProviderError(connection.ID, callErr)
			lastErr = mapped
			if errors.Is(mapped, turn.ErrAuthFailed) {
				continue
			}
			if !retryable {
				return turn.ModelResponse{}, mapped
			}
			continue
		}

		g.health.RecordSuccess(connection.ID)
		g.persistConnectionHealth(ctx, connection.ID, string(HealthStateHealthy))
		usageOverride := tokenUsageFromModelUsage(result.Usage)
		if err := g.completeInvocation(ctx, invocation, result.Content, startedAt, result.FirstChunkAt, usageOverride); err != nil {
			return turn.ModelResponse{}, err
		}
		g.recordSpan(orgID, req, provider, connection, invocation.ID, startedAt, stream, nil)
		if usageOverride != nil {
			metrics.RecordModelTokens(provider.Slug, req.Profile.ModelName, usageOverride.InputTokens, usageOverride.OutputTokens, usageOverride.CacheReadTokens)
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
	invocation.RunID = cloneUUIDPointer(req.RunID)
	invocation.RunStepID = cloneUUIDPointer(req.RunStepID)
	invocation.RunAttemptID = cloneUUIDPointer(req.RunAttemptID)

	return g.invocations.Create(ctx, invocation)
}

func (g *LiveModelGateway) markInvocationFailed(ctx context.Context, invocationID uuid.UUID, cause error) {
	if invocationID == uuid.Nil || g == nil || g.invocations == nil {
		return
	}
	cleanupCtx, cancel := detachedCleanupContext(ctx)
	defer cancel()
	failureClass := classifyInvocationFailure(cause)
	if healthStatus, ok := healthStatusForInvocationFailure(failureClass); ok {
		invocation, getErr := g.invocations.GetByID(cleanupCtx, invocationID)
		if getErr == nil && invocation.ProviderConnectionID != nil {
			g.persistConnectionHealth(cleanupCtx, *invocation.ProviderConnectionID, healthStatus)
		}
	}
	errCode := invocationErrorCode(failureClass)
	errMsg := strings.TrimSpace(cause.Error())
	if errMsg == "" {
		errMsg = "model provider call failed"
	}
	if _, err := g.invocations.UpdateFailure(cleanupCtx, invocationID, "failed", stringPtr(string(failureClass)), &errCode, &errMsg); err != nil {
		g.logger.Warn("failed to mark model invocation as failed", "invocation_id", invocationID, "error", err)
	}
}

func detachedCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, defaultCleanupTimeout)
}

func (g *LiveModelGateway) recordSpan(
	orgID uuid.UUID,
	req turn.ModelRequest,
	provider repo.ModelProvider,
	connection repo.ProviderConnection,
	invocationID uuid.UUID,
	startedAt time.Time,
	stream bool,
	callErr error,
) {
	if g == nil || g.spans == nil {
		return
	}

	endedAt := g.now().UTC()
	durationMS := int(endedAt.Sub(startedAt).Milliseconds())

	status := "ok"
	if callErr != nil {
		status = "error"
	}

	traceID := req.TurnID
	if traceID == uuid.Nil {
		traceID = invocationID
	}

	attrs, _ := json.Marshal(map[string]any{
		"model.name":       req.Profile.ModelName,
		"model.profile_id": req.Profile.LogicalProfileID,
		"model.purpose":    req.Purpose,
		"model.streaming":  stream,
		"provider.id":      provider.ID.String(),
		"provider.slug":    provider.Slug,
		"connection.id":    connection.ID.String(),
		"invocation.id":    invocationID.String(),
	})

	span := repo.TraceSpan{
		TraceID:        traceID,
		OrganizationID: &orgID,
		SpanName:       "model.invoke",
		Service:        "gateway",
		Kind:           "client",
		Status:         status,
		Attributes:     attrs,
		StartedAt:      startedAt,
		EndedAt:        &endedAt,
		DurationMS:     &durationMS,
	}

	if _, err := g.spans.Create(context.Background(), span); err != nil {
		g.logger.Warn("failed to record model invocation trace span", "invocation_id", invocationID, "error", err)
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
		case http.StatusUnauthorized:
			g.health.MarkUnavailable(connectionID)
			g.persistConnectionHealth(context.Background(), connectionID, string(HealthStateUnavailable))
			return fmt.Errorf("%w", turn.ErrAuthFailed), false
		case http.StatusForbidden:
			g.health.MarkUnavailable(connectionID)
			g.persistConnectionHealth(context.Background(), connectionID, string(HealthStateUnavailable))
			return fmt.Errorf("%w", turn.ErrAuthFailed), false
		case http.StatusTooManyRequests:
			slog.Warn("provider rate limited", "connection_id", connectionID, "retry_after", providerErr.RetryAfter, "detail", providerErr.Err)
			g.health.MarkRateLimited(connectionID)
			g.persistConnectionHealth(context.Background(), connectionID, string(HealthStateRateLimited))
			return turn.NewRateLimitedError(providerErr.RetryAfter, providerErr), true
		default:
			if providerErr.StatusCode >= http.StatusInternalServerError {
				g.health.RecordFailure(connectionID, err)
				g.persistConnectionHealth(context.Background(), connectionID, string(g.health.GetState(connectionID)))
				return fmt.Errorf("%w", turn.ErrModelTransient), true
			}
		}
		return err, false
	}

	if errors.Is(err, context.Canceled) {
		return err, false
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		g.health.RecordFailure(connectionID, err)
		g.persistConnectionHealth(context.Background(), connectionID, string(g.health.GetState(connectionID)))
		return fmt.Errorf("%w", turn.ErrModelTransient), true
	}

	if errors.Is(err, context.DeadlineExceeded) {
		g.health.RecordFailure(connectionID, err)
		g.persistConnectionHealth(context.Background(), connectionID, string(g.health.GetState(connectionID)))
		return fmt.Errorf("%w", turn.ErrModelTransient), true
	}

	return err, false
}

type invocationFailureClass string

const (
	invocationFailureProviderAuth      invocationFailureClass = "provider_auth"
	invocationFailureProviderRateLimit invocationFailureClass = "provider_rate_limit"
	invocationFailureProviderTransient invocationFailureClass = "provider_transient"
	invocationFailureProductRuntime    invocationFailureClass = "product_runtime"
)

func classifyInvocationFailure(err error) invocationFailureClass {
	var providerErr ProviderHTTPError
	switch {
	case errors.As(err, &providerErr) && (providerErr.StatusCode == http.StatusUnauthorized || providerErr.StatusCode == http.StatusForbidden):
		return invocationFailureProviderAuth
	case errors.As(err, &providerErr) && providerErr.StatusCode == http.StatusTooManyRequests:
		return invocationFailureProviderRateLimit
	case errors.As(err, &providerErr) && providerErr.StatusCode >= http.StatusInternalServerError:
		return invocationFailureProviderTransient
	case errors.Is(err, turn.ErrAuthFailed):
		return invocationFailureProviderAuth
	case errors.Is(err, turn.ErrRateLimited):
		return invocationFailureProviderRateLimit
	case errors.Is(err, turn.ErrModelTransient), errors.Is(err, context.DeadlineExceeded):
		return invocationFailureProviderTransient
	default:
		var netErr net.Error
		if errors.As(err, &netErr) {
			return invocationFailureProviderTransient
		}
		return invocationFailureProductRuntime
	}
}

func invocationErrorCode(class invocationFailureClass) string {
	switch class {
	case invocationFailureProviderAuth:
		return "provider_auth_failed"
	case invocationFailureProviderRateLimit:
		return "provider_rate_limited"
	case invocationFailureProviderTransient:
		return "provider_transient_failure"
	default:
		return "product_runtime_failure"
	}
}

func healthStatusForInvocationFailure(class invocationFailureClass) (string, bool) {
	switch class {
	case invocationFailureProviderAuth:
		return string(HealthStateUnavailable), true
	case invocationFailureProviderRateLimit:
		return string(HealthStateRateLimited), true
	case invocationFailureProviderTransient:
		return string(HealthStateDegraded), true
	default:
		return "", false
	}
}

func (g *LiveModelGateway) persistConnectionHealth(ctx context.Context, connectionID uuid.UUID, healthStatus string) {
	if g == nil || g.healthStore == nil || connectionID == uuid.Nil || strings.TrimSpace(healthStatus) == "" {
		return
	}
	if _, err := g.healthStore.SetHealthStatus(ctx, connectionID, healthStatus); err != nil {
		g.logger.Warn("failed to persist provider connection health", "connection_id", connectionID, "health_status", healthStatus, "error", err)
	}
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

	body, err := buildProviderBody(providerType, req, stream)
	if err != nil {
		return providerCallResult{}, err
	}

	if providerType == "anthropic" {
		authConfig, authErr := anthropicAuthConfigFromConnection(connection)
		if authErr != nil {
			return providerCallResult{}, authErr
		}
		if authConfig.Mode == anthropicAuthModeSubscription {
			return g.callAnthropicSubscriptionProvider(ctx, orgID, endpointURL, body, stream, onChunk, connection.ID, authConfig)
		}
		apiKey, resolveErr := g.secrets.ResolveRef(ctx, orgID, authConfig.APIKeySecretRef)
		if resolveErr != nil {
			return providerCallResult{}, resolveErr
		}
		return g.executeProviderCall(ctx, endpointURL, providerType, body, stream, onChunk, func(httpReq *http.Request) {
			applyAuthHeaders(httpReq, providerType, apiKey)
		})
	}

	apiKey, err := g.secrets.ResolveRef(ctx, orgID, connection.APIKeyRef)
	if err != nil {
		return providerCallResult{}, err
	}
	return g.executeProviderCall(ctx, endpointURL, providerType, body, stream, onChunk, func(httpReq *http.Request) {
		applyAuthHeaders(httpReq, providerType, apiKey)
	})
}

func (g *LiveModelGateway) executeProviderCall(
	ctx context.Context,
	endpointURL string,
	providerType string,
	body []byte,
	stream bool,
	onChunk func(string) error,
	applyHeaders func(*http.Request),
) (providerCallResult, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(body))
	if err != nil {
		return providerCallResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if applyHeaders != nil {
		applyHeaders(httpReq)
	}

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

func (g *LiveModelGateway) callAnthropicSubscriptionProvider(
	ctx context.Context,
	orgID uuid.UUID,
	endpointURL string,
	body []byte,
	stream bool,
	onChunk func(string) error,
	connectionID uuid.UUID,
	authConfig anthropicAuthConfig,
) (providerCallResult, error) {
	state, err := g.resolveAnthropicSubscriptionState(ctx, orgID, connectionID, authConfig)
	if err != nil {
		return providerCallResult{}, err
	}

	result, err := g.executeProviderCall(ctx, endpointURL, "anthropic", body, stream, onChunk, func(httpReq *http.Request) {
		applyAnthropicSubscriptionHeaders(httpReq, state.AccessToken)
	})
	if err == nil {
		return result, nil
	}

	var providerErr ProviderHTTPError
	if !errors.As(err, &providerErr) || providerErr.StatusCode != http.StatusUnauthorized {
		return providerCallResult{}, err
	}
	if strings.TrimSpace(state.RefreshToken) == "" {
		return providerCallResult{}, err
	}

	refreshed, refreshErr := g.refreshAnthropicSubscriptionToken(ctx, authConfig, state.RefreshToken)
	if refreshErr != nil {
		return providerCallResult{}, refreshErr
	}
	g.setAnthropicSubscriptionState(connectionID, refreshed)

	return g.executeProviderCall(ctx, endpointURL, "anthropic", body, stream, onChunk, func(httpReq *http.Request) {
		applyAnthropicSubscriptionHeaders(httpReq, refreshed.AccessToken)
	})
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

	// OpenAI streaming tool call accumulator keyed by delta index.
	type openAIToolAccum struct {
		id   string
		name string
		args strings.Builder
	}
	openAIAccums := map[int]*openAIToolAccum{}

	// Anthropic streaming tool block accumulator keyed by block index.
	type anthropicToolAccum struct {
		id   string
		name string
		json strings.Builder
	}
	anthropicAccums := map[int]*anthropicToolAccum{}

	err := parseSSE(body, func(eventType, data string) error {
		if strings.TrimSpace(data) == "" {
			return nil
		}

		switch providerType {
		case "anthropic":
			chunk, toolStart, toolDelta, eventUsage, done, err := parseAnthropicStreamEvent(eventType, data)
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
			if toolStart != nil {
				anthropicAccums[toolStart.Index] = &anthropicToolAccum{
					id:   toolStart.ID,
					name: toolStart.Name,
				}
			}
			if toolDelta != nil {
				if accum, ok := anthropicAccums[toolDelta.Index]; ok {
					accum.json.WriteString(toolDelta.PartialJSON)
				}
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
			chunk, toolCallDeltas, eventUsage, done, err := parseOpenAIStreamEvent(data)
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
			for _, delta := range toolCallDeltas {
				accum := openAIAccums[delta.Index]
				if accum == nil {
					accum = &openAIToolAccum{}
					openAIAccums[delta.Index] = accum
				}
				if delta.ID != "" {
					accum.id = delta.ID
				}
				if delta.Function.Name != "" {
					accum.name = delta.Function.Name
				}
				accum.args.WriteString(delta.Function.Arguments)
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

	// Finalize accumulated streaming tool calls.
	var toolCalls []turn.ModelToolCall

	if len(openAIAccums) > 0 {
		indices := make([]int, 0, len(openAIAccums))
		for idx := range openAIAccums {
			indices = append(indices, idx)
		}
		sort.Ints(indices)
		for _, idx := range indices {
			accum := openAIAccums[idx]
			var args map[string]any
			if s := accum.args.String(); s != "" {
				if jsonErr := json.Unmarshal([]byte(s), &args); jsonErr != nil {
					args = map[string]any{"_raw": s}
				}
			}
			toolCalls = append(toolCalls, turn.ModelToolCall{
				ID:        accum.id,
				Name:      accum.name,
				Arguments: args,
			})
		}
	}

	if len(anthropicAccums) > 0 {
		indices := make([]int, 0, len(anthropicAccums))
		for idx := range anthropicAccums {
			indices = append(indices, idx)
		}
		sort.Ints(indices)
		for _, idx := range indices {
			accum := anthropicAccums[idx]
			args := map[string]any{}
			if s := accum.json.String(); s != "" {
				if jsonErr := json.Unmarshal([]byte(s), &args); jsonErr != nil {
					args = map[string]any{"_raw": s}
				}
			}
			toolCalls = append(toolCalls, turn.ModelToolCall{
				ID:        accum.id,
				Name:      accum.name,
				Arguments: args,
			})
		}
	}

	result := providerCallResult{
		Content:      builder.String(),
		ToolCalls:    toolCalls,
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

type openAIStreamToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAIStreamPayload struct {
	Choices []struct {
		Delta struct {
			Content   string                      `json:"content"`
			ToolCalls []openAIStreamToolCallDelta `json:"tool_calls"`
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

func parseOpenAIStreamEvent(data string) (chunk string, toolCallDeltas []openAIStreamToolCallDelta, usage *turn.ModelUsage, done bool, err error) {
	if strings.EqualFold(strings.TrimSpace(data), "[DONE]") {
		return "", nil, nil, true, nil
	}

	var payload openAIStreamPayload
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return "", nil, nil, false, err
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
		toolCallDeltas = append(toolCallDeltas, choice.Delta.ToolCalls...)
	}
	return chunk, toolCallDeltas, usage, false, nil
}

type anthropicToolBlockStart struct {
	Index int
	ID    string
	Name  string
}

type anthropicToolBlockDelta struct {
	Index       int
	PartialJSON string
}

type anthropicStreamPayload struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
	ContentBlock *struct {
		Type string `json:"type"`
		Text string `json:"text"`
		ID   string `json:"id"`
		Name string `json:"name"`
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

func parseAnthropicStreamEvent(eventType, data string) (chunk string, toolStart *anthropicToolBlockStart, toolDelta *anthropicToolBlockDelta, usage *turn.ModelUsage, done bool, err error) {
	var payload anthropicStreamPayload
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return "", nil, nil, nil, false, err
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
		return "", nil, nil, usage, true, nil
	case "content_block_start":
		if payload.ContentBlock != nil && payload.ContentBlock.Type == "tool_use" {
			toolStart = &anthropicToolBlockStart{
				Index: payload.Index,
				ID:    payload.ContentBlock.ID,
				Name:  payload.ContentBlock.Name,
			}
		} else if payload.ContentBlock != nil {
			chunk = payload.ContentBlock.Text
		}
	case "content_block_delta":
		if payload.Delta != nil {
			if payload.Delta.Type == "input_json_delta" {
				toolDelta = &anthropicToolBlockDelta{
					Index:       payload.Index,
					PartialJSON: payload.Delta.PartialJSON,
				}
			} else {
				chunk = payload.Delta.Text
			}
		}
	default:
		if payload.Delta != nil {
			chunk = payload.Delta.Text
		}
	}

	return chunk, toolStart, toolDelta, usage, false, nil
}

type openAICompletionPayload struct {
	Choices []struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
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
	var toolCalls []turn.ModelToolCall
	for _, choice := range payload.Choices {
		if strings.TrimSpace(choice.Message.Content) != "" {
			contentBuilder.WriteString(choice.Message.Content)
		}
		for _, tc := range choice.Message.ToolCalls {
			var args map[string]any
			if tc.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					args = map[string]any{"_raw": tc.Function.Arguments}
				}
			}
			toolCalls = append(toolCalls, turn.ModelToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: args,
			})
		}
	}

	result := providerCallResult{
		Content:   contentBuilder.String(),
		ToolCalls: toolCalls,
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
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
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
	var toolCalls []turn.ModelToolCall
	for _, block := range payload.Content {
		switch block.Type {
		case "tool_use":
			var args map[string]any
			if len(block.Input) > 0 {
				if err := json.Unmarshal(block.Input, &args); err != nil {
					args = map[string]any{"_raw": string(block.Input)}
				}
			}
			toolCalls = append(toolCalls, turn.ModelToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: args,
			})
		default:
			if strings.TrimSpace(block.Text) != "" {
				contentBuilder.WriteString(block.Text)
			}
		}
	}

	result := providerCallResult{
		Content:   contentBuilder.String(),
		ToolCalls: toolCalls,
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

func anthropicAuthConfigFromConnection(connection repo.ProviderConnection) (anthropicAuthConfig, error) {
	config := anthropicAuthConfig{
		Mode:            anthropicAuthModeAPIKey,
		APIKeySecretRef: strings.TrimSpace(connection.APIKeyRef),
	}

	metadata := metadataMap(connection.Metadata)
	if mode, _ := metadata[providerConnectionMetadataAuthMode].(string); strings.TrimSpace(mode) != "" {
		config.Mode = strings.ToLower(strings.TrimSpace(mode))
	}
	if config.Mode != anthropicAuthModeAPIKey && config.Mode != anthropicAuthModeSubscription {
		return anthropicAuthConfig{}, fmt.Errorf("unsupported anthropic auth mode %q", config.Mode)
	}
	if config.Mode == anthropicAuthModeAPIKey {
		if strings.TrimSpace(config.APIKeySecretRef) == "" {
			return anthropicAuthConfig{}, fmt.Errorf("anthropic api_key auth requires api key secret ref")
		}
		return config, nil
	}

	accessRef, _ := metadata[providerConnectionMetadataSubscriptionAccessTokenSecretRef].(string)
	accessRef = strings.TrimSpace(accessRef)
	if accessRef == "" {
		accessRef = strings.TrimSpace(connection.APIKeyRef)
	}
	if accessRef == "" {
		return anthropicAuthConfig{}, fmt.Errorf("anthropic subscription auth requires access token secret ref")
	}
	refreshRef, _ := metadata[providerConnectionMetadataSubscriptionRefreshTokenSecretRef].(string)
	tokenURL, _ := metadata[providerConnectionMetadataSubscriptionTokenURL].(string)
	clientID, _ := metadata[providerConnectionMetadataSubscriptionClientID].(string)
	expiresAtText, _ := metadata[providerConnectionMetadataSubscriptionExpiresAt].(string)

	config.AccessTokenRef = strings.TrimSpace(accessRef)
	config.RefreshTokenRef = strings.TrimSpace(refreshRef)
	config.TokenURL = strings.TrimSpace(tokenURL)
	if config.TokenURL == "" {
		config.TokenURL = anthropicSubscriptionTokenURL
	}
	config.ClientID = strings.TrimSpace(clientID)
	if config.ClientID == "" {
		config.ClientID = anthropicSubscriptionClientID
	}
	if parsed, err := parseOptionalRFC3339(expiresAtText); err == nil {
		config.ExpiresAt = parsed
	}

	return config, nil
}

func parseOptionalRFC3339(raw string) (time.Time, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func metadataMap(metadata json.RawMessage) map[string]any {
	result := map[string]any{}
	if len(metadata) == 0 {
		return result
	}
	_ = json.Unmarshal(metadata, &result)
	return result
}

func (g *LiveModelGateway) resolveAnthropicSubscriptionState(
	ctx context.Context,
	orgID uuid.UUID,
	connectionID uuid.UUID,
	authConfig anthropicAuthConfig,
) (anthropicSubscriptionTokenState, error) {
	if cached, ok := g.getAnthropicSubscriptionState(connectionID); ok {
		if g.subscriptionStateValid(cached) {
			return cached, nil
		}
		if strings.TrimSpace(cached.RefreshToken) != "" {
			refreshed, err := g.refreshAnthropicSubscriptionToken(ctx, authConfig, cached.RefreshToken)
			if err == nil {
				g.setAnthropicSubscriptionState(connectionID, refreshed)
				return refreshed, nil
			}
		}
	}

	accessToken, err := g.secrets.ResolveRef(ctx, orgID, authConfig.AccessTokenRef)
	if err != nil {
		return anthropicSubscriptionTokenState{}, err
	}
	refreshToken := ""
	if strings.TrimSpace(authConfig.RefreshTokenRef) != "" {
		refreshToken, err = g.secrets.ResolveRef(ctx, orgID, authConfig.RefreshTokenRef)
		if err != nil {
			return anthropicSubscriptionTokenState{}, err
		}
	}
	state := anthropicSubscriptionTokenState{
		AccessToken:  strings.TrimSpace(accessToken),
		RefreshToken: strings.TrimSpace(refreshToken),
		ExpiresAt:    authConfig.ExpiresAt,
	}

	if g.subscriptionStateExpired(state) && strings.TrimSpace(state.RefreshToken) != "" {
		refreshed, refreshErr := g.refreshAnthropicSubscriptionToken(ctx, authConfig, state.RefreshToken)
		if refreshErr != nil {
			return anthropicSubscriptionTokenState{}, refreshErr
		}
		state = refreshed
	}
	g.setAnthropicSubscriptionState(connectionID, state)
	return state, nil
}

func (g *LiveModelGateway) refreshAnthropicSubscriptionToken(
	ctx context.Context,
	authConfig anthropicAuthConfig,
	refreshToken string,
) (anthropicSubscriptionTokenState, error) {
	tokenURL := strings.TrimSpace(authConfig.TokenURL)
	if tokenURL == "" {
		tokenURL = anthropicSubscriptionTokenURL
	}
	payload := map[string]any{
		"grant_type":    "refresh_token",
		"client_id":     strings.TrimSpace(authConfig.ClientID),
		"refresh_token": strings.TrimSpace(refreshToken),
	}
	if strings.TrimSpace(payload["client_id"].(string)) == "" {
		payload["client_id"] = anthropicSubscriptionClientID
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return anthropicSubscriptionTokenState{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(body))
	if err != nil {
		return anthropicSubscriptionTokenState{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", anthropicClaudeCodeUserAgent)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return anthropicSubscriptionTokenState{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return anthropicSubscriptionTokenState{}, providerHTTPErrorFromResponse(resp)
	}

	var refreshed anthropicTokenRefreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&refreshed); err != nil {
		return anthropicSubscriptionTokenState{}, err
	}
	access := strings.TrimSpace(refreshed.AccessToken)
	if access == "" {
		return anthropicSubscriptionTokenState{}, fmt.Errorf("anthropic token refresh returned empty access_token")
	}
	nextRefresh := strings.TrimSpace(refreshed.RefreshToken)
	if nextRefresh == "" {
		nextRefresh = strings.TrimSpace(refreshToken)
	}
	var expiresAt time.Time
	if refreshed.ExpiresIn > 0 {
		expiresAt = g.now().UTC().Add(time.Duration(refreshed.ExpiresIn) * time.Second).Add(-anthropicTokenRefreshSkew)
	}
	return anthropicSubscriptionTokenState{
		AccessToken:  access,
		RefreshToken: nextRefresh,
		ExpiresAt:    expiresAt,
	}, nil
}

func (g *LiveModelGateway) subscriptionStateValid(state anthropicSubscriptionTokenState) bool {
	return strings.TrimSpace(state.AccessToken) != "" && !g.subscriptionStateExpired(state)
}

func (g *LiveModelGateway) subscriptionStateExpired(state anthropicSubscriptionTokenState) bool {
	if state.ExpiresAt.IsZero() {
		return false
	}
	return !state.ExpiresAt.After(g.now().UTC())
}

func (g *LiveModelGateway) getAnthropicSubscriptionState(connectionID uuid.UUID) (anthropicSubscriptionTokenState, bool) {
	g.subscription.mu.Lock()
	defer g.subscription.mu.Unlock()
	state, ok := g.subscription.tokens[connectionID]
	return state, ok
}

func (g *LiveModelGateway) setAnthropicSubscriptionState(connectionID uuid.UUID, state anthropicSubscriptionTokenState) {
	g.subscription.mu.Lock()
	defer g.subscription.mu.Unlock()
	if g.subscription.tokens == nil {
		g.subscription.tokens = make(map[uuid.UUID]anthropicSubscriptionTokenState)
	}
	g.subscription.tokens[connectionID] = state
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

func applyAnthropicSubscriptionHeaders(req *http.Request, accessToken string) {
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("anthropic-version", defaultAnthropicVersion)
	req.Header.Set("anthropic-dangerous-direct-browser-access", "true")
	req.Header.Set("anthropic-beta", anthropicSubscriptionBeta)
	req.Header.Set("x-app", "cli")
	req.Header.Set("accept", "application/json")
	req.Header.Set("user-agent", anthropicClaudeCodeUserAgent)
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
	names := make([]string, 0, len(req.Prompt.ToolDescriptors))
	for _, descriptor := range req.Prompt.ToolDescriptors {
		apiName := strings.TrimSpace(descriptor.APIName)
		if apiName == "" {
			apiName = tools.SanitizeToolNameForAPI(strings.TrimSpace(descriptor.Name))
		}
		names = append(names, apiName)
		result = append(result, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        apiName,
				"description": strings.TrimSpace(descriptor.Description),
				"parameters":  normalizeToolSchema(descriptor.InputSchema),
			},
		})
	}
	slog.Debug("openai_tools_sent", "count", len(result), "names", names)
	return result
}

func anthropicTools(req turn.ModelRequest) []map[string]any {
	if req.Prompt == nil || len(req.Prompt.ToolDescriptors) == 0 {
		return nil
	}

	result := make([]map[string]any, 0, len(req.Prompt.ToolDescriptors))
	for _, descriptor := range req.Prompt.ToolDescriptors {
		apiName := strings.TrimSpace(descriptor.APIName)
		if apiName == "" {
			apiName = tools.SanitizeToolNameForAPI(strings.TrimSpace(descriptor.Name))
		}
		result = append(result, map[string]any{
			"name":         apiName,
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

	// OpenAI and Anthropic require object schemas to include a "properties" key.
	// If the parsed schema is an object type without "properties", inject an empty one.
	// Also: OpenAI standard function calling does not support additionalProperties:false
	// without strict mode. When all params are optional (required is absent or empty),
	// remove additionalProperties:false to prevent OpenAI from silently dropping the tool.
	if obj, ok := parsed.(map[string]any); ok {
		if t, _ := obj["type"].(string); t == "object" {
			if _, hasProps := obj["properties"]; !hasProps {
				obj["properties"] = map[string]any{}
			}
			if _, hasAddl := obj["additionalProperties"]; hasAddl {
				req, hasReq := obj["required"]
				isEmpty := !hasReq
				if hasReq {
					if arr, ok := req.([]any); ok && len(arr) == 0 {
						isEmpty = true
					}
				}
				if isEmpty {
					delete(obj, "additionalProperties")
					delete(obj, "required")
				}
			}
			// OpenAI rejects array properties that don't specify an items schema.
			// Inject an empty items schema (accepts any value) for bare array types.
			if props, ok := obj["properties"].(map[string]any); ok {
				for k, v := range props {
					if propObj, ok := v.(map[string]any); ok {
						if propObj["type"] == "array" {
							if _, hasItems := propObj["items"]; !hasItems {
								propObj["items"] = map[string]any{}
								props[k] = propObj
							}
						}
					}
				}
			}
		}
	}

	return parsed
}

type providerMessage struct {
	Role       string  `json:"role"`
	Content    string  `json:"content"`
	ToolCallID *string `json:"tool_call_id,omitempty"`
}

// openAIMessages converts prompt messages to OpenAI API format.
// Returns []any so assistant messages with tool_calls can have a different
// shape (object with "tool_calls" array) than regular text messages.
func openAIMessages(req turn.ModelRequest) []any {
	base := requestMessages(req)
	messages := make([]any, 0, len(base))
	// Track whether the most recent assistant message carried tool_calls,
	// so we can skip orphaned tool-result messages (from previous broken turns
	// where tool_calls were not stored in the assistant message metadata).
	lastAssistantHadToolCalls := false
	// Index of the most recently added assistant+tool_calls message. Used to
	// retroactively remove it if a non-tool message appears before any tool
	// results were added (conversation history with interleaved user messages
	// or dispatch interrupted before results were stored).
	lastToolCallsIdx := -1
	// Whether any tool_result messages were added after the last assistant+tool_calls.
	// Only remove the assistant+tool_calls retroactively if no tool_results followed.
	toolResultsAdded := false
	// Valid tool_call_id values from the most recent assistant+tool_calls message.
	// Tool results that reference IDs not in this set are dropped (handles race
	// conditions where two workers both stored an assistant+tool_calls message for
	// the same turn; the earlier one is dropped but its tool_results remain).
	validToolCallIDs := map[string]bool{}
	for _, item := range base {
		role := strings.ToLower(strings.TrimSpace(item.Role))
		switch role {
		case "system", "user", "assistant", "tool":
		case "tool_result":
			role = "tool"
		default:
			role = "user"
		}

		// Skip orphaned tool messages that have no preceding assistant+tool_calls.
		// This handles old conversation history from before this fix was deployed.
		if role == "tool" && !lastAssistantHadToolCalls {
			continue
		}

		// Skip tool messages without a tool_call_id (system-generated messages
		// like run_log_link from scheduler/supervisor).
		if role == "tool" && item.ToolCallID == nil {
			continue
		}

		// Skip tool_result messages whose call_id does not match any of the
		// IDs declared in the current assistant+tool_calls message. This handles
		// duplicate-worker race conditions where two assistant+tool_calls blocks
		// were stored back-to-back; the first is dropped as dangling but its
		// corresponding tool_result would otherwise survive and mismatch.
		if role == "tool" && len(validToolCallIDs) > 0 && !validToolCallIDs[*item.ToolCallID] {
			continue
		}

		// A non-tool message signals the end of a tool exchange. If no tool_results
		// were added after the assistant+tool_calls, the sequence is dangling
		// (user replied before results were stored, or dispatch was interrupted).
		// Retroactively drop the assistant+tool_calls so the API doesn't reject.
		if role != "tool" && lastAssistantHadToolCalls {
			if !toolResultsAdded && lastToolCallsIdx >= 0 {
				messages = append(messages[:lastToolCallsIdx], messages[lastToolCallsIdx+1:]...)
			} else if toolResultsAdded {
				// Inject synthetic tool results for any tool_call_ids that
				// don't have matching responses (e.g. turn ended mid-dispatch).
				answeredIDs := map[string]bool{}
				for i := lastToolCallsIdx + 1; i < len(messages); i++ {
					if m, ok := messages[i].(map[string]any); ok {
						if id, _ := m["tool_call_id"].(string); id != "" {
							answeredIDs[id] = true
						}
					}
				}
				for id := range validToolCallIDs {
					if !answeredIDs[id] {
						messages = append(messages, map[string]any{
							"role":         "tool",
							"tool_call_id": id,
							"content":      "[Turn ended before tool result was received]",
						})
					}
				}
			}
			lastAssistantHadToolCalls = false
			lastToolCallsIdx = -1
			toolResultsAdded = false
			validToolCallIDs = map[string]bool{}
		}

		if role != "tool" {
			lastAssistantHadToolCalls = false
		}

		// Assistant messages that triggered tool calls must carry tool_calls
		// so OpenAI can match them to subsequent tool result messages.
		if role == "assistant" && len(item.ToolCalls) > 0 {
			tcs := make([]map[string]any, 0, len(item.ToolCalls))
			validToolCallIDs = map[string]bool{}
			for _, tc := range item.ToolCalls {
				argsJSON, _ := json.Marshal(tc.Arguments)
				tcs = append(tcs, map[string]any{
					"id":   tc.ID,
					"type": "function",
					"function": map[string]any{
						"name":      tc.Name,
						"arguments": string(argsJSON),
					},
				})
				validToolCallIDs[tc.ID] = true
			}
			msg := map[string]any{
				"role":       "assistant",
				"tool_calls": tcs,
			}
			if item.Content != "" {
				msg["content"] = item.Content
			}
			lastAssistantHadToolCalls = true
			lastToolCallsIdx = len(messages)
			toolResultsAdded = false
			messages = append(messages, msg)
			continue
		}

		msg := map[string]any{
			"role":    role,
			"content": item.Content,
		}
		if role == "tool" && item.ToolCallID != nil {
			msg["tool_call_id"] = *item.ToolCallID
		}
		if role == "tool" {
			toolResultsAdded = true
		}
		messages = append(messages, msg)
	}
	// Handle trailing assistant+tool_calls at the end of conversation.
	if lastAssistantHadToolCalls && lastToolCallsIdx >= 0 {
		if !toolResultsAdded {
			// No tool results at all — remove the dangling assistant+tool_calls.
			messages = append(messages[:lastToolCallsIdx], messages[lastToolCallsIdx+1:]...)
		} else {
			// Inject synthetic tool results for any unmatched tool_call_ids.
			answeredIDs := map[string]bool{}
			for i := lastToolCallsIdx + 1; i < len(messages); i++ {
				if m, ok := messages[i].(map[string]any); ok {
					if id, _ := m["tool_call_id"].(string); id != "" {
						answeredIDs[id] = true
					}
				}
			}
			for id := range validToolCallIDs {
				if !answeredIDs[id] {
					messages = append(messages, map[string]any{
						"role":         "tool",
						"tool_call_id": id,
						"content":      "[Turn ended before tool result was received]",
					})
				}
			}
		}
	}
	if len(messages) == 0 {
		messages = append(messages, map[string]any{"role": "user", "content": "Respond with a concise answer."})
	}
	return messages
}

func anthropicMessages(req turn.ModelRequest) (string, []any) {
	base := requestMessages(req)
	systemParts := make([]string, 0, 2)
	out := make([]any, 0, len(base))
	lastAssistantHadToolCalls := false
	// Index of the most recently added assistant+tool_calls message in out.
	// Used to retroactively remove it if a non-tool message appears before
	// the matching tool_result (conversation history with interleaved messages).
	lastToolCallsIdx := -1
	// Valid tool_call_id values from the most recent assistant+tool_calls message.
	// Tool results whose tool_use_id is not in this set are dropped (handles race
	// conditions where two workers both stored an assistant+tool_calls message for
	// the same turn; the earlier one is dropped but its tool_results remain).
	validToolCallIDs := map[string]bool{}
	// pendingToolResults collects consecutive tool_result blocks to merge into
	// one Anthropic user message (Anthropic requires all tool results for a
	// parallel tool call to appear as a single user turn).
	var pendingToolResults []any

	flushToolResults := func() {
		if len(pendingToolResults) > 0 {
			// Inject synthetic tool_result blocks for any tool_use IDs that
			// don't have matching results (e.g. turn ended mid-dispatch due to
			// max tool calls or model output truncation).
			answeredIDs := map[string]bool{}
			for _, tr := range pendingToolResults {
				if m, ok := tr.(map[string]any); ok {
					if id, _ := m["tool_use_id"].(string); id != "" {
						answeredIDs[id] = true
					}
				}
			}
			for id := range validToolCallIDs {
				if !answeredIDs[id] {
					pendingToolResults = append(pendingToolResults, map[string]any{
						"type":        "tool_result",
						"tool_use_id": id,
						"content":     "[Turn ended before tool result was received]",
					})
				}
			}
			out = append(out, map[string]any{
				"role":    "user",
				"content": pendingToolResults,
			})
			pendingToolResults = nil
		} else if lastAssistantHadToolCalls && lastToolCallsIdx >= 0 {
			// No tool results were accumulated despite having an assistant+tool_calls.
			// Retroactively drop the dangling assistant+tool_calls so the Anthropic
			// API doesn't reject the request.
			out = append(out[:lastToolCallsIdx], out[lastToolCallsIdx+1:]...)
		}
		// pendingToolResults populated → tool results were consumed (valid sequence).
		// Empty pendingToolResults → either no tool_calls preceded this, or they were dangling.
		lastAssistantHadToolCalls = false
		lastToolCallsIdx = -1
		validToolCallIDs = map[string]bool{}
	}

	for _, item := range base {
		role := strings.ToLower(strings.TrimSpace(item.Role))
		switch role {
		case "system":
			if strings.TrimSpace(item.Content) != "" {
				systemParts = append(systemParts, item.Content)
			}
			continue
		case "assistant":
			// Flush any pending tool results before new assistant turn.
			flushToolResults()
			if len(item.ToolCalls) > 0 {
				// Anthropic: assistant with tool_use blocks.
				content := make([]map[string]any, 0, len(item.ToolCalls)+1)
				if item.Content != "" {
					content = append(content, map[string]any{"type": "text", "text": item.Content})
				}
				validToolCallIDs = map[string]bool{}
				for _, tc := range item.ToolCalls {
					inputArgs := any(tc.Arguments)
					if tc.Arguments == nil {
						inputArgs = map[string]any{}
					}
					content = append(content, map[string]any{
						"type":  "tool_use",
						"id":    tc.ID,
						"name":  tc.Name,
						"input": inputArgs,
					})
					validToolCallIDs[tc.ID] = true
				}
				lastAssistantHadToolCalls = true
				lastToolCallsIdx = len(out)
				out = append(out, map[string]any{"role": "assistant", "content": content})
				continue
			}
			out = append(out, map[string]any{"role": "assistant", "content": item.Content})
		case "tool_result":
			// Skip orphaned tool results with no preceding assistant+tool_calls.
			if !lastAssistantHadToolCalls {
				continue
			}
			// Skip tool_result messages without a tool_call_id (system-generated
			// messages like run_log_link from scheduler/supervisor).
			if item.ToolCallID == nil {
				continue
			}
			// Skip tool_result messages whose call_id does not match the current
			// assistant+tool_calls block (handles duplicate-worker race condition).
			if len(validToolCallIDs) > 0 && !validToolCallIDs[*item.ToolCallID] {
				continue
			}
			// Accumulate tool results — they'll be flushed as one user message.
			pendingToolResults = append(pendingToolResults, map[string]any{
				"type":        "tool_result",
				"tool_use_id": *item.ToolCallID,
				"content":     item.Content,
			})
		default:
			// Flush pending tool results before a user/other message.
			flushToolResults()
			out = append(out, map[string]any{"role": "user", "content": item.Content})
		}
	}
	// Flush any trailing tool results.
	flushToolResults()

	// Anthropic rejects requests where the last message is from the assistant
	// (prefill is not supported on most models). Drop any trailing assistant
	// messages so the conversation always ends on a user turn.
	for len(out) > 0 {
		last := out[len(out)-1]
		if m, ok := last.(map[string]any); ok {
			if r, _ := m["role"].(string); r == "assistant" {
				out = out[:len(out)-1]
				continue
			}
		}
		break
	}

	if len(out) == 0 {
		out = append(out, map[string]any{"role": "user", "content": "Respond with a concise answer."})
	}
	return strings.Join(systemParts, "\n\n"), out
}

func requestMessages(req turn.ModelRequest) []prompt.PromptMessage {
	messages := make([]prompt.PromptMessage, 0)
	if req.Prompt != nil && len(req.Prompt.Messages) > 0 {
		for _, item := range req.Prompt.Messages {
			isEmpty := strings.TrimSpace(item.Content) == ""
			hasToolCalls := len(item.ToolCalls) > 0
			// Include messages that have content OR tool_calls (or both).
			// Previously empty-content messages were dropped, which removed the
			// assistant message that triggered tool calls from the history.
			if isEmpty && !hasToolCalls && item.ToolCallID == nil {
				continue
			}
			messages = append(messages, item)
		}
	}
	if len(messages) > 0 {
		return messages
	}

	if strings.TrimSpace(req.InstructionHint) != "" {
		messages = append(messages, prompt.PromptMessage{Role: "system", Content: strings.TrimSpace(req.InstructionHint)})
	}
	for _, item := range req.HumanMessages {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		messages = append(messages, prompt.PromptMessage{Role: "user", Content: trimmed})
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
	if req.InvocationID != nil && *req.InvocationID != uuid.Nil {
		payload["invocation_id"] = req.InvocationID.String()
	}
	if req.RunID != nil && *req.RunID != uuid.Nil {
		payload["run_id"] = req.RunID.String()
	}
	if req.RunStepID != nil && *req.RunStepID != uuid.Nil {
		payload["run_step_id"] = req.RunStepID.String()
	}
	if req.RunAttemptID != nil && *req.RunAttemptID != uuid.Nil {
		payload["run_attempt_id"] = req.RunAttemptID.String()
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

func cloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func uuidPtr(value uuid.UUID) *uuid.UUID {
	if value == uuid.Nil {
		return nil
	}
	copied := value
	return &copied
}
