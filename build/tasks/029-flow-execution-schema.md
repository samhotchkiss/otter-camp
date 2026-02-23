# 029: Flow Execution Schema

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | S (≤1 day) |
| Spec refs | doc 03 §FlowNodeExecution, doc 03 §ProjectSubtask, doc 03 §ProjectTaskDependency, doc 03 §ProjectTaskParticipant |
| Spec status | finished |
| Depends on | 027, 017 |
| Blocks | 030, 032, 060 |

## Scope

Build the four flow execution domain tables: `flow_node_execution`, `project_subtask`,
`project_task_dependency`, and `project_task_participant`. Includes all repository layers.
Does not include the service logic that drives flow advancement (task 030).

### Must build

**Migrations:**
- `0036_flow_node_execution.sql`
- `0037_project_subtask.sql`
- `0038_project_task_dependency.sql`
- `0039_project_task_participant.sql`

**`flow_node_execution` table** (doc 03):
- `id uuid primary key default gen_random_uuid()`
- `task_id uuid not null references project_task(id) on delete cascade`
- `flow_node_id uuid not null references flow_node(id) on delete cascade`
- `visit_number integer not null default 1` — incremented each time this node is re-entered (rejection loop counter)
- `status text not null default 'active' check (status in ('active','completed','rejected','abandoned'))`
- `session_id uuid` — references `chat_session(id)` at application layer (soft FK; chat_session is L4); populated when the per-node async session is created
- `commit_sha text` — git commit SHA recorded when this execution completes (optional; only for nodes with git operations)
- `started_at timestamptz not null default now()`
- `completed_at timestamptz`
- `metadata jsonb not null default '{}'`
- Index: `(task_id, flow_node_id, visit_number)` — visit count lookup
- Index: `(task_id, status)` — active execution query
- Index: `(session_id) WHERE session_id IS NOT NULL` — session linkage

**`project_subtask` table** (doc 03):
- `id uuid primary key default gen_random_uuid()`
- `task_id uuid not null references project_task(id) on delete cascade`
- `flow_node_execution_id uuid not null references flow_node_execution(id) on delete cascade` — subtasks are scoped to a specific node execution
- `title text not null`
- `description text`
- `work_status text not null default 'pending' check (work_status in ('pending','in_progress','done','cancelled'))` — note: NO 'review' or 'on_hold' states (doc 03 explicitly excludes them)
- `sequence_number integer not null` — ordering within the node execution; subtasks execute sequentially on shared task branch
- `assignee_type text check (assignee_type in ('human_user','agent'))` — null = unassigned
- `assignee_id uuid` — application-layer FK; references `human_user.id` or `agent.id`
- `created_by_type text not null check (created_by_type in ('human_user','agent','system'))`
- `created_by_id uuid`
- `completed_at timestamptz`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Unique constraint: `(flow_node_execution_id, sequence_number)` — sequence numbers unique within a node execution
- Index: `(task_id, work_status)` — task subtask status rollup
- Index: `(flow_node_execution_id, sequence_number)` — ordered subtask list for a node execution

**`project_task_dependency` table** (doc 03):
- `id uuid primary key default gen_random_uuid()`
- `source_type text not null check (source_type in ('project_task','project_subtask'))` — the entity that has the dependency
- `source_id uuid not null` — references `project_task.id` or `project_subtask.id` (application-layer)
- `depends_on_type text not null check (depends_on_type in ('project_task','project_subtask'))` — the upstream entity
- `depends_on_id uuid not null` — references `project_task.id` or `project_subtask.id` (application-layer)
- `created_by_type text not null check (created_by_type in ('human_user','agent','system'))`
- `created_by_id uuid`
- `created_at timestamptz not null default now()`
- Check constraint: `source_type = depends_on_type` — same-level-only constraint; no cross-level dependencies between task and subtask
- Unique constraint: `(source_type, source_id, depends_on_type, depends_on_id)` — no duplicate edges
- Check constraint: `source_id != depends_on_id` — no self-dependency
- Index: `(source_type, source_id)` — outbound edges
- Index: `(depends_on_type, depends_on_id)` — inbound edges (unblock lookup)

**`project_task_participant` table** (doc 03):
- `id uuid primary key default gen_random_uuid()`
- `task_id uuid not null references project_task(id) on delete cascade`
- `participant_type text not null check (participant_type in ('human_user','agent'))`
- `participant_id uuid not null` — application-layer FK
- `role text not null check (role in ('planner','worker','reviewer','observer'))`
- `joined_at timestamptz not null default now()`
- `left_at timestamptz` — null = still active participant
- Unique constraint: `(task_id, participant_type, participant_id)` — one record per participant per task
- Index: `(task_id, left_at) WHERE left_at IS NULL` — active participants

