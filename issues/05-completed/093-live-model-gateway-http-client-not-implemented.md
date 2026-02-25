# 093: Live Model Gateway HTTP Client Not Implemented

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | M (2–3 days) |
| Spec refs | doc 07 §ModelGateway, doc 07 §ProviderConnections, doc 07 §StreamingInference |
| Spec status | unimplemented |
| Depends on | 035 (model gateway routing — complete), 036 (streaming/tracking — complete) |
| Blocks | All live LLM functionality |
| Severity | CRITICAL — nothing works with a real LLM |

## Problem

The worker always assigns `turn.UnavailableModelGateway{}` in production:

```go
// internal/worker/worker.go:190
modelGateway := turn.ModelGateway(turn.UnavailableModelGateway{})
if strings.EqualFold(strings.TrimSpace(os.Getenv("OTTERCAMP_MODE")), "test") {
    modelGateway = deterministicTurnModelGateway{}
}
```

`UnavailableModelGateway.StreamComplete` and `Complete` both return:
```
fmt.Errorf("model gateway is not configured")
```

The `internal/gateway` package has routing (`router.go`), health checking (`health.go`),
streaming infrastructure (`streaming.go`), token counting (`token_count.go`), and
concurrency control (`concurrency.go`) — but **no HTTP client** that calls the provider API.

There is no code anywhere in the codebase that makes a live HTTP request to
`https://api.openai.com/v1/chat/completions` or `https://api.anthropic.com/v1/messages`.

## Verified

Discovered during live validation:
- Bootstrap succeeds ✓
- Auth, agents, sessions all work ✓
- `chat send --wait` sends message, worker receives job
- Worker logs: silent (no job processing errors, no success)
- Turn engine calls `StreamComplete` → `UnavailableModelGateway` → error
- No LLM response ever returns

## Must Build

### 1. `internal/gateway/client.go` — HTTP provider client

Implement `turn.ModelGateway` interface backed by the existing gateway router:

```go
type LiveModelGateway struct {
    router      *gateway.Router
    connections providerConnectionLookup
    secret      SecretResolver    // resolves "ref:slug" → plaintext API key
    invocations ModelInvocationRepo
    enqueuer    RollupEnqueuer
    orgID       uuid.UUID
    profileID   string
    httpClient  *http.Client
}

func (g *LiveModelGateway) StreamComplete(ctx context.Context, req turn.ModelRequest, onChunk func(string) error) (turn.ModelResponse, error)
func (g *LiveModelGateway) Complete(ctx context.Context, req turn.ModelRequest) (turn.ModelResponse, error)
```

Must support:
- **OpenAI** — `POST {base_url}/chat/completions` with `Authorization: Bearer {key}`
  - Request: `{"model": model, "messages": [...], "stream": true}`
  - SSE stream: `data: {"choices":[{"delta":{"content":"..."}}]}`
- **Anthropic** — `POST {base_url}/v1/messages` with `x-api-key: {key}`, `anthropic-version: 2023-06-01`
  - Request: `{"model": model, "messages": [...], "stream": true}`
  - SSE stream: `event: content_block_delta`, `data: {"delta":{"text":"..."}}`

Detect provider format by checking `provider.Slug` (`"openai"` vs `"anthropic"`) or
by inspecting `model_name` (contains "claude" → Anthropic format).

### 2. Wire into `internal/worker/worker.go`

Replace the `UnavailableModelGateway` with the live implementation:

```go
modelGateway := turn.ModelGateway(newLiveModelGateway(
    gatewayRouter,
    connectionRepo,
    secretService,
    invocationRepo,
    jqWorker,
))
```

### 3. Model invocation record

Before calling the provider, create a `ModelInvocation` record via `repo.NewModelInvocationRepo`.
After completion, update it with token counts and latency. The `StreamProcessor` in
`internal/gateway/streaming.go` already has the update logic — use it.

### 4. Secret resolution

The `ProviderConnection.APIKeyRef` is `"ref:{slug}"`. Use `internal/secret.Service.ResolveRef`
to decrypt and obtain the plaintext API key. The secret service requires `OTTERCAMP_MASTER_KEY`.

### 5. Error mapping

Map provider HTTP errors to:
- 401/403 → mark connection unhealthy in `HealthChecker`, return `turn.ErrAuthFailed`
- 429 → mark rate-limited in `HealthChecker`, return `turn.ErrRateLimited`
- 5xx → mark degraded, retry if `maxFallbackHops` allows

## Test

Add integration test: `internal/gateway/client_integration_test.go`
- Spin up `MockProviderServer` with a canned SSE response
- Call `StreamComplete`, assert tokens accumulate and response is returned
- Assert `ModelInvocation` record is updated in DB

## Notes

- The existing `modelgw` routing integration tests (issue 035) already validate
  router selection — those remain green
- `deterministicTurnModelGateway` in `internal/worker/deterministic_model_gateway.go`
  is the test-mode implementation — use it as a reference for the interface contract
- The `internal/gateway/concurrency.go` semaphore should wrap each live call
  to respect `MaxConcurrent` on the provider connection
