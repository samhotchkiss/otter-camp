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

