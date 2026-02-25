# 027: Project Task Schema

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | M (1–2 days) |
| Spec refs | doc 03 §ProjectTask, doc 03 §ProjectTaskEvent, doc 03 §InboxItem, doc 03 §MergeQueueEntry, doc 03a §MergeQueueArchival |
| Spec status | finished |
| Depends on | 016, 017, 005, 025 |
| Blocks | 028, 029, 030, 032 |

## Scope

Build the four project task domain tables: `project_task`, `project_task_event`, `inbox_item`,
and `merge_queue_entry`. Includes all repository layers and the `task_number` auto-increment
sequence per project.

### Must build

**Migrations:**
- `0032_project_task.sql`
- `0033_project_task_event.sql`
- `0034_inbox_item.sql`
- `0035_merge_queue_entry.sql`

**`project_task` table** (doc 03):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `project_id uuid not null references project(id) on delete cascade`
- `task_number integer not null` — auto-increment per project; display format: `{project_slug}-{task_number}`
- `title text not null`
- `description text`
- `work_status text not null default 'draft' check (work_status in ('draft','queued','in_progress','blocked','on_hold','review','done','cancelled'))`
- `current_flow_node_id uuid references flow_node(id) on delete set null` — null when no flow is active
- `flow_template_id uuid references flow_template(id) on delete set null` — template in use for this task
- `schedule_id uuid references task_schedule(id) on delete set null` — non-null for schedule-triggered tasks
- `branch_name text` — git branch associated with this task; null until first commit
- `requires_human_review boolean not null default false` — true when PM scope requires human approval before queuing
- `created_by_type text not null check (created_by_type in ('human_user','agent','system'))`
- `created_by_id uuid` — null for system sentinel
- `assigned_agent_id uuid references agent(id) on delete set null` — primary worker agent; null = unassigned
- `metadata jsonb not null default '{}'`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- `completed_at timestamptz` — set when `work_status` transitions to `done` or `cancelled`
- Unique constraint: `(project_id, task_number)` — task numbers are unique within a project
- Index: `(project_id, work_status)` — list tasks by project and status
- Index: `(project_id, created_at DESC)` — chronological listing
- Index: `(current_flow_node_id) WHERE current_flow_node_id IS NOT NULL` — flow advancement queries
- Index: `(assigned_agent_id) WHERE assigned_agent_id IS NOT NULL`

**task_number sequence per project:** Implemented via a PostgreSQL advisory lock + `SELECT COALESCE(MAX(task_number), 0) + 1 FROM project_task WHERE project_id = $1` within the task INSERT transaction. This avoids a separate sequence table while keeping task numbers gapless-by-convention (gaps can occur on rollback, which is acceptable per doc 03).

**`project_task_event` table** (doc 03):
- `id uuid primary key default gen_random_uuid()`
- `task_id uuid not null references project_task(id) on delete cascade`
- `project_id uuid not null references project(id) on delete cascade` — denormalized for partition key
- `event_type text not null` — e.g. `task.created`, `status.changed`, `flow.advanced`, `comment.added`
- `actor_type text not null check (actor_type in ('human_user','agent','system','supervisor'))`
- `actor_id uuid` — null for system/supervisor
- `flow_node_id uuid references flow_node(id) on delete set null` — which node was active at event time
- `payload jsonb not null default '{}'` — event-specific data (e.g. `{from_status, to_status}`)
- `created_at timestamptz not null default now()`
- Index: `(task_id, created_at DESC)` — task event log query
- Index: `(project_id, event_type, created_at DESC)` — project-level event stream

**`inbox_item` table** (doc 03):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `target_user_id uuid references human_user(id) on delete cascade` — null = all org admins (broadcast)
- `item_type text not null check (item_type in ('task_review','human_approval_required','blocker_filed','comment_mention','draft_action_review','browser_handoff','system_alert'))`
- `source_project_id uuid references project(id) on delete cascade`
- `source_task_id uuid references project_task(id) on delete cascade`
- `created_by_type text not null check (created_by_type in ('human_user','agent','system'))`
- `created_by_id uuid`
- `title text not null`
- `body text`
- `action_payload jsonb not null default '{}'` — type-specific action data; shape varies by `item_type`
- `is_read boolean not null default false`
- `is_acted boolean not null default false` — true after any action taken (approve/reject/dismiss)
- `acted_at timestamptz`
- `acted_by_id uuid references human_user(id) on delete set null`
- `expires_at timestamptz` — null = no expiry; browser_handoff items expire after 24h
- `created_at timestamptz not null default now()`
- Index: `(target_user_id, is_acted, created_at DESC)` — inbox feed query
- Index: `(organization_id, is_acted) WHERE target_user_id IS NULL` — broadcast items
- Index: `(source_task_id) WHERE source_task_id IS NOT NULL`
- Index: `(expires_at) WHERE expires_at IS NOT NULL AND is_acted = false` — expiry sweep

