---
## Summary

This spec defines OtterCamp V2's model and inference gateway -- the centralized system through which every LLM call in the platform flows, whether from agent chat turns, listening evaluations, summarization jobs, or memory extraction pipelines. The system integrates directly with model providers (Anthropic as primary, plus OpenAI, Google, and any OpenAI-compatible endpoint for self-hosting) using a provider adapter pattern that translates between a canonical gateway format and each provider's native API.

The key operational entity is the **provider connection** -- a specific API key or credential linked to a provider. An org can have multiple connections per provider (e.g., two Anthropic keys, one personal, one team). Connections track health state, token usage, and rate limit status. When a connection fails (rate limited, quota exhausted, auth failed), the gateway automatically **fails over** to the next available connection in the org's failover chain, which can span providers entirely (Anthropic -> OpenAI -> Kimi). Failover events generate real-time notifications so the human knows when and why the system switched providers.

The core abstraction is the **model profile**, which bundles a provider, model identifier, context window limits, generation settings (temperature, top_p, etc.), tool call policy, streaming policy, timeout, retry count, and an optional fallback profile into a single versioned entity. Agents and flow nodes reference profiles -- never raw model IDs. Profiles are assigned via a four-level hierarchy: flow node override > agent default > project default > org default. System profiles (for summarization, listening eval, memory extraction/synthesis) are configured separately at the org level and are not overridable per-agent or per-project. Profile versioning uses a stable logical identity across versions; in-progress turns are unaffected by version changes.

The gateway manages **concurrency and queuing** with global and per-provider session limits, a four-tier priority system (sync interactive > sync system > async agent > async system), and soft preemption that allows sync requests to pause async work without cancelling it. **Token tracking** is per-invocation with multi-dimensional attribution (org, project, agent, session, task), per-connection and per-model token rollups, and a **subscription dashboard** showing all connected providers, their health status, usage stats, and failover history.

The database schema consists of 6 tables: `model_provider` (instance-level provider registration), `provider_connection` (per-org multi-key connection management with health tracking), `model_profile` (versioned profile definitions with stable logical identity), `model_profile_assignment` (hierarchy-based profile assignment), `model_invocation` (granular per-call record with full attribution, token counts, latency, prompt/response storage references, and retry/fallback chain linking), and `model_usage_rollup` (aggregated token usage data by multiple dimensions). Every invocation captures the full prompt and response to object storage (subject to redaction policy) for replay and evaluation purposes.

---

# 07. Models and Inference

## Objective

OtterCamp V2 integrates directly with model providers -- no intermediary platforms, no OpenClaw dependency. The model system owns provider abstraction, request routing, connection management, concurrency, token tracking, and inference controls. Every model call in the system -- whether from a chat turn, a listening eval, a summarization job, or a memory extraction pipeline -- flows through this gateway.

The design principles:

- **Provider-agnostic at the interface, opinionated in defaults.** The system supports multiple providers, but Anthropic is the primary provider and the default for all profiles at launch.
- **Explicit routing, not heuristic.** The human or PM assigns model profiles to agents and flow nodes. The system does not guess which model is best for a task.
- **Keep my agents running.** When a connection fails, the system fails over automatically and tells the human what happened. The goal is uninterrupted work, not blocked agents.
- **Concurrency as a first-class resource.** Model access is finite and contended. The system manages it like any other scarce resource: limits, queuing, priorities.

## Provider Abstraction

### Provider Adapter Pattern

Each supported model provider has an adapter that implements a common interface. The adapter handles the provider-specific details: authentication, request formatting, response parsing, streaming protocol, error classification, and rate limit interpretation.

The gateway never speaks a provider's native protocol directly. All interaction flows through the adapter.

**Adapter responsibilities:**

- Translate the gateway's canonical request format into the provider's API format.
- Translate the provider's response (streaming or non-streaming) into the gateway's canonical response format.
- Classify provider errors into the gateway's error taxonomy (see Error Classification).
- Report rate limit state (remaining requests, reset time) back to the gateway for connection health tracking.
- Handle authentication (API keys, OAuth tokens, service account credentials) using secrets resolved at call time from the org's secret store.

**Supported providers at V2 launch:**

- **Anthropic** -- primary provider, default for all profiles. Claude model family.
- **OpenAI** -- secondary provider. GPT model family.
- **Google** -- secondary provider. Gemini model family.
- **OpenAI-compatible** -- catch-all adapter for any provider exposing an OpenAI-compatible API (local models via Ollama/vLLM/LM Studio, Groq, Together, Fireworks, Kimi, etc.). This is the self-hosting and long-tail provider path.

Adding a new provider means implementing the adapter interface and registering it. No changes to the gateway, routing, or tracking logic.

### Provider Registration

Providers are registered at the instance level (system-wide) and connections are configured at the org level. An instance administrator defines which providers are available. An org administrator connects their subscriptions (API keys, OAuth tokens) to specific providers.

This two-level model means:

- A self-hosted instance can restrict available providers (e.g., only allow local models).
- An org can connect multiple subscriptions to the same provider (e.g., two Anthropic API keys with different rate limits).
- Credentials are always org-scoped. Two orgs on the same instance use their own API keys.

### Error Classification

The gateway classifies all errors -- both from providers and from internal gateway operations -- into a canonical taxonomy. This is what the fallback chain, retry logic, and supervisor all operate on.

| Error Class | Source | Meaning | Retryable | Examples |
|---|---|---|---|---|
| `rate_limited` | Provider | Provider rate limit hit | Yes (with backoff) | HTTP 429, provider-specific throttle responses |
| `overloaded` | Provider | Provider at capacity | Yes (with backoff) | HTTP 503, model overloaded responses |
| `timeout` | Provider | Request exceeded time limit | Yes (once) | Network timeout, provider processing timeout |
| `auth_failed` | Provider | Credentials invalid or expired | No (trigger failover) | HTTP 401, HTTP 403 |
| `invalid_request` | Provider | Request was malformed or rejected | No | Context window exceeded, content policy violation |
| `model_unavailable` | Provider | Specific model is not available | No (trigger failover) | Model deprecated, region unavailable |
| `provider_error` | Provider | Unclassified provider-side failure | Yes (once) | HTTP 500, unexpected response format |
| `network_error` | Provider/Gateway | Could not reach provider | Yes (with backoff) | DNS failure, connection refused, TLS error |
| `quota_exhausted` | Provider | Account/subscription quota used up | No (trigger failover) | Monthly token limit reached, billing limit |
| `queue_timeout` | Gateway | Request waited too long in queue | No (trigger failover) | All slots occupied beyond timeout window |

