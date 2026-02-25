# 084: Project and Task Flow E2E

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | M (1–2 days) |
| Spec refs | doc 03 §ProjectTask, doc 03 §FlowNode, doc 03 §MergeQueue, doc 03a §DeliveryModes, doc 03a §ProjectEnvironment, doc 21 §E2ETests |
| Spec status | finished |
| Depends on | 001–080 |
| Blocks | — |

## Scope

E2E test scenario for the full project and task flow lifecycle. Uses only the `ottercamp`
CLI binary and REST API. Verifies: project creation, task creation with a 3-node flow
template, flow advancement through all three nodes (including human review inbox item and
approval), task reaching `completed` work_status, merge queue entry creation, deploy
trigger, `archived_at` set on merge queue entry, and `project_environment.deployed_commit_sha`
updated.

### Must build

**Test file:** `e2e/project_task_flow_test.go`

Build tag: `//go:build e2e`

Test setup: calls `POST /test/reset` and `ottercamp bootstrap` before each scenario.
Uses standard `e2e/testutil/` helpers plus:
- `WaitForInboxItem(t, baseURL, token, filter, timeout)` — polls
  `GET /v1/inbox` until an item matching `filter` appears or timeout expires; returns the
  item
- `WaitForTaskStatus(t, baseURL, token, taskID, status, timeout)` — polls
  `GET /v1/tasks/<id>` until `work_status == status` or timeout expires
- `WaitForMergeQueueEntry(t, baseURL, token, projectID, taskID, timeout)` — polls
  `GET /v1/projects/<id>/merge-queue` until an entry for `taskID` appears

**Scenario: `TestProjectTaskFlow_ThreeNodeFlow`**

Step 1 — Reset, bootstrap, get token:
```
POST /test/reset → 204
ottercamp bootstrap → exit 0
admin token via POST /v1/auth/login
```

Step 2 — Create a project:
```
POST /v1/projects
Authorization: Bearer <token>
{
  "name": "Flow Test Project",
  "slug": "flow-test",
  "delivery_mode": "gated"
}
→ 201
→ body.data.id (project_id)
→ body.data.slug == "flow-test"
```

Step 3 — Create a 3-node flow template:
```
POST /v1/projects/<project_id>/flow-templates
Authorization: Bearer <token>
{
  "name": "Three Node Flow",
  "nodes": [
    {
      "name": "Implementation",
      "node_type": "agent_work",
      "position": 1
    },
    {
      "name": "Human Review",
      "node_type": "human_review",
      "position": 2,
      "requires_human_review": true
    },
    {
      "name": "Finalization",
      "node_type": "agent_work",
      "position": 3
    }
  ]
}
→ 201
→ body.data.id (template_id)
→ body.data.nodes length == 3
→ body.data.is_current == true
→ record node IDs: node1_id (Implementation), node2_id (Human Review), node3_id (Finalization)
```

Step 4 — Create a task using the 3-node template:
```
POST /v1/projects/<project_id>/tasks
Authorization: Bearer <token>
{
  "title": "Build the feature",
  "flow_template_id": "<template_id>",
  "description": "Implement and review the feature"
}
→ 201
→ body.data.id (task_id)
→ body.data.work_status == "queued" or "in_progress"
→ body.data.current_flow_node_id == node1_id  (starts at first node)
```

Step 5 — Agent signals completion of node 1 (Implementation):
```
POST /v1/tasks/<task_id>/advance-flow
Authorization: Bearer <token>
{
  "decision": "complete",
  "commit_sha": "abc1234def5678"
}
→ 200
→ body.data.current_flow_node_id == node2_id  (advanced to Human Review)
```

Step 6 — Verify human review inbox item created:
```
WaitForInboxItem(filter: { item_type: "review_request", source_task_id: task_id }, timeout: 30s)
→ inbox_item_id returned
→ item.item_type == "review_request"
→ item.source_task_id == task_id
```

Step 7 — Approve the review from inbox:
```
POST /v1/inbox/<inbox_item_id>/act
Authorization: Bearer <token>
{
  "action": "approve"
}
→ 200
→ body.data.status == "resolved"
```

Step 8 — Verify task advanced past node 2 to node 3 (Finalization):
```
GET /v1/tasks/<task_id>
→ 200
→ body.data.current_flow_node_id == node3_id
```

Step 9 — Agent signals completion of node 3 (Finalization):
```
POST /v1/tasks/<task_id>/advance-flow
Authorization: Bearer <token>
{
  "decision": "complete",
  "commit_sha": "def5678ghi9012"
}
→ 200
```

Step 10 — Verify task reaches `completed` work_status:
```
WaitForTaskStatus(taskID: task_id, status: "done", timeout: 30s)
→ body.data.work_status == "done"
→ body.data.current_flow_node_id is null (flow complete)
```

