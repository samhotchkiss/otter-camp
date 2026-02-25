# 065: Scheduling Engine — Cron Execution, Overlap Policy, and Schedule API

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | S (≤1 day) |
| Spec refs | doc 03 §TaskSchedule, doc 03 §ScheduleOverlapPolicy, doc 12 §DomainEvents |
| Spec status | finished |
| Depends on | 016, 018, 028, 024, 019 |
| Blocks | 073, 080, 084 |

## Scope

Build the scheduling engine: `schedule_tick` job worker; cron expression parsing and
next-run calculation; task instantiation from schedule; overlap policy enforcement
(skip and queue variants); schedule enable/disable; domain events; and the
`ottercamp schedule` CLI commands.

### Must build

**Schedule tick worker** (`internal/scheduling/tick_worker.go`):

Job type: `schedule_tick`.

The scheduler does NOT use a `task_schedule` row to trigger itself (circular dependency).
Instead, a background goroutine in the worker process fires a `schedule_tick` job every
60 seconds:
- `SchedulerHeartbeat.Start(ctx)`: starts a ticker goroutine that enqueues a
  `schedule_tick` job every 60 seconds with `priority='low'`.
- Only one `schedule_tick` job should be in the queue at a time (use idempotency key
  `schedule_tick_global` with the current minute timestamp as suffix: `schedule_tick_20240115_1430`).

`ScheduleTickWorker.Execute(ctx, job Job) error`:
- Loads all enabled schedules: `SELECT * FROM task_schedule WHERE enabled=true AND project_id IS NOT NULL`.
- For each schedule: calls `ScheduleEngine.MaybeFire(ctx, schedule)`.
- Returns nil after processing all schedules (individual schedule errors are logged but do
  not fail the job — isolation of schedule failures).

**Cron parsing and next-run calculation** (`internal/scheduling/cron.go`):

`CronParser.ParseExpression(expr string) (CronSchedule, error)`:
- Supports standard 5-field cron syntax: `minute hour day month weekday`.
- Does NOT support: seconds, `@yearly`, `@monthly`, `@weekly`, `@daily`, `@hourly` aliases
  (these are added if spec is extended — stub them as parse errors for now).
- Uses `github.com/robfig/cron/v3` or equivalent; expose as an internal interface
  `CronSchedule { Next(t time.Time) time.Time }`.

`CronParser.ValidateExpression(expr string) error`:
- Called by `TaskScheduleService.CreateSchedule` (task 018) and the API handler.
- Returns a descriptive error if the expression is invalid.

**Schedule engine** (`internal/scheduling/engine.go`):

`ScheduleEngine.MaybeFire(ctx, schedule TaskSchedule) error`:
- Computes the last scheduled run time: the most recent cron time before `now()` (UTC).
- Checks if a task was already created for this scheduled run:
  `SELECT 1 FROM project_task WHERE schedule_id=$1 AND created_at >= $2 LIMIT 1`
  where `$2` = start of the last scheduled window (cron time - 60 seconds).
- If a task exists: no-op (idempotent — the 60s window prevents double-fire).
- If no task: proceeds to fire (see below).

`ScheduleEngine.Fire(ctx, schedule TaskSchedule) error`:
- Applies overlap policy before creating the task:

  **`overlap_policy='skip'`** (default):
  - Query: `SELECT COUNT(*) FROM project_task WHERE schedule_id=$1 AND work_status IN ('draft','queued','in_progress','blocked','on_hold')`.
  - If count > 0: emit `domain_event(event_type='system.schedule.skipped', payload={schedule_id, reason:'overlap_policy_skip'})` and return nil.
  - If count = 0: create task.

  **`overlap_policy='queue'`**:
  - Always create the task regardless of existing pending tasks.
  - No overlap check needed.

- Task creation via `TaskService.CreateTask` (task 028):
  - `title = schedule.title_template` (or `"Scheduled run — {schedule_name}"` if not set)
  - `task_type = 'scheduled'`
  - `flow_template_id = schedule.flow_template_id`
  - `schedule_id = schedule.id`
  - `created_by_type = 'system'`, `created_by_id = system_sentinel_uuid`
- Update `task_schedule.last_fired_at = now()`, `task_schedule.next_run_at = cron.Next(now())`.
- Emit `domain_event(event_type='system.schedule.fired', payload={schedule_id, task_id, project_id})`.

`ScheduleEngine.ComputeNextRun(schedule TaskSchedule, from time.Time) time.Time`:
- Returns `CronParser.ParseExpression(schedule.cron_expression).Next(from)`.
- Returns zero time if expression is invalid (caller should disable the schedule).

**Schedule enable/disable service** (`internal/scheduling/schedule_service.go`):

Extend `TaskScheduleService` (task 018) with:

`TaskScheduleService.Enable(ctx, scheduleID uuid) error`:
- Sets `task_schedule.enabled = true`, `next_run_at = ScheduleEngine.ComputeNextRun(schedule, now())`.
- Emits `domain_event(event_type='system.schedule.enabled', payload={schedule_id})`.

`TaskScheduleService.Disable(ctx, scheduleID uuid) error`:
- Sets `task_schedule.enabled = false`, `next_run_at = null`.
- Emits `domain_event(event_type='system.schedule.disabled', payload={schedule_id})`.

**`max_duration_ms` timeout enforcement:**
- If a scheduled task's `work_status` does not reach `done` within
  `schedule.max_duration_ms` milliseconds: the Supervisor (task 053) detects this via its
  stuck-task scan (it checks `max_duration_ms` on tasks with `schedule_id IS NOT NULL`).
- This task adds the max_duration check to the Supervisor's stuck criteria:
  `elapsed_ms > project_task.schedule.max_duration_ms` → mark as `timed_out` (not `stuck`).

**Schedule API endpoints** (`internal/api/schedule_handler.go`):

The base schedule CRUD already exists on task 019 (`GET /v1/projects/:id/schedules`, `POST`, `PATCH`, `DELETE`). Add:

`POST /v1/projects/:id/schedules/:sid/enable`:
- Calls `TaskScheduleService.Enable`.
- Returns 200 `{data: {schedule_id, next_run_at}}`.

`POST /v1/projects/:id/schedules/:sid/disable`:
- Calls `TaskScheduleService.Disable`.
- Returns 200 `{data: {schedule_id, enabled: false}}`.

**`ottercamp schedule` CLI commands** (extend task 068's CLI binary):

```
ottercamp schedule list   --project <slug>        [--json]
ottercamp schedule enable --project <slug> <name>
ottercamp schedule disable --project <slug> <name>
```

- `schedule list`: calls `GET /v1/projects/:id/schedules`; displays table with columns:
  `NAME`, `CRON`, `ENABLED`, `LAST_FIRED`, `NEXT_RUN`.
- `schedule enable`/`disable`: calls the enable/disable endpoint; prints confirmation.
- All three support `--json` flag for JSON output.

**Domain event taxonomy for scheduling:**
- `system.schedule.fired` — task created from schedule
- `system.schedule.skipped` — overlap policy prevented task creation
- `system.schedule.enabled` — schedule enabled
- `system.schedule.disabled` — schedule disabled

### Must NOT build

- `task_schedule` DDL (task 016)
- `TaskScheduleService` CRUD (task 018)
- Schedule list/create/update/delete API (task 019)
- Task creation logic (task 028)
- Supervisor stuck-task detection core (task 053) — this task adds a criterion to it
- Job queue infrastructure (task 024)

## Acceptance Criteria

- [ ] `CronParser.ParseExpression("*/5 * * * *")` succeeds; `.Next(t)` returns the next 5-minute mark
- [ ] `CronParser.ParseExpression("invalid")` returns a descriptive error
- [ ] `ScheduleEngine.MaybeFire` with no prior task and `overlap_policy='skip'` creates a task and emits `system.schedule.fired`
- [ ] `ScheduleEngine.MaybeFire` with an existing pending task and `overlap_policy='skip'` emits `system.schedule.skipped` and creates no task
- [ ] `ScheduleEngine.MaybeFire` with an existing pending task and `overlap_policy='queue'` creates a second task
- [ ] `ScheduleEngine.MaybeFire` within 60s of a prior task creation is idempotent (window check prevents double-fire)
- [ ] `TaskScheduleService.Enable` sets `enabled=true` and computes `next_run_at`
- [ ] `POST /v1/projects/:id/schedules/:sid/enable` returns 200 with `next_run_at` set

## Tests Required

**Unit tests:**
- `CronParser.ParseExpression`: valid 5-field cron → success; 6-field cron → error; empty string → error; `@daily` → error (not supported)
- `ScheduleEngine.MaybeFire`: overlap policy `skip` with pending task → skip event emitted; no pending task → `TaskService.CreateTask` called
- `ScheduleEngine.MaybeFire`: 60s idempotency window → second call within window is no-op
- `ScheduleEngine.ComputeNextRun`: verify next run is in the future for a valid schedule

**Integration tests:**
- `ScheduleTickWorker.Execute`: insert 3 enabled schedules; fire tick job; verify 3 `MaybeFire` calls; one schedule disabled → not fired
- Enable/disable round-trip: create schedule; call `Enable`; verify `enabled=true` and `next_run_at` set; call `Disable`; verify `enabled=false` and `next_run_at=null`
- Overlap enforcement: create schedule; create pending task for schedule; run tick; verify skip event in `domain_event` table

**E2E tests:**
- None — covered by dedicated E2E task 080