**Repository layer:**
- `FlowNodeExecutionRepo`: `Create`, `GetByID`, `GetActive`, `ListByTask`, `Complete`, `Reject`, `Abandon`, `RecordCommitSHA`, `SetSessionID`
- `ProjectSubtaskRepo`: `Create`, `GetByID`, `ListByExecution`, `UpdateStatus`, `NextSequenceNumber`
- `ProjectTaskDependencyRepo`: `Add`, `Remove`, `ListOutbound`, `ListInbound`, `CheckCycle`
- `ProjectTaskParticipantRepo`: `Add`, `Remove`, `ListActive`, `GetByParticipant`

### Must NOT build
- Flow execution service (task 030)
- `chat_session` creation for flow nodes (task 060 — L4)
- Control plane `run` table (task 051 — L4)
- Dependency DAG enforcement logic (task 030)

## Acceptance Criteria

- [ ] All four migrations apply cleanly in order
- [ ] `flow_node_execution.visit_number` starts at 1; incrementing is the service's responsibility (schema does not auto-increment)
- [ ] `project_subtask.work_status` check constraint excludes `'review'` and `'on_hold'`
- [ ] `project_task_dependency` check constraint `source_type = depends_on_type` is present and rejects cross-level edges at the DB level
- [ ] `project_task_dependency` self-dependency check `source_id != depends_on_id` is present
- [ ] `project_task_participant` unique constraint prevents the same participant from being added twice to a task
- [ ] `FlowNodeExecutionRepo.GetActive` returns at most one row per `(task_id, flow_node_id)` with `status='active'`
- [ ] `ProjectSubtaskRepo.NextSequenceNumber` returns `MAX(sequence_number) + 1` for a given `flow_node_execution_id`, or 1 if no subtasks exist

## Tests Required

**Unit tests:**
- Dependency constraint: verify `source_type = depends_on_type` check rejects `source_type='project_task'` + `depends_on_type='project_subtask'` at DB level
- Self-dependency: insert row with `source_id = depends_on_id` → DB rejects with check violation
- `NextSequenceNumber`: empty execution → returns 1; existing subtasks with max=3 → returns 4

**Integration tests:**
- `flow_node_execution` lifecycle: create → complete → verify `completed_at` set
- Rejection increment: create execution with visit_number=1 → service increments to 2 on rejection (integration test calls repo directly; service logic tested in task 030)
- `project_subtask` uniqueness: insert subtask with sequence_number=1 → insert another with sequence_number=1 for same execution → DB rejects
- Dependency cross-level rejection: `ADD` with `source_type='project_task'` and `depends_on_type='project_subtask'` → DB CHECK violation
- `project_task_participant` soft removal: `Add` → `Remove` (sets `left_at`) → `ListActive` excludes removed participant; `GetByParticipant` still finds the row

**E2E tests:**
- None — covered by dedicated E2E task 084

## Implementer Notes

> ⚠️ ISSUE #16 (BLOCKER): `flow_node_execution` references `flow_node(id)` which is affected by the unresolved flow_node schema conflict (ISSUE #16). The FK reference itself is safe regardless of which columns are on `flow_node`, but do not implement any logic that reads `flow_node.skills jsonb` — that column must not exist per the ISSUE #16 resolution already applied in task 017.

- `flow_node_execution.session_id` is stored as a plain `uuid` with no SQL FK. This is because `chat_session` is at L4 (task 043) and a SQL FK here would create a forward-reference migration problem. The session linkage is established in task 060 at the application layer.
- `project_subtask.work_status` intentionally excludes `'review'` and `'on_hold'` states. Doc 03 explicitly states these states do not apply to subtasks — subtasks either complete or are cancelled. If a review is needed at the subtask level, it should be a separate flow node with `requires_human_review=true`.
- `project_task_dependency.depends_on_id` for a `project_task` target that is later cancelled: the flow execution service (task 030) is responsible for detecting cancelled dependencies and creating a resolution task. The schema does not cascade on cancellation.
- The `same-level-only` constraint (`source_type = depends_on_type`) means task-to-task and subtask-to-subtask dependencies are both valid, but a task cannot directly depend on a subtask or vice versa. This is enforced at the DB level via CHECK constraint.