Step 11 — Verify merge queue entry created:
```
WaitForMergeQueueEntry(projectID: project_id, taskID: task_id, timeout: 30s)
→ merge_queue_entry found
→ merge_queue_entry.task_id == task_id
→ merge_queue_entry.archived_at is null  (not yet deployed)
```

Step 12 — Trigger deploy (gated mode: human triggers via API):
```
POST /v1/projects/<project_id>/deploy
Authorization: Bearer <token>
{
  "entry_ids": ["<merge_queue_entry_id>"]
}
→ 200 or 202
→ body.data.deploy_task_id is non-empty UUID  (deploy task created)
```

Step 13 — Wait for deploy task to complete:
```
WaitForTaskStatus(taskID: deploy_task_id, status: "done", timeout: 60s)
→ deploy task reaches "done"
```

Step 14 — Verify `archived_at` set on merge queue entry:
```
GET /v1/projects/<project_id>/merge-queue
→ entry with id == merge_queue_entry_id
→ entry.archived_at is non-null timestamp
```

Step 15 — Verify `project_environment.deployed_commit_sha` updated:
```
GET /v1/projects/<project_id>/environments
→ 200
→ body.data length >= 1
→ body.data[0].deployed_commit_sha == "def5678ghi9012"  (last node commit sha)
```

**Scenario: `TestProjectTaskFlow_FlowNodeRejectionLoop`**

Verifies that a rejected node creates a new flow_node_execution with visit_counter incremented.

Step 1 — Reset, bootstrap, create project and 2-node flow template with a review node at position 1.

Step 2 — Create task, verify it starts at node 1.

Step 3 — Reject node 1 (first visit):
```
POST /v1/tasks/<task_id>/advance-flow
{ "decision": "reject", "reason": "Needs rework" }
→ 200
→ body.data.current_flow_node_id == node1_id  (back to start of same node)
```

Step 4 — Verify visit counter incremented:
```
GET /v1/tasks/<task_id>/flow
→ body.data.current_execution.visit_count == 2  (second visit)
→ body.data.current_execution.node_id == node1_id
```

### Must NOT build

- UI or TUI interactions
- Internal Go package calls
- Subtask sequencing tests (tested in task 073)
- Dependency DAG enforcement tests (tested in task 073)

## Acceptance Criteria

- [ ] `TestProjectTaskFlow_ThreeNodeFlow` passes end-to-end through all 15 steps
- [ ] Human review inbox item appears after node 2 advance; approval unblocks node 3
- [ ] Task reaches `work_status == "done"` after node 3 completes
- [ ] Merge queue entry `archived_at` is set after deploy completes
- [ ] `project_environment.deployed_commit_sha` matches the commit SHA from node 3
- [ ] `TestProjectTaskFlow_FlowNodeRejectionLoop` passes: visit count is 2 after first rejection
- [ ] ISSUE #7 (archived_at trigger): test asserts `archived_at` is set on deploy completion, NOT on merge completion
- [ ] Full scenario completes in under 4 minutes

## Tests Required

**Unit tests:** None — this task IS the test suite.

**Integration tests:** None — this is an E2E test suite.

**E2E tests:**
- `TestProjectTaskFlow_ThreeNodeFlow` — full 15-step flow with review, deploy, archived_at, and deployed_commit_sha
- `TestProjectTaskFlow_FlowNodeRejectionLoop` — rejection increments visit counter; task stays at same node

## Implementer Notes

**ISSUE #27 (RESOLVED — path prefix):**
API routes use `/v1/*` except health (`/health*`) and test-mode reset (`/test/reset`). Doc 21 examples have been corrected.

**ISSUE #7 (archived_at trigger):**
The test explicitly asserts that `archived_at` is null before deploy and non-null after
deploy completes. Doc 03a is authoritative: `archived_at` is set on deploy completion,
not merge completion. The test verifies this by checking `archived_at` at step 11 (after
merge queue entry created but before deploy) and step 14 (after deploy).

**Flow template node IDs:**
The 3-node flow template creation response should include node IDs in the response body.
If the create endpoint only returns the template ID and the implementer must do a
separate GET for node IDs, add a `GET /v1/flow-templates/<template_id>/nodes` call after
creation to collect node IDs.

**Deploy task in gated mode:**
In gated delivery mode (doc 03a), deploying creates a deploy task that an agent works.
In `OTTERCAMP_MODE=test`, the agent auto-completes deploy tasks deterministically. The
`WaitForTaskStatus` poll for the deploy task uses a 60-second timeout to account for the
test-mode agent processing time.

**commit_sha in test mode:**
The `commit_sha` values passed in `advance-flow` are synthetic test values. The
`project_environment.deployed_commit_sha` update verifies that the system records and
surfaces the commit SHA correctly, not that it represents a real git commit.
