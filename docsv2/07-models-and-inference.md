---
## Summary

This spec defines OtterCamp V2's model and inference gateway -- the centralized system through which every LLM call in the platform flows, whether from agent chat turns, listening evaluations, summarization jobs, or memory extraction pipelines. The system integrates directly with model providers (Anthropic as primary, plus OpenAI, Google, and any OpenAI-compatible endpoint for self-hosting) using a provider adapter pattern that translates between a canonical gateway format and each provider's native API. Providers are registered at the instance level and activated per-org with org-scoped credentials. All provider errors are classified into a canonical taxonomy (rate_limited, overloaded, timeout, auth_failed, etc.) that the retry and fallback logic operates on.

The core abstraction is the **model profile**, which bundles a provider, model identifier, context window limits, generation settings (temperature, top_p, etc.), tool call policy, streaming policy, timeout, cost limit, retry count, and an optional fallback profile into a single versioned entity. Agents and flow nodes reference profiles -- never raw model IDs. Profiles are assigned via a four-level hierarchy: flow node override > agent default > project default > org default. System profiles (for summarization, listening eval, memory extraction/synthesis) are configured separately at the org level and are not overridable per-agent or per-project. Profile versioning creates new rows with incremented version counters; in-progress turns are unaffected by version changes.

The gateway manages **concurrency and queuing** with global and per-provider session limits, a four-tier priority system (sync interactive > sync system > async agent > async system), and soft preemption that allows sync requests to pause async work without cancelling it. **Cost tracking** is per-invocation with multi-dimensional attribution (org, project, agent, session, task), pre-computed daily rollups, and enforced budgets at org and project levels with soft (warning) and hard (block) thresholds. Hard budget hits pause queued work and create inbox items for the human. **Fallback chains** walk sequentially through up to 3 alternate profiles on failure, with retry logic using exponential backoff and jitter.

The database schema consists of 6 tables: `model_provider` (instance-level provider registration), `model_provider_config` (per-org provider activation and credentials), `model_profile` (versioned profile definitions with pricing), `model_profile_assignment` (hierarchy-based profile assignment), `model_invocation` (granular per-call record with full attribution, token counts, cost, latency, prompt/response storage references, and retry/fallback chain linking), and `model_cost_rollup` / `model_cost_budget` (aggregated cost data and budget enforcement). Every invocation captures the full prompt and response to object storage (subject to redaction policy) for replay and evaluation purposes.

---

# 07. Models and Inference

## Objective

OtterCamp V2 integrates directly with model providers -- no intermediary platforms, no OpenClaw dependency. The model system owns provider abstraction, request routing, concurrency management, cost tracking, and inference controls. Every model call in the system -- whether from a chat turn, a listening eval, a summarization job, or a memory extraction pipeline -- flows through this gateway.

The design principles:

- **Provider-agnostic at the interface, opinionated in defaults.** The system supports multiple providers, but Anthropic is the primary provider and the default for all profiles at launch.
- **Explicit routing, not heuristic.** The human or PM assigns model profiles to agents and flow nodes. The system does not guess which model is best for a task.
- **Predictable cost behavior.** Every request is tracked, every cost is attributed, budgets are enforced before work starts.
- **Concurrency as a first-class resource.** Model access is finite and contended. The system manages it like any other scarce resource: limits, queuing, priorities.

## Provider Abstraction

### Provider Adapter Pattern

Each supported model provider has an adapter that implements a common interface. The adapter handles the provider-specific details: authentication, request formatting, response parsing, streaming protocol, error classification, and rate limit interpretation.

The gateway never speaks a provider's native protocol directly. All interaction flows through the adapter.

**Adapter responsibilities:**

- Translate the gateway's canonical request format into the provider's API format.
- Translate the provider's response (streaming or non-streaming) into the gateway's canonical response format.
- Classify provider errors into the gateway's error taxonomy (see Error Classification).
- Report rate limit state (remaining requests, reset time) back to the gateway for concurrency management.
- Handle authentication (API keys, OAuth tokens, service account credentials) using secrets resolved at call time from the org's secret store.

**Supported providers at V2 launch:**

