# 087: Control Plane E2E

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | M (1–2 days) |
| Spec refs | doc 16 §RunLifecycle, doc 16 §ToolExecution, doc 16 §CapabilityPolicy, doc 16 §RunCancellation, doc 04 §AuditEvents, doc 21 §E2ETests |
| Spec status | finished |
| Depends on | 001–080 |
| Blocks | — |

## Scope

E2E test scenario for the control plane. Uses only the `ottercamp` CLI binary and REST
API. Verifies: agent task execution creates a run that reaches `active` state, a
tier-2 tool call creates a `tool_execution` row with `allow` decision, a blocked
capability tool call creates a `tool_execution` row with `deny` decision without failing
the run, audit events are written for both decisions, and run cancellation reaches
`cancelled` state.

### Must build

**Test file:** `e2e/control_plane_test.go`

Build tag: `//go:build e2e`

Test setup: calls `POST /v1/test/reset` and `ottercamp bootstrap` before each scenario.
Uses standard `e2e/testutil/` helpers plus:
- `WaitForRunStatus(t, baseURL, token, runID, status, timeout)` — polls
  `GET /v1/control/runs/<id>` until `status == status` or timeout expires
- `WaitForToolExecution(t, baseURL, token, filter, timeout)` — polls
  `GET /v1/control/runs/<run_id>/tool-executions` (or a query endpoint) until a
  `tool_execution` matching `filter` appears or timeout expires

**Scenario: `TestControlPlane_RunLifecycle`**

Step 1 — Reset, bootstrap, get token:
```
POST /v1/test/reset → 204
ottercamp bootstrap → exit 0
admin token via POST /v1/auth/login
```

Step 2 — Create a project, create a task:
```
POST /v1/projects { "name": "Control Plane Test", "slug": "cp-test" }
→ 201 → project_id

POST /v1/projects/<project_id>/tasks
{ "title": "Run lifecycle test task" }
→ 201 → task_id
```

Step 3 — Start a run by advancing the task (triggers agent execution in test mode):
```
POST /v1/tasks/<task_id>/advance-flow
{ "decision": "start" }
→ 200 or the agent starts working automatically
```

Alternatively, in test mode a run can be created directly:
```
POST /v1/control/runs
Authorization: Bearer <token>
{
  "task_id": "<task_id>",
  "principal_type": "agent",
  "principal_id": "<frank_id>"
}
→ 201
→ body.data.id (run_id)
→ body.data.status == "created" or "in_progress"
```

Step 4 — Verify run reaches `active` (in_progress) state:
```
WaitForRunStatus(runID: run_id, status: "in_progress", timeout: 30s)
→ body.data.status == "in_progress"
→ body.data.task_id == task_id
→ body.data.principal_type == "agent"
```

Step 5 — Verify run steps are being created:
```
GET /v1/control/runs/<run_id>/steps
→ 200
→ body.data length >= 1
```

**Scenario: `TestControlPlane_PolicyAllow`**

This scenario uses a task that causes the agent to invoke a tier-2 tool that is allowed
by policy. In test mode, a special task title triggers this behavior.

Step 1 — Reset, bootstrap, create project and task:
```
POST /v1/projects/<project_id>/tasks
{
  "title": "[tool-call:file.write] Write a test file",
  "description": "triggers a file.write tier-2 tool call in test mode"
}
→ 201 → task_id
```

Step 2 — Verify capability policy allows `file.write`:
```
POST /v1/control/policies/evaluate
Authorization: Bearer <token>
{
  "capability": "system.file.write",
  "agent_id": "<frank_id>",
  "project_id": "<project_id>"
}
→ 200
→ body.data.decision == "allow"
→ body.data.policy_layer where the allow was found
```

Step 3 — Start the task (trigger agent run):
```
POST /v1/control/runs
{
  "task_id": "<task_id>",
  "principal_type": "agent",
  "principal_id": "<frank_id>"
}
→ 201 → run_id
```

Step 4 — Wait for run to progress to a tool execution:
```
WaitForRunStatus(runID: run_id, status: "in_progress", timeout: 30s)
WaitForToolExecution(filter: { run_id: run_id, tool_name: "file.write" }, timeout: 30s)
→ tool_execution found
→ tool_execution.policy_decision == "allow"
→ tool_execution.tool_tier == "tier2"
→ tool_execution.tool_domain contains "file"
→ tool_execution.status == "completed"
```

Step 5 — Verify audit event written for allow decision:
```
GET /v1/audit?action=capability_allowed&run_id=<run_id>
→ 200
→ body.data length >= 1
→ body.data[0].action == "capability_allowed" or "tool_executed"
→ body.data[0].metadata.capability contains "file.write"
→ body.data[0].metadata.decision == "allow"
```

**Scenario: `TestControlPlane_PolicyDeny_RunContinues`**

Verifies that a denied tool call creates a `deny` decision in `tool_execution` but does
NOT cause the run to fail (the agent receives the denial gracefully).

Step 1 — Create a capability policy that denies `system.network.outbound` for the test agent:
```
POST /v1/control/policies
Authorization: Bearer <token>
{
  "policy_layer": "project",
  "project_id": "<project_id>",
  "capability": "system.network.outbound",
  "effect": "deny"
}
→ 201 → policy_id
```