Non-retryable errors that trigger failover (`auth_failed`, `model_unavailable`, `quota_exhausted`, `queue_timeout`) cause the gateway to try the next connection in the failover chain rather than retrying the same connection.

## Provider Connections

### What a Connection Represents

A provider connection is a specific subscription or API key linked to a provider. It represents one set of credentials with its own rate limits, quotas, and billing relationship with the provider.

An org can have:

- **Multiple connections to the same provider.** Two Anthropic API keys -- a personal key and a team key. The system can fail over between them, or use them for different agents.
- **Connections to different providers.** Anthropic, OpenAI, and a local Ollama instance. The system fails over across providers when one is unavailable.
- **Multiple connections to OpenAI-compatible endpoints.** One for a local Ollama instance, another for a Groq cloud endpoint, another for Together AI.

### Connection Health

Each connection tracks its operational health:

- **Status:** `healthy`, `degraded` (experiencing intermittent errors), `rate_limited` (actively throttled, with estimated reset time), `unavailable` (auth failed or quota exhausted). Operator disable is handled by the `is_enabled` boolean, not by health status — health status only tracks operational states.
- **Last successful call:** timestamp of the most recent successful invocation through this connection.
- **Last error:** timestamp and error class of the most recent failure.
- **Rate limit state:** remaining requests/tokens and reset time, as reported by the provider's response headers (via the adapter).
- **Cumulative stats:** total invocations, total input/output tokens, error count -- tracked per connection for the subscription dashboard.

Health status is updated on every invocation result for connections that are receiving traffic.

**Active health probing.** Connections in `degraded`, `rate_limited`, or `unavailable` status receive periodic lightweight health checks (a minimal API call, e.g., a model list request) to detect recovery. Default interval: 60 seconds for `degraded`/`rate_limited`, 5 minutes for `unavailable`. When a probe succeeds, the connection transitions back to `healthy`.

### Connection Selection

When the gateway dispatches a request to a provider, it needs to select which connection to use. The selection logic:

1. **Resolve profile.** The profile specifies a provider and model.
2. **Find eligible connections.** All connections for this org + provider where `is_enabled = true` and `health_status` is not `unavailable`. Connections with `is_enabled = false` or `health_status = 'unavailable'` are excluded.
3. **Sort by health tier.** `healthy` > `degraded`. Connections with `health_status = 'rate_limited'` are skipped (they are not eligible for selection until the rate limit resets or active health probing detects recovery).
4. **Within the same health tier, sort by `failover_priority`** (lower number = tried first).
5. **If no eligible connections exist for the profile's provider, trigger cross-provider failover** (see Failover).

This is not round-robin or load-balancing. It is health-aware selection with a preference for the healthiest connection, with deterministic ordering via `failover_priority`. In the common case (one healthy connection per provider), selection is trivial.

### Subscription Dashboard

The subscription dashboard is the human's view into all connected providers. It shows:

**Per-connection:**
- Provider name and connection label (human-assigned, e.g., "Personal Anthropic", "Team OpenAI", "Local Ollama")
- Current health status (with color coding: green/yellow/red)
- Models available through this connection
- Token usage: total input tokens, total output tokens, by time period (today, this week, this month)
- Invocation count and error rate
- Last used timestamp
- Rate limit state (if known)

**Failover history:**
- Recent failover events: when, from which connection/provider, to which connection/provider, why (error class), and whether the failover succeeded
- Failover frequency trends (are failovers becoming more common?)

**Per-model stats** (aggregated across connections):
- Total input/output tokens per model
- Invocation count per model
- Which connections serve each model

The dashboard data comes from `provider_connection` health fields and `model_usage_rollup` aggregations.

## Model Profiles

A model profile is the core abstraction. It bundles a provider, a specific model, and all the settings needed to make an inference call. Agents, flow nodes, and system components reference profiles -- never raw model identifiers.

### What a Profile Contains

- **Provider and model identifier.** Which provider adapter to use and which model to request. Uses the `provider/model` naming convention (e.g., `anthropic/claude-sonnet-4-20250514`).
- **Context window configuration.** Max input tokens and max output tokens. These may be less than the model's actual limits -- the profile intentionally constrains usage.
- **Generation settings.** Temperature, top_p, top_k, presence/frequency penalties. These control the character of the output.
- **Tool call policy.** Whether the model should be allowed to use tools, and whether parallel tool calls are permitted.
- **Streaming policy.** Whether to stream responses from this profile, or wait for full completion.
- **Timeout.** Per-call wall clock timeout. If the model doesn't complete within this window, the call is aborted.
- **Fallback profile.** What to use if this profile fails with a retryable error after exhausting retries. Null means no fallback.

### Profile Versioning

Model profiles are versioned. When the underlying model changes (Anthropic releases a new Claude version), or when settings need to be tuned, a new version of the profile is created. The old version remains available for in-progress work.

Each profile has a **stable logical identity** (`logical_profile_id`) that persists across versions. Assignments and fallback chains reference this logical ID, not individual version rows.

**Versioning rules:**

- Each profile has a `version` counter starting at 1.
- When a profile is updated, a new row is created with `version` incremented and the same `logical_profile_id`. The old row's `is_current` is set to `false`.
- Agents and flow nodes reference a profile by `logical_profile_id`. They always resolve to the current version at the time of invocation.
- In-progress turns are not affected by version changes. A turn that started with version 3 continues using version 3 until the turn completes. The next turn picks up version 4 if it has been published.
- The operator can pin a specific profile version on an agent or flow node to prevent automatic pickup of new versions. This is opt-in -- the default is "use current."

### System Profiles

OtterCamp uses model calls internally for operations that are not agent turns. These get dedicated system profiles:

- **Summarization profile** -- used for progressive conversation summarization (see 02-chat.md Session Continuity). Optimized for speed, not depth. Haiku-tier model.
- **Listening eval profile** -- used for the multi-party listening evaluation pass (see 02-chat.md Turn Cycle Phase 2). Must be fast -- the human is waiting. Haiku-tier model.
- **Memory extraction profile** -- used by the memory pipeline to extract memories from conversations (see 06-memory.md). Runs async, no latency pressure. Haiku-tier model.
- **Memory synthesis profile** -- used for entity synthesis and deduplication in the memory system. Mid-tier model, async.