- **Anthropic** -- primary provider, default for all profiles. Claude model family.
- **OpenAI** -- secondary provider. GPT model family.
- **Google** -- secondary provider. Gemini model family.
- **OpenAI-compatible** -- catch-all adapter for any provider exposing an OpenAI-compatible API (local models via Ollama/vLLM/LM Studio, Groq, Together, Fireworks, etc.). This is the self-hosting path.

Adding a new provider means implementing the adapter interface and registering it. No changes to the gateway, routing, or cost tracking logic.

### Provider Registration

Providers are registered at the instance level (system-wide) and activated at the org level. An instance administrator defines which providers are available. An org administrator configures credentials and enables specific providers for their org.

This two-level model means:

- A self-hosted instance can restrict available providers (e.g., only allow local models).
- An org can choose which of the available providers to enable (e.g., enable Anthropic and OpenAI but not Google).
- Credentials are always org-scoped. Two orgs on the same instance use their own API keys.

### Error Classification

The gateway classifies all provider errors into a canonical taxonomy. This is what the fallback chain, retry logic, and supervisor all operate on -- they never see raw provider errors.

| Error Class | Meaning | Retryable | Examples |
|---|---|---|---|
| `rate_limited` | Provider rate limit hit | Yes (with backoff) | HTTP 429, provider-specific throttle responses |
| `overloaded` | Provider at capacity | Yes (with backoff) | HTTP 503, model overloaded responses |
| `timeout` | Request exceeded time limit | Yes (once) | Network timeout, provider processing timeout |
| `auth_failed` | Credentials invalid or expired | No | HTTP 401, HTTP 403 |
| `invalid_request` | Request was malformed or rejected | No | Context window exceeded, content policy violation |
| `model_unavailable` | Specific model is not available | No (trigger fallback) | Model deprecated, region unavailable |
| `provider_error` | Unclassified provider-side failure | Yes (once) | HTTP 500, unexpected response format |
| `network_error` | Could not reach provider | Yes (with backoff) | DNS failure, connection refused, TLS error |

## Model Profiles

A model profile is the core abstraction. It bundles a provider, a specific model, and all the settings needed to make an inference call. Agents, flow nodes, and system components reference profiles -- never raw model identifiers.

### What a Profile Contains

- **Provider and model identifier.** Which provider adapter to use and which model to request.
- **Context window configuration.** Max input tokens and max output tokens. These may be less than the model's actual limits -- the profile intentionally constrains usage.
- **Generation settings.** Temperature, top_p, top_k, presence/frequency penalties. These control the character of the output.
- **Tool call policy.** Whether the model should be allowed to use tools, and whether parallel tool calls are permitted.
- **Streaming policy.** Whether to stream responses from this profile, or wait for full completion.
- **Timeout.** Per-call wall clock timeout. If the model doesn't complete within this window, the call is aborted.
- **Cost limit.** Maximum estimated cost per single invocation. A safety valve against runaway single calls.
- **Fallback profile.** What to use if this profile fails with a retryable error after exhausting retries. Null means no fallback.

### Profile Versioning

Model profiles are versioned. When the underlying model changes (Anthropic releases Claude 4 to replace Claude 3.5 Sonnet), or when settings need to be tuned, a new version of the profile is created. The old version remains available for in-progress work.

**Versioning rules:**

- Each profile has a `version` counter starting at 1.
- When a profile is updated, a new row is created with `version` incremented. The old row's `is_current` is set to `false`.
- Agents and flow nodes reference a profile by ID (the logical profile), not by version. They always resolve to the current version at the time of invocation.
- In-progress turns are not affected by version changes. A turn that started with version 3 continues using version 3 until the turn completes. The next turn picks up version 4 if it has been published.
- The operator can pin a specific profile version on an agent or flow node to prevent automatic pickup of new versions. This is opt-in -- the default is "use current."

### System Profiles

OtterCamp uses model calls internally for operations that are not agent turns. These get dedicated system profiles:

- **Summarization profile** -- used for progressive conversation summarization (see 02-chat.md Session Continuity). Optimized for speed and cost, not depth. Haiku-tier model.
- **Listening eval profile** -- used for the multi-party listening evaluation pass (see 02-chat.md Turn Cycle Phase 2). Must be fast -- the human is waiting. Haiku-tier model.
- **Memory extraction profile** -- used by the memory pipeline to extract memories from conversations (see 06-memory.md). Runs async, no latency pressure. Mid-tier model.
- **Memory synthesis profile** -- used for entity synthesis and deduplication in the memory system. Mid-tier model, async.

