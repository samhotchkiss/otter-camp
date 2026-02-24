# 085: Agent Management E2E

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | M (1–2 days) |
| Spec refs | doc 05 §StaffAgentLifecycle, doc 05 §TempAgentLifecycle, doc 05 §TempScopes, doc 14 §StarterTrio, doc 21 §E2ETests |
| Spec status | finished |
| Depends on | 001–080 |
| Blocks | — |

## Scope

E2E test scenario for agent lifecycle management. Uses only the `ottercamp` CLI binary
and REST API. Verifies: staff agent creation in draft state and activation, project
assignment, temp agent creation (active immediately, no review, project-scoped),
temp persistence across task completion, explicit temp retirement, and starter trio presence.

### Must build

**Test file:** `e2e/agent_management_test.go`

Build tag: `//go:build e2e`

Test setup: calls `POST /v1/test/reset` and `ottercamp bootstrap` before each scenario.
Uses standard `e2e/testutil/` helpers plus:
- `WaitForAgentStatus(t, baseURL, token, agentID, status, timeout)` — polls
  `GET /v1/agents/<id>` until `lifecycle_status == status` or timeout expires
- `CompleteTask(t, baseURL, token, taskID)` — advances the task through all flow nodes
  to reach `work_status == "done"` using test-mode shortcuts

**Scenario: `TestAgent_StaffLifecycle`**

Step 1 — Reset, bootstrap, get token:
```
POST /v1/test/reset → 204
ottercamp bootstrap → exit 0
admin token via POST /v1/auth/login
```

Step 2 — Create a staff agent in draft state:
```
POST /v1/agents
Authorization: Bearer <token>
{
  "name": "Ada",
  "agent_class": "staff",
  "role": "engineer",
  "system_prompt": "You are Ada, a senior software engineer."
}
→ 201
→ body.data.id (staff_agent_id)
→ body.data.lifecycle_status == "draft"
→ body.data.agent_class == "staff"
```

Step 3 — Verify draft agent is not active:
```
GET /v1/agents/<staff_agent_id>
→ body.data.lifecycle_status == "draft"
```

Step 4 — Approve activation (human action simulated via API):
```
POST /v1/agents/<staff_agent_id>/activate
Authorization: Bearer <token>
→ 200
→ body.data.lifecycle_status == "active"
```

Step 5 — Verify agent is now active:
```
GET /v1/agents/<staff_agent_id>
→ body.data.lifecycle_status == "active"
```

Step 6 — Create a project:
```
POST /v1/projects
{ "name": "Agent Test Project", "slug": "agent-test" }
→ 201
→ body.data.id (project_id)
```

Step 7 — Assign staff agent to project:
```
POST /v1/agents/<staff_agent_id>/project-assignments
Authorization: Bearer <token>
{
  "project_id": "<project_id>",
  "role": "worker"
}
→ 201
→ body.data.agent_id == staff_agent_id
→ body.data.project_id == project_id
→ body.data.is_active == true
```

Step 8 — Verify assignment appears in agent listing:
```
GET /v1/agents/<staff_agent_id>/project-assignments
→ 200
→ body.data includes entry with project_id == project_id and is_active == true
```

**Scenario: `TestAgent_TempLifecycle_ProjectScoped`**

Step 1 — Reset, bootstrap, create project (reuse or create fresh).

Step 2 — Create a temp agent (project-scoped):
```
POST /v1/agents
Authorization: Bearer <token>
{
  "name": "temp-worker-1",
  "agent_class": "temp",
  "temp_project_id": "<project_id>",
  "role": "worker"
}
→ 201
→ body.data.id (temp_agent_id)
→ body.data.lifecycle_status == "active"  (temp agents are immediately active — no review)
→ body.data.agent_class == "temp"
→ body.data.temp_project_id == project_id
```

Step 3 — Verify temp agent is active immediately (no approval required):
```
GET /v1/agents/<temp_agent_id>
→ body.data.lifecycle_status == "active"
```

Step 4 — Create a task and complete it:
```
POST /v1/projects/<project_id>/tasks
{ "title": "Temp agent test task" }
→ 201 → task_id

CompleteTask(taskID: task_id)
→ GET /v1/tasks/<task_id> → work_status == "done"
```

Step 5 — Verify temp agent is STILL ACTIVE after task completion (temps persist across tasks):
```
GET /v1/agents/<temp_agent_id>
→ body.data.lifecycle_status == "active"
```

Step 6 — Explicitly retire the temp agent:
```
POST /v1/agents/<temp_agent_id>/retire
Authorization: Bearer <token>
→ 200
→ body.data.lifecycle_status == "retired"
```

