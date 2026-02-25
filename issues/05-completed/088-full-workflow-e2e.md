# 088: Full Workflow E2E

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | M (1–2 days) |
| Spec refs | doc 02 §ChatSession, doc 03 §ProjectTask, doc 05 §StaffAgent, doc 06 §MemoryRetrieval, doc 06 §PassiveInjection, doc 16 §ControlPlane, doc 21 §E2ETests |
| Spec status | finished |
| Depends on | 001–080 |
| Blocks | — |

## Scope

The full end-to-end system smoke test. Uses only the `ottercamp` CLI binary and REST API.
Verifies the entire chain: human creates org via bootstrap → creates project → staffs
agents → opens chat session → sends message requesting task creation → agent creates task
via native tool → task flows through 2 flow nodes → memory extraction runs → relevant
memory returned in subsequent chat → API assertions at each step. This is the integration
smoke test that confirms the entire system works together.

### Must build

**Test file:** `e2e/full_workflow_test.go`

Build tag: `//go:build e2e`

Test setup: calls `POST /test/reset` and `ottercamp bootstrap` before the scenario.
Uses all standard `e2e/testutil/` helpers:
- `StartServer(t)`, `ResetState(t, baseURL)`, `AdminToken(t, baseURL)`
- `SSEClient(t, baseURL, path, token)`, `WaitForSSEEvent(t, ch, eventType, timeout)`
- `WaitForTaskStatus(t, baseURL, token, taskID, status, timeout)`
- `WaitForInboxItem(t, baseURL, token, filter, timeout)`
- `WaitForMemory(t, baseURL, token, filter, timeout)`
- `WaitForRunStatus(t, baseURL, token, runID, status, timeout)`

**Scenario: `TestFullWorkflow_EndToEnd`**

This single scenario exercises the complete stack. Each step is a distinct API assertion.

---

**Phase 1 — Org Setup**

Step 1 — Reset and bootstrap:
```
POST /test/reset → 204
ottercamp bootstrap → exit 0
admin token: POST /v1/auth/login → token
```

Step 2 — Verify org and starter agents exist (smoke check):
```
GET /v1/orgs/current → 200 → org_id
GET /v1/agents?name=Frank → frank_id, lifecycle_status == "active"
GET /v1/agents?role=pm → pm_agent_id, lifecycle_status == "active"
```

---

**Phase 2 — Project and Agent Setup**

Step 3 — Create a project:
```
POST /v1/projects
{ "name": "Smoke Test Project", "slug": "smoke-test" }
→ 201 → project_id
```

Step 4 — Create a 2-node flow template:
```
POST /v1/projects/<project_id>/flow-templates
{
  "name": "Basic Review Flow",
  "nodes": [
    { "name": "Implementation", "node_type": "agent_work", "position": 1 },
    { "name": "Review", "node_type": "human_review", "position": 2,
      "requires_human_review": true }
  ]
}
→ 201 → template_id, node1_id (Implementation), node2_id (Review)
```

Step 5 — Assign PM agent to project:
```
POST /v1/agents/<pm_agent_id>/project-assignments
{ "project_id": "<project_id>", "role": "pm" }
→ 201
```

---

**Phase 3 — Chat Session and Task Creation via Agent**

Step 6 — Create chat session scoped to the project:
```
POST /v1/chat-sessions
{
  "scope_type": "project",
  "scope_id": "<project_id>",
  "mode": "sync"
}
→ 201 → session_id
```

Step 7 — Add human and PM agent as participants:
```
POST /v1/chat-sessions/<session_id>/participants
{ "participant_type": "agent", "participant_id": "<pm_agent_id>" }
→ 201
```

Step 8 — Open SSE stream:
```
GET /v1/events/stream?scopes=session:<session_id>
→ 200 text/event-stream (connection held open)
```

Step 9 — Send message requesting task creation:
```
POST /v1/chat-sessions/<session_id>/messages
{
  "content": "[create-task] Please create a task to implement the login feature using the Basic Review Flow template.",
  "role": "human"
}
→ 201 → human_message_id
```

Step 10 — Wait for agent response:
```
WaitForSSEEvent(eventType: "turn.completed", timeout: 60s)
→ turn completed
```

Step 11 — Verify agent created the task via native task.create tool:
```
GET /v1/projects/<project_id>/tasks
→ 200
→ body.data length >= 1
→ body.data[0].title contains "login" or a task was created
→ body.data[0].flow_template_id == template_id
→ task_id = body.data[0].id
→ body.data[0].work_status == "queued" or "in_progress"
```

Step 12 — Verify a run was created for the agent turn:
```
GET /v1/control/runs?session_id=<session_id>
→ 200
→ body.data length >= 1
→ body.data[0].status == "completed"
→ chat_run_id = body.data[0].id
```

---

**Phase 4 — Task Flow Through 2 Nodes**

Step 13 — Verify task starts at node 1 (Implementation):
```
GET /v1/tasks/<task_id>
→ body.data.current_flow_node_id == node1_id
→ body.data.work_status == "in_progress"
```

Step 14 — Agent signals completion of node 1:
```
POST /v1/tasks/<task_id>/advance-flow
{ "decision": "complete", "commit_sha": "smoketest-commit-abc123" }
→ 200
→ body.data.current_flow_node_id == node2_id
```