System profiles are configured at the org level. Sensible defaults ship with the system. The operator can tune them but most should never need to touch them.

### Profile Assignment

Model profiles are assigned, not auto-selected. The assignment hierarchy:

1. **Flow node level.** A flow node can specify a profile override. This is for cases where a specific step needs a specific model -- a code review node that needs a strong reasoning model, or a lightweight triage node that should use a cheap model.
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

1. **Resolve profile.** Walk the assignment hierarchy (flow node > agent > project > org). Get the current version of the resolved profile.
2. **Check budget.** Verify the org and project budgets have not been exceeded (see Cost Budgets). If a hard budget is hit, reject the request immediately.
3. **Check provider availability.** Is the provider healthy? Is it within per-provider concurrency limits?
4. **Check global concurrency.** Is the system within its global concurrent session limit?
5. **Enqueue or dispatch.** If limits allow, dispatch immediately. If at capacity, enqueue with the appropriate priority.

The routing decision is deterministic and fully traceable. Every step is logged on the `model_invocation` record.

## Concurrency and Queuing

The system enforces limits on concurrent model sessions to manage resource usage (RAM, API rate limits, cost exposure). Concurrency management is the gateway's responsibility -- callers (the chat turn loop, the memory pipeline, the summarizer) do not need to manage it.

### Global Concurrency Limit

A configurable maximum number of concurrent model sessions across all providers. This is primarily for resource conservation -- especially important in self-hosted/single-node deployments where RAM is constrained by model context windows held in memory during streaming.

When the limit is reached, new requests queue and execute in order as slots free up.

The global limit is set at the instance level by the operator. It is not configurable per-org -- it is an infrastructure constraint, not a tenant policy.

### Per-Provider Concurrency Limit

Each provider can have its own concurrent session cap, independent of the global limit. This accounts for provider-specific rate limits, API quotas, and cost exposure. A provider hitting its limit does not block requests to other providers (as long as the global limit is not also reached).

Per-provider limits are set at the instance level (matching the provider's API rate limits) but can be further constrained at the org level (if an org wants to limit its own exposure to a provider).

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

### Queue Observability

Queue state is observable via the operational dashboard (see 13-security-observability-costs.md):

- Current queue depth by priority tier.
- Average and p95 wait time by priority tier.
- Current concurrent sessions by provider.
- Global concurrent sessions vs limit.
- Preemption events (count, affected sessions).

### Queue Timeout

Queued requests have a configurable timeout. If a request waits in the queue longer than the timeout, it fails with error class `queue_timeout`. The caller (turn loop, memory pipeline, etc.) handles this according to its own retry/escalation logic.

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

These are set on the model profile, not on the model itself. A profile may set max output tokens to 4096 even if the underlying model supports 8192 -- this is intentional constraint for cost control.

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

The streaming policy on a profile can be overridden per-call by the gateway caller. This is used for cases like async turns where the human joins mid-turn (session mode switches to sync) -- the next model call in the turn loop can request streaming even if the profile defaults to non-streaming.

### Deterministic Mode

For testing and replay scenarios, a model profile can enable deterministic mode:

- Temperature set to 0.
- Seed parameter set (if the provider supports it).
- Response caching enabled: identical prompts return cached responses.

Deterministic mode is never used in production. It exists for the evaluation and regression testing pipeline (see Evaluation and Debugging).

## Cost Tracking

### Per-Invocation Tracking

Every model call produces a `model_invocation` record with full cost and performance metadata:

- **Token counts.** Input tokens, output tokens, cache read tokens, cache write tokens (for providers that support prompt caching).
- **Cost estimate.** Calculated from the provider's pricing for the specific model, applied to the token counts. Stored in USD (or the org's configured currency).
- **Latency.** Time to first token (for streaming) and total duration.
- **Provider metadata.** Request ID from the provider, model version string, any provider-specific response headers.

Cost calculation uses a price table maintained per provider per model. The price table is part of the model profile configuration and can be updated when providers change pricing.

### Cost Attribution

Every invocation is attributed to multiple dimensions for aggregation:

- **Organization** -- always present.
- **Project** -- present for project-scoped work. Null for org-level activity (chatting with Frank, Lori).
- **Agent** -- present when an agent triggered the invocation. Null for system profiles (summarization, memory extraction).
- **Session** -- the chat session context, if any.
- **Task** -- present for task-scoped work.

This multi-dimensional attribution allows the operator to answer: "How much did Project X cost this month?", "Which agent is the most expensive?", "What's the cost breakdown between chat and autonomous work?"

### Cost Aggregation

The system maintains pre-computed cost rollups for fast querying:

- **Per-org daily.** Total cost and token usage per org per day.
- **Per-project daily.** Total cost per project per day.
- **Per-agent daily.** Total cost per agent per day.
- **Per-model daily.** Total cost per model (across orgs) per day. Useful for the instance operator.

Rollups are computed incrementally: each completed invocation updates the relevant rollup rows. Historical rollups are immutable.

### Cost Budgets

Budgets are the enforcement mechanism. Two levels:

**Org-level budget:**
- Applies to all activity within the org.
- Configured by the org administrator.
- Two thresholds: soft limit and hard limit.
- **Soft limit**: when reached, the system emits a warning event. The operator sees a notification. Work continues.
- **Hard limit**: when reached, all new model invocations are rejected. In-progress invocations complete (the call has already been made). Queued invocations are failed with error class `budget_exhausted`.
- Budget period: monthly (resets on the first of each month) or custom period.

**Project-level budget:**
- Applies to all activity attributed to a specific project.
- Configured by the human or PM.
- Same soft/hard mechanism as org-level.
- A project hitting its hard limit does not affect other projects in the org.

When a hard budget is hit:

1. Queued async work for that scope is paused (not cancelled).
2. The PM is notified (and Frank, for org-level).
3. An inbox item is created for the human: "Project X has reached its cost limit. Increase the budget or cancel pending work."
4. The human can increase the budget (work resumes) or cancel pending tasks.

Budget checks happen at routing time (step 2 of the invocation flow). The check is optimistic -- it verifies the current spend is below the hard limit, not that the upcoming call will stay under it. This is intentional: precise cost prediction before a call is unreliable (output token count is unknown). The hard limit may be slightly exceeded by in-progress calls.

## Fallback Chains

When a model invocation fails, the gateway follows a structured recovery path before giving up.

### Retry Logic

For retryable errors (see Error Classification), the gateway retries with exponential backoff:

- **Max retries per call**: 2 (configurable on the profile).
- **Base backoff**: 1 second.
- **Backoff multiplier**: 2x (1s, 2s).
- **Jitter**: random 0-500ms added to each backoff interval to prevent thundering herd.

Rate limit errors use the provider's `Retry-After` header (or adapter-reported reset time) instead of the default backoff schedule.

### Fallback to Alternate Profile

If retries are exhausted and the profile has a fallback profile configured:

1. Log the failure on the original invocation record.
2. Create a new invocation using the fallback profile.
3. The fallback profile may use a different provider entirely (e.g., primary is Anthropic Claude Opus, fallback is OpenAI GPT-4o).
4. Fallback chains are walked sequentially: A fails -> try B -> B fails -> try C -> C fails -> give up.
5. Maximum chain depth: 3 profiles. Prevents circular fallback configurations.

**Fallback constraints:**

- The fallback profile's context window must be >= the prompt size. If the fallback model has a smaller context window, the gateway attempts to truncate non-critical context (conversation history) to fit. If the prompt cannot be made to fit (identity + policies alone exceed the limit), the fallback is skipped.
- The fallback profile's tool call policy must be compatible. If the original profile allowed tool calls but the fallback does not, the fallback is skipped for turns that require tool use.

### What the Caller Sees

The turn loop (or other caller) does not manage retries or fallbacks. It makes one request to the gateway and receives either a successful response or a terminal error. The gateway handles all retry and fallback logic internally. The caller sees the final result, and the `model_invocation` records capture the full chain of attempts.

If all retries and fallbacks fail, the gateway returns a terminal error to the caller with:

- The original error class.
- The number of retry attempts.
- Whether a fallback was attempted and why it failed.
- The full chain of invocation IDs for debugging.

The turn loop then handles the failure according to its own logic (see 02-chat.md Crash Recovery and 16-agent-control-plane.md Failure State Repair).

## Evaluation and Debugging

### Prompt Capture