System profiles are configured at the org level. They have a `system_purpose` field (`summarization`, `listening_eval`, `memory_extraction`, `memory_synthesis`) for programmatic lookup. Sensible defaults ship with the system. The operator can tune them but most should never need to touch them.

### Profile Assignment

Model profiles are assigned, not auto-selected. The assignment hierarchy:

1. **Flow node level.** A flow node can specify a profile override via the `model_profile_assignment` table with `scope_type = 'flow_node'`. This is for cases where a specific step needs a specific model -- a code review node that needs a strong reasoning model, or a lightweight triage node that should use a cheap model.
2. **Agent level.** The agent profile (see 05-agents-staff-and-temps.md) specifies a default model profile. This is what the agent uses unless a flow node overrides it.
3. **Project level.** The project can specify a default profile for agents that don't have their own. This is the "all agents in this project should use Claude Sonnet unless specified otherwise" level.
4. **Org level.** The org default. The fallback when nothing else is specified.

Resolution is top-down: flow node override > agent profile > project default > org default. The first non-null value wins.

System profiles (summarization, listening eval, memory) are resolved separately from the assignment hierarchy. They are configured at the org level and are not overridable per-project or per-agent -- they are infrastructure, not creative choices.

## Routing

### Routing is Explicit

There is no heuristic auto-routing in OtterCamp. The system does not analyze a prompt and decide "this looks like a coding task, route it to a code-optimized model." That kind of magic is unpredictable, hard to debug, and undermines the operator's control.

Instead:

- The human (or PM during scoping) assigns model profiles to agents and flow nodes.
- The system resolves the profile at invocation time using the assignment hierarchy above.
- If no profile is found anywhere in the hierarchy, the request fails with a clear error. There is no hidden default -- the org must configure at least an org-level default profile.

This keeps routing transparent. When the operator asks "why did this agent use Claude Sonnet instead of Opus?", the answer is always traceable: "because the flow node specified a Sonnet profile" or "because the agent profile specifies Sonnet" -- never "because the system decided."

### Routing at Invocation Time

When the gateway receives an inference request:

1. **Resolve profile.** Walk the assignment hierarchy (flow node > agent > project > org). Get the current version of the resolved profile (via `logical_profile_id`).
2. **Select connection.** Find the best available connection for the profile's provider (see Connection Selection).
3. **Check provider availability.** Is the selected connection healthy? Is it within per-provider concurrency limits?
4. **Check global concurrency.** Is the system within its global concurrent session limit?
5. **Enqueue or dispatch.** If limits allow, dispatch immediately. If at capacity, enqueue with the appropriate priority.

The routing decision is deterministic and fully traceable. Every step is logged on the `model_invocation` record, including which connection was selected.

## Concurrency and Queuing

The system enforces limits on concurrent model sessions to manage resource usage (RAM, API rate limits). Concurrency management is the gateway's responsibility -- callers (the chat turn loop, the memory pipeline, the summarizer) do not need to manage it.

### Global Concurrency Limit

A configurable maximum number of concurrent model sessions across all providers. This is primarily for resource conservation -- especially important in self-hosted/single-node deployments where RAM is constrained by model context windows held in memory during streaming.

When the limit is reached, new requests queue and execute in order as slots free up.

**Reserved sync slot.** The model gateway always reserves at least one concurrency slot for synchronous (interactive) requests. Background/async agent work cannot consume all available slots — a live user's chat must never wait because async tasks have saturated the connection pool.

The global limit is set at the instance level by the operator. It is not configurable per-org -- it is an infrastructure constraint, not a tenant policy.

### Per-Provider Concurrency Limit

Each provider can have its own concurrent session cap, independent of the global limit. This accounts for provider-specific rate limits, API quotas, and exposure. A provider hitting its limit does not block requests to other providers (as long as the global limit is not also reached).

