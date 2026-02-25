# 064: Delivery Execution — Background Workers and Deploy State Machine

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | M (1–2 days) |
| Spec refs | doc 03a §DeliveryModes, doc 03a §AutoPush, doc 03a §DeployTaskStateMachine, doc 03a §RollbackFlow, doc 03 §MergeQueueEntry |
| Spec status | finished |
| Depends on | 031, 028, 030, 052, 053, 024, 061 |
| Blocks | 073, 080, 084 |

## Scope

Build the background delivery workers: merge queue execution; auto-push with retry;
deploy task creation for the three delivery modes (continuous, gated, scheduled);
`project_environment` updates on deploy completion; rollback flow; and
`merge_queue_entry.archived_at` on deploy completion (resolving ISSUE #7).

### Must build

**Merge queue worker** (`internal/delivery/merge_worker.go`):

Job type: `merge_execute` (registered in job queue from task 024).

`MergeWorker.Execute(ctx, job Job) error`:
- Parse job payload: `{merge_queue_entry_id: uuid, project_id: uuid}`.
- Load `merge_queue_entry` row; verify `status='pending'` (idempotent: if already
  `status='merged'` or `archived_at` is set, log and return nil).
- Acquire a per-project advisory lock:
  `SELECT pg_try_advisory_xact_lock(project_id_as_bigint)`.
  - If lock not acquired: re-enqueue the job with 5-second delay and return nil.
- Execute the merge operation via `GitService.Merge(ctx, projectID, branchName, targetBranch)`:
  - `targetBranch` = `project.default_branch` (from `project_remote.default_branch`).
  - On success: update `merge_queue_entry.status = 'merged'`, `merged_at = now()`.
  - On conflict: update `merge_queue_entry.status = 'conflict'`; create
    `inbox_item(item_type='merge_conflict', ...)` for the PM via `InboxService.CreateItem`.
- Release advisory lock (automatic at transaction end).
- After successful merge: enqueue `push_execute` job for the same project.
- Emit `domain_event(event_type='task.merged', payload={merge_queue_entry_id, project_id, branch_name})`.

**Auto-push worker** (`internal/delivery/push_worker.go`):

Job type: `push_execute`.

`PushWorker.Execute(ctx, job Job) error`:
- Parse payload: `{project_id: uuid, remote_id: uuid, commit_sha: string, attempt: int}`.
- Load `project_remote` row.
- Call `GitService.Push(ctx, projectRemote)`.
- On success:
  - Emit `domain_event(event_type='project.push_succeeded', payload={project_id, remote_id, commit_sha})`.
  - Enqueue `deploy_task_create` job.
- On failure (attempt < 3):
  - Re-enqueue `push_execute` with `attempt+1`, exponential backoff delay:
    `delay = 2^attempt * 5 seconds` (attempt 1: 5s, attempt 2: 10s, attempt 3: 20s).
  - Add ±25% jitter.
- On failure (attempt >= 3):
  - Create `project_task_event(event_type='push_failed', task_id=deploy_task_id)`.
  - Create PM inbox item `(item_type='escalation', urgency='urgent', summary='Push to remote failed after 3 retries')`.
  - Emit `domain_event(event_type='project.push_failed', ...)`.

**Deploy task creation worker** (`internal/delivery/deploy_worker.go`):

Job type: `deploy_task_create`.

`DeployWorker.Execute(ctx, job Job) error`:
- Parse payload: `{project_id: uuid, commit_sha: string, triggered_by_task_id: uuid, delivery_mode: string}`.
- Route by `delivery_mode`:

  **Continuous mode** (`delivery_mode='continuous'`):
  - Acquire per-project advisory lock to prevent race.
  - Check if a deploy task is already `in_progress` or `queued` for this project:
    `SELECT 1 FROM project_task WHERE project_id=$1 AND task_type='deploy' AND work_status IN ('queued','in_progress') LIMIT 1`.
  - If one exists: log "deploy already in flight, skipping" and return nil.
  - If not: call `TaskService.CreateTask(ctx, CreateTaskInput{project_id, task_type:'deploy', ...})` (task 028).
  - Update `project_environment.deploy_task_id` → new task ID.

  **Gated mode** (`delivery_mode='gated'`):
  - Do NOT auto-create a deploy task.
  - Create `inbox_item(item_type='deploy_approval', target_user_id=pm_user_id, source_project_id=project_id, action_payload={commit_sha, triggered_by_task_id})`.
  - The human approves via `POST /v1/inbox/:id/act` → which then enqueues a `deploy_task_create` job with `delivery_mode='continuous'`.

  **Scheduled mode** (`delivery_mode='scheduled'`):
  - Check the project's deploy schedule (loaded from `task_schedule` where
    `schedule_type='deploy'`).
  - If the next scheduled deploy window has not arrived: do not create task; return nil.
  - If within the window (±5 minutes of next scheduled time): create the deploy task as in
    continuous mode.

**`project_environment` update on deploy completion** (`internal/delivery/env_updater.go`):