Every model invocation captures the full prompt and response for offline evaluation and debugging. This data lives on the `model_invocation` record:

- **Prompt hash.** SHA-256 of the complete prompt. Allows deduplication and quick comparison.
- **Prompt reference.** Pointer to the full prompt content in object storage. Prompts can be large (100k+ tokens) and are not stored inline in the database.
- **Response reference.** Pointer to the full response content in object storage.
- **Prompt metadata.** Which layers were included, token allocation per layer, what was truncated. This is the prompt assembly audit trail -- it answers "why did this agent not know about X?" (because memory was truncated to fit the budget).

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
- Replay invocations are tagged as `replay` and do not count against budgets.

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
  base_url        text,                            -- default API endpoint (overridable per org)
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),
  metadata        jsonb not null default '{}'
);
```

- Registered at the instance level by the operator.
- `adapter_type` maps to the Go adapter implementation. For the four launch providers: `anthropic`, `openai`, `google`, `openai_compat`.
- `base_url` is the default endpoint. For the OpenAI-compatible adapter, this is where the local model server lives. For cloud providers, it's the standard API endpoint.
- `is_enabled` controls instance-wide availability. Disabled providers cannot be used by any org.

### model_provider_config

```sql
create table model_provider_config (
  id                    uuid primary key default gen_random_uuid(),
  organization_id       uuid not null references organization(id),
  provider_id           uuid not null references model_provider(id),
  is_enabled            boolean not null default true,
  api_key_secret_ref    text,              -- reference to encrypted secret store
  base_url_override     text,              -- org-specific endpoint override
  max_concurrent        int,               -- org-level per-provider concurrency cap (null = use instance default)
  created_at            timestamptz not null default now(),
  updated_at            timestamptz not null default now(),
  metadata              jsonb not null default '{}',

  unique (organization_id, provider_id)
);

create index on model_provider_config (organization_id);
```

- Per-org activation and configuration for each provider.
- `api_key_secret_ref` is a reference into the encrypted secret store (see 13-security-observability-costs.md). API keys are never stored in plaintext in this table.
- `base_url_override` lets an org point to a different endpoint (e.g., a regional endpoint, a proxy, a different local model server).
- `max_concurrent` is the org-level constraint on concurrent sessions to this provider. The effective limit is `min(instance per-provider limit, org per-provider limit)`.

### model_profile

```sql
create table model_profile (
  id                    uuid primary key default gen_random_uuid(),
  organization_id       uuid references organization(id),  -- null for system-provided default profiles
  slug                  text not null,
  name                  text not null,
  description           text,
  provider_id           uuid not null references model_provider(id),
  model_id              text not null,                     -- provider-specific model identifier (e.g., 'claude-sonnet-4-20250514')
  version               int not null default 1,
  is_current            boolean not null default true,
  profile_type          text not null default 'agent',     -- 'agent', 'system'

  -- context window
  max_input_tokens      int not null,
  max_output_tokens     int not null,

  -- generation settings
  temperature           numeric(3,2) not null default 0.7,
  top_p                 numeric(3,2),
  top_k                 int,

  -- tool call policy
  tool_use_enabled      boolean not null default true,
  parallel_tool_calls   boolean not null default true,

  -- streaming
  stream_by_default     boolean not null default true,

  -- timeouts
  per_call_timeout_ms   int not null default 60000,

  -- cost controls
  max_cost_per_call     numeric(10,4),                    -- max estimated cost for a single call (USD)

  -- pricing (for cost estimation)
  price_per_input_token   numeric(12,8) not null,         -- USD per token
  price_per_output_token  numeric(12,8) not null,         -- USD per token
  price_per_cache_read    numeric(12,8),                  -- USD per cached token read
  price_per_cache_write   numeric(12,8),                  -- USD per cached token write

  -- fallback
  fallback_profile_id   uuid references model_profile(id),

  -- retry
  max_retries           int not null default 2,

  created_by_type       text not null,                     -- 'human', 'agent', 'system'
  created_by_id         uuid,
  created_at            timestamptz not null default now(),
  updated_at            timestamptz not null default now(),
  metadata              jsonb not null default '{}'
);

