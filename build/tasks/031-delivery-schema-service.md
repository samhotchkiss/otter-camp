# 031: Delivery Schema and Service

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | M (1–2 days) |
| Spec refs | doc 03a §ProjectRemote, doc 03a §ProjectEnvironment, doc 03a §DeliveryModes, doc 03a §DeliveryPatterns |
| Spec status | finished |
| Depends on | 027, 016, 024 |
| Blocks | 032, 064 |

## Scope

Build the delivery subsystem: `project_remote` and `project_environment` tables, their
repository layers, and the delivery service covering all three delivery modes (continuous,
gated, scheduled). Includes auto-push retry logic and advisory lock for continuous mode.
Does not include the merge queue worker execution (task 064) or deploy task runner (task 064).

### Must build

**Migrations:**
- `0040_project_remote.sql`
- `0041_project_environment.sql`

**`project_remote` table** (doc 03a):
- `id uuid primary key default gen_random_uuid()`
- `project_id uuid not null references project(id) on delete cascade`
- `name text not null` — display name (e.g. "origin", "backup")
- `url text not null` — remote repository URL (may contain `ref:<slug>` credential references)
- `is_default boolean not null default false` — exactly one default remote per project (partial unique index)
- `transport text not null default 'https' check (transport in ('https','ssh'))` — transport protocol
- `credential_ref text` — `ref:<slug>` reference into the secret store; null = no authentication
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Partial unique index: `(project_id) WHERE is_default = true` — at most one default remote per project
- Unique constraint: `(project_id, name)` — remote names unique within a project
- Index: `(project_id)`

**`project_environment` table** (doc 03a):
- `id uuid primary key default gen_random_uuid()`
- `project_id uuid not null references project(id) on delete cascade`
- `name text not null` — e.g. "production", "staging"
- `delivery_mode text not null check (delivery_mode in ('continuous','gated','scheduled'))` — doc 03a: three modes
- `remote_id uuid references project_remote(id) on delete set null` — which remote to push to
- `target_branch text not null default 'main'` — branch on the remote to push/merge to
- `deploy_task_id uuid references project_task(id) on delete set null` — most recent deploy task (Pattern 2); null for Pattern 1
- `last_deployed_commit text` — most recently successfully deployed commit SHA
- `last_deployed_at timestamptz` — timestamp of most recent successful deploy
- `is_active boolean not null default true`
- `schedule_id uuid references task_schedule(id) on delete set null` — for scheduled delivery mode only
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Unique constraint: `(project_id, name)` — environment names unique within a project
- Index: `(project_id, is_active)`
- Index: `(deploy_task_id) WHERE deploy_task_id IS NOT NULL`

**Repository layer:**
- `ProjectRemoteRepo`: `Create`, `GetByID`, `ListByProject`, `Update`, `Delete`, `GetDefault`, `SetDefault`
- `ProjectEnvironmentRepo`: `Create`, `GetByID`, `ListByProject`, `Update`, `SetDeployTask`, `RecordDeployment`, `GetActiveByMode`

**Delivery service (`internal/delivery/service.go` — `DeliveryService`):**

**Pattern 1 — delivery inside a flow node:**
- The agent's git operations (via `cli.execute` calling git push) happen within the flow node
- The delivery service tracks the push result via domain events (`push_succeeded` / `push_failed`)
- On `push_succeeded` event: call `RecordDeployment(environmentID, commitSHA, now())` to update `last_deployed_commit` and `last_deployed_at`
- No deploy task is created in Pattern 1

**Pattern 2 — separate deploy task:**
- Called when task completes and the environment's delivery mode is `gated` or `scheduled`
- `CreateDeployTask(ctx, environmentID, commitSHA)` — creates a new `project_task` with `title = "Deploy {env.name} to {remote.url}"`, `work_status='queued'`, assigns to PM
- Sets `project_environment.deploy_task_id` to the new task
- The deploy task follows the standard task lifecycle; on completion, the delivery service records the deployment

**Continuous mode delivery (`TriggerContinuousDelivery`):**
- Called when a task transitions to `done` and the project environment is `delivery_mode='continuous'`
- Acquires a PostgreSQL advisory lock keyed to `(project_id, environment_id)` to prevent concurrent push races
- Lock key derivation: `hashtext(project_id::text || environment_id::text)` as `int8` advisory lock ID
- If lock acquisition fails (timeout 5 seconds): enqueue job `push_execute` with 30-second delay and return
- If lock acquired: enqueue job `push_execute` with no delay; release lock on job enqueue
- The actual push execution (retries, failure handling) is performed by the job worker in task 064