Step 2 — Create task that triggers a blocked capability tool call:
```
POST /v1/projects/<project_id>/tasks
{
  "title": "[tool-call:network.fetch] Fetch external URL",
  "description": "triggers a network.fetch tier-2 tool call; blocked by policy"
}
→ 201 → blocked_task_id
```

Step 3 — Start a run for this task:
```
POST /v1/control/runs
{
  "task_id": "<blocked_task_id>",
  "principal_type": "agent",
  "principal_id": "<frank_id>"
}
→ 201 → blocked_run_id
```

Step 4 — Wait for tool execution with deny decision:
```
WaitForToolExecution(filter: { run_id: blocked_run_id, policy_decision: "deny" }, timeout: 30s)
→ tool_execution found
→ tool_execution.policy_decision == "deny"
→ tool_execution.tool_name contains "network" or "fetch"
```

Step 5 — Verify run does NOT fail (run remains in_progress or completes normally):
```
GET /v1/control/runs/<blocked_run_id>
→ body.data.status != "failed"
   (run continues; agent handles the denial gracefully)
```

Step 6 — Verify audit event written for deny decision:
```
GET /v1/audit?action=capability_denied&run_id=<blocked_run_id>
→ 200
→ body.data length >= 1
→ body.data[0].metadata.decision == "deny"
→ body.data[0].metadata.capability contains "network"
```

**Scenario: `TestControlPlane_RunCancellation`**

Step 1 — Start a long-running task (test mode: use `[slow-run]` title prefix for a
deterministic delay):
```
POST /v1/projects/<project_id>/tasks
{ "title": "[slow-run] Long running task" }
→ 201 → slow_task_id

POST /v1/control/runs
{ "task_id": "<slow_task_id>", "principal_type": "agent", "principal_id": "<frank_id>" }
→ 201 → slow_run_id
```

Step 2 — Wait for run to reach in_progress:
```
WaitForRunStatus(runID: slow_run_id, status: "in_progress", timeout: 30s)
```

Step 3 — Cancel the run:
```
POST /v1/control/runs/<slow_run_id>/cancel
Authorization: Bearer <token>
→ 200
→ body.data.status == "cancelling" or "cancelled"
```

Step 4 — Wait for run to reach cancelled state:
```
WaitForRunStatus(runID: slow_run_id, status: "cancelled", timeout: 30s)
→ body.data.status == "cancelled"
```

Step 5 — Verify a run_event with type "cancelled" was recorded:
```
GET /v1/control/runs/<slow_run_id>/events
→ 200
→ body.data contains event with event_type == "cancelled" or "run.cancelled"
```

### Must NOT build

- UI or TUI interactions
- Internal Go package calls
- Budget hard limit enforcement tests (those require budget setup; tested in task 076)
- Retry envelope tests (tested in task 076)
- Supervisor stuck detection tests (tested in task 076)

## Acceptance Criteria

- [ ] `TestControlPlane_RunLifecycle` passes: run created via task progression reaches `in_progress`
- [ ] `TestControlPlane_PolicyAllow` passes: `tool_execution` row has `policy_decision == "allow"` for an allowed tier-2 tool
- [ ] Audit event written with `decision == "allow"` for the allowed tool call
- [ ] `TestControlPlane_PolicyDeny_RunContinues` passes: `tool_execution` row has `policy_decision == "deny"` for blocked tool; run does NOT reach `failed`
- [ ] Audit event written with `decision == "deny"` for the denied tool call
- [ ] `TestControlPlane_RunCancellation` passes: run reaches `cancelled` state; `run_event` with cancelled type is present
- [ ] Full scenario completes in under 4 minutes

## Tests Required

**Unit tests:** None — this task IS the test suite.

**Integration tests:** None — this is an E2E test suite.

**E2E tests:**
- `TestControlPlane_RunLifecycle` — run created, reaches active state, run steps created
- `TestControlPlane_PolicyAllow` — tier-2 tool call with allow decision; audit event recorded
- `TestControlPlane_PolicyDeny_RunContinues` — blocked capability; deny in tool_execution; run does not fail; audit event recorded
- `TestControlPlane_RunCancellation` — run cancellation propagates to cancelled state

## Implementer Notes

**ISSUE #27 (path prefix):**
All API calls use `/v1/` paths.

**Test-mode task title keywords:**
The `[tool-call:file.write]`, `[tool-call:network.fetch]`, and `[slow-run]` title
prefixes are test-mode contracts. In `OTTERCAMP_MODE=test`, the agent execution engine
recognizes these prefixes and triggers the named tool call or delay deterministically.
This requires coordination with the turn execution engine implementation (task 048) and
the control plane service (task 053).

**ISSUE #17 (instance policy write protection):**
The `TestControlPlane_PolicyDeny_RunContinues` scenario creates a project-level deny
policy (not instance-level) to avoid ISSUE #17 ambiguity. Instance-level policies are not
written or modified in this E2E test.

**tool_execution query endpoint:**
The `WaitForToolExecution` helper may need to use `GET /v1/control/runs/<id>/steps` and
then GET each step's executions, or a dedicated `GET /v1/control/tool-executions?run_id=<id>`
endpoint. The exact endpoint must match what task 054 (control plane API) implements.
Use whichever endpoint is available; fall back to polling run steps if no direct
tool-execution query endpoint exists.

**Run cancellation timing:**
The run transitions through `cancelling` before reaching `cancelled`. The
`WaitForRunStatus` helper must accept either `cancelled` or `cancelling` as intermediate
states. The final assertion checks for `cancelled` specifically.