create unique index on model_profile (organization_id, slug, version);
create index on model_profile (organization_id, profile_type, is_current);
create index on model_profile (provider_id);
```

- `organization_id` is nullable. System-provided default profiles (shipped with OtterCamp) have null org ID and are available to all orgs as starting points.
- `slug` is the human-readable identifier: `claude-opus-primary`, `claude-sonnet-fast`, `gpt4o-fallback`, `haiku-eval`.
- `version` + `is_current` implement the versioning model. Only one version per slug per org is current.
- `profile_type` distinguishes agent-facing profiles from system profiles (summarization, listening eval, memory extraction). System profiles are not assignable to agents.
- Pricing fields store the per-token costs for cost estimation. Updated when providers change pricing.
- `fallback_profile_id` creates the fallback chain. The application layer enforces max chain depth of 3 and rejects circular references.

### model_profile_assignment

```sql
create table model_profile_assignment (
  id                uuid primary key default gen_random_uuid(),
  organization_id   uuid not null references organization(id),
  scope_type        text not null,                  -- 'org', 'project', 'agent', 'flow_node'
  scope_id          uuid not null,                  -- org ID, project ID, agent ID, or flow_node ID
  profile_id        uuid not null references model_profile(id),
  pinned_version    int,                            -- null = use current, set = use specific version
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now(),

  unique (scope_type, scope_id)
);

create index on model_profile_assignment (organization_id);
create index on model_profile_assignment (profile_id);
```

- Implements the assignment hierarchy. The gateway resolves a profile by walking: flow_node > agent > project > org.
- `pinned_version` allows the operator to lock a specific profile version. When null, the system resolves to the current version of the referenced profile.
- One assignment per scope. An agent has one default model profile, not multiple. (The agent can be overridden at the flow node level for specific steps.)

### model_invocation

```sql
create table model_invocation (
  id                  uuid primary key default gen_random_uuid(),
  organization_id     uuid not null references organization(id),
  profile_id          uuid not null references model_profile(id),
  profile_version     int not null,
  provider_id         uuid not null references model_provider(id),
  model_id            text not null,                    -- actual model used (resolved from profile)

  -- attribution
  session_id          uuid,                             -- chat session, if applicable
  turn_id             uuid,                             -- chat turn, if applicable
  agent_id            uuid,                             -- agent that triggered this, if applicable
  project_id          uuid,                             -- project context, if applicable
  task_id             uuid,                             -- task context, if applicable
  invocation_purpose  text not null,                    -- 'agent_turn', 'listening_eval', 'summarization', 'memory_extraction', 'memory_synthesis', 'replay'

  -- request
  request_priority    text not null,                    -- 'sync_interactive', 'sync_system', 'async_agent', 'async_system'
  queue_wait_ms       int,                              -- time spent in queue before dispatch
  was_preempted       boolean not null default false,   -- was this preempted during execution
  stream_mode         boolean not null,

  -- token counts
  input_tokens        int,
  output_tokens       int,
  cache_read_tokens   int,
  cache_write_tokens  int,

  -- cost
  estimated_cost_usd  numeric(10,6),

  -- latency
  time_to_first_token_ms  int,                          -- null for non-streaming
  total_duration_ms       int,

  -- prompt capture
  prompt_hash         text,                             -- SHA-256 of the full prompt
  prompt_storage_ref  text,                             -- object storage reference for full prompt
  response_storage_ref text,                            -- object storage reference for full response

  -- outcome
  status              text not null,                    -- 'success', 'error', 'timeout', 'cancelled'
  error_class         text,                             -- from error taxonomy, null on success
  error_message       text,

  -- retry/fallback chain
  parent_invocation_id uuid references model_invocation(id),  -- points to the original invocation if this is a retry or fallback
  attempt_number       int not null default 1,
  is_fallback          boolean not null default false,

  -- provider response metadata
  provider_request_id  text,                            -- the provider's own request ID
  provider_model_version text,                          -- exact model version string from provider

  created_at          timestamptz not null default now(),
  completed_at        timestamptz,

  metadata            jsonb not null default '{}'
);