**Gated mode delivery (`RequestGatedDelivery`):**
- Called when task completes and `delivery_mode='gated'`
- Creates `inbox_item` of type `system_alert` or a dedicated deploy inbox item requesting human to trigger deploy
- Human triggers deploy via API (task 032) → calls `CreateDeployTask`

**Scheduled mode delivery:**
- The `task_schedule` on the environment's `schedule_id` is evaluated by the scheduling engine (task 065)
- On schedule tick: call `CreateDeployTask` for all active scheduled environments whose schedule just fired

**Auto-push retry policy (applies to Pattern 1 git push failures and `push_execute` job):**
- Max 3 attempts with exponential backoff: 30s, 120s, 480s
- On final failure: create `inbox_item` of type `system_alert` targeting org admin; set push status to failed; do NOT create another deploy task automatically
- On success: call `RecordDeployment`

### Must NOT build
- Merge queue worker (`merge_execute` job handler) — task 064
- Deploy task worker (`deploy_task_create` job handler) — task 064
- Push execution job handler (`push_execute`) — task 064
- Scheduling engine (task 065)
- Delivery API endpoints (task 032)

## Acceptance Criteria

- [ ] Migration `0040_project_remote.sql` applies cleanly; partial unique index `(project_id) WHERE is_default=true` is present
- [ ] Migration `0041_project_environment.sql` applies cleanly; `delivery_mode` check constraint covers all three values
- [ ] `ProjectRemoteRepo.SetDefault` deactivates prior default remote in the same transaction before activating the new one
- [ ] `TriggerContinuousDelivery` acquires advisory lock before enqueuing push job; concurrent calls for the same (project, environment) result in only one immediate job enqueue (the other gets the delayed retry)
- [ ] `CreateDeployTask` sets `deploy_task_id` on the environment row
- [ ] `RecordDeployment` sets both `last_deployed_commit` and `last_deployed_at` atomically
- [ ] Auto-push retry: after 3 failed `push_execute` job attempts, an inbox item of type `system_alert` is created; no further auto-retry

## Tests Required

**Unit tests:**
- Advisory lock key derivation: verify deterministic key for same `(project_id, environment_id)` pair; different pairs produce different keys
- Auto-push backoff computation: attempts 0, 1, 2 → verify `run_after` delays are 30s, 120s, 480s
- `CreateDeployTask` title format: `"Deploy {env.name} to {remote.url}"` with URL truncated to 64 chars if longer

**Integration tests:**
- `project_remote` default: set default → verify partial unique index; set new default → verify old default's `is_default=false`
- `TriggerContinuousDelivery` lock contention: two goroutines call for same environment simultaneously → only one `push_execute` job enqueued without delay; the other is enqueued with 30-second `run_after`
- Pattern 2 deploy task creation: `CreateDeployTask` → verify task created in DB with correct title and `work_status='queued'`; verify `deploy_task_id` set on environment
- `RecordDeployment`: update `last_deployed_commit` and `last_deployed_at`; subsequent call with new SHA updates again

**E2E tests:**
- None — covered by dedicated E2E task 084

## Implementer Notes

- ISSUE #7 (AMBIGUOUS): `merge_queue_entry.archived_at` is set on deploy completion (not merge completion) per doc 03a. The delivery service is responsible for calling `MergeQueueEntryRepo.Archive(entryID)` when `RecordDeployment` succeeds. This wires up the archival trigger correctly.
- Doc 03a defines two delivery patterns, not two delivery modes. Delivery patterns (Pattern 1 vs Pattern 2) describe how git operations are integrated into the task flow, while delivery modes (continuous/gated/scheduled) describe when deploys are triggered. These are orthogonal. An environment can use Pattern 1 with continuous mode, or Pattern 2 with gated mode.
- The advisory lock for continuous mode is a session-level advisory lock (`pg_try_advisory_lock`), not a transaction-level lock. It must be explicitly released after job enqueue. Use `defer pg_advisory_unlock(key)` to ensure release even on error.
- `credential_ref` on `project_remote` uses the `ref:<slug>` convention from task 009 (secret store). The delivery service resolves this reference before constructing the git push URL. Never log the resolved credential value.
- The `deploy_task_id` on `project_environment` always points to the **most recent** deploy task, not a history. Prior deploy tasks are discoverable via `project_task` queries.