Per-provider limits are set at the instance level (matching the provider's API rate limits) but can be further constrained at the org level (if an org wants to limit its own usage of a provider).

### Priority System

Queued requests execute in priority order:

1. **Sync interactive** (highest) -- a human is typing in a chat session and waiting for a response. Latency directly impacts the user experience.
2. **Sync system** -- system calls supporting sync sessions: listening eval, summarization triggered during a sync turn.
3. **Async agent** -- autonomous agent work on tasks. Latency acceptable.
4. **Async system** (lowest) -- background system operations: memory extraction, sleep-time reflection, scheduled maintenance.

Within the same priority tier, requests are processed FIFO (earliest enqueued first).

### Preemption

Sync sessions can preempt async work when the system is at full capacity. If a sync request arrives and all slots are occupied by async work:

- The most recently started async session is **soft-preempted**: it is allowed to finish its current model call but is not dispatched for the next iteration of the tool loop. It goes back to the queue with its original priority.
- The sync request gets the freed slot.

Preemption is not cancellation. The async work resumes when a slot becomes available. The async session's turn loop is paused, not killed -- it picks up where it left off.

**Slot holding:** A concurrency slot is held for the duration of a model call, not the entire turn. Between model calls (during tool execution), the slot is released. The session re-enters the queue for its next model call. This means preemption is naturally efficient -- slots are only held when the model is actively being called.

### Queue Observability

Queue state is observable via the operational dashboard (see 13-security-observability-costs.md):

- Current queue depth by priority tier.
- Average and p95 wait time by priority tier.
- Current concurrent sessions by provider.
- Global concurrent sessions vs limit.
- Preemption events (count, affected sessions).

### Queue Timeout

Queued requests have a configurable timeout. If a request waits in the queue longer than the timeout, it fails with error class `queue_timeout`. The caller (turn loop, memory pipeline, etc.) handles this according to its own retry/escalation logic. A `queue_timeout` can also trigger cross-provider failover if an alternate connection is available.

Default queue timeouts:

- Sync interactive: 30 seconds. If a human is waiting more than 30 seconds for a queue slot, something is seriously wrong.
- Sync system: 15 seconds. Listening evals and in-session summarization should be fast.
- Async agent: 5 minutes. Agent work can tolerate a wait.
- Async system: 30 minutes. Background work is patient.

## Inference Controls

### Token Limits

Every model invocation has explicit token limits, derived from the resolved model profile:

- **Max input tokens.** The maximum number of tokens the prompt can contain. The prompt assembly pipeline (see 05-agents-staff-and-temps.md) respects this limit during context assembly -- it is the budget the pipeline fills.
- **Max output tokens.** The maximum number of tokens the model can generate in a single response. Prevents runaway generation.

These are set on the model profile, not on the model itself. A profile may set max output tokens to 4096 even if the underlying model supports 8192 -- this is intentional constraint.

### Per-Call Timeout

Each model call has a wall clock timeout, set on the model profile. If the model does not produce a complete response (or a stream-termination signal) within this window, the call is aborted.

The per-call timeout is distinct from the per-turn timeout (see 02-chat.md Stop Conditions). The per-call timeout governs a single model invocation. The per-turn timeout governs the entire tool loop. A turn may contain many model calls, each with their own timeout.

Default per-call timeouts:

- Sync profiles: 60 seconds. A model call taking longer than a minute in a sync session is unacceptable.
- Async profiles: 120 seconds. More headroom for complex reasoning.
- System profiles: 45 seconds. Summarization and eval calls should be fast.

### Streaming

Streaming is the default behavior for sync sessions. Non-streaming (wait for full completion) is the default for async sessions. This is set on the model profile via the streaming policy.

**Why streaming for sync, non-streaming for async:**

- In sync sessions, the human is watching. Streaming provides immediate feedback and reduces perceived latency. The turn loop processes tool calls as they arrive in the stream.
- In async sessions, no one is watching. Streaming adds protocol complexity (SSE/WebSocket handling, partial response buffering, reconnection logic) for no benefit. A single response completion is simpler and more reliable.

The streaming policy on a profile can be overridden per-call by the gateway caller. This is used for cases like async turns where the human joins mid-turn (session mode switches to sync) -- the next model call in the turn loop can request streaming even if the profile defaults to non-streaming. Note: the current in-progress non-streaming call completes without streaming; the switch takes effect on the next model call. The UI should indicate that the agent is working but output will appear when the current call completes.

### Deterministic Mode

For testing and replay scenarios, a model profile can enable deterministic mode:

- Temperature set to 0.
- Seed parameter set (if the provider supports it).
- Response caching enabled: identical prompts return cached responses.

Deterministic mode is enabled via a `deterministic` boolean on the profile. When true, temperature is forced to 0 and the seed is set from the profile's `deterministic_seed` field. Deterministic mode is never used in production. It exists for the evaluation and regression testing pipeline (see Evaluation and Debugging).

## Token Tracking

### Per-Invocation Tracking

Every model call produces a `model_invocation` record with full token and performance metadata:

- **Token counts.** Input tokens, output tokens, cache read tokens, cache write tokens (for providers that support prompt caching).
- **Latency.** Time to first token (for streaming) and total duration.
- **Provider metadata.** Request ID from the provider, model version string, any provider-specific response headers.
- **Connection used.** Which `provider_connection` served this request.

Token counts come from the provider's response (extracted by the adapter). When actual counts are unavailable, the adapter estimates based on content length.

### Token Attribution

Every invocation is attributed to multiple dimensions for aggregation:

- **Organization** -- always present.
- **Project** -- present for project-scoped work. Null for org-level activity (chatting with Frank, Lori).
- **Agent** -- present when an agent triggered the invocation. Also set for system profile invocations that are causally linked to an agent's work (e.g., summarization of an agent's conversation, listening eval during an agent's turn cycle). Null only for truly agent-independent system work.
- **Session** -- the chat session context, if any.
- **Task** -- present for task-scoped work.
- **Connection** -- which provider connection was used.

This multi-dimensional attribution allows the operator to answer: "How many tokens did Project X use this month?", "Which agent uses the most tokens?", "What's the token split between chat and autonomous work?", "How much traffic goes through each connection?"

### Token Rollups

The system maintains pre-computed token rollups for fast dashboard queries:

- **Per-connection daily.** Total tokens (input + output) per connection per day. This is the primary data for the subscription dashboard.
- **Per-model daily.** Total tokens per model (across connections) per day. Answers "how much Sonnet vs Opus are we using?"
- **Per-agent daily.** Total tokens per agent per day. Answers "which agent is the heaviest user?"
- **Per-project daily.** Total tokens per project per day.

Rollups are computed incrementally: each completed invocation updates the relevant rollup rows. Historical rollups are immutable.

## Failover

### Connection-Level Failover

When a model invocation fails with a non-retryable error that indicates the connection itself is the problem (`auth_failed`, `quota_exhausted`, `rate_limited` after retries exhausted), the gateway attempts to use a different connection:

1. **Same provider, different connection.** If the org has multiple connections to the same provider, try the next healthiest one.
2. **Different provider, via fallback profile.** If no same-provider connections are available (or all have failed), and the current profile has a `fallback_profile_id`, switch to the fallback profile. The fallback may use a completely different provider (e.g., primary is Anthropic, fallback is OpenAI).

This two-level failover (connection within provider, then profile across providers) means the system tries the least disruptive path first (same model, different key) before escalating to a different model entirely.

### Failover Notifications

Every failover event generates a notification to the human:

- **What happened:** "Your Anthropic connection 'Personal Key' hit its rate limit."
- **What the system did:** "Switched to connection 'Team Key' for the current request." or "Switched to fallback profile 'gpt4o-fallback' using OpenAI."
- **Current state:** "Connection 'Personal Key' is rate-limited. Estimated reset: 2 minutes." or "Connection 'Personal Key' has exhausted its quota."

Notifications are delivered through the standard notification system (inbox items, real-time events). They are informational, not blocking -- the system has already handled the failover. The human can choose to take action (add a new connection, increase a quota, disable a failing connection) or do nothing.

If failovers are happening frequently (more than N times in a time window -- configurable), the system escalates to a more prominent alert: "Your Anthropic connections are experiencing frequent failures. Consider adding an additional connection or configuring a fallback provider."

### Retry Logic

For retryable errors (see Error Classification), the gateway retries with exponential backoff before triggering failover:

- **Max retries per call**: 2 (configurable on the profile).
- **Base backoff**: 1 second.
- **Backoff multiplier**: 2x (1s, 2s).
- **Jitter**: random 0-500ms added to each backoff interval to prevent thundering herd.

Rate limit errors use the provider's `Retry-After` header (or adapter-reported reset time) instead of the default backoff schedule.

### Profile-Level Fallback Chains

If retries are exhausted and connection failover doesn't resolve the issue, and the profile has a fallback profile configured:

1. Log the failure on the original invocation record.
2. Create a new invocation using the fallback profile.
3. The fallback profile may use a different provider entirely (e.g., primary is Anthropic Claude Opus, fallback is OpenAI GPT-4o).
4. Fallback chains are walked sequentially: A fails -> try B -> B fails -> try C -> C fails -> give up.
5. Maximum chain depth: 3 profiles. Prevents circular fallback configurations.

**Fallback constraints:**

- The fallback profile's context window must be >= the prompt size. If the fallback model has a smaller context window, the gateway attempts to truncate non-critical context (conversation history) to fit. If the prompt cannot be made to fit (identity + policies alone exceed the limit), the fallback is skipped.
- The fallback profile's tool call policy must be compatible. If the original profile allowed tool calls but the fallback does not, the fallback is skipped for turns that require tool use.

### What the Caller Sees

The turn loop (or other caller) does not manage retries, connection failover, or profile fallbacks. It makes one request to the gateway and receives either a successful response or a terminal error. The gateway handles all recovery logic internally. The caller sees the final result, and the `model_invocation` records capture the full chain of attempts.

If all retries, connection failovers, and profile fallbacks fail, the gateway returns a terminal error to the caller with:

- The original error class.
- The number of retry attempts.
- Whether connection failover was attempted and why it failed.
- Whether a profile fallback was attempted and why it failed.
- The full chain of invocation IDs for debugging.

The turn loop then handles the failure according to its own logic (see 02-chat.md Crash Recovery and 16-agent-control-plane.md Failure State Repair).

## Evaluation and Debugging

### Prompt Capture

Every model invocation captures the full prompt and response for offline evaluation and debugging. This data lives on the `model_invocation` record:

- **Prompt hash.** SHA-256 of the complete prompt. Allows deduplication and quick comparison.
- **Prompt reference.** Pointer to the full prompt content in object storage. Prompts can be large (100k+ tokens) and are not stored inline in the database.
- **Response reference.** Pointer to the full response content in object storage.
- **Prompt metadata.** Structured context assembly audit trail stored in `model_invocation.metadata` under the `context_assembly` key. Records:
  - **Per-layer token counts**: tokens consumed by each of the 7 prompt layers (identity, policies, scope_context, skills, memory, conversation, tools). Enables optimization of the assembly pipeline — is memory consistently starved? Is conversation history over-allocated?
  - **Memory injection manifest**: memory IDs injected via passive retrieval with their relevance scores, and IDs excluded due to budget or injection cooldown. Enables retrieval quality assessment.
  - **Compression events**: whether progressive summarization fired, which message ranges were summarized, tokens reclaimed.
  - **Truncation events**: which layers were truncated to fit the budget and by how much.
  This metadata answers "why did this agent not know about X?" and powers the inference context replay capability described in 13-security-observability-costs.md.

### Redaction

Prompt and response captures are subject to the org's redaction policy. Redaction rules can:

- Strip specific patterns (API keys, passwords, PII patterns) before storage.
- Replace entity names with tokens for anonymization.
- Omit specific prompt layers (e.g., never capture the memory injection layer).

Redaction is applied at capture time. The stored content is already redacted. There is no "un-redacted" copy -- this is intentional for compliance.

Default redaction: strip anything that looks like an API key or credential (regex pattern matching). Additional rules configured per-org.

### Replay

Captured prompts can be replayed against a different model or profile version for evaluation:

- The operator selects a set of invocations (by time range, agent, session, or task).
- The system re-submits the captured prompts to the specified target profile.
- Responses are captured and stored alongside the original for comparison.
- Replay invocations are tagged as `replay` and are tracked separately in token rollups.

This is the primary mechanism for evaluating model upgrades. Before switching an agent from Claude Sonnet to Claude Opus, the operator can replay recent invocations and compare quality.

### Regression Suite

The operator can curate a set of invocations into a named regression suite. When a model profile is updated (new version), the regression suite is automatically replayed against the new version, and results are surfaced for review.

Regression suites are a future enhancement -- not required for V2 launch. The infrastructure (prompt capture, replay) ships in V2. The regression suite UX is deferred.

## Database Schema

### model_provider

```sql
create table model_provider (
  id              uuid primary key default gen_random_uuid(),
  slug            text not null unique,            -- 'anthropic', 'openai', 'google', 'openai-compat'
  name            text not null,                   -- 'Anthropic', 'OpenAI', 'Google', 'OpenAI-Compatible'
  adapter_type    text not null,                   -- which adapter implementation to use
  is_enabled      boolean not null default true,   -- instance-level enable/disable
  base_url        text,                            -- default API endpoint (overridable per connection)
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),
  metadata        jsonb not null default '{}'
);
```

- Registered at the instance level by the operator.
- `adapter_type` maps to the Go adapter implementation. For the four launch providers: `anthropic`, `openai`, `google`, `openai_compat`.
- `base_url` is the default endpoint. For the OpenAI-compatible adapter, this is where the local model server lives. For cloud providers, it's the standard API endpoint.
- `is_enabled` controls instance-wide availability. Disabled providers cannot be used by any org.

### provider_connection

```sql
create table provider_connection (
  id                    uuid primary key default gen_random_uuid(),
  organization_id       uuid not null references organization(id),
  provider_id           uuid not null references model_provider(id),
  label                 text not null,               -- human-assigned name: 'Personal Anthropic', 'Team OpenAI'
  is_enabled            boolean not null default true,
  api_key_secret_ref    text,                        -- secret.slug reference; null for keyless endpoints (e.g., local Ollama). See doc 08 secret reference convention.
  base_url_override     text,                        -- connection-specific endpoint override
  max_concurrent        int,                         -- per-connection concurrency cap (null = use provider default)

  -- health tracking
  health_status         text not null default 'healthy' check (health_status in ('healthy', 'degraded', 'rate_limited', 'unavailable')),
  last_success_at       timestamptz,
  last_error_at         timestamptz,
  last_error_class      text,                        -- from error taxonomy
  rate_limit_remaining  int,                         -- requests remaining (from provider headers)
  rate_limit_reset_at   timestamptz,                 -- when rate limit resets (from provider headers)

  -- failover
  failover_priority     int not null default 0,      -- lower = preferred. connections tried in priority order within a provider.

  -- cumulative stats (updated on each invocation)
  total_invocations     bigint not null default 0,
  total_input_tokens    bigint not null default 0,
  total_output_tokens   bigint not null default 0,
  total_errors          bigint not null default 0,

  created_at            timestamptz not null default now(),
  updated_at            timestamptz not null default now(),
  metadata              jsonb not null default '{}'
);

create index on provider_connection (organization_id);
create index on provider_connection (organization_id, provider_id, is_enabled, health_status);
create index on provider_connection (organization_id, provider_id, failover_priority);
```

- Per-org connection to a provider. Multiple connections per provider are allowed and expected.
- `label` is the human-readable name shown in the subscription dashboard.
- `api_key_secret_ref` is a reference into the encrypted secret store (see 13-security-observability-costs.md). API keys are never stored in plaintext.
- `base_url_override` lets a connection point to a different endpoint (e.g., a regional endpoint, a proxy, a different local model server).
- `health_status` is updated by the gateway after each invocation. The gateway transitions: `healthy` -> `degraded` (on intermittent errors) -> `rate_limited` (on 429) -> `unavailable` (on persistent auth/quota failures). Recovery: when a previously failing connection succeeds (via traffic or active health probing), it transitions back to `healthy`. Operator disable is handled by `is_enabled`, not `health_status` — the two are independent.
- `failover_priority` determines the order connections are tried within a provider. The operator sets this. Lower numbers are tried first.
- Cumulative stats are denormalized counters updated atomically on each invocation. They power the subscription dashboard without requiring joins to the invocation table.

### model_profile

```sql
create table model_profile (
  id                    uuid primary key default gen_random_uuid(),
  logical_profile_id    uuid not null,                -- stable identity across versions
  organization_id       uuid references organization(id),  -- null for system-provided default profiles
  slug                  text not null,
  name                  text not null,
  description           text,
  provider_id           uuid not null references model_provider(id),
  model_id              text not null,                     -- provider-specific model identifier (e.g., 'claude-sonnet-4-20250514')
  version               int not null default 1,
  is_current            boolean not null default true,
  profile_type          text not null default 'agent' check (profile_type in ('agent', 'system')),
  system_purpose        text check (system_purpose in ('summarization', 'listening_eval', 'memory_extraction', 'memory_synthesis')),

  -- context window
  max_input_tokens      int not null,
  max_output_tokens     int not null,

  -- generation settings
  temperature           numeric(4,2) not null default 0.7,
  top_p                 numeric(4,2),
  top_k                 int,
  presence_penalty      real,                            -- provider-dependent; null = provider default
  frequency_penalty     real,                            -- provider-dependent; null = provider default

  -- tool call policy
  tool_use_enabled      boolean not null default true,
  parallel_tool_calls   boolean not null default true,

  -- streaming
  stream_by_default     boolean not null default true,

  -- timeouts
  per_call_timeout_ms   int not null default 60000,

  -- deterministic mode (testing only)
  deterministic         boolean not null default false,
  deterministic_seed    int,

  -- fallback
  fallback_profile_id   uuid,                              -- references model_profile.logical_profile_id (not id)

  -- retry
  max_retries           int not null default 2,

  created_by_type       text not null check (created_by_type in ('human', 'agent', 'system')),
  created_by_id         uuid not null,                     -- sentinel UUID for system-provided profiles
  created_at            timestamptz not null default now(),
  updated_at            timestamptz not null default now(),
  metadata              jsonb not null default '{}'
);

create unique index on model_profile (organization_id, slug, version) where (organization_id is not null);
create unique index on model_profile (slug, version) where (organization_id is null);
create index on model_profile (logical_profile_id, is_current);
create index on model_profile (organization_id, profile_type, is_current);
create index on model_profile (organization_id, system_purpose) where system_purpose is not null;
create index on model_profile (provider_id);
```

- `logical_profile_id` is a stable UUID shared across all versions of the same profile. Generated once when the profile is first created, carried forward on every version. This is what assignments, fallback chains, and external references use. **Note:** `logical_profile_id` is intentionally non-unique (multiple version rows share it). References from other tables (`agent.default_model_profile_id`, `model_profile_assignment.profile_id`, `fallback_profile_id`) are application-layer logical references resolved via `WHERE logical_profile_id = $1 AND is_current = true`, not SQL FOREIGN KEY constraints.
- `organization_id` is nullable. System-provided default profiles (shipped with OtterCamp) have null org ID and are available to all orgs as starting points.
- `slug` is the human-readable identifier: `claude-opus-primary`, `claude-sonnet-fast`, `gpt4o-fallback`, `haiku-eval`.
- `version` + `is_current` implement the versioning model. Only one version per `logical_profile_id` is current.
- `profile_type` distinguishes agent-facing profiles from system profiles. System profiles are not assignable to agents.
- `system_purpose` allows programmatic lookup of system profiles by role, not by slug convention.
- `temperature` is `numeric(4,2)` to accommodate values up to 2.0 (supported by some providers).
- `fallback_profile_id` references another profile's `logical_profile_id`, not a specific version row. The application layer enforces max chain depth of 3 and rejects circular references.

### model_profile_assignment

```sql
create table model_profile_assignment (
  id                uuid primary key default gen_random_uuid(),
  organization_id   uuid not null references organization(id),
  scope_type        text not null check (scope_type in ('org', 'project', 'agent', 'flow_node')),
  scope_id          uuid not null,                  -- org ID, project ID, agent ID, or flow_node ID
  profile_id        uuid not null,                  -- references model_profile.logical_profile_id
  pinned_version    int,                            -- null = use current, set = use specific version
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now(),

  unique (scope_type, scope_id)
);

create index on model_profile_assignment (organization_id);
create index on model_profile_assignment (profile_id);
```

- Implements the assignment hierarchy. The gateway resolves a profile by walking: flow_node > agent > project > org.
- `profile_id` references `logical_profile_id` on `model_profile`, ensuring stability across profile versions.
- `pinned_version` allows the operator to lock a specific profile version. When null, the system resolves to the current version of the referenced logical profile.
- One assignment per scope. An agent has one default model profile, not multiple. (The agent can be overridden at the flow node level for specific steps.)
- Flow node model overrides are stored in this table (with `scope_type = 'flow_node'`), not in `flow_node.metadata`. This is the single source of truth for all profile assignments.

### model_invocation

```sql
create table model_invocation (
  id                  uuid primary key default gen_random_uuid(),
  organization_id     uuid not null references organization(id),
  profile_id          uuid not null,                    -- logical_profile_id used
  profile_version     int not null,
  provider_id         uuid not null references model_provider(id),
  model_id            text not null,                    -- actual model used (resolved from profile)
  connection_id       uuid references provider_connection(id), -- null when no eligible connection was found

  -- attribution
  session_id          uuid references chat_session(id), -- chat session, if applicable
  turn_id             uuid references chat_turn(id),    -- chat turn, if applicable
  agent_id            uuid references agent(id),        -- agent that triggered this, if applicable (including for causal system calls)
  project_id          uuid references project(id),      -- project context, if applicable
  task_id             uuid references project_task(id), -- task context, if applicable

  -- control plane context (doc 16)
  run_id              uuid references run(id),          -- control plane Run, if this call happened during a Run
  run_step_id         uuid references run_step(id),     -- specific RunStep within the Run
  run_attempt_id      uuid references run_attempt(id),  -- specific RunAttempt within the RunStep
  invocation_purpose  text not null check (invocation_purpose in ('agent_turn', 'listening_eval', 'summarization', 'skill_summarization', 'memory_extraction', 'memory_synthesis', 'memory_dedup', 'memory_reflection', 'memory_classification', 'memory_reranking', 'replay')),

  -- request
  request_priority    text not null check (request_priority in ('sync_interactive', 'sync_system', 'async_agent', 'async_system')),
  queue_wait_ms       int,                              -- time spent in queue before dispatch
  was_preempted       boolean not null default false,   -- was this preempted during execution
  stream_mode         boolean not null,

  -- token counts
  input_tokens        int,
  output_tokens       int,
  cache_read_tokens   int,
  cache_write_tokens  int,

  -- latency
  time_to_first_token_ms  int,                          -- null for non-streaming
  total_duration_ms       int,

  -- prompt capture
  prompt_hash         text,                             -- SHA-256 of the full prompt
  prompt_storage_ref  text,                             -- object storage reference for full prompt
  response_storage_ref text,                            -- object storage reference for full response

  -- outcome
  status              text not null check (status in ('success', 'error', 'timeout', 'cancelled')),
  error_class         text,                             -- from error taxonomy, null on success
  error_message       text,

  -- retry/fallback chain
  retry_of_invocation_id   uuid references model_invocation(id),  -- points to the invocation being retried
  fallback_of_invocation_id uuid references model_invocation(id), -- points to the invocation that failed and triggered this fallback
  attempt_number       int not null default 1,

  -- provider response metadata
  provider_request_id  text,                            -- the provider's own request ID
  provider_model_version text,                          -- exact model version string from provider

  created_at          timestamptz not null default now(),
  completed_at        timestamptz,

  metadata            jsonb not null default '{}'
);

create index on model_invocation (organization_id, created_at);
create index on model_invocation (connection_id, created_at);
create index on model_invocation (session_id) where session_id is not null;
create index on model_invocation (turn_id) where turn_id is not null;
create index on model_invocation (agent_id) where agent_id is not null;
create index on model_invocation (project_id) where project_id is not null;
create index on model_invocation (task_id) where task_id is not null;
create index on model_invocation (run_id) where run_id is not null;
create index on model_invocation (run_step_id) where run_step_id is not null;
create index on model_invocation (profile_id, created_at);
create index on model_invocation (provider_id, created_at);
create index on model_invocation (retry_of_invocation_id) where retry_of_invocation_id is not null;
create index on model_invocation (fallback_of_invocation_id) where fallback_of_invocation_id is not null;
```

- One row per model call. This is the most granular tracking record in the system.
- `connection_id` records which specific connection served this request. Essential for the subscription dashboard and failover analysis.
- `invocation_purpose` includes expanded values for memory pipeline operations (`memory_dedup`, `memory_reflection`, `memory_classification`, `memory_reranking`) beyond the basic extraction/synthesis. `memory_classification` and `memory_reranking` use the `memory_synthesis` system profile, same as `memory_dedup` and `memory_reflection`. `skill_summarization` uses the `summarization` system profile (for LLM-powered skill condensation when the layer 4 token budget is exceeded; see doc 10). `agent_turn` and `replay` use agent profiles, not system profiles.
- `retry_of_invocation_id` and `fallback_of_invocation_id` are separate FK columns for unambiguous chain reconstruction. A retry points to the invocation it retried. A fallback points to the invocation that failed and triggered the fallback. No ambiguity about the chain structure.
- `prompt_storage_ref` and `response_storage_ref` point to object storage (S3-compatible). Full prompts and responses are stored off-table to keep the invocation table lean and queryable.
- `agent_id` is set for system profile invocations that are causally linked to an agent (e.g., summarization triggered by an agent's conversation), not just for direct agent turns. This enables accurate per-agent token attribution.

### model_usage_rollup

```sql
create table model_usage_rollup (
  id                uuid primary key default gen_random_uuid(),
  organization_id   uuid not null references organization(id),
  rollup_type       text not null check (rollup_type in ('connection', 'model', 'agent', 'project')),
  rollup_id         uuid not null,                   -- connection ID, provider_id (for model), agent ID, or project ID
  model_id          text,                            -- model identifier, for model-level rollups
  period_date       date not null,                   -- single day
  total_invocations int not null default 0,
  total_input_tokens bigint not null default 0,
  total_output_tokens bigint not null default 0,
  total_cache_read_tokens bigint not null default 0,
  total_cache_write_tokens bigint not null default 0,
  total_errors      int not null default 0,

  unique (organization_id, rollup_type, rollup_id, model_id, period_date)
);

create index on model_usage_rollup (organization_id, rollup_type, period_date);
create index on model_usage_rollup (rollup_type, rollup_id, period_date);
```

- Pre-computed daily rollups for fast dashboard queries.
- Updated incrementally as invocations complete. Each completed invocation atomically increments the relevant rollup rows.
- `rollup_type` + `rollup_id` is polymorphic: the same table handles connection, model, agent, and project aggregations. All rollups are org-scoped (no cross-org rollups in this table -- instance-level stats are derived by summing across orgs).
- `model_id` is included for model-level rollups to distinguish between different models within the same provider.
- Tracks errors alongside tokens for operational dashboards.
- Historical rows are immutable once the period has passed.

## Cross-Entity Relationships

- `model_invocation.session_id` references `chat_session.id` (see 02-chat.md).
- `model_invocation.turn_id` references `chat_turn.id` (see 02-chat.md). Aggregate token counts on `chat_turn` are rollups from `model_invocation` rows for that turn.
- `model_invocation.agent_id` references `agent.id` (see 05-agents-staff-and-temps.md). The agent's model policy includes allowed profiles.
- `model_invocation.task_id` references `project_task.id` (see 03-projects-and-task-flow.md).
- `model_profile_assignment` with `scope_type = 'flow_node'` references `flow_node.id` (see 03-projects-and-task-flow.md). This is the single source of truth for flow node model overrides (not `flow_node.metadata`).
- `provider_connection` health state feeds the subscription dashboard and failover logic.
- Prompt/response storage references point to the same object store used by chat artifacts (see 02-chat.md Storage).
- Failover notifications are delivered through the standard notification system (see 12-api-events-and-realtime.md).

## Integration with Other V2 Specs

- **01-architecture-and-domain.md**: model gateway is a top-level runtime component. `ModelProfile` is a canonical domain entity. `ProviderConnection` is a new canonical entity.
- **02-chat.md**: the turn loop calls the gateway for every model invocation. Token counts on `chat_turn` are derived from `model_invocation`. Streaming mode determines how the turn loop processes responses.
- **03-projects-and-task-flow.md**: flow nodes can override agent model profiles via `model_profile_assignment`. Task scheduling respects per-provider concurrency limits. Token usage per task is aggregated from `model_invocation` records where `task_id` matches.
- **05-agents-staff-and-temps.md**: agent profiles specify a default model profile and model policy (allowed profiles). The prompt assembly pipeline operates within the resolved profile's `max_input_tokens` budget. Flow node model overrides are stored in `model_profile_assignment`, not `flow_node.metadata`.
- **06-memory.md**: memory extraction and synthesis pipelines use dedicated system profiles. Memory invocations are attributed to the org, project, and the agent whose work triggered them.
- **13-security-observability-costs.md**: token rollups feed the operational dashboard. The subscription dashboard is a first-class view. Redaction policies apply to prompt capture.
- **16-agent-control-plane.md**: `ModelInvocation` is a canonical execution entity. The control plane's `Run` and `RunStep` records reference model invocations for token attribution. Failover events are surfaced to the supervisor.

## Resolved Decisions

- **Anthropic is the primary provider for V2 launch.** Default profiles ship with Anthropic models. Other providers are supported but secondary. The system is provider-agnostic at the interface level.
- **Explicit routing, not heuristic auto-routing.** The human or PM assigns model profiles to agents and flow nodes. The system does not analyze prompts to select models. Routing is always traceable.
- **Model profiles are the core abstraction.** Agents and flow nodes reference profiles, never raw model IDs. A profile bundles provider + model + settings + fallback chain. Uses `provider/model` naming convention.
- **Model profiles use stable logical identity across versions.** `logical_profile_id` is shared across all versions. Assignments and fallback chains reference logical IDs, not per-version row IDs. This eliminates broken FK references on version changes.
- **Four-level assignment hierarchy.** Flow node > agent > project > org. First non-null wins. No magic fallbacks or heuristic selection.
- **Flow node model overrides use `model_profile_assignment`.** Stored in the assignment table with `scope_type = 'flow_node'`, not in `flow_node.metadata`. Single source of truth.
- **System profiles are separate from agent profiles.** Summarization, listening eval, and memory extraction have their own profiles with a `system_purpose` field for programmatic lookup. Configured at the org level. Not overridable per-project or per-agent.
- **Streaming for sync, non-streaming for async.** Streaming reduces perceived latency when a human is watching. Non-streaming is simpler and more reliable for autonomous work. Overridable per-call for edge cases. When async-to-sync switch happens mid-turn, the current non-streaming call completes normally; streaming takes effect on the next model call.
- **Concurrency slots are held per-model-call, not per-turn.** Slots are released during tool execution between model calls. Sessions re-enter the queue for their next model call. This makes preemption naturally efficient.
- **Concurrency is managed at two levels: global and per-provider.** Global limit is an instance-level infrastructure constraint. Per-provider limits account for API rate limits. Org-level per-provider overrides allow further restriction.
- **Four priority tiers for queue ordering.** Sync interactive > sync system > async agent > async system. FIFO within tiers.
- **Sync sessions can soft-preempt async work.** The async session's current model call completes but the next iteration is paused. Not cancellation -- work resumes when a slot opens.
- **No dollar-based cost tracking at V2 launch.** The system tracks tokens per model per connection, not USD. Token rollups power the subscription dashboard. Dollar-based cost estimation can be added later as a layer on top of token tracking.
- **Multiple connections per provider.** An org can connect multiple API keys to the same provider. Connections have health tracking, failover priority, and cumulative stats.
- **Automatic failover with notifications.** When a connection fails, the gateway tries the next connection (same provider first, then cross-provider via fallback profiles). Every failover event generates a notification to the human.
- **Subscription dashboard is a first-class view.** Shows all connected providers, their health, token usage, and failover history. This is how the human understands their model infrastructure.
- **Error taxonomy includes gateway-internal errors.** `queue_timeout` and `quota_exhausted` are in the canonical taxonomy alongside provider errors. Non-retryable failover-triggering errors are explicitly classified.
- **System profile invocations are attributed to the triggering agent.** Summarization and listening eval calls set `agent_id` to the agent whose work caused them, enabling accurate per-agent token reporting.
- **Fallback chains are walked sequentially.** Maximum depth of 3 profiles. Circular references rejected at configuration time. Fallback is skipped if context window or tool policy is incompatible.
- **Retry with exponential backoff.** Max 2 retries per call. Jitter to prevent thundering herd. Rate-limited errors use provider's Retry-After.
- **Retry and fallback chains use separate FK columns.** `retry_of_invocation_id` and `fallback_of_invocation_id` on `model_invocation` for unambiguous chain reconstruction.
- **Prompt and response capture for all invocations.** Stored in object storage with references from `model_invocation`. Subject to org redaction policy. Enables replay and evaluation.
- **Token rollups are pre-computed daily aggregations.** Incrementally updated. Historical rollups immutable. Four dimensions: connection, model, agent, project.
- **Queue timeout varies by priority tier.** Sync interactive: 30s. Sync system: 15s. Async agent: 5min. Async system: 30min.
- **Provider adapters classify all errors into a canonical taxonomy.** The gateway, fallback chain, and retry logic operate on canonical error classes, never raw provider errors.
- **Provider credentials are org-scoped.** Two orgs on the same instance use their own API keys. Keys stored as encrypted secret references, resolved at call time.
- **Deterministic mode is stored on the profile.** `deterministic` boolean and `deterministic_seed` fields. Never used in production. Exists for testing and replay.
- **Regression suites are deferred to post-V2.** The infrastructure (prompt capture, replay) ships in V2. The regression suite UX is a future enhancement.
- **Per-layer token budget attribution in prompt metadata.** Every `model_invocation` records structured context assembly metadata: per-layer token counts (7 layers), memory injection manifest (IDs + scores + exclusions), compression events, truncation events. Stored in `metadata.context_assembly`. Powers inference context replay (doc 13) and prompt assembly optimization. Inspired by context engineering cost-per-layer tracking patterns.

## Open Questions

_None currently outstanding._