create index on model_invocation (organization_id, created_at);
create index on model_invocation (session_id) where session_id is not null;
create index on model_invocation (turn_id) where turn_id is not null;
create index on model_invocation (agent_id) where agent_id is not null;
create index on model_invocation (project_id) where project_id is not null;
create index on model_invocation (task_id) where task_id is not null;
create index on model_invocation (profile_id, created_at);
create index on model_invocation (provider_id, created_at);
create index on model_invocation (parent_invocation_id) where parent_invocation_id is not null;
```

- One row per model call. This is the most granular cost and performance record in the system.
- `invocation_purpose` classifies what the call was for. This is critical for cost attribution -- the operator needs to know how much is spent on agent turns vs summarization vs memory work.
- `parent_invocation_id` links retries and fallbacks into a chain. The original invocation is the root (null parent). Retries point to the original. Fallback invocations point to the last failed attempt in the chain.
- `prompt_storage_ref` and `response_storage_ref` point to object storage (S3-compatible). Full prompts and responses are stored off-table to keep the invocation table lean and queryable.
- `estimated_cost_usd` is calculated at completion time from the token counts and the profile's pricing fields. It is an estimate because provider billing may differ slightly (rounding, special pricing tiers).

### model_cost_rollup

```sql
create table model_cost_rollup (
  id                uuid primary key default gen_random_uuid(),
  organization_id   uuid not null references organization(id),
  rollup_type       text not null,                   -- 'org', 'project', 'agent', 'model'
  rollup_id         uuid not null,                   -- org ID, project ID, agent ID, or profile ID
  period_start      date not null,
  period_end        date not null,
  total_invocations int not null default 0,
  total_input_tokens bigint not null default 0,
  total_output_tokens bigint not null default 0,
  total_cost_usd    numeric(12,4) not null default 0,

  unique (organization_id, rollup_type, rollup_id, period_start)
);

create index on model_cost_rollup (organization_id, rollup_type, period_start);
create index on model_cost_rollup (rollup_type, rollup_id, period_start);
```

- Pre-computed daily rollups for fast dashboard queries.
- Updated incrementally as invocations complete. Each completed invocation atomically increments the relevant rollup rows.
- `rollup_type` + `rollup_id` is polymorphic: the same table handles org, project, agent, and model aggregations.
- Historical rows are immutable once the period has passed.

### model_cost_budget

```sql
create table model_cost_budget (
  id                uuid primary key default gen_random_uuid(),
  organization_id   uuid not null references organization(id),
  scope_type        text not null,                    -- 'org', 'project'
  scope_id          uuid not null,                    -- org ID or project ID
  period_type       text not null default 'monthly',  -- 'monthly', 'custom'
  period_start      date,                             -- for custom periods
  period_end        date,                             -- for custom periods
  soft_limit_usd    numeric(12,4),                    -- warning threshold
  hard_limit_usd    numeric(12,4),                    -- enforcement threshold
  current_spend_usd numeric(12,4) not null default 0, -- running total for current period
  is_active         boolean not null default true,
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now(),

  unique (scope_type, scope_id)
);