`EnvUpdater.OnDeployCompleted(ctx, deployTaskID uuid) error`:
- Called by a domain event consumer listening on `task.completed` where `task_type='deploy'`.
- Load the completed `project_task` row to get `project_id` and the final `commit_sha`
  (from the task's output or from the last `flow_node_execution.commit_sha`).
- Update `project_environment`:
  - `deployed_commit_sha = commit_sha`
  - `deployed_at = now()`
  - `deploy_task_id = deployTaskID`
  - `status = 'deployed'`
- Set `merge_queue_entry.archived_at = now()` for all entries associated with this deploy:
  `UPDATE merge_queue_entry SET archived_at = now() WHERE task_id = $1 AND archived_at IS NULL`.
- Emit `domain_event(event_type='project.deployed', payload={project_id, commit_sha, deploy_task_id})`.

> ✅ ISSUE #7 (RESOLVED): `merge_queue_entry.archived_at` is set when the deploy including the entry completes, not on merge completion. `MergeWorker` does NOT set `archived_at`.

**Rollback flow** (`internal/delivery/rollback.go`):

`RollbackService.InitiateRollback(ctx, projectID, targetCommitSHA string) error`:
- Validates `targetCommitSHA` against the project's git history (via `GitService.CommitExists`).
- Creates a new `project_task(task_type='deploy', branch_name=rollback-{timestamp})` via
  `TaskService.CreateTask` with `title='Rollback to {short_sha}'`.
- Sets `project_task.metadata = {rollback: true, target_commit_sha: targetCommitSHA}`.
- Queues the task immediately (bypasses `requires_human_review` for rollbacks).
- Emits `domain_event(event_type='project.rollback_initiated', payload={project_id, target_commit_sha, rollback_task_id})`.

`POST /v1/projects/:id/rollback` API handler (add to task 032 router):
- Body: `{target_commit_sha: string}`.
- Calls `RollbackService.InitiateRollback`.
- Returns 202 Accepted with `{data: {rollback_task_id: uuid}}`.
- Requires `project:admin` role.

**Domain event taxonomy for delivery:**
- `task.merged` — emitted by `MergeWorker`
- `project.push_succeeded` — emitted by `PushWorker` on success
- `project.push_failed` — emitted by `PushWorker` after 3 failed attempts
- `project.deployed` — emitted by `EnvUpdater.OnDeployCompleted`
- `project.rollback_initiated` — emitted by `RollbackService`
- `deploy.approval_requested` — emitted by `DeployWorker` in gated mode when inbox item created

**Domain event consumer wiring** (`internal/delivery/event_consumer.go`):

Register a consumer cursor for `delivery_worker` on the `domain_event` table (task 024):
- Consumes `task.completed` events where `payload.task_type='deploy'` →
  calls `EnvUpdater.OnDeployCompleted`.
- Consumes `run.completed` events where `payload.run_type='push'` →
  checks if push succeeded; on failure triggers retry logic.

### Must NOT build

- Delivery schema DDL (`project_remote`, `project_environment`, `merge_queue_entry`) — task 031
- Task service or task creation logic — task 028
- Advisory lock infrastructure — task 002 (PostgreSQL connection pool)
- `GitService` implementation (git operations are out of scope for this task; use an interface
  `GitService interface { Merge(...), Push(...), CommitExists(...) }` and provide a mock)
- `GET/POST /v1/projects/:id/remotes` or environments (task 061)

## Acceptance Criteria

- [ ] `MergeWorker.Execute` acquires per-project advisory lock; second concurrent call blocks and re-enqueues rather than proceeding
- [ ] `MergeWorker.Execute` on an already-merged entry is idempotent (returns nil, no second merge)
- [ ] `PushWorker.Execute` re-enqueues with exponential delay on failure (attempt 1: ~5s, attempt 2: ~10s); after attempt 3 creates escalation inbox item
- [ ] `DeployWorker.Execute` in `gated` mode creates an inbox item and does NOT create a deploy task
- [ ] `DeployWorker.Execute` in `continuous` mode skips creation when an `in_progress` deploy task already exists for the project
- [ ] `EnvUpdater.OnDeployCompleted` updates `project_environment.deployed_commit_sha` and sets `merge_queue_entry.archived_at` for associated entries
- [ ] `RollbackService.InitiateRollback` creates a `project_task` with `task_type='deploy'` and `metadata.rollback=true`
- [ ] `POST /v1/projects/:id/rollback` returns 202 with `rollback_task_id`

## Tests Required

**Unit tests:**
- `MergeWorker.Execute`: mock advisory lock (acquired) → merge called; mock lock not acquired → job re-enqueued, merge not called
- `PushWorker.Execute` retry: mock `GitService.Push` returning error; verify backoff delay doubles each attempt; on attempt 3 → inbox item created
- `DeployWorker.Execute` mode routing: continuous + no in-flight → task created; continuous + in-flight → no task; gated → inbox item, no task; scheduled + not in window → no task
- `EnvUpdater.OnDeployCompleted`: mock repositories; verify `project_environment` updated and `merge_queue_entry.archived_at` set

**Integration tests:**
- Advisory lock test: two concurrent `MergeWorker.Execute` calls for same project; verify only one merge proceeds, second re-enqueues
- Deploy completion cascade: create project + merge_queue_entry + deploy task; call `OnDeployCompleted`; verify `project_environment.deployed_commit_sha` set and `merge_queue_entry.archived_at` set
- `POST /v1/projects/:id/rollback`: 202 response; verify `project_task` created with `metadata.rollback=true`

**E2E tests:**
- None — covered by dedicated E2E task 080 (delivery + scheduling integration) and 084 (project + task flow E2E)