Step 7 — Verify retired temp agent is no longer usable:
```
POST /v1/agents/<temp_agent_id>/project-assignments
{ "project_id": "<project_id>", "role": "worker" }
→ 422 or 409  (retired agent cannot be assigned)
```

**Scenario: `TestAgent_TempLifecycle_TTL_Expires`**

Verifies that a temp with `temp_ttl_seconds` set auto-retires when the TTL elapses,
but NOT before.

Step 1 — Reset, bootstrap, create project.

Step 2 — Create a temp agent with a TTL:
```
POST /v1/agents
Authorization: Bearer <token>
{
  "name": "ttl-temp-worker",
  "agent_class": "temp",
  "temp_project_id": "<project_id>",
  "temp_ttl_seconds": 3600,
  "role": "worker"
}
→ 201
→ body.data.lifecycle_status == "active"
→ body.data.temp_expires_at is a non-null future timestamp
```

Step 3 — Complete a task and verify temp is still active (TTL not elapsed):
```
CompleteTask(taskID: new task)
GET /v1/agents/<ttl_temp_id>
→ body.data.lifecycle_status == "active"
```

Step 4 — Verify TTL expiry time is in the future:
```
GET /v1/agents/<ttl_temp_id>
→ body.data.temp_expires_at > now
```

**Scenario: `TestAgent_StarterTrio_AlwaysPresent`**

Verifies that Frank, and the other starter trio agents are present and active after bootstrap.

Step 1 — Reset, bootstrap.

Step 2 — Verify starter trio agents:
```
GET /v1/agents
Authorization: Bearer <token>
→ 200
→ body.data contains agents with names "Frank", "Lori", "Ellie"
   (or roles matching Chief of Staff, PM Lead, QA Lead as defined in bootstrap)
→ all three have lifecycle_status == "active"
→ all three have agent_class == "staff"
```

### Must NOT build

- UI or TUI interactions
- Internal Go package calls
- Concurrent temp limit enforcement tests (tested in task 074 integration tests)
- Promotion workflow (Lori review → draft staff → human approval) — tested in task 074

## Acceptance Criteria

- [ ] `TestAgent_StaffLifecycle` passes: draft → active on activation; project assignment created
- [ ] `TestAgent_TempLifecycle_ProjectScoped` passes: temp is immediately active; remains active after task completes; explicitly retired temp cannot be assigned
- [ ] `TestAgent_TempLifecycle_TTL_Expires` passes: TTL-temp remains `active` after task completion; `temp_expires_at` is in the future
- [ ] `TestAgent_StarterTrio_AlwaysPresent` passes: Frank, Lori, Ellie are all present and active after bootstrap
- [ ] Full scenario completes in under 3 minutes

## Tests Required

**Unit tests:** None — this task IS the test suite.

**Integration tests:** None — this is an E2E test suite.

**E2E tests:**
- `TestAgent_StaffLifecycle` — draft → active, project assignment
- `TestAgent_TempLifecycle_ProjectScoped` — temp active immediately; persists after task complete; explicit retirement works
- `TestAgent_TempLifecycle_TTL_Expires` — TTL temp persists after task complete; temp_expires_at in future
- `TestAgent_StarterTrio_AlwaysPresent` — starter trio exists and is active

## Implementer Notes

**ISSUE #27 (RESOLVED — path prefix):**
All API calls use `/v1/` paths (doc 12 authoritative).

**ISSUE #5 (lifecycle_status constraints):**
The lifecycle_status machine enforcement is application-layer only. The test for
"expired agent cannot be assigned" verifies the application layer rejects the assignment,
not a DB constraint. The expected status code is 422 (Unprocessable Entity) or 409.

**ISSUE #8 (RESOLVED — temp scope):**
All temps are project-scoped. `temp_scope_type` and `temp_scope_id` are replaced by
`temp_project_id` (required for all temps). Task completion does NOT retire temps —
the `TestAgent_TempLifecycle_ProjectScoped` scenario explicitly verifies this.

**ISSUE #4 (RESOLVED — private memory defaults):**
`private_memory_enabled = false` for all agents. The test does not assert private_memory
values — that is tested in the memory E2E (task 086).

**Temp retirement:**
Temps are explicitly retired by PM or Lori via `POST /v1/agents/:id/retire`. TTL-based
temps auto-expire via the scheduler when `temp_expires_at` passes. Neither task completion
nor session close retires a temp.

**CompleteTask helper:**
`CompleteTask(t, baseURL, token, taskID)` is a test utility that advances the task
through all flow nodes using the `POST /v1/tasks/<id>/advance-flow` endpoint with
`decision: "complete"`. For tasks without a flow template, it directly sets
`work_status = "done"` via a test-mode endpoint. The implementation in `e2e/testutil/`
must handle both cases.
