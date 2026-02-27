# Test 028: Model Gateway, Control Plane, Realtime & Observability

**Sections:** 10. Model Gateway, 15. Agent Control Plane, 17. Realtime, 19. Observability
**Tested:** 2026-02-26
**Result:** PARTIAL

## Model Gateway (Section 10)

**`GET /v1/model/profiles`** — PASS ✓
- Returns profiles with logical_profile_id, provider_id, model_name, max_tokens, is_current ✓
- Current: OpenAI gpt-4o (high-cap) and gpt-4o-mini (haiku) after switch

**`GET /v1/model/providers`** — PASS ✓
- Returns OpenAI and Anthropic providers with api_base_url, supported_features ✓

**`GET /v1/model/providers/:id`** — PASS ✓ (confirmed in prior iteration)

**`PATCH /v1/model/profiles/:id`** — PASS ✓ (confirmed in prior iteration — used to switch between Anthropic and OpenAI)

**Usage endpoint:**
- `GET /v1/usage?period=today&group_by=agent` — PASS ✓
- Returns token counts per agent ✓
- `total_cost_microcents: 0` — FAIL (cost tracking bug, known issue in MEMORY.md)
- Required parameters: period (today/yesterday/7d/30d/YYYY-MM-DD) AND group_by (agent/project/model_provider/provider_connection) ✓

## Control Plane (Section 15)

**`GET /v1/control/health`** — PASS ✓
- Returns: status=ok, active_runs count, supervisor_last_tick, tool_execution_audit ✓

**`GET /v1/control/runs`** — PASS ✓
- Returns run list with project_id, task_id, session_id, status, trigger_type ✓

**`GET /v1/control/policies`** — PASS ✓
- Returns policies with capability, effect, conditions, priority, policy_layer ✓

**`GET /v1/control/cost/summary`** — PASS ✓
- Returns total_tokens by group_key ✓
- Cost in microcents all 0 (known bug)

**Policy management:**
- `POST /v1/control/policies` — tested in prior iterations ✓
- `DELETE /v1/control/policies/:id` — tested ✓

## Realtime & Events (Section 17)

**`GET /v1/events/stream`** — PASS ✓
- SSE stream established with `event: connected` first ✓
- high_watermark provided for sequence tracking ✓
- Events include: system.retention.completed, chat.session.created, chat.message.created, chat.message.user_sent, etc. ✓
- Filter by scopes: `?scopes=org` works ✓

**Event envelope format:**
- event_id, event_type, occurred_at, org_id, payload, seq ✓

**`GET /v1/events`** (bulk history) — not tested

**Trace spans:**
- `GET /v1/trace/spans` — FAIL: 404 Not Found

## Observability (Section 19)

**Usage rollup:** PASS ✓ (per-agent token counts work)
**Cost tracking:** FAIL — microcents always 0 (known issue)
**Trace spans:** FAIL — endpoint 404
**Metrics endpoint:** Not directly tested (restricted to localhost only per security fix)

## Issues Filed

- Issue #181 — GET /v1/trace/spans returns 404 (trace inspection not accessible)
