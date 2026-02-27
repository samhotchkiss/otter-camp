# Test 027: Agents, Memory, and Skills

**Sections:** 5. Agents, 9. Memory System, 13. Skills System
**Tested:** 2026-02-26
**Result:** PARTIAL

## Agents (Section 5)

**`GET /v1/agents`** — PASS ✓
- Returns Frank, Lori, Ellie (starter trio) + any created agents
- Fields: id, display_name, agent_class, lifecycle_status, system_prompt, agent_type, is_starter_trio, memory_read_scopes, tool_allow_list, budget_cap_tokens ✓

**`GET /v1/agents/:id/config`** — PASS ✓ (confirmed from previous iteration)
- Returns system_prompt, operator_instructions, model profile, memory scopes, tool policy, budget

**`GET /v1/agents/:id/tools`** — PASS ✓ (confirmed from previous iteration)
- Returns 67 tools filtered by allow/deny list

**Agent creation** (`POST /v1/agents`):
- Requires: display_name, agent_type (pm/worker/reviewer/general), agent_class (staff/temp) ✓
- Tested in prior iterations

**Temp agents:**
- `temp_project_id`, `temp_ttl_seconds`, `temp_expires_at` fields present in response ✓

**Notable gap:**
- `DELETE /v1/agents/:id` — not tested
- Promotion from temp to staff (`promoted_to_agent_id`) — not tested

## Memory System (Section 9)

**`GET /v1/memory/items`** — PASS ✓
- Default filter (active status) returns empty (0 active items)
- `?status=candidate` returns candidate items ✓
- Items have: memory_type, scope, content, confidence, utility_score, status ✓

**`POST /v1/memory/query`** — PASS ✓
- Mode parameter: passive/mention/agent_query (NOT semantic) ✓
- Returns `{memories, entity_profiles, fallback_used}` ✓
- Current query results empty (new system, no mature memories)

**Memory taxonomy:** FAIL — empty (Ellie not bootstrapped taxonomy)
**Entity synthesis:** FAIL — 0 entities (by design for new installation)

**Memory candidate lifecycle:**
- Items appear as "candidate" after agent responses ✓
- 7-day hold before promotion (by design)

## Skills System (Section 13)

**`GET /v1/skills`** — PASS ✓
- Returns org-level skills: code-review, and others from bootstrap ✓
- Fields: id, slug, display_name, description, file_path, version, is_active ✓

**`GET /v1/projects/:id/skills`** — PASS ✓ (confirmed in prior iteration)

**`GET /v1/agents/:id/skills`** — not tested directly

**Skill content:** descriptions stored in database, skill files referenced via file_path ✓

## Issues Filed

None new (existing gaps already tracked in prior iterations)