create index on model_cost_budget (organization_id);
create index on model_cost_budget (scope_type, scope_id, is_active);
```

- One active budget per scope (org or project). Budget changes create a new row; the old one is deactivated.
- `current_spend_usd` is the running total for the current period. Reset to 0 when the period rolls over (monthly reset or custom period end).
- The gateway checks `current_spend_usd` against `hard_limit_usd` at routing time. If exceeded, the invocation is rejected.
- When `current_spend_usd` crosses `soft_limit_usd`, the system emits a warning event (notification to the operator, PM, and Frank).

## Cross-Entity Relationships

- `model_invocation.session_id` references `chat_session.id` (see 02-chat.md).
- `model_invocation.turn_id` references `chat_turn.id` (see 02-chat.md). Aggregate token counts on `chat_turn` are rollups from `model_invocation` rows for that turn.
- `model_invocation.agent_id` references `agent.id` (see 05-agents-staff-and-temps.md). The agent's model policy includes allowed profiles.
- `model_invocation.task_id` references `project_task.id` (see 03-projects-and-task-flow.md).
- `model_profile_assignment` with `scope_type = 'flow_node'` references `flow_node.id` (see 03-projects-and-task-flow.md).
- Cost budgets interact with the control plane: when a hard budget is hit, the control plane's supervisor pauses queued work for that scope (see 16-agent-control-plane.md Failure State Repair).
- Prompt/response storage references point to the same object store used by chat artifacts (see 02-chat.md Storage).

## Integration with Other V2 Specs

- **01-architecture-and-domain.md**: model gateway is a top-level runtime component. `ModelProfile` is a canonical domain entity.
- **02-chat.md**: the turn loop calls the gateway for every model invocation. Token counts on `chat_turn` are derived from `model_invocation`. Streaming mode determines how the turn loop processes responses.
- **03-projects-and-task-flow.md**: flow nodes can override agent model profiles. Task scheduling respects per-provider concurrency limits. Cost per task is aggregated from `model_invocation` records where `task_id` matches.
- **05-agents-staff-and-temps.md**: agent profiles specify a default model profile and model policy (allowed profiles, budget caps). The prompt assembly pipeline operates within the resolved profile's `max_input_tokens` budget.
- **06-memory.md**: memory extraction and synthesis pipelines use dedicated system profiles. Memory invocations are attributed to the org and project but not to an agent.
- **13-security-observability-costs.md**: cost rollups feed the operational dashboard. Budget enforcement generates events for the alerting system. Redaction policies apply to prompt capture.
- **16-agent-control-plane.md**: `ModelInvocation` is a canonical execution entity. The control plane's `Run` and `RunStep` records reference model invocations for cost attribution. The supervisor uses budget exhaustion signals to pause work.

## Resolved Decisions

- **Anthropic is the primary provider for V2 launch.** Default profiles ship with Anthropic models. Other providers are supported but secondary. The system is provider-agnostic at the interface level.
- **Explicit routing, not heuristic auto-routing.** The human or PM assigns model profiles to agents and flow nodes. The system does not analyze prompts to select models. Routing is always traceable.
- **Model profiles are the core abstraction.** Agents and flow nodes reference profiles, never raw model IDs. A profile bundles provider + model + settings + cost controls + fallback chain.
- **Model profiles are versioned.** New versions create new rows with incremented version counter. In-progress turns are not affected by version changes. Pinning a specific version is opt-in.
- **Four-level assignment hierarchy.** Flow node > agent > project > org. First non-null wins. No magic fallbacks or heuristic selection.
- **System profiles are separate from agent profiles.** Summarization, listening eval, and memory extraction have their own profiles configured at the org level. Not overridable per-project or per-agent.
- **Streaming for sync, non-streaming for async.** Streaming reduces perceived latency when a human is watching. Non-streaming is simpler and more reliable for autonomous work. Overridable per-call for edge cases.
- **Concurrency is managed at two levels: global and per-provider.** Global limit is an instance-level infrastructure constraint. Per-provider limits account for API rate limits. Org-level per-provider overrides allow further restriction.
- **Four priority tiers for queue ordering.** Sync interactive > sync system > async agent > async system. FIFO within tiers.
- **Sync sessions can soft-preempt async work.** The async session's current model call completes but the next iteration is paused. Not cancellation -- work resumes when a slot opens.
- **Cost budgets: soft (warning) and hard (block).** Per-org and per-project. Hard limit rejects new invocations and pauses queued async work. Budget checks are optimistic (may slightly overshoot).
- **Monthly budget periods as the default.** Custom periods supported for special cases.
- **Fallback chains are walked sequentially.** Maximum depth of 3 profiles. Circular references rejected at configuration time. Fallback is skipped if context window or tool policy is incompatible.
- **Retry with exponential backoff.** Max 2 retries per call. Jitter to prevent thundering herd. Rate-limited errors use provider's Retry-After.
- **Prompt and response capture for all invocations.** Stored in object storage with references from `model_invocation`. Subject to org redaction policy. Enables replay and evaluation.
- **Cost rollups are pre-computed daily aggregations.** Incrementally updated. Historical rollups immutable. Four dimensions: org, project, agent, model.
- **Queue timeout varies by priority tier.** Sync interactive: 30s. Sync system: 15s. Async agent: 5min. Async system: 30min.
- **Provider adapters classify all errors into a canonical taxonomy.** The gateway, fallback chain, and retry logic operate on canonical error classes, never raw provider errors.
- **Provider credentials are org-scoped.** Two orgs on the same instance use their own API keys. Keys stored as encrypted secret references, resolved at call time.
- **Deterministic mode exists for testing only.** Temperature 0, seed parameter, response caching. Never used in production.
- **Regression suites are deferred to post-V2.** The infrastructure (prompt capture, replay) ships in V2. The regression suite UX is a future enhancement.

## Open Questions

_None currently outstanding._
