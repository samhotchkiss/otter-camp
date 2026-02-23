# 017: Flow Node Schema

| Field | Value |
|-------|-------|
| Layer | L2 |
| Size | S (≤1 day) |
| Spec refs | doc 03 §FlowNode, doc 09 §FlowNodeMCPTools, doc 10 §FlowNodeSkill, doc 20 §FlowNodeToolDomains |
| Spec status | finished |
| Depends on | 016, 011 |
| Blocks | 018, 019, 025, 027, 029, 030 |

## Scope

Build the `flow_node` table DDL migration and repository layer. Also builds the
`flow_node_skill` join table. The final authoritative schema synthesizes three
cross-doc changes: drop `skills jsonb` (doc 10), add `mcp_tools jsonb` (doc 09),
add `tool_domains jsonb` (doc 20).

### Must build

**Migrations:**
- `0020_flow_node.sql` — includes the `flow_node` table and the deferred FK from `flow_template.start_node_id`
- `0021_flow_node_skill.sql` — join table

**`flow_node` table** (authoritative final schema — see ISSUE #16 notes below):
- `id uuid primary key default gen_random_uuid()`
- `flow_template_id uuid not null references flow_template(id) on delete cascade`
- `display_name text not null`
- `node_type text not null check (node_type in ('work','review','decision','parallel','merge'))` — doc 03
- `position integer not null default 0` — ordering within template for display
- `actor_type text check (actor_type in ('agent','role','human'))` — null = any available agent of the right type
- `actor_id uuid` — references `agent(id)` when actor_type='agent' (application-layer FK; agent is L2 peer)
- `next_node_id uuid references flow_node(id) on delete set null` — happy path next node; null = terminal
- `reject_node_id uuid references flow_node(id) on delete set null` — rejection path; null = no rejection loop
- `mcp_tools jsonb not null default '[]'` — array of `{connection_id, tool_name}` objects; resolved against `mcp_tool_catalog` at session start (doc 09)
- `tool_domains jsonb not null default '[]'` — array of tool domain strings for stage 3 soft deprioritization (doc 20)
- `requires_human_review boolean not null default false` — true = human must approve before advancing
- `max_visits integer not null default 10` — rejection loop guard; ErrMaxVisitsExceeded after this many visits
- `metadata jsonb not null default '{}'`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Index: `(flow_template_id, position)`
- Index: `(next_node_id) WHERE next_node_id IS NOT NULL`
- Index: `(reject_node_id) WHERE reject_node_id IS NOT NULL`

**Note:** `skills jsonb` column is intentionally ABSENT from this DDL. Skills are linked
via `flow_node_skill` join table only.

**Deferred FK addition (in `0020_flow_node.sql`):**
```sql
ALTER TABLE flow_template ADD CONSTRAINT fk_flow_template_start_node
    FOREIGN KEY (start_node_id) REFERENCES flow_node(id) ON DELETE SET NULL;
```

**`flow_node_skill` join table** (doc 10, ISSUE #6 RESOLVED):
- `id uuid primary key default gen_random_uuid()`
- `flow_node_id uuid not null references flow_node(id) on delete cascade`
- `skill_id uuid not null references skill(id) on delete cascade`
- `position integer not null default 0` — priority ordering among skills on this node
- `created_at timestamptz not null default now()`
- Unique constraint: `(flow_node_id, skill_id)`
- Index: `(flow_node_id, position)`

**Repository layer:**
- `FlowNodeRepo`: `Create`, `GetByID`, `ListByTemplate`, `Update`, `Delete`, `GetByTemplateOrdered`
- `FlowNodeSkillRepo`: `Attach`, `Detach`, `ListByNode`, `SetPosition`

### Must NOT build
- Flow execution logic (task 030)
- `flow_node_execution` table (task 029 — L3)
- Model profile assignment for flow_node scope (task 010 already handles this)
- MCP tool resolution at session start (task 049)

## Acceptance Criteria

- [ ] Migration `0020_flow_node.sql` applies cleanly; `skills jsonb` column is NOT present
- [ ] `mcp_tools` and `tool_domains` columns exist with `jsonb not null default '[]'`
- [ ] Deferred FK `flow_template.start_node_id → flow_node.id` is created in migration `0020`
- [ ] Self-referential FKs (`next_node_id`, `reject_node_id`) use `ON DELETE SET NULL` (not cascade)
- [ ] Migration `0021_flow_node_skill.sql` applies cleanly; unique constraint on `(flow_node_id, skill_id)`
- [ ] `FlowNodeRepo.GetByTemplateOrdered` returns nodes sorted by `position` ascending
- [ ] `FlowNodeSkillRepo.Attach` on duplicate `(flow_node_id, skill_id)` returns `ErrAlreadyAttached` (not a DB error)
- [ ] `FlowNodeRepo.Delete` on a node that is another node's `next_node_id` sets the FK to NULL (ON DELETE SET NULL behavior verified)

## Tests Required

**Unit tests:**
- `FlowNodeRepo` field mapping: verify `mcp_tools` and `tool_domains` (jsonb arrays) marshal to/from Go `[]json.RawMessage` or typed structs correctly; empty arrays serialize to `[]` not `null`

**Integration tests:**
- Full flow_node CRUD: create template → create nodes → set `start_node_id` → verify FK
- Self-reference chain: create node A with `next_node_id = B`, node B with `next_node_id = null`; `ListByTemplate` returns both
- ON DELETE SET NULL: delete node B → node A's `next_node_id` becomes null
- `flow_node_skill`: attach skill to node → verify `(flow_node_id, skill_id)` unique constraint; attach same skill again → `ErrAlreadyAttached`
- `FlowNodeSkillRepo.SetPosition`: reorder skills on a node → `ListByNode` returns in new order

**E2E tests:**
- None — covered by dedicated E2E task 084

## Implementer Notes

> ✅ ISSUES #16 + #25 (RESOLVED): Doc 03 DDL updated. Final `flow_node` schema: no `skills jsonb`; has `mcp_tools jsonb` and `tool_domains jsonb`. Implement exactly as specified in this task — no flags or confirmation needed.

- `tool_domains jsonb` stores tool domain strings (e.g. `["file", "cli", "mcp"]`) used by the tool resolution pipeline (task 049) for stage 3 soft deprioritization. It does not restrict tools — it only soft-deprioritizes them.
- The `actor_id` column references `agent.id` but is stored as a plain `uuid` without a SQL FK. This is because `flow_node` and `agent` are peers at L2; adding a SQL FK here would create a circular migration dependency. Enforce referential integrity at the application layer in the flow node service (task 018).
- `mcp_tools` jsonb array element shape: `{"connection_id": "<uuid>", "tool_name": "<string>"}`. The `connection_id` is validated against `mcp_connection.id` at session start (task 049), not at storage time.
- `max_visits` defaults to 10 as a guard against infinite rejection loops. The rejection loop logic (incrementing visit counter, comparing against max_visits) is implemented in the flow execution service (task 030).
- The `flow_node_skill` join table is at L2 depth in this file but is assigned to L3 in the dependency graph because it depends on `flow_node` (L2) + `skill` (L1). Writing it here alongside `flow_node` is acceptable because it is a direct extension of the flow_node schema and has no additional L3 dependencies.
