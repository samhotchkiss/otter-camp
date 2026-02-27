# Test 030: Observability, Security & Deployment

**Sections:** 17 (continued), 18. Security, 19. Observability, 20. Deployment
**Tested:** 2026-02-26
**Result:** PARTIAL

## Observability (Section 19)

**`GET /v1/usage`** — PASS ✓ (confirmed from test 028)
- Requires: period (today/yesterday/7d/30d/YYYY-MM-DD) + group_by (agent/project/model_provider/provider_connection)
- Returns per-agent token counts ✓
- total_cost_microcents: 0 (known bug)

**`GET /v1/model/usage-rollup`** — FAIL: 404 Not Found
- Endpoint not found; path may differ

**`GET /v1/trace/spans`** — FAIL: 404 Not Found — see Issue #181
- Trace spans ARE collected in DB (issue #112 fixed partition)
- But no API endpoint to query them

**`GET /v1/control/health`** — PASS ✓ (confirmed from test 028)

**`GET /v1/control/tool-executions`** — PASS ✓
- Returns all tool executions with policy_decision, status, error_message

**Memory taxonomy:**
- `GET /v1/memory/taxonomy` — PASS ✓ (returns empty array — Ellie not bootstrapped taxonomy)

## Security (Section 18)

Confirmed from prior iterations and current session:
- API key scopes enforced at middleware ✓
- 4MB request body limit ✓
- Email lowercase normalization ✓
- Account lockout → "invalid credentials" ✓
- /metrics restricted to localhost ✓
- SSRF protection in MCP URL validation ✓

## Deployment (Section 20)

- Binary: `/tmp/ottercamp-bin/ottercamp` — single binary ✓
- `ottercamp server` — running on port 4110 ✓
- `ottercamp worker` — running, processes jobs ✓
- `ottercamp tui` — launches TUI ✓
- Local single-node mode: confirmed ✓
- PostgreSQL + pgvector: confirmed ✓
- Local filesystem storage: OTTERCAMP_STORAGE_ROOT=/tmp/ottercamp-data/objects ✓
- Port 4110: confirmed ✓
- Migrations 0088+ applied ✓
- Bootstrap ran on startup ✓

## Issues Found

- `GET /v1/model/usage-rollup` returns 404 — filed as new Issue #182
