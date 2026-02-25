# Task 095: Seed flow nodes for system flow templates / add flow node management API

Layer: L2
Effort: M
Depends on: none

## Context

The two system-level flow templates seeded by bootstrap — `default-single-agent` and `default-review` — are created with `start_node_id = null` and no flow node rows. This means any task assigned one of these templates cannot be started: `flow.StartFlow` checks `template.StartNodeID == nil` and returns `ErrFlowNotStarted`.

Confirmed live (2026-02-25):
```sql
SELECT id, slug, is_system, start_node_id FROM flow_template;
-- d30bcce9 | default-single-agent | t | (null)
-- 4f599904 | default-review       | t | (null)
```

There is also **no API** to create or modify flow nodes. Routes exist for flow templates (`POST /projects/{id}/flow-templates`, `PATCH /flow-templates/{id}`) but there is no `POST /flow-templates/{id}/nodes` or equivalent. There is no way, via any API or seed, for a flow template to ever have nodes.

The `flow_node` table schema supports:
- `node_type`: `work`, `review`, `decision`, `parallel`, `merge`
- `actor_type`: `agent`, `role`, `human`
- `actor_id`: UUID of the specific agent/role
- `next_node_id`: chain to the next node
- `requires_human_review`: boolean gate

## Required Fix

### Part 1: Seed flow nodes for system templates

In the bootstrap seed (or a new migration), add flow nodes to the two system templates:

**`default-single-agent`**: A single `work` node with `actor_type = 'role'`, representing the assigned agent. Wire it as the `start_node_id`.

**`default-review`**: Two nodes — a `work` node followed by a `review` node (`requires_human_review = true`). Wire the `work` node as `start_node_id`, set `next_node_id` to the review node.

The seed should be idempotent: if the templates already have nodes, skip.

### Part 2: Add flow node management API endpoints

Add routes under the flow template resource:

```
POST   /flow-templates/{id}/nodes        — create a flow node
GET    /flow-templates/{id}/nodes        — list nodes for a template
PATCH  /flow-templates/{id}/nodes/{nid}  — update a node (actor, type, next, etc.)
DELETE /flow-templates/{id}/nodes/{nid}  — remove a node
PATCH  /flow-templates/{id}              — update start_node_id (existing, extend to allow it)
```

The create node request body:
```json
{
  "display_name": "Work",
  "node_type": "work",
  "actor_type": "role",
  "actor_id": null,
  "next_node_id": null,
  "requires_human_review": false,
  "position": 0
}
```

Org-scoped templates are readable by any member; system templates (`organization_id = null`) are read-only for non-admin principals.

## Acceptance Criteria

- [ ] `SELECT start_node_id FROM flow_template WHERE slug = 'default-single-agent'` returns a non-null UUID after bootstrap
- [ ] `SELECT start_node_id FROM flow_template WHERE slug = 'default-review'` returns a non-null UUID after bootstrap
- [ ] `StartFlow(ctx, taskID)` succeeds for tasks with `default-single-agent` template
- [ ] `POST /flow-templates/{id}/nodes` creates a flow node and returns 201
- [ ] `GET /flow-templates/{id}/nodes` lists nodes in position order
- [ ] Idempotent bootstrap: running seed twice doesn't error or duplicate nodes
- [ ] `go build ./...` passes

## Required Tests

- Integration: create a task with `default-single-agent` template, transition to `in_progress`, call `StartFlow` — should succeed and create a `flow_node_execution` row
- Integration: `POST /flow-templates/{id}/nodes` — creates node, sets `next_node_id`
- Unit: idempotent seed helper