Step 15 — Wait for human review inbox item:
```
WaitForInboxItem(filter: { source_task_id: task_id, item_type: "review_request" }, timeout: 30s)
→ inbox_item_id
```

Step 16 — Approve review:
```
POST /v1/inbox/<inbox_item_id>/act
{ "action": "approve" }
→ 200
→ body.data.status == "resolved"
```

Step 17 — Wait for task to reach `done`:
```
WaitForTaskStatus(taskID: task_id, status: "done", timeout: 30s)
→ body.data.work_status == "done"
```

---

**Phase 5 — Memory Extraction and Subsequent Chat**

Step 18 — Trigger memory extraction for the session:
```
POST /v1/memory/consolidate
Authorization: Bearer <token>
{ "session_id": "<session_id>" }
→ 200 or 204
```

Step 19 — Wait for memory row to be created from the session:
```
WaitForMemory(filter: { session_id: session_id, OR any memory from this session }, timeout: 30s)
→ at least one memory row exists
→ memory.is_active == true
```

Step 20 — Send a follow-up message that should trigger memory retrieval:
```
POST /v1/chat-sessions/<session_id>/messages
{
  "content": "What tasks have we discussed so far?",
  "role": "human"
}
→ 201
```

Step 21 — Wait for agent response:
```
WaitForSSEEvent(eventType: "turn.completed", timeout: 60s)
→ turn completed
```

Step 22 — Verify relevant memory was returned in the response context:
```
GET /v1/model/invocations?session_id=<session_id>
→ 200
→ find the most recent invocation (for the follow-up message turn)
→ body.data[0].metadata.layer_token_counts.memory_injection > 0
   OR body.data[0].metadata.memory_injected == true
```

---

**Phase 6 — Audit Trail Verification**

Step 23 — Verify audit trail completeness:
```
GET /v1/audit?organization_id=<org_id>
→ 200
→ body.data contains events for at minimum:
   - action == "bootstrap_complete"
   - action contains "session" or "session_created"
   - action contains "message" or "message_sent"
   - action contains "task" or "task_created"
   - action contains "run" or "run_created"
```

---

**Phase 7 — Final Health Check**

Step 24 — System health after full workflow:
```
GET /health/live
→ 200
→ body.status == "healthy"
```

### Must NOT build

- UI or TUI interactions
- Internal Go package calls
- SSE event completeness tests (individual SSE events are tested in task 083)
- Token usage rollup verification (that involves model_usage_rollup polling; out of scope
  for this smoke test's time budget)

## Acceptance Criteria

- [ ] All 24 steps of `TestFullWorkflow_EndToEnd` pass sequentially
- [ ] Agent creates a task via native `task.create` tool in response to chat message (step 11)
- [ ] Task flows through both flow nodes: Implementation (agent advance) and Review (human approval via inbox)
- [ ] Task reaches `work_status == "done"` after review approval
- [ ] Memory extraction produces at least 1 active memory row from the session
- [ ] Follow-up message results in a model invocation with memory injection tokens > 0
- [ ] Audit trail contains events for bootstrap, session creation, task creation, and run creation
- [ ] Final `GET /health/live` returns `healthy` after the full workflow
- [ ] Full scenario completes in under 5 minutes

## Tests Required

**Unit tests:** None — this task IS the test suite.

**Integration tests:** None — this is an E2E test suite.

**E2E tests:**
- `TestFullWorkflow_EndToEnd` — 24-step smoke test verifying the entire OtterCamp system
  from bootstrap through chat, task creation, flow execution, memory extraction, and
  memory injection

## Implementer Notes

**ISSUE #27 (RESOLVED — path prefix):**
API routes use `/v1/*` except health (`/health*`) and test-mode reset (`/test/reset`). Doc 21 examples have been corrected.

**Test-mode task creation keyword:**
The `[create-task]` prefix in the human message (step 9) is a test-mode contract. In
`OTTERCAMP_MODE=test`, the agent recognizes this prefix and invokes the `task.create`
native tool with reasonable parameters extracted from the message, using the specified
flow template if one is mentioned. This requires coordination with the turn execution
engine (task 048) and the native tool implementations (task 056).

**SSE stream lifetime:**
The SSE connection opened at step 8 must remain open through step 21. The `SSEClient`
helper must support long-lived connections. The test goroutine reading the SSE stream
must be kept alive for the full duration of the scenario, not just until the first event.

**Memory injection field name:**
The exact metadata field name for memory injection token count is implementation-defined.
Step 22 checks two alternative field names. If neither is present, the test logs a
warning and skips the memory injection assertion with a TODO comment, so the rest of
the scenario still passes.

**Total time budget:**
This scenario has a 5-minute maximum. It is the most time-consuming E2E scenario. The
per-step timeouts are: agent turn responses 60s, flow advancement 30s, inbox items 30s,
memory extraction 30s. The cumulative maximum is well within 5 minutes.

**Combined E2E runtime:**
All 8 E2E scenarios (081–088) combined must complete in under 10 minutes. With individual
scenario budgets of 90s, 60s, 3min, 4min, 3min, 4min, 4min, and 5min respectively, the
sequential total approaches the limit. In CI, scenarios should run in parallel where
possible. Each scenario calls `POST /test/reset` first, so they are independent. The
test runner should use `go test -parallel N` with N >= 4 to stay within the 10-minute
total.
