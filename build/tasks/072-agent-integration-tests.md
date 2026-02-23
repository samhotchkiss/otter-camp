# 072: Agent Integration Tests

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | S (≤1 day) |
| Spec refs | doc 05 §AgentLifecycle, doc 05 §TempAgents, doc 05 §PromotionWorkflow, doc 10 §SkillAttachment, doc 21 §IntegrationTests |
| Spec status | finished |
| Depends on | 013, 014, 015, 025, 026 |
| Blocks | 089 |

## Scope

Integration test suite for the agent domain: CRUD operations, lifecycle state machine
transitions for both staff and temp agent classes, temp agent scoping and auto-retirement,
project assignment management, skill attachment, and budget cap stub behavior. All tests
use a real PostgreSQL database via `testdb.New(t)`.

### Must build

**Test file:** `internal/agent/agent_integration_test.go`

Build tag: `//go:build integration`

Test setup helpers in `internal/testutil/agent.go`:
- `MakeAgent(t, db, orgID, opts)` — creates an agent row with given options (class,
  lifecycle_status, etc.); returns `*agent.Agent`
- `MakeSkill(t, db, orgID)` — creates a skill row; returns `*skill.Skill`

**Test scenarios:**

`TestAgent_CRUD` — create agent via POST /v1/agents; GET /v1/agents/:id returns created
agent; PATCH /v1/agents/:id updates display_name; GET /v1/agents confirms agent appears
in list for org; DELETE is not supported (verify 405 or soft-retire path).

`TestAgent_StaffLifecycle_DraftToActive` — create agent with `lifecycle_status='draft'`;
POST /v1/agents/:id/activate (or equivalent transition endpoint); agent row has
`lifecycle_status='active'`; `agent_profile_template` row is created or updated; audit
event row for the transition is present.

`TestAgent_StaffLifecycle_ActiveToPaused` — POST /v1/agents/:id/pause on active agent;
`lifecycle_status='paused'`; paused agent is excluded from run dispatch.

`TestAgent_StaffLifecycle_Retire` — POST /v1/agents/:id/retire on active agent;
`lifecycle_status='retired'`; agent no longer appears in GET /v1/agents (default
filter excludes retired); explicit `?include_retired=true` parameter surfaces it.

`TestAgent_StaffLifecycle_IllegalTransition` — attempt to set a staff agent to 'expired'
(temp-only state); service returns error; `lifecycle_status` unchanged in DB.

`TestAgent_TempAgent_Scoping` — create temp agent with `temp_scope_type='project'` and
`temp_scope_id` set to a real project ID; GET /v1/agents/:id shows correct scope fields;
temp agent appears in project-scoped agent list.

`TestAgent_TempAgent_TTLExpiry` — create temp agent with `temp_scope_type='ttl'` and
`temp_expires_at` in the past (use `clock.Fake`); trigger auto-retirement check; agent
`lifecycle_status` becomes 'expired'; `archival_summary` field is populated.

`TestAgent_TempAgent_TaskCompletion_AutoRetire` — create temp agent scoped to a task;
emit `task.completed` domain event for that task; assert auto-retirement runs and agent
`lifecycle_status='expired'`.

`TestAgent_TempAgent_ConcurrentLimit` — org settings has `max_concurrent_temps=2`; create
2 temp agents (both active); attempt to create a 3rd; service returns error with code
`agent.concurrent_temp_limit_exceeded`; only 2 active temp rows in DB.

`TestAgent_ProjectAssignment` — POST /v1/agents/:id/project-assignments assigns agent to
project; GET /v1/agents/:id/project-assignments returns assignment; DELETE removes it;
attempting a second PM-role assignment to the same project returns conflict (partial unique
index enforced at DB level).

`TestAgent_SkillAttachment` — POST /v1/agents/:id/skills attaches skill with priority=1;
GET /v1/agents/:id/skills returns skill; POST again with same skill_id returns 409;
DELETE /v1/agents/:id/skills/:sid detaches; priority ordering preserved for multiple skills.

