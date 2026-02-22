# 07. Models and Inference (Pearl-Informed)

## Objective

- Native model integration without OpenClaw.
- Provider-agnostic runtime with predictable behavior and cost controls.

## Provider Abstraction

- OpenAI
- Anthropic
- Google
- Local/self-host models (via compatible APIs)

## Model Profile Concept

A model profile defines:

- Provider + model ID
- Context window and token limits
- Tool call mode/policy
- Temperature and reasoning settings
- Retry/fallback strategy
- Cost budget limits

## Routing

- Route by task type (chat, code, review, planning).
- Route by user/org policy.
- Route by budget and latency targets.
- Fallback chain on errors or policy rejections.

## Concurrency and Queuing

The system enforces limits on concurrent LLM sessions to manage resource usage (RAM, API rate limits, cost).

### Global Concurrency Limit

- A configurable maximum number of concurrent LLM sessions across all providers.
- Primarily for resource conservation — especially important in self-hosted/single-node deployments where RAM is constrained.
- When the limit is reached, new requests queue and execute in order as slots free up.

### Per-Provider Concurrency Limit

- Each provider can have its own concurrent session cap, independent of the global limit.
- Accounts for provider-specific rate limits, API quotas, and cost exposure.
- A provider hitting its limit does not block requests to other providers (as long as the global limit isn't also reached).

### Queuing Behavior

- Queued requests execute in FIFO order within priority tiers.
- Synchronous sessions (human waiting) get priority over asynchronous sessions (autonomous agent work).
- Queue depth and wait times are observable via the operational dashboard.
- Configurable timeout for queued requests — if a request waits too long, it fails with a clear error rather than blocking indefinitely.

## Inference Controls

- Max prompt and completion tokens.
- Per-turn and per-run timeout budgets.
- Deterministic mode for test and replay scenarios.

## Cost Tracking

- Store token usage and cost estimate per request.
- Aggregate by org, project, user, agent.
- Enforce soft/hard budgets.

## Evaluation Hooks

- Capture prompts/responses for offline eval (with redaction policies).
- Regression suite for major profile changes.

## Open Questions

- Should default routing be explicit policy or heuristic auto-routing?
- Which provider becomes mandatory for first release?
- How do we define a stable “model profile version” contract?