**`merge_queue_entry` table** (doc 03, doc 03a):
- `id uuid primary key default gen_random_uuid()`
- `project_id uuid not null references project(id) on delete cascade`
- `task_id uuid not null references project_task(id) on delete cascade`
- `branch_name text not null` — git branch to merge
- `target_branch text not null default 'main'` — merge target
- `commit_sha text` — last commit SHA on the branch at time of queue entry
- `position integer not null` — ordering within the queue; lower = merge first
- `status text not null default 'queued' check (status in ('queued','merging','merged','failed','skipped'))`
- `enqueued_at timestamptz not null default now()`
- `merged_at timestamptz` — set when merge operation completes
- `archived_at timestamptz` — set when the deploy including this entry completes (doc 03a authoritative); never hard-deleted
- `failure_reason text`
- `metadata jsonb not null default '{}'`
- Unique constraint: `(project_id, task_id)` — a task may appear only once in the merge queue at a time
- Index: `(project_id, archived_at, position) WHERE archived_at IS NULL` — active queue query
- Index: `(task_id)`

**Repository layer:**
- `ProjectTaskRepo`: `Create`, `GetByID`, `GetByProjectAndNumber`, `ListByProject`, `UpdateStatus`, `Update`, `SetFlowNode`, `SetBranch`
- `ProjectTaskEventRepo`: `Record`, `ListByTask`, `ListByProject`
- `InboxItemRepo`: `Create`, `GetByID`, `ListForUser`, `ListBroadcast`, `MarkRead`, `MarkActed`, `ExpireDue`
- `MergeQueueEntryRepo`: `Enqueue`, `GetByTask`, `ListActive`, `UpdateStatus`, `Archive`, `GetPosition`

### Must NOT build
- Task service (work_status machine, review gates) — task 028
- Flow execution tables (`flow_node_execution`, `project_subtask`) — task 029
- Task API endpoints — task 032
- Merge queue worker execution — task 064
- `browser_handoff` table — task 059 (the `item_type='browser_handoff'` value is pre-declared here per the check constraint; the `browser_handoff` table itself is task 059)

## Acceptance Criteria

- [ ] All four migrations apply cleanly in order
- [ ] `project_task.task_number` is unique per `project_id`; two tasks in different projects may share the same number
- [ ] `project_task.work_status` check constraint rejects values outside the 8-value enum
- [ ] `project_task_event.actor_type` includes `'supervisor'` as a valid value (consistent with domain_event convention)
- [ ] `inbox_item.item_type` check constraint includes `'browser_handoff'` as a valid value
- [ ] `merge_queue_entry.archived_at` is null for active entries; set to a timestamp on deploy completion (not merge completion) per doc 03a
- [ ] `ProjectTaskRepo.Create` assigns `task_number` atomically (no gap under concurrent inserts for same project)
- [ ] `InboxItemRepo.ListForUser` returns items ordered by `created_at DESC` with cursor pagination; excludes `is_acted=true` by default (filter param to include)

## Tests Required

**Unit tests:**
- `task_number` increment logic: verify MAX+1 strategy; concurrent insert simulation → numbers are unique (pessimistic advisory lock test)
- `InboxItemRepo` query builder: target_user_id filter, broadcast fallback (target_user_id IS NULL), acted filter
- `merge_queue_entry` position assignment: insert 3 entries; verify positions 1, 2, 3; requeue after failure assigns next available position

**Integration tests:**
- `project_task` CRUD: create task → verify task_number assigned → update status → verify `updated_at` changes
- Task number uniqueness: insert 10 tasks concurrently for same project → verify 10 distinct task numbers
- `project_task_event`: record events for a task → `ListByTask` returns in `created_at DESC` order
- `inbox_item` expiry: create item with `expires_at = now() - 1 minute` → `InboxItemRepo.ExpireDue` marks as acted → not returned in `ListForUser`
- `merge_queue_entry`: enqueue → list active → set `merged_at` → set `archived_at` → not returned in `ListActive`

**E2E tests:**
- None — covered by dedicated E2E task 084

## Implementer Notes

- ✅ ISSUE #7 (RESOLVED): `merge_queue_entry.archived_at` is set when the **deploy including this entry completes**, NOT when the merge operation itself completes. Doc 03a is authoritative. The `merged_at` column records merge completion; `archived_at` records deploy completion. Both are nullable; a merged-but-not-deployed entry has `merged_at` set and `archived_at` null.
- ✅ ISSUE #3 (RESOLVED): `inbox_item.item_type = 'browser_handoff'` is included in the check constraint per doc 03. The full `browser_handoff` table and the inbox item creation flow for browser handoffs are defined in task 059. This task only reserves the enum value.
- The `task_number` generation strategy (MAX+1 with advisory lock) is intentionally simpler than a per-project sequence. This avoids schema migration complexity. The advisory lock key should be derived from the `project_id` UUID to avoid contention across projects: `hashtext(project_id::text)` as the advisory lock ID.
- `project_task_event.actor_type` includes `'supervisor'` to be consistent with `domain_event.actor_type`. This is the same extension documented in ISSUE #20 — supervisor-initiated task events (e.g., auto-escalation) must be recordable.
- `inbox_item.action_payload` shape by type: `task_review` → `{decision_options: ['approve','reject'], review_url}`; `human_approval_required` → `{approval_token}`; `draft_action_review` → `{tool_name, draft_content, approve_action, reject_action}`; `browser_handoff` → defined in task 059. Document these shapes in the repo layer code, not in a separate doc.