`TestAgent_PromotionWorkflow` — create temp agent; POST promotion request endpoint; agent
`lifecycle_status='promoted'`; `agent_profile_template` row with `source='promoted'`
created; promoted agent requires human approval before becoming draft staff.

`TestAgent_BudgetCap_Stub` — create agent with `budget_cap_tokens` and `budget_period` set;
verify the column values are persisted and returned correctly via `AgentRepo.GetByID`;
budget *enforcement* tests (checking the cap during a run) are in the control plane
integration tests (task 076).

### Must NOT build

- E2E tests for agent management (those are in task 085)
- Tests for prompt assembly or turn execution (tasks 072/083)
- Budget enforcement integration tests — covered by control plane tests (task 076)

## Acceptance Criteria

- [ ] All tests pass with `go test ./internal/agent/... -tags integration`
- [ ] `TestAgent_StaffLifecycle_IllegalTransition` verifies the error is returned at the service layer (not just SQL constraint)
- [ ] `TestAgent_TempAgent_ConcurrentLimit` reads `max_concurrent_temps` from the org's `settings` jsonb column
- [ ] `TestAgent_ProjectAssignment` verifies the partial unique index on PM role raises a DB-level conflict error that the service wraps as a domain error
- [ ] `TestAgent_BudgetCap_Stub` documents ISSUES #1 and #23 in a comment and verifies only persistence, not enforcement
- [ ] All lifecycle transition tests assert an `audit_event` row is written for each transition

## Tests Required

**Unit tests:** None — this task IS the test suite.

**Integration tests:**
- `TestAgent_CRUD`
- `TestAgent_StaffLifecycle_DraftToActive`
- `TestAgent_StaffLifecycle_ActiveToPaused`
- `TestAgent_StaffLifecycle_Retire`
- `TestAgent_StaffLifecycle_IllegalTransition`
- `TestAgent_TempAgent_Scoping`
- `TestAgent_TempAgent_TTLExpiry`
- `TestAgent_TempAgent_TaskCompletion_AutoRetire`
- `TestAgent_TempAgent_ConcurrentLimit`
- `TestAgent_ProjectAssignment`
- `TestAgent_SkillAttachment`
- `TestAgent_PromotionWorkflow`
- `TestAgent_BudgetCap_Stub`

**E2E tests:** None — covered by task 085.

## Implementer Notes

**What is real vs mocked:**
- PostgreSQL: real, via `testdb.New(t)`
- Domain event bus: real (uses testdb; LISTEN/NOTIFY wired within the test transaction scope)
- Clock: injected `clock.Fake` for TTL-expiry tests
- Model gateway: not involved in agent tests

**ISSUE #1 (RESOLVED):**
`TestAgent_BudgetCap_Stub` uses `budget_cap_tokens` (the resolved column name). No TODO needed.

**ISSUE #23 (RESOLVED — budget enforcement path):**
Budget cap enforcement is tested in the control plane integration suite (task 076), not here.
`TestAgent_BudgetCap_Stub` tests persistence only. `TestPolicy_Eval_BudgetGate` in task 076
tests the full three-level hierarchical/additive check (org → project → agent). No
`t.Skip` or blocked test needed in this suite.

**ISSUE #5 (lifecycle cross-class constraint):**
`TestAgent_StaffLifecycle_IllegalTransition` verifies application-layer enforcement only.
No DB CHECK constraint prevents the transition; the test confirms the service rejects it
before writing to the DB.

**Temp auto-retirement event wiring:**
`TestAgent_TempAgent_TaskCompletion_AutoRetire` requires the domain event
`task.completed` to be published and consumed within the test. Use the synchronous
event dispatch path (in-process consumer) rather than the LISTEN/NOTIFY async path to
keep the test deterministic.
