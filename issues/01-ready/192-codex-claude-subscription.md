# Task 192: Codex — support Claude subscription (not just API key)

Layer: L3
Effort: L
Depends on: none

## Context

Codex (the model provider layer) currently only supports Claude via Anthropic API keys. We need to add the ability to use a Claude subscription (Pro/Team plan) as an alternative authentication method. This was previously figured out for Pearl (https://github.com/samhotchkiss/openclaw-pearl/) and the same approach should be adapted here.

## Current State

- Model providers in OtterCamp are configured via `model_provider` table with API keys
- Anthropic provider stores `api_key` in provider config
- No support for subscription-based authentication or session tokens
- Pearl has a working implementation of subscription-based Claude access

## Required Fix

**1. Research Pearl's subscription workaround**
- Review the Pearl codebase implementation for Claude subscription access
- Document the proxy/token approach used

**2. Add subscription auth mode to Codex**
- Add a new auth mode to the Anthropic provider configuration (e.g., `auth_mode: "subscription"`)
- Implement the session token / cookie-based authentication flow
- Store subscription credentials securely (same pattern as API keys)

**3. Provider configuration UI**
- Allow selecting auth mode (API key vs subscription) when configuring Anthropic provider
- Surface any token refresh / re-auth requirements to the user

## Acceptance Criteria

- [ ] Anthropic provider supports subscription-based auth as alternative to API key
- [ ] Token refresh/rotation handled gracefully
- [ ] Existing API key auth continues working unchanged
- [ ] Configuration documented

## Required Tests

- Unit test: subscription auth mode constructs correct request headers
- Unit test: token refresh triggers when subscription token expires
- Integration test: successful completion request with subscription auth
